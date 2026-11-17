package main

import (
 "bytes"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

// TestCapturePagesRetainBytes checks immutable paged delivery and bounds.
//
// Example: a long JSON string is recovered after the source changes.
func TestCapturePagesRetainBytes(t *testing.T) {
 t.Parallel()
 store := newCaptureStore()
 defer store.close()
 f, err := os.CreateTemp(t.TempDir(), "snapshot-")
 if err != nil { t.Fatal(err) }
 if err = os.Remove(f.Name()); err != nil { t.Fatal(err) }
 data := append(bytes.Repeat([]byte("a"), 93), []byte("😀")...)
 if _, err = f.Write(data); err != nil { t.Fatal(err) }
 token, err := store.publish(f, int64(len(data)))
 if err != nil { t.Fatal(err) }
 var got []byte
 for i := int64(0); i < capturePages(int64(len(data))); i++ {
  page, err := store.page(token, i)
  if err != nil { t.Fatal(err) }
  got = append(got, page.Data...)
  again, err := store.page(token, i)
  if err != nil || again != page { t.Fatalf("page not stable: %v",err) }
  third, err := store.page(token, i)
  if err != nil || third != page { t.Fatalf("third page read changed: %v",err) }
  if i==1 && (page.Start!=int64(len(data)) || page.End!=int64(len(data)) || page.Data!="") { t.Fatalf("final crossed-rune page: %#v",page) }
 }
 if !bytes.Equal(got, data) { t.Fatal("snapshot bytes changed") }
 if _, err := store.page(token, 999); err == nil { t.Fatal("accepted invalid page") }
 if _, err := store.page("missing", 0); err == nil { t.Fatal("accepted unknown token") }
}

// TestCaptureCompactsAcrossWrites preserves numeric and escape spellings.
//
// Example: an escaped quote split between writes remains inside its string.
func TestCaptureCompactsAcrossWrites(t *testing.T) {
 t.Parallel()
 var out bytes.Buffer
 compact:=captureJSONCompactor{out:&out}
 for _,part:=range []string{` { "n" : 1`, `e400, "s" : "a\\`, `\" b", "i":123456789012345678901234567890 } `} {
  if _,err:=compact.Write([]byte(part)); err!=nil { t.Fatal(err) }
 }
 want:=`{"n":1e400,"s":"a\\\" b","i":123456789012345678901234567890}`
 if out.String()!=want { t.Fatalf("got %q want %q",out.String(),want) }
}

// TestCaptureRejectsExpiredToken checks that pages never silently recapture.
//
// Example: a restarted or expired token returns an error.
func TestCaptureRejectsExpiredToken(t *testing.T) {
 t.Parallel()
 store:=newCaptureStore()
 f,err:=os.CreateTemp(t.TempDir(),"capture-"); if err!=nil { t.Fatal(err) }
 if err:=os.Remove(f.Name()); err!=nil { t.Fatal(err) }
 if _,err:=f.WriteString("hello"); err!=nil { t.Fatal(err) }
 token,err:=store.publish(f,5); if err!=nil { t.Fatal(err) }
 if err:=store.close(); err!=nil { t.Fatal(err) }
 if _,err:=store.captureResource("capture://snapshot/"+token+"/0/1"); err==nil { t.Fatal("closed token accepted") }
}

// TestCaptureFrameProvidesRecoverableCoverage verifies batched and single-chunk reads agree.
//
// Example: retrying one lost chunk never captures a newer observation.
func TestCaptureFrameProvidesRecoverableCoverage(t *testing.T) {
 t.Parallel()
 store:=newCaptureStore(); defer store.close()
 f,err:=os.CreateTemp(t.TempDir(),"capture-"); if err!=nil { t.Fatal(err) }
 if err:=os.Remove(f.Name()); err!=nil { t.Fatal(err) }
 data:=strings.Repeat("x",100)+"界"+strings.Repeat("y",100)
 if _,err:=f.WriteString(data); err!=nil { t.Fatal(err) }
 token,err:=store.publish(f,int64(len(data))); if err!=nil { t.Fatal(err) }
 uri:="capture://snapshot/"+token+"/0/16"
 frame,err:=store.captureResource(uri); if err!=nil { t.Fatal(err) }
 if !strings.HasPrefix(frame,"FRAME|"+token+"|0|3|203\n") || !strings.Contains(frame,"\nEND|3|203|true") { t.Fatalf("invalid frame: %s",frame) }
 single,err:=store.captureResource("capture://snapshot/"+token+"/1/1"); if err!=nil { t.Fatal(err) }
 line:=strings.Split(single,"\n")[1]
 if !strings.Contains(frame,line+"\n") { t.Fatalf("same-token retry differs: %s",single) }
 if _,err:=store.captureResource("capture://snapshot/"+token+"/0/17"); err==nil { t.Fatal("oversized frame accepted") }
}

// TestCaptureFramePreservesLexicalSeams reconstructs exact JSON across frame boundaries.
//
// Example: an exponent or escaped quote may straddle two chunks.
func TestCaptureFramePreservesLexicalSeams(t *testing.T) {
 t.Parallel()
 data:=`{"text":"`+strings.Repeat("a",75)+`\\\" tail","number":1e400,"large":123456789012345678901234567890}`
 store:=newCaptureStore(); defer store.close()
 f,err:=os.CreateTemp(t.TempDir(),"capture-"); if err!=nil { t.Fatal(err) }
 if err:=os.Remove(f.Name()); err!=nil { t.Fatal(err) }
 if _,err:=f.WriteString(data); err!=nil { t.Fatal(err) }
 token,err:=store.publish(f,int64(len(data))); if err!=nil { t.Fatal(err) }
 frame,err:=store.captureResource("capture://snapshot/"+token+"/0/16"); if err!=nil { t.Fatal(err) }
 lines:=strings.Split(frame,"\n"); var reconstructed strings.Builder
 for _,line:=range lines[1:len(lines)-1] {
  fields:=strings.SplitN(line,"|",5)
  if len(fields)!=5 || fields[0]!="CHUNK" { t.Fatalf("bad chunk: %q",line) }
  reconstructed.WriteString(fields[4])
 }
 if reconstructed.String()!=data { t.Fatalf("lost lexical data: %q",reconstructed.String()) }
 if !json.Valid([]byte(reconstructed.String())) { t.Fatal("reconstructed JSON invalid") }
}

// TestCaptureRejectsInvalidUTF8 checks the unpublished spill boundary.
//
// Example: malformed bytes cannot be offered as a valid snapshot token.
func TestCaptureRejectsInvalidUTF8(t *testing.T) {
 t.Parallel()
 f,err:=os.CreateTemp(t.TempDir(),"capture-"); if err!=nil { t.Fatal(err) }; defer f.Close()
 if _,err:=f.Write([]byte{0xff}); err!=nil { t.Fatal(err) }
 if err:=validateCaptureUTF8(f); err==nil { t.Fatal("invalid UTF-8 accepted") }
}

// TestCaptureUninitializedDoesNotCreateStore checks that reads have no initialization side effect.
//
// Example: an empty session requires explicit native initialization.
func TestCaptureUninitializedDoesNotCreateStore(t *testing.T) {
 t.Parallel()
 dir := t.TempDir()
 var out bytes.Buffer
 if err:=writeLedgerCaptureDir(dir,"session",&out); err==nil || out.Len()!=0 { t.Fatalf("uninitialized capture: %v, %q",err,out.String()) }
 if _,err:=os.Stat(filepath.Join(dir,"ledger.db")); !os.IsNotExist(err) { t.Fatalf("unexpected database: %v",err) }
}

// TestCaptureCapacityCountsPending checks the reservation boundary.
//
// Example: incomplete captures cannot overcommit the descriptor budget.
func TestCaptureCapacityCountsPending(t *testing.T) {
 t.Parallel()
 store := newCaptureStore()
 for range captureLimit {
  if err := store.reserve(); err != nil { t.Fatal(err) }
 }
 if err := store.reserve(); err == nil { t.Fatal("accepted capture beyond capacity") }
 store.releaseReservation()
 if err := store.reserve(); err != nil { t.Fatal(err) }
}

// TestCaptureActivePreservesPresence checks native present-empty collections.
//
// Example: an empty forecast is present rather than absent.
func TestCaptureActivePreservesPresence(t *testing.T) {
 t.Parallel()
 dir := t.TempDir()
 db:=createTestLedger(t,dir)
 if _,err:=db.Exec("UPDATE ledger_meta SET session_id='session',ledger_present=1,forecasts_present=1 WHERE id=1"); err!=nil { t.Fatal(err) }
 if err:=db.Close(); err!=nil { t.Fatal(err) }
 var out bytes.Buffer
 if err := writeLedgerCaptureDir(dir,"session",&out); err != nil { t.Fatal(err) }
 var snapshot struct {
  DatabaseID string `json:"database_id"`
  Generation string `json:"generation"`
  Ledger struct { Present bool `json:"present"`; Sections map[string]json.RawMessage `json:"sections"`; Records map[string]json.RawMessage `json:"records"` } `json:"ledger"`
  Forecasts struct { Present bool `json:"present"`; Records map[string]json.RawMessage `json:"records"` } `json:"forecasts"`
  Report struct { Present bool `json:"present"` } `json:"report"`
 }
 if err := json.Unmarshal(out.Bytes(),&snapshot); err != nil { t.Fatal(err) }
 if snapshot.DatabaseID=="" || snapshot.Generation!="0" { t.Fatalf("missing exact CAS token: %s",out.Bytes()) }
 if !snapshot.Ledger.Present || snapshot.Ledger.Sections == nil || snapshot.Ledger.Records == nil || !snapshot.Forecasts.Present || snapshot.Forecasts.Records == nil || snapshot.Report.Present { t.Fatalf("wrong presence: %s",out.Bytes()) }
 out.Reset()
 if err:=writeLedgerCaptureDir(dir,"other",&out); err==nil || out.Len()!=0 { t.Fatalf("wrong identity accepted: %v, %q",err,out.String()) }
}

// TestCaptureReportOnlyPreservesAbsence checks independent report ownership.
//
// Example: an initialized report does not imply a ledger or forecast exists.
func TestCaptureReportOnlyPreservesAbsence(t *testing.T) {
 t.Parallel()
 dir:=t.TempDir()
 db:=createTestLedger(t,dir)
 if _,err:=db.Exec("UPDATE ledger_meta SET session_id='session' WHERE id=1"); err!=nil { t.Fatal(err) }
 if _,err:=db.Exec("INSERT INTO report(id,document_json,revision) VALUES(1,?,1)",`{"state":"active"}`); err!=nil { t.Fatal(err) }
 if err:=db.Close(); err!=nil { t.Fatal(err) }
 var out bytes.Buffer
 if err:=writeLedgerCaptureDir(dir,"session",&out); err!=nil { t.Fatal(err) }
 var snapshot struct {
  Ledger struct { Present bool `json:"present"` } `json:"ledger"`
  Forecasts struct { Present bool `json:"present"` } `json:"forecasts"`
  Report struct { Present bool `json:"present"`; Document json.RawMessage `json:"document"`; Revision *int64 `json:"revision"` } `json:"report"`
 }
 if err:=json.Unmarshal(out.Bytes(),&snapshot); err!=nil { t.Fatal(err) }
 if snapshot.Ledger.Present || snapshot.Forecasts.Present || !snapshot.Report.Present || snapshot.Report.Revision==nil || string(snapshot.Report.Document)!=`{"state":"active"}` { t.Fatalf("wrong report state: %s",out.Bytes()) }
}
