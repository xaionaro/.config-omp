package codingskill

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
	require.NoError(t, Run([]string{"reduce"}, bytes.NewReader(request), &output))
	var wire struct {
		State struct {
			Reads map[string]bool `json:"reads"`
		} `json:"state"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &wire))
	require.Equal(t, map[string]bool{"coding-common": true, "coding-go": true}, wire.State.Reads)
	var reduced struct {
		State State `json:"state"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &reduced))
	request, err = json.Marshal(cliInput{State: reduced.State, Call: cliEvent{ToolName: "write", Input: map[string]any{"path": "a.go"}}})
	require.NoError(t, err)
	output.Reset()
	require.NoError(t, Run([]string{"check"}, bytes.NewReader(request), &output))
	var verdict struct {
		Block bool `json:"block"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &verdict))
	require.False(t, verdict.Block)
}

// TestDescribeListsCodeExtensions keeps the TS prefilter on the Go extension map.
//
// Example: describe lists ".cpp" because skillForPath maps it to coding-common.
func TestDescribeListsCodeExtensions(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, Run([]string{"describe"}, nil, &output))
	var described struct {
		CodeExtensions []string `json:"codeExtensions"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &described))
	require.NotEmpty(t, codeExtensions)
	keys := make([]string, 0, len(codeExtensions))
	for ext := range codeExtensions {
		keys = append(keys, ext)
	}
	require.ElementsMatch(t, keys, described.CodeExtensions)
	require.Contains(t, described.CodeExtensions, ".cpp")
}
