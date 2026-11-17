package main

import (
 "bufio"
 "crypto/rand"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/url"
 "math"
 "os"
 "strconv"
 "strings"
 "sync"
 "time"
 "unicode/utf8"
)

const (
 // capturePageBytes bounds each resource response below the read tool's line limit.
 //
 // Example: each page spans 96 nominal snapshot bytes.
 capturePageBytes = 96
 // captureLimit bounds retained file descriptors, including captures in progress.
 //
 // Example: a seventeenth concurrent snapshot is rejected.
 captureLimit = 16
 // captureIdle specifies how long a snapshot survives without a page read.
 //
 // Example: each successful page extends its snapshot's lifetime.
 captureIdle = 60 * time.Second
)

// captureHandle owns an unlinked immutable snapshot until expiration.
//
// Example: pages continue to refer to the same bytes after a ledger edit.
type captureHandle struct { file *os.File; size int64; timer *time.Timer; deadline time.Time }
// captureStore serializes capture publication, paging and expiry.
//
// Example: expiry cannot close a file during a page read.
type captureStore struct { mu sync.Mutex; handles map[string]*captureHandle; pending int; cleanup error }
// capturePage is a UTF-8 aligned portion of the immutable snapshot.
//
// Example: concatenating payloads in ordinal order recovers the snapshot.
type capturePage struct { Start int64; End int64; Size int64; Pages int64; Data string }

// newCaptureStore creates an empty server-owned snapshot table.
//
// Example: a restarted server knows no earlier tokens.
func newCaptureStore() *captureStore { return &captureStore{handles: make(map[string]*captureHandle)} }
// reserve claims a slot before expensive capture work starts.
//
// Example: pending captures count toward the handle limit.
func (s *captureStore) reserve() error { s.mu.Lock(); defer s.mu.Unlock(); if s.cleanup != nil { return s.cleanup }; if len(s.handles)+s.pending >= captureLimit { return fmt.Errorf("capture capacity exhausted") }; s.pending++; return nil }
// releaseReservation releases a slot after failed capture.
//
// Example: an I/O failure does not exhaust the table permanently.
func (s *captureStore) releaseReservation() { s.mu.Lock(); defer s.mu.Unlock(); s.pending-- }
// publish transfers a completed anonymous file to the store.
//
// Example: a token is returned only after capture commits.
func (s *captureStore) publish(f *os.File, size int64) (string, error) {
 var id [16]byte
 if _, err := rand.Read(id[:]); err != nil { return "", err }
 token := hex.EncodeToString(id[:])
 s.mu.Lock(); defer s.mu.Unlock()
 if _, exists := s.handles[token]; exists { return "", fmt.Errorf("capture token collision") }
 h := &captureHandle{file:f,size:size,deadline:time.Now().Add(captureIdle)}
 s.handles[token] = h
 h.timer = time.AfterFunc(captureIdle, (&captureExpiry{store:s,token:token}).expire)
 return token,nil
}
// captureExpiry identifies a timer's snapshot without retaining request state.
//
// Example: expiration closes only the matching token.
type captureExpiry struct { store *captureStore; token string }
// expire removes and closes an idle snapshot.
//
// Example: no future page can reopen the expired file.
func (e *captureExpiry) expire() { s:=e.store; s.mu.Lock(); defer s.mu.Unlock(); h,ok:=s.handles[e.token]; if !ok { return }; if remaining:=time.Until(h.deadline); remaining>0 { h.timer=time.AfterFunc(remaining,e.expire); return }; delete(s.handles,e.token); s.cleanup=errors.Join(s.cleanup,h.file.Close()) }
// capturePages returns the ordinal count without overflowing the byte size.
//
// Example: 97 bytes occupy two nominal pages.
func capturePages(size int64) int64 { if size==0 { return 0 }; return 1+(size-1)/capturePageBytes }

