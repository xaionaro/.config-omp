package main

import (
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "fmt"
)

// baselineObservation binds publication to identity, source generation and report presence.
//
// Example: Late creation refuses a database replaced after preparation.
type baselineObservation struct { Initial bool; DatabaseID string; Generation int64 }

// publishBaseline exercises one actual SQLite transaction for initial or late report creation.
//
// Example: A log conflict rolls back schema/row creation as well as report insertion.
func publishBaseline(ctx context.Context, db *sql.DB, dir string, observation baselineObservation, prepared []byte, log replayLogSnapshot) (_err error) {
 tx,err:=db.BeginTx(ctx,nil);if err!=nil{return err};defer func(){if err:=tx.Rollback();err!=nil&&!errors.Is(err,sql.ErrTxDone){_err=errors.Join(_err,err)}}()
 if observation.Initial {
  if _,err:=tx.ExecContext(ctx,"CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY,database_id TEXT,generation INTEGER); CREATE TABLE report(id INTEGER PRIMARY KEY,document_json TEXT,revision INTEGER); INSERT INTO ledger_meta VALUES(1,?,0)",observation.DatabaseID);err!=nil{return err}
 }
 var identity string;var generation int64
 if err:=tx.QueryRowContext(ctx,"SELECT database_id,generation FROM ledger_meta WHERE id=1").Scan(&identity,&generation);err!=nil{return err}
 if identity!=observation.DatabaseID||generation!=observation.Generation{return fmt.Errorf("baseline observation conflict")}
 var count int;if err:=tx.QueryRowContext(ctx,"SELECT count(*) FROM report").Scan(&count);err!=nil{return err};if count!=0{return fmt.Errorf("report already present")}
 if err:=verifyReplayLog(dir,log);err!=nil{return err}
 if !json.Valid(prepared){return fmt.Errorf("invalid prepared report")}
 if _,err:=tx.ExecContext(ctx,"INSERT INTO report VALUES(1,?,1)",string(prepared));err!=nil{return err}
 if _,err:=tx.ExecContext(ctx,"UPDATE ledger_meta SET generation=generation+1 WHERE id=1");err!=nil{return err}
 return tx.Commit()
}

// adoptBaselineCurrent explicitly releases held fields against the owner's exact current expectations.
//
// Example: Adopting forecast B is prospective permission, not a claim that historical A equalled B.
func adoptBaselineCurrent(document []byte, update syncUpdate, expected map[string]syncOwnership) ([]byte,error) {
 value,err:=decodeReportValue(document);if err!=nil{return nil,err};root,ok:=value.(map[string]any);if !ok{return nil,fmt.Errorf("report object required")}
 raw,err:=json.Marshal(root["status_sync"]);if err!=nil{return nil,err};var state syncState;if err:=json.Unmarshal(raw,&state);err!=nil{return nil,err}
 for field,prior:=range expected {
  key:=syncTargetKey(update,field);if state.Applied[key].Kind!=syncOwnershipHeld{return nil,fmt.Errorf("field is not held")}
  current,err:=baselineCurrent(root,update,field);if err!=nil{return nil,err};if current!=prior{return nil,fmt.Errorf("current ownership expectation conflict")}
  state.Applied[key]=current
 }
 root["status_sync"]=state;return json.Marshal(root)
}
// publishBaselineReconciliation atomically checks native identity/generation and the captured log.
//
// Example: A stale observation cannot publish even a subset of a reconciliation's field permissions.
func publishBaselineReconciliation(ctx context.Context,db *sql.DB,dir string,observation baselineObservation,log replayLogSnapshot,request baselineReconciliation) (_err error) {
 tx,err:=db.BeginTx(ctx,nil);if err!=nil{return err};defer func(){if err:=tx.Rollback();err!=nil&&!errors.Is(err,sql.ErrTxDone){_err=errors.Join(_err,err)}}()
 var identity,document string;var generation int64;var revision int
 if err:=tx.QueryRowContext(ctx,"SELECT m.database_id,m.generation,r.document_json,r.revision FROM ledger_meta m CROSS JOIN report r WHERE m.id=1 AND r.id=1").Scan(&identity,&generation,&document,&revision);err!=nil{return err}
 if identity!=observation.DatabaseID||generation!=observation.Generation{return fmt.Errorf("reconciliation observation conflict")}
 if err:=verifyReplayLog(dir,log);err!=nil{return err};candidate,err:=reconcileBaselinePending([]byte(document),log.Bytes,request);if err!=nil{return err};if err:=verifyReplayLog(dir,log);err!=nil{return err}
 result,err:=tx.ExecContext(ctx,"UPDATE report SET document_json=?,revision=revision+1 WHERE id=1 AND revision=?",string(candidate),revision);if err!=nil{return err};affected,err:=result.RowsAffected();if err!=nil{return err};if affected!=1{return fmt.Errorf("report reconciliation conflict")}
 result,err=tx.ExecContext(ctx,"UPDATE ledger_meta SET generation=generation+1 WHERE id=1 AND database_id=? AND generation=?",identity,generation);if err!=nil{return err};affected,err=result.RowsAffected();if err!=nil{return err};if affected!=1{return fmt.Errorf("generation reconciliation conflict")}
 return tx.Commit()
}
