package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIncompleteSkillMetadataNeverCreditsRead checks all reported truncation forms.
//
// Example: an apparently successful summarized read cannot authorize a write.
func TestIncompleteSkillMetadataNeverCreditsRead(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root,"coding-common")
	if err := os.MkdirAll(dir,0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir,"SKILL.md"),[]byte("common\n"),0600); err != nil { t.Fatal(err) }
	input := map[string]any{"path":"skill://coding-common"}
	display := map[string]any{"text":"common", "startLine":float64(1)}
	cases := []map[string]any{
		{"displayContent":display,"summary":map[string]any{"summarized":true}},
		{"displayContent":display,"truncation":map[string]any{"truncated":true}},
		{"displayContent":display,"meta":map[string]any{"truncation":map[string]any{"truncated":true}}},
		{"displayContent":display,"meta":map[string]any{"limits":map[string]any{"maxLines":1}}},
		{"displayContent":map[string]any{"text":"common","startLine":float64(2)}},
		{"displayContent":map[string]any{"text":"common","startLine":float64(1),"lineNumbers":[]any{float64(2)}}},
	}
	for _, details := range cases {
		got, err := verifiedSkill(input,details,"/",root)
		if err != nil || got != "" { t.Fatalf("incomplete result credited: %v %q %v",details,got,err) }
	}
	got, err := verifiedSkill(input,map[string]any{"displayContent":display},"/",root)
	if err != nil || got != "coding-common" { t.Fatalf("complete read not credited: %q %v",got,err) }
}

// TestMissingLanguageSkillDoesNotInventRequirement keeps uninstalled languages ordinary.
//
// Example: with no Python skill, a Python write is not subject to this gate.
func TestMissingLanguageSkillDoesNotInventRequirement(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"common","go"} {
		dir := filepath.Join(root,"coding-"+name)
		if err := os.MkdirAll(dir,0700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir,"SKILL.md"),[]byte(name+"\n"),0600); err != nil { t.Fatal(err) }
	}
	req := Request{Action:"call",Tool:"write",CWD:t.TempDir(),SkillsRoot:root,Input:map[string]any{"path":"a.py"}}
	got, err := decide(req)
	if err != nil || got.Reason != "" { t.Fatalf("missing Python skill blocked: %+v %v",got,err) }
	req.Input["path"] = "a.go"
	got, err = decide(req)
	if err != nil || !strings.Contains(got.Reason,"coding-go") { t.Fatalf("installed Go skill not checked: %+v %v",got,err) }
}
