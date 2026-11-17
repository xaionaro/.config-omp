package main

import (
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "os"
 "path/filepath"
)

// captureTarget resolves an existing explicit session without creating it.
//
// Example: an advisor can select its owner's session rather than its own ID.
func captureTarget(sid string) (string,error) {
 if !validSessionID(sid) { return "",fmt.Errorf("invalid session id") }
 home:=accountHome(); if home=="" { return "",fmt.Errorf("account home unavailable") }
 dir:=filepath.Join(home,".agent","context","sessions",sid)
 info,err:=os.Lstat(dir); if err!=nil { return "",fmt.Errorf("resolve session %s: %w",sid,err) }
 if !info.IsDir() || info.Mode()&os.ModeSymlink!=0 { return "",fmt.Errorf("unsafe session directory: %s",dir) }
 return dir,nil
}
// writeLedgerCapture streams one coherent validated observation to an unpublished writer.
//
// Example: both the CLI and resource server use the same authority checks.
func writeLedgerCapture(sid string, out io.Writer) error {
 dir,err:=captureTarget(sid); if err!=nil { return err }
 return writeLedgerCaptureDir(dir,sid,out)
}

// writeLedgerCaptureDir captures the already resolved session under its authority lock.
//
// Example: explicit session resolution precedes this shared transaction.
func writeLedgerCaptureDir(dir, sid string, out io.Writer) (_err error) {
 compact:=captureJSONCompactor{out:out}
 out=&compact
 lock,err:=acquireSessionLock(dir); if err!=nil { return err }; defer func(){ _err=errors.Join(_err,lock.Close()) }()
 ctx:=context.Background()
 db,err:=openNativeLedgerRead(ctx,dir); if err!=nil { return fmt.Errorf("open session %s ledger: %w",sid,err) }; defer func(){ _err=errors.Join(_err,db.Close()) }()
 tx,err:=db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true}); if err!=nil { return err }
 defer func(){ if err:=tx.Rollback(); err!=nil && !errors.Is(err,sql.ErrTxDone) { _err=errors.Join(_err,err) } }()
 observation,err:=validateNativeLedgerTransaction(ctx,tx,sid); if err!=nil { return err }
 id,generation:=observation.DatabaseID,observation.Generation
 ledger,forecasts,err:=ledgerPresence(ctx,tx); if err!=nil { return err }
 idJSON,err:=json.Marshal(id); if err!=nil { return err }
 if _,err=fmt.Fprintf(out,"{\"source\":\"active\",\"database_id\":%s,\"generation\":\"%d\",\"ledger\":{\"present\":%t",idJSON,generation,ledger); err!=nil { return err }
 if ledger { if err=writeCaptureCollection(ctx,tx,out,"sections"); err!=nil { return err }; if err=writeCaptureCollection(ctx,tx,out,"records"); err!=nil { return err } }
 if _,err=fmt.Fprintf(out,"},\"forecasts\":{\"present\":%t",forecasts); err!=nil { return err }
 if forecasts { if err=writeCaptureCollection(ctx,tx,out,"forecasts"); err!=nil { return err } }
 var doc []byte; var revision int64
 err=tx.QueryRowContext(ctx,"SELECT document_json,revision FROM report WHERE id=1").Scan(&doc,&revision)
 switch { case errors.Is(err,sql.ErrNoRows): _,err=io.WriteString(out,"},\"report\":{\"present\":false}}\n"); case err!=nil: return err; default: _,err=fmt.Fprintf(out,"},\"report\":{\"present\":true,\"document\":%s,\"revision\":%d}}\n",doc,revision) }
 if err!=nil { return err }; return tx.Commit()
}
// writeCaptureCollection writes ordered raw JSON rows without materializing the collection.
//
// Example: numeric lexemes remain unchanged across snapshot pages.
func writeCaptureCollection(ctx context.Context, tx *sql.Tx, out io.Writer, table string) (_err error) {
 key:=table; if table=="forecasts" { key="records" }
 if _,err:=fmt.Fprintf(out,",%q:{",key); err!=nil { return err }
 rows,err:=tx.QueryContext(ctx,"SELECT id,value FROM "+table+" ORDER BY id"); if err!=nil { return err }; defer func(){ _err=errors.Join(_err,rows.Close()) }()
 first:=true
 for rows.Next() { var id string; var value []byte; if err:=rows.Scan(&id,&value); err!=nil { return err }; name,err:=json.Marshal(id); if err!=nil { return err }; if !first { if _,err:=io.WriteString(out,","); err!=nil { return err } }; first=false; if _,err:=fmt.Fprintf(out,"%s:%s",name,value); err!=nil { return err } }
 if err:=rows.Err(); err!=nil { return err }; _,err=io.WriteString(out,"}"); return err
}
// runLedgerCapture prints a complete snapshot only after capture succeeds.
//
// Example: ledger-capture --session ID prints one JSON envelope.
func runLedgerCapture(args []string) (_err error) {
 if len(args)!=3 || args[1]!="--session" { return usageError() }
 f,err:=captureAnonymous(); if err!=nil { return err }; defer func(){ _err=errors.Join(_err,f.Close()) }()
 if err:=writeLedgerCapture(args[2],f); err!=nil { return err }; if err:=validateCaptureUTF8(f); err!=nil { return err }; if _,err:=f.Seek(0,io.SeekStart); err!=nil { return err }; _,err=io.Copy(os.Stdout,f); return err
}