// captureBoundary finds the next UTF-8 rune start at or after a nominal boundary.
//
// Example: an ordinal may start at EOF when a final rune crosses its boundary.
func captureBoundary(f *os.File, size, nominal int64) (int64,error) {
 if nominal>=size { return size,nil }
 var b [4]byte
 n:=min(int64(len(b)),size-nominal)
 if _,err:=f.ReadAt(b[:n],nominal); err!=nil { return 0,err }
 for i:=int64(0); i<n; i++ { if utf8.RuneStart(b[i]) { return nominal+i,nil } }
 if nominal+n==size && n<4 { return size,nil }
 return 0,fmt.Errorf("invalid UTF-8 boundary")
}

// page reads one UTF-8 aligned slice and renews its idle deadline.
//
// Example: an invalid ordinal never overflows its byte offset.
func (s *captureStore) page(token string, index int64) (capturePage,error) {
 s.mu.Lock(); defer s.mu.Unlock()
 h,ok:=s.handles[token]; if !ok { return capturePage{},fmt.Errorf("unknown or expired capture") }
 if !time.Now().Before(h.deadline) { h.timer.Stop(); delete(s.handles,token); err:=h.file.Close(); s.cleanup=errors.Join(s.cleanup,err); return capturePage{},errors.Join(fmt.Errorf("expired capture"),err) }
 pages:=capturePages(h.size)
 if index<0 || index>=pages || index>math.MaxInt64/capturePageBytes { return capturePage{},fmt.Errorf("invalid capture page") }
 start,err:=captureBoundary(h.file,h.size,index*capturePageBytes); if err!=nil { return capturePage{},err }
 end:=h.size
 if index+1<pages { end,err=captureBoundary(h.file,h.size,(index+1)*capturePageBytes); if err!=nil { return capturePage{},err } }
 data:=make([]byte,int(end-start)); if len(data)>0 { if _,err:=h.file.ReadAt(data,start); err!=nil { return capturePage{},err } }
 if !utf8.Valid(data) { return capturePage{},fmt.Errorf("invalid UTF-8 in capture") }
 h.deadline=time.Now().Add(captureIdle); h.timer.Stop(); h.timer=time.AfterFunc(captureIdle,(&captureExpiry{store:s,token:token}).expire)
 return capturePage{Start:start,End:end,Size:h.size,Pages:pages,Data:string(data)},nil
}
// close disposes every retained descriptor and reports cleanup failures.
//
// Example: server shutdown invalidates all pages.
func (s *captureStore) close() error { s.mu.Lock(); defer s.mu.Unlock(); for token,h:=range s.handles { h.timer.Stop(); s.cleanup=errors.Join(s.cleanup,h.file.Close()); delete(s.handles,token) }; return s.cleanup }

// captureAnonymous creates an unlinked private spill before any content is written.
//
// Example: a crash leaves no named snapshot data.
func captureAnonymous() (*os.File,error) { return openPrivateSpill("eci-capture-") }

