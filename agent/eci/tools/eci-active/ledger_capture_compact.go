package main

import (
 "io"
)

// captureJSONCompactor removes only insignificant JSON whitespace across writes.
//
// Example: spaces and escape spellings inside strings remain unchanged.
type captureJSONCompactor struct {
 out io.Writer
 inString bool
 escaped bool
}

// Write streams compacted JSON while retaining string and escape state.
//
// Example: a backslash at the end of one write protects the next byte.
func (c *captureJSONCompactor) Write(p []byte) (int,error) {
 start:=0
 for i,b:=range p {
  if c.inString {
   switch { case c.escaped: c.escaped=false; case b=='\\': c.escaped=true; case b=='"': c.inString=false }
   continue
  }
  if b=='"' { c.inString=true; continue }
  if b!=' ' && b!='\t' && b!='\r' && b!='\n' { continue }
  if i>start { n,err:=c.out.Write(p[start:i]); if err!=nil { return start+n,err }; if n!=i-start { return start+n,io.ErrShortWrite } }
  start=i+1
 }
 if start<len(p) { n,err:=c.out.Write(p[start:]); if err!=nil { return start+n,err }; if n!=len(p)-start { return start+n,io.ErrShortWrite } }
 return len(p),nil
}
