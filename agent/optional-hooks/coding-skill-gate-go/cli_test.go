package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"os/exec"
	"testing"
)

// TestDescribe advertises the events the optional omp bridge forwards.
//
// Example: direct writes are preflighted and successful reads update evidence.
func TestDescribe(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "describe").CombinedOutput()
	if err != nil { t.Fatalf("describe failed: %v: %s", err, out) }
	var description struct { CallTools []string `json:"callTools"`; ResultTools []string `json:"resultTools"` }
	if err := json.Unmarshal(out, &description); err != nil { t.Fatalf("invalid description: %v: %s", err, out) }
	if len(description.CallTools) == 0 || len(description.ResultTools) == 0 { t.Fatalf("missing event descriptions: %s", out) }
}

// TestReduceCheckRoundTrip verifies the real JSON protocol and state transfer.
//
// Example: two completed reads allow a later Go write while Python remains denied.
func TestReduceCheckRoundTrip(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"common","go","python"} {
		dir := filepath.Join(root,"coding-"+name)
		if err := os.MkdirAll(dir,0700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir,"SKILL.md"),[]byte(name+"\n"),0600); err != nil { t.Fatal(err) }
	}
	payload := cliInput{SkillsRoot:root,CWD:t.TempDir(),Results:[]cliEvent{
		{ToolName:"read",Input:map[string]any{"path":"skill://coding-common"},Details:map[string]any{"displayContent":map[string]any{"text":"common","startLine":1}}},
		{ToolName:"read",Input:map[string]any{"path":"skill://coding-go"},Details:map[string]any{"displayContent":map[string]any{"text":"go","startLine":1}}},
	}}
	data, err := json.Marshal(payload)
	if err != nil { t.Fatal(err) }
	var output bytes.Buffer
	if err := run([]string{"reduce"},bytes.NewReader(data),&output); err != nil { t.Fatal(err) }
	var reduced struct{ State State `json:"state"` }
	if err := json.Unmarshal(output.Bytes(),&reduced); err != nil { t.Fatal(err) }
	if !reduced.State.Reads["coding-common"] || !reduced.State.Reads["coding-go"] { t.Fatalf("missing reduced reads: %+v",reduced.State) }
	for _, tc := range []struct{ path string; blocked bool }{{"a.go",false},{"a.py",true}} {
		check := cliInput{SkillsRoot:root,CWD:payload.CWD,State:reduced.State,Call:cliEvent{ToolName:"write",Input:map[string]any{"path":tc.path}}}
		data, err := json.Marshal(check)
		if err != nil { t.Fatal(err) }
		output.Reset()
		if err := run([]string{"check"},bytes.NewReader(data),&output); err != nil { t.Fatal(err) }
		var verdict struct{ Block bool `json:"block"` }
		if err := json.Unmarshal(output.Bytes(),&verdict); err != nil { t.Fatal(err) }
		if verdict.Block != tc.blocked { t.Fatalf("%s: got blocked=%t; want %t",tc.path,verdict.Block,tc.blocked) }
	}
}
