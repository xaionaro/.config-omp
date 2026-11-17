package main

import (
 "encoding/base64"
 "os"
 "path/filepath"
 "testing"
)

// TestCaptureLogObservation distinguishes absence, empty, and arbitrary bytes.
//
// Example: an incomplete UTF-8 tail is preserved byte-for-byte.
func TestCaptureLogObservation(t *testing.T) {
 t.Parallel()
 dir:=t.TempDir()
 absent,err:=captureLogObservation(dir); if err!=nil { t.Fatal(err) }; if absent.Present || absent.Device!="" || absent.Inode!="" || absent.BytesBase64!="" { t.Fatalf("unexpected absent log: %+v",absent) }
 path:=filepath.Join(dir,"high_level_log.jsonl")
 if err:=os.WriteFile(path,nil,0600); err!=nil { t.Fatal(err) }
 empty,err:=captureLogObservation(dir); if err!=nil { t.Fatal(err) }; if !empty.Present || empty.Inode=="" || empty.BytesBase64!="" { t.Fatalf("unexpected empty log: %+v",empty) }
 data:=[]byte{'x',0xff}; if err:=os.WriteFile(path,data,0600); err!=nil { t.Fatal(err) }
 observed,err:=captureLogObservation(dir); if err!=nil { t.Fatal(err) }; decoded,err:=base64.StdEncoding.DecodeString(observed.BytesBase64); if err!=nil || string(decoded)!=string(data) { t.Fatalf("lost bytes: %x %v",decoded,err) }
 if err:=os.Rename(path,path+".old"); err!=nil { t.Fatal(err) }; if err:=os.WriteFile(path,data,0600); err!=nil { t.Fatal(err) }
 replaced,err:=captureLogObservation(dir); if err!=nil { t.Fatal(err) }; if replaced.Inode==observed.Inode { t.Fatal("replacement identity lost") }
 if _,err:=os.Stat(filepath.Join(dir,"ledger.db")); !os.IsNotExist(err) { t.Fatalf("unexpected database: %v",err) }
}
