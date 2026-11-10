package main

import (
	"strings"
	"testing"
)

// completeRead models host read metadata for a complete two-line skill.
//
// Example: both line numbers and totalLines describe the entire result.
func completeRead() map[string]any {
	return map[string]any{"totalLines": float64(2), "displayContent": map[string]any{"startLine": float64(1), "lineNumbers": []any{float64(1), float64(2)}, "text": "first\nsecond"}}
}

// TestDirectWritesRequirePriorReads covers both ordinary editing tools.
//
// Example: Go writes remain denied until common and Go reads finish.
func TestDirectWritesRequirePriorReads(t *testing.T) {
	state := State{}
	for _, tc := range []struct {
		tool  string
		input map[string]any
	}{
		{"write", map[string]any{"path": "file.go"}},
		{"edit", map[string]any{"input": "*** Begin Patch\n[file.go#ABCD]\nCUT 1.=1\n*** End Patch"}},
	} {
		got, err := decide(Request{Action: "call", Tool: tc.tool, Input: tc.input, State: state})
		if err != nil || !strings.Contains(got.Reason, "skill://coding-common") || !strings.Contains(got.Reason, "skill://coding-go") {
			t.Fatalf("%s denied incorrectly: %+v %v", tc.tool, got, err)
		}
	}
	for _, name := range []string{"coding-common", "coding-go"} {
		got, err := decide(Request{Action: "result", Tool: "read", Input: map[string]any{"path": "skill://" + name}, Details: completeRead(), State: state})
		if err != nil {
			t.Fatal(err)
		}
		state = got.State
	}
	for _, tc := range []struct {
		tool  string
		input map[string]any
	}{
		{"write", map[string]any{"path": "file.go"}},
		{"edit", map[string]any{"input": "*** Begin Patch\n[file.go#ABCD]\nCUT 1.=1\n*** End Patch"}},
	} {
		got, err := decide(Request{Action: "call", Tool: tc.tool, Input: tc.input, State: state})
		if err != nil || got.Reason != "" {
			t.Fatalf("%s blocked after reads: %+v %v", tc.tool, got, err)
		}
	}
	got, err := decide(Request{Action: "call", Tool: "write", Input: map[string]any{"path": "a.py"}, State: state})
	if err != nil || !strings.Contains(got.Reason, "skill://coding-python") {
		t.Fatalf("Python read not required: %+v %v", got, err)
	}
}

// TestSkillEvidenceRejectsPartialReads verifies host completeness metadata.
//
// Example: a raw range is not a complete skill read.
func TestSkillEvidenceRejectsPartialReads(t *testing.T) {
	for _, path := range []string{"skill://coding-go:1-2", "skill://coding-go:raw:1-2", "/tmp/coding-go/SKILL.md"} {
		if got := verifiedSkill(map[string]any{"path": path}, completeRead()); got != "" {
			t.Fatalf("%s credited as %s", path, got)
		}
	}
	partial := completeRead()
	partial["totalLines"] = float64(3)
	if got := verifiedSkill(map[string]any{"path": "skill://coding-go"}, partial); got != "" {
		t.Fatalf("partial credited: %s", got)
	}
	truncated := completeRead()
	truncated["truncation"] = map[string]any{"truncated": true}
	if got := verifiedSkill(map[string]any{"path": "skill://coding-go"}, truncated); got != "" {
		t.Fatalf("truncation credited: %s", got)
	}
	for _, path := range []string{"skill://coding-go", "skill://coding-go/SKILL.md"} {
		if got := verifiedSkill(map[string]any{"path": path}, completeRead()); got != "coding-go" {
			t.Fatalf("%s not credited: %s", path, got)
		}
	}
}

// TestRawSkillRead accepts the host's trailing empty line in raw output.
//
// Example: totalLines excludes the final newline but lineNumbers includes it.
func TestRawSkillRead(t *testing.T) {
	details := map[string]any{"raw": true, "totalLines": float64(2), "displayContent": map[string]any{"startLine": float64(1), "lineNumbers": []any{float64(1), float64(2), float64(3)}, "text": "first\nsecond\n"}}
	if got := verifiedSkill(map[string]any{"path": "skill://coding-go:raw"}, details); got != "coding-go" {
		t.Fatalf("complete raw read not credited: %s", got)
	}
}

// TestBranchHydrationRequiresSuccessfulPairs restores only the active transcript.
//
// Example: a failed read cannot authorize a subsequent edit.
func TestBranchHydrationRequiresSuccessfulPairs(t *testing.T) {
	branch := []map[string]any{
		{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "name": "read", "id": "1", "arguments": map[string]any{"path": "skill://coding-common"}}, map[string]any{"type": "toolCall", "name": "read", "id": "2", "arguments": map[string]any{"path": "skill://coding-go"}}}}},
		{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "1", "details": completeRead()}},
		{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "read", "toolCallId": "2", "details": completeRead(), "isError": true}},
	}
	got, err := decide(Request{Action: "call", Tool: "write", Input: map[string]any{"path": "a.go"}, Branch: branch})
	if err != nil || strings.Contains(got.Reason, "coding-common") || !strings.Contains(got.Reason, "coding-go") {
		t.Fatalf("incorrect hydration: %+v %v", got, err)
	}
}
