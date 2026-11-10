package main

import (
	"testing"

	"github.com/stretchr/testify/require"
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
		require.NoError(t, err)
		require.Contains(t, got.Reason, "skill://coding-common")
		require.Contains(t, got.Reason, "skill://coding-go")
	}
	for _, name := range []string{"coding-common", "coding-go"} {
		got, err := decide(Request{Action: "result", Tool: "read", Input: map[string]any{"path": "skill://" + name}, Details: completeRead(), State: state})
		require.NoError(t, err)
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
		require.NoError(t, err)
		require.Empty(t, got.Reason)
	}
	got, err := decide(Request{Action: "call", Tool: "write", Input: map[string]any{"path": "a.py"}, State: state})
	require.NoError(t, err)
	require.Contains(t, got.Reason, "skill://coding-python")
}

// TestSkillEvidenceRejectsPartialReads verifies host completeness metadata.
//
// Example: a raw range is not a complete skill read.
func TestSkillEvidenceRejectsPartialReads(t *testing.T) {
	for _, path := range []string{"skill://coding-go:1-2", "skill://coding-go:raw:1-2", "/tmp/coding-go/SKILL.md"} {
		require.Empty(t, verifiedSkill(map[string]any{"path": path}, completeRead()), path)
	}
	partial := completeRead()
	partial["totalLines"] = float64(3)
	require.Empty(t, verifiedSkill(map[string]any{"path": "skill://coding-go"}, partial))
	truncated := completeRead()
	truncated["truncation"] = map[string]any{"truncated": true}
	require.Empty(t, verifiedSkill(map[string]any{"path": "skill://coding-go"}, truncated))
	for _, path := range []string{"skill://coding-go", "skill://coding-go/SKILL.md"} {
		require.Equal(t, skillGo, verifiedSkill(map[string]any{"path": path}, completeRead()), path)
	}
}

// TestRawSkillRead accepts the host's trailing empty line in raw output.
//
// Example: totalLines excludes the final newline but lineNumbers includes it.
func TestRawSkillRead(t *testing.T) {
	details := map[string]any{"raw": true, "totalLines": float64(2), "displayContent": map[string]any{"startLine": float64(1), "lineNumbers": []any{float64(1), float64(2), float64(3)}, "text": "first\nsecond\n"}}
	require.Equal(t, skillGo, verifiedSkill(map[string]any{"path": "skill://coding-go:raw"}, details))
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
	require.NoError(t, err)
	require.NotContains(t, got.Reason, "coding-common")
	require.Contains(t, got.Reason, "coding-go")
}