// validateCaptureUTF8 rejects malformed storage before publishing a snapshot.
//
// Example: valid replacement characters remain distinct from malformed bytes.
func validateCaptureUTF8(f *os.File) error {
 if _,err:=f.Seek(0,io.SeekStart); err!=nil { return err }
 reader:=bufio.NewReader(f)
 for {
  r,size,err:=reader.ReadRune()
  if errors.Is(err,io.EOF) { return nil }
  if err!=nil { return err }
  if r==utf8.RuneError && size==1 { return fmt.Errorf("invalid UTF-8 in capture") }
 }
}
// captureResource creates a snapshot or serves one page of a previous snapshot.
//
// Example: ledger://session-id/capture initiates a new observation.
func (s *captureStore) captureResource(uri string) (string,error) {
 u,err:=url.Parse(uri); if err!=nil { return "",err }
 switch {
 case u.Scheme=="ledger" && (u.Path=="/capture" || u.Path=="/log") && u.RawQuery=="" && u.Fragment=="":
  if err:=s.reserve(); err!=nil { return "",err }; defer s.releaseReservation()
  f,err:=captureAnonymous(); if err!=nil { return "",err }
  switch u.Path { case "/capture": err=writeLedgerCapture(u.Host,f); case "/log": err=writeLedgerLogCapture(u.Host,f) }
  if err!=nil { return "",errors.Join(err,f.Close()) }
  size,err:=f.Seek(0,io.SeekCurrent); if err!=nil { return "",errors.Join(err,f.Close()) }
  if err=validateCaptureUTF8(f); err!=nil { return "",errors.Join(err,f.Close()) }
  token,err:=s.publish(f,size); if err!=nil { return "",errors.Join(err,f.Close()) }
  return fmt.Sprintf("CAPTURE|%s|%d|%d|%d",token,size,capturePages(size),capturePageBytes),nil
 case u.Scheme=="capture" && u.Host=="snapshot" && u.RawQuery=="" && u.Fragment=="":
  parts:=strings.Split(strings.TrimPrefix(u.Path,"/"),"/"); if len(parts)!=3 { return "",fmt.Errorf("invalid capture frame URI") }
  first,err:=strconv.ParseInt(parts[1],10,64); if err!=nil { return "",fmt.Errorf("invalid capture first page: %w",err) }
  count,err:=strconv.ParseInt(parts[2],10,64); if err!=nil || count<1 || count>16 { return "",fmt.Errorf("invalid capture frame count") }
  page,err:=s.page(parts[0],first); if err!=nil { return "",err }
  end:=first+min(count,page.Pages-first)
  var frame strings.Builder
  fmt.Fprintf(&frame,"FRAME|%s|%d|%d|%d\n",parts[0],first,end-first,page.Size)
  for ordinal:=first; ordinal<end; ordinal++ {
   if ordinal!=first { page,err=s.page(parts[0],ordinal); if err!=nil { return "",err } }
   fmt.Fprintf(&frame,"CHUNK|%d|%d|%d|%s\n",ordinal,page.Start,page.End,page.Data)
  }
  fmt.Fprintf(&frame,"END|%d|%d|%t",end,page.End,end==page.Pages)
  return frame.String(),nil
 default: return "",fmt.Errorf("unknown capture resource")
 }
}
// runLedgerServe serves the selected MCP resource protocol over standard input and output.
//
// Example: configure eci-active ledger-serve as a stdio MCP server.
func runLedgerServe(args []string) (_err error) {
 if len(args)!=1 { return usageError() }
 store:=newCaptureStore(); defer func(){ _err=errors.Join(_err,store.close()) }()
 scanner:=bufio.NewScanner(os.Stdin); scanner.Buffer(make([]byte,4096),1024*1024); encoder:=json.NewEncoder(os.Stdout)
 for scanner.Scan() {
  var req struct { ID json.RawMessage `json:"id"`; Method string `json:"method"`; Params struct { URI string `json:"uri"` } `json:"params"` }
  if err:=json.Unmarshal(scanner.Bytes(),&req); err!=nil { return fmt.Errorf("decode MCP request: %w",err) }; if len(req.ID)==0 { continue }
  var result any; var failure error
  switch req.Method {
  case "initialize": result=map[string]any{"protocolVersion":"2025-03-26","serverInfo":map[string]string{"name":"eci-ledger","version":"1"},"capabilities":map[string]any{"resources":map[string]any{}}}
  case "resources/list": result=map[string]any{"resources":[]any{}}
  case "resources/templates/list": result=map[string]any{"resourceTemplates":[]any{map[string]string{"uriTemplate":"ledger://{session}/capture","name":"ledger capture"},map[string]string{"uriTemplate":"ledger://{session}/log","name":"log inventory"},map[string]string{"uriTemplate":"capture://snapshot/{token}/{first}/{count}","name":"capture frame"}}}
  case "resources/read":
   var text string; text,failure=store.captureResource(req.Params.URI)
   if failure==nil { result=map[string]any{"contents":[]any{map[string]string{"uri":req.Params.URI,"mimeType":"text/plain","text":text}}} }
  default: failure=fmt.Errorf("unsupported MCP method %s",req.Method)
  }
  response:=map[string]any{"jsonrpc":"2.0","id":req.ID}; if failure!=nil { response["error"]=map[string]any{"code":-32603,"message":failure.Error()} } else { response["result"]=result }; if err:=encoder.Encode(response); err!=nil { return err }
 }
 return scanner.Err()
}
