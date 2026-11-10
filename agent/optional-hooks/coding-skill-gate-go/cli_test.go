package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCLIRoundTrip checks real JSON state transfer between reduce and check.
//
// Example: completed common and Go reads allow a later Go write.
func TestCLIRoundTrip(t *testing.T) {
	input := cliInput{Results: []cliEvent{
		{ToolName: "read", Input: map[string]any{"path": "skill://coding-common"}, Details: completeRead()},
		{ToolName: "read", Input: map[string]any{"path": "skill://coding-go"}, Details: completeRead()},
	}}
	request, err := json.Marshal(input)
	require.NoError(t, err)
	var output bytes.Buffer
	require.NoError(t, run([]string{"reduce"}, bytes.NewReader(request), &output))
	var reduced struct {
		State State `json:"state"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &reduced))
	request, err = json.Marshal(cliInput{State: reduced.State, Call: cliEvent{ToolName: "write", Input: map[string]any{"path": "a.go"}}})
	require.NoError(t, err)
	output.Reset()
	require.NoError(t, run([]string{"check"}, bytes.NewReader(request), &output))
	var verdict struct {
		Block bool `json:"block"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &verdict))
	require.False(t, verdict.Block)
}
