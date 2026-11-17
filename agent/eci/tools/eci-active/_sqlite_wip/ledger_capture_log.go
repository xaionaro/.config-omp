package main

import (
 "bytes"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "os"
 "os/exec"
 "path/filepath"
 "syscall"
)

// captureLogObservation reads identity and bytes from one open canonical file.
//
// Example: an absent log differs from a present empty log.
func captureLogObservation(dir string) (_ nativeLogObservation, _err error) {
 path:=filepath.Join(dir,"high_level_log.jsonl")
 f,err:=os.OpenFile(path,os.O_RDONLY|syscall.O_NOFOLLOW,0)
 if errors.Is(err,os.ErrNotExist) { return makeNativeLogObservation(nil,nil) }
 if err!=nil { return nativeLogObservation{},fmt.Errorf("open session log: %w",err) }
 defer func(){ _err=errors.Join(_err,f.Close()) }()
 info,err:=f.Stat(); if err!=nil { return nativeLogObservation{},fmt.Errorf("stat session log: %w",err) }
 if !info.Mode().IsRegular() { return nativeLogObservation{},fmt.Errorf("session log is not regular") }
 data,err:=io.ReadAll(f); if err!=nil { return nativeLogObservation{},fmt.Errorf("read session log: %w",err) }
 return makeNativeLogObservation(info,data)
}

// writeLedgerLogCapture writes a log observation and report-owned occurrence inventory.
//
// Example: capture works before any session database has been created.
func writeLedgerLogCapture(sid string, out io.Writer) (_err error) {
 dir,err:=captureTarget(sid); if err!=nil { return err }
 compact:=captureJSONCompactor{out:out}
 out=&compact
 lock,err:=acquireSessionLock(dir); if err!=nil { return err }; defer func(){ _err=errors.Join(_err,lock.Close()) }()
 observation,err:=captureLogObservation(dir); if err!=nil { return err }
 request,err:=json.Marshal(observation); if err!=nil { return err }
 cmd:=exec.Command(filepath.Join(eciBaseDir(),"bin","status-report"),"log-inventory","--stdin")
 cmd.Stdin=bytes.NewReader(request); cmd.Stderr=os.Stderr
 response,err:=cmd.Output(); if err!=nil { return fmt.Errorf("build log inventory: %w",err) }
 if err:=validateReportStructure(response); err!=nil { return fmt.Errorf("invalid log inventory: %w",err) }
 var inventory struct { CompleteLines *int `json:"complete_lines"`; Occurrences json.RawMessage `json:"occurrences"` }
 if err:=json.Unmarshal(response,&inventory); err!=nil { return fmt.Errorf("decode log inventory: %w",err) }
 if inventory.CompleteLines==nil || *inventory.CompleteLines<0 || len(inventory.Occurrences)==0 || inventory.Occurrences[0]!='[' || !json.Valid(inventory.Occurrences) { return fmt.Errorf("invalid log inventory") }
 _,err=fmt.Fprintf(out,"{\"log\":%s,\"complete_lines\":%d,\"occurrences\":%s}",request,*inventory.CompleteLines,inventory.Occurrences)
 return err
}

// runLedgerLogCapture publishes the complete inventory only after capture succeeds.
//
// Example: ledger-log-capture --session ID works without a database.
func runLedgerLogCapture(args []string) (_err error) {
 if len(args)!=3 || args[1]!="--session" { return usageError() }
 f,err:=captureAnonymous(); if err!=nil { return err }; defer func(){ _err=errors.Join(_err,f.Close()) }()
 if err:=writeLedgerLogCapture(args[2],f); err!=nil { return err }
 if _,err:=f.Seek(0,io.SeekStart); err!=nil { return err }
 _,err=io.Copy(os.Stdout,f); return err
}
