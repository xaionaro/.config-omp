package codingskill

import (
	"bytes"
	"encoding/json"
	"io"
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
	var reduced struct {
		State State `json:"state"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &reduced))
	require.True(t, reduced.State.Reads.has(skillCommon))
	require.True(t, reduced.State.Reads.has(skillGo))
	require.False(t, reduced.State.Reads.has(skillPython))
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

// TestCLIAcceptsLegacyReadsObject keeps running bridges that still hold object-form state working.
//
// Example: {"reads":{"coding-common":true,"coding-go":true}} allows a Go write; without coding-go it blocks.
func TestCLIAcceptsLegacyReadsObject(t *testing.T) {
	for _, tc := range []struct {
		reads string
		block bool
	}{
		{`{"coding-common":true,"coding-go":true}`, false},
		{`{"coding-common":true}`, true},
	} {
		request := `{"state":{"reads":` + tc.reads + `,"hydrated":true},"call":{"toolName":"write","input":{"path":"a.go"}}}`
		var output bytes.Buffer
		require.NoError(t, Run([]string{"check"}, bytes.NewReader([]byte(request)), &output))
		var verdict struct {
			Block bool `json:"block"`
		}
		require.NoError(t, json.Unmarshal(output.Bytes(), &verdict))
		require.Equal(t, tc.block, verdict.Block, tc.reads)
	}
}

// TestCLIRejectsMalformedReads names the state form that failed to decode.
//
// Example: {"reads":-1} fails as a bitset; {"reads":{"coding-common":1}} fails as a skill-name object.
func TestCLIRejectsMalformedReads(t *testing.T) {
	for reads, want := range map[string]string{
		`-1`:                  "reads is not a bitset: json: cannot unmarshal number -1 into Go value of type uint64",
		`{"coding-common":1}`: "reads is not a skill-name object: json: cannot unmarshal number into Go value of type bool",
	} {
		request := `{"state":{"reads":` + reads + `},"call":{"toolName":"write","input":{"path":"a.go"}}}`
		require.EqualError(t, Run([]string{"check"}, bytes.NewReader([]byte(request)), io.Discard), want, reads)
	}
}

// TestDescribeListsGatedExtensions keeps the TS prefilter on the Go extension sets.
//
// Example: describe lists ".cpp" and ".md" because skillForPath can gate both.
func TestDescribeListsGatedExtensions(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, Run([]string{"describe"}, nil, &output))
	var described struct {
		GatedExtensions []string `json:"gatedExtensions"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &described))
	require.NotEmpty(t, codeExtensions)
	keys := append([]string{}, instructionExtensions...)
	for ext := range codeExtensions {
		keys = append(keys, ext)
	}
	require.ElementsMatch(t, keys, described.GatedExtensions)
	require.Contains(t, described.GatedExtensions, ".cpp")
}

// TestCLICheckCarriesAgentName gates an ECI agent's task through check.
//
// Example: an orchestrator task call is blocked until its role reads finish.
func TestCLICheckCarriesAgentName(t *testing.T) {
	for _, tc := range []struct {
		agent string
		block bool
	}{
		{"eci-preset-orchestrator-openai-default", true},
		{"main", false},
	} {
		request, err := json.Marshal(cliInput{Call: cliEvent{ToolName: "task", Input: map[string]any{}, AgentName: tc.agent}})
		require.NoError(t, err)
		var output bytes.Buffer
		require.NoError(t, Run([]string{"check"}, bytes.NewReader(request), &output))
		var verdict struct {
			Block bool `json:"block"`
		}
		require.NoError(t, json.Unmarshal(output.Bytes(), &verdict))
		require.Equal(t, tc.block, verdict.Block, tc.agent)
	}
}

// TestDescribeListsRoleGating exposes task and the ECI agent prefix to the adapter.
//
// Example: the adapter checks task calls from eci-preset-* agents.
func TestDescribeListsRoleGating(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, Run([]string{"describe"}, nil, &output))
	var described struct {
		CallTools          []string `json:"callTools"`
		GatedAgentPrefixes []string `json:"gatedAgentPrefixes"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &described))
	require.Equal(t, []string{"write", "edit", "task"}, described.CallTools)
	require.Equal(t, []string{"eci-preset-"}, described.GatedAgentPrefixes)
}
