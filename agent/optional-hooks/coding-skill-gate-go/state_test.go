package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestASTPreviewStack retains unresolved previews through failed or unrelated writes.
//
// Example: rejecting the newest preview leaves the prior Go preview protected.
func TestASTPreviewStack(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"common", "go", "python"} {
		dir := filepath.Join(root,"coding-"+name)
		if err := os.MkdirAll(dir,0700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir,"SKILL.md"),[]byte(name+"\n"),0600); err != nil { t.Fatal(err) }
	}
	req := Request{Action:"result",Tool:"ast_edit",CWD:t.TempDir(),SkillsRoot:root, Details:map[string]any{"files":[]any{"first.go"}}}
	first, err := decide(req)
	if err != nil { t.Fatal(err) }
	req.State = first.State
	req.Details = map[string]any{"files":[]any{"second.py"}}
	second, err := decide(req)
	if err != nil || len(second.State.Pending) != 2 { t.Fatalf("preview stack: %+v %v", second.State, err) }
	req.State, req.Tool, req.Input = second.State, "write", map[string]any{"path":"xd://reject"}
	req.Details = map[string]any{"xdev":map[string]any{"inner":map[string]any{"sourceToolName":"other", "action":"discard"}}}
	unrelated, err := decide(req)
	if err != nil || len(unrelated.State.Pending) != 2 { t.Fatalf("unrelated result changed stack: %+v %v", unrelated.State, err) }
	req.State, req.IsError = unrelated.State, true
	req.Details = map[string]any{"xdev":map[string]any{"inner":map[string]any{"sourceToolName":"ast_edit", "action":"discard"}}}
	resolved, err := decide(req)
	if err != nil || len(resolved.State.Pending) != 1 { t.Fatalf("consumed preview not retired: %+v %v", resolved.State, err) }
	req.State, req.Action, req.Input, req.IsError = resolved.State, "call", map[string]any{"path":"xd://resolve"}, false
	denied, err := decide(req)
	if err != nil || !strings.Contains(denied.Reason,"first.go") || strings.Contains(denied.Reason,"second.py") { t.Fatalf("wrong preview denied: %+v %v", denied, err) }
}

// TestWholeRawReadRequiresExactContent rejects a missing final newline or a range.
//
// Example: skill://coding-go:raw is credited only with all bytes displayed.
func TestWholeRawReadRequiresExactContent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root,"coding-go")
	if err := os.MkdirAll(dir,0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir,"SKILL.md"),[]byte("go skill\n"),0600); err != nil { t.Fatal(err) }
	input := map[string]any{"path":"skill://coding-go:raw"}
	details := map[string]any{"raw":true,"displayContent":map[string]any{"text":"go skill", "startLine":float64(1)}}
	if got,err := verifiedSkill(input,details,"/",root); err != nil || got != "" { t.Fatalf("short raw content credited: %q %v",got,err) }
	details["displayContent"] = map[string]any{"text":"go skill\n", "startLine":float64(1)}
	if got,err := verifiedSkill(input,details,"/",root); err != nil || got != "coding-go" { t.Fatalf("whole raw read rejected: %q %v",got,err) }
	input["path"] = "skill://coding-go:raw:1-2"
	if got,err := verifiedSkill(input,details,"/",root); err != nil || got != "" { t.Fatalf("raw range credited: %q %v",got,err) }
}
