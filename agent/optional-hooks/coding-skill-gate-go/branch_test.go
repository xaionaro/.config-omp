package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBranchHydrationRequiresPairedSuccessfulWholeReads checks transcript recovery.
//
// Example: an errored Go read in a branch never credits that skill.
func TestBranchHydrationRequiresPairedSuccessfulWholeReads(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"common", "go"} {
		dir := filepath.Join(root,"coding-"+name)
		if err := os.MkdirAll(dir,0700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(filepath.Join(dir,"SKILL.md"),[]byte(name+"\n"),0600); err != nil { t.Fatal(err) }
	}
	branch := []map[string]any{
		{"type":"message", "message":map[string]any{"role":"assistant", "content":[]any{
			map[string]any{"type":"toolCall", "name":"read", "id":"c", "arguments":map[string]any{"path":"skill://coding-common"}},
			map[string]any{"type":"toolCall", "name":"read", "id":"g", "arguments":map[string]any{"path":"skill://coding-go"}},
		}}},
		{"type":"message", "message":map[string]any{"role":"toolResult", "toolName":"read", "toolCallId":"c", "details":map[string]any{"displayContent":map[string]any{"startLine":float64(1),"text":"common"}}}},
		{"type":"message", "message":map[string]any{"role":"toolResult", "toolName":"read", "toolCallId":"g", "isError":true, "details":map[string]any{"displayContent":map[string]any{"startLine":float64(1),"text":"go"}}}},
	}
	req := Request{Action:"call",Tool:"write",Input:map[string]any{"path":"test.go"}, CWD:t.TempDir(),SkillsRoot:root,Branch:branch}
	response, err := decide(req)
	if err != nil || !strings.Contains(response.Reason,"coding-go") || strings.Contains(response.Reason,"coding-common") { t.Fatalf("bad branch result %+v %v",response,err) }
	message := asMap(branch[2]["message"])
	message["isError"] = false
	response, err = decide(req)
	if err != nil || response.Reason != "" { t.Fatalf("complete branch denied %+v %v",response,err) }
}
