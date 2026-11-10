package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGateReadEvidence checks complete successful reads and prior-batch enforcement.
//
// Example: a Go write is denied until both applicable skills have been read.
func TestGateReadEvidence(t *testing.T) {
	root := t.TempDir()
	for _, skill := range []string{"coding-common", "coding-go", "coding-python"} {
		dir := filepath.Join(root, skill)
		if err := os.MkdirAll(dir, 0700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill+"\n"), 0600); err != nil { t.Fatal(err) }
	}
	request := Request{SkillsRoot: root, CWD: t.TempDir(), State: State{}, Action: "call", Tool: "write", Input: map[string]any{"path":"file.go"}}
	response, err := decide(request)
	if err != nil || !strings.Contains(response.Reason, "coding-common") || !strings.Contains(response.Reason, "coding-go") { t.Fatalf("initial response: %+v %v", response, err) }
	request.State = response.State
	request.Action, request.Tool = "result", "read"
	request.Input = map[string]any{"path":"skill://coding-common:1-2"}
	request.Details = map[string]any{"displayContent":map[string]any{"text":"coding-common", "startLine":float64(1)}}
	response, err = decide(request)
	if err != nil || response.State.Reads["coding-common"] { t.Fatalf("partial read credited: %+v %v", response, err) }
	request.State, request.Input = response.State, map[string]any{"path":"skill://coding-common"}
	response, err = decide(request)
	if err != nil || !response.State.Reads["coding-common"] { t.Fatalf("common read missing: %+v %v", response, err) }
	request.State, request.Input = response.State, map[string]any{"path":"skill://coding-go"}
	request.Details = map[string]any{"displayContent":map[string]any{"text":"coding-go", "startLine":float64(1)}}
	response, err = decide(request)
	if err != nil || !response.State.Reads["coding-go"] { t.Fatalf("Go read missing: %+v %v", response, err) }
	request.State, request.Action, request.Tool, request.Input = response.State, "call", "write", map[string]any{"path":"file.go"}
	response, err = decide(request)
	if err != nil || response.Reason != "" { t.Fatalf("qualified write denied: %+v %v", response, err) }
	request.Input = map[string]any{"path":"file.py"}
	response, err = decide(request)
	if err != nil || !strings.Contains(response.Reason,"coding-python") || strings.Contains(response.Reason,"coding-common") { t.Fatalf("Python write: %+v %v", response, err) }
}

// TestResolvedSkillPath accepts host-resolved home paths without trusting ranges.
//
// Example: a whole read of ~/skills/coding-go/SKILL.md credits coding-go.
func TestResolvedSkillPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "coding-go")
	if err := os.MkdirAll(dir, 0700); err != nil { t.Fatal(err) }
	file := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(file, []byte("go skill\n"), 0600); err != nil { t.Fatal(err) }
	details := map[string]any{"resolvedPath": file, "displayContent": map[string]any{"text":"go skill", "startLine":float64(1)}}
	got, err := verifiedSkill(map[string]any{"path":"~/skills/coding-go/SKILL.md"}, details, t.TempDir(), root)
	if err != nil || got != "coding-go" { t.Fatalf("resolved skill: %q, %v", got, err) }
	got, err = verifiedSkill(map[string]any{"path":"~/skills/coding-go/SKILL.md:1-2"}, details, t.TempDir(), root)
	if err != nil || got != "" { t.Fatalf("partial read credited: %q, %v", got, err) }
}

// TestLanguageForHostPath accepts hashline and file-backed target syntax.
//
// Example: [src/a.go#ABCD] is a Go target, while notes.md is not.
func TestLanguageForHostPath(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"common", "go", "python"} {
		dir := filepath.Join(root, "coding-"+name)
		if err := os.MkdirAll(dir, 0700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name), 0600); err != nil { t.Fatal(err) }
	}
	for _, tc := range []struct{ path, want string }{
		{"[src/a.go#ABCD]", "go"},
		{"local://a.go:raw", "go"},
		{"vault://a.py", "python"},
		{"archive.zip:src/a.go", "go"},
		{"notes.md", ""},
	} {
		got, err := languageForPath(tc.path, root)
		if err != nil || got != tc.want { t.Errorf("%q: got %q, %v; want %q", tc.path, got, err, tc.want) }
	}
}
