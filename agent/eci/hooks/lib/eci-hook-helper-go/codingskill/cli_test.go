package codingskill

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCLIRoundTrip checks real JSON state transfer between reduce and check.
//
// Example: completed common and Go reads allow a later Go write.
func TestCLIRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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

// TestCLICheckHonorsDisableFile lets running sessions turn every check off and back on without a restart.
//
// Example: while ~/.omp/agent/coding-skill-gate.disabled exists, an unread Go write gets {"block":false}.
func TestCLICheckHonorsDisableFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	disableFile := filepath.Join(home, ".omp", "agent", "coding-skill-gate.disabled")
	require.NoError(t, os.MkdirAll(filepath.Dir(disableFile), 0o755))
	for _, call := range []cliEvent{
		{ToolName: "write", Input: map[string]any{"path": "a.go"}},
		{ToolName: "task", Input: map[string]any{}, AgentName: "eci-preset-orchestrator-openai-default"},
	} {
		request, err := json.Marshal(cliInput{Call: call})
		require.NoError(t, err)

		// check runs one check request and returns its raw reply.
		//
		// Example: an unread Go write replies with "block":true while the gate is enabled.
		check := func() string {
			var output bytes.Buffer
			require.NoError(t, Run([]string{"check"}, bytes.NewReader(request), &output))
			return output.String()
		}
		require.Contains(t, check(), `"block":true`, call.ToolName)

		require.NoError(t, os.WriteFile(disableFile, nil, 0o644))
		require.JSONEq(t, `{"block":false}`, check(), call.ToolName)

		require.NoError(t, os.Remove(disableFile))
		require.Contains(t, check(), `"block":true`, call.ToolName)
	}
}

// TestCLICheckIgnoresRequestWhileDisabled allows a call whose state a changed helper cannot decode, and reads the whole request so the bridge's write cannot hit a closed pipe.
//
// Example: with the disable file present, {"state":{"reads":-1},...} gets {"block":false}.
func TestCLICheckIgnoresRequestWhileDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	disableFile := filepath.Join(home, ".omp", "agent", "coding-skill-gate.disabled")
	require.NoError(t, os.MkdirAll(filepath.Dir(disableFile), 0o755))
	require.NoError(t, os.WriteFile(disableFile, nil, 0o644))
	request := strings.NewReader(`{"state":{"reads":-1},"call":{"toolName":"write","input":{"path":"a.go","content":"` + strings.Repeat("x", 1<<20) + `"}}}`)
	var output bytes.Buffer
	require.NoError(t, Run([]string{"check"}, request, &output))
	require.JSONEq(t, `{"block":false}`, output.String())
	require.Zero(t, request.Len())
}

// TestCLIReduceCreditsReadsWhileDisabled keeps recording skill reads while the gate is disabled, so they still count after it is re-enabled.
//
// Example: coding-common and coding-go read while disabled allow a Go write after the disable file is deleted; a Python write still blocks.
func TestCLIReduceCreditsReadsWhileDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	disableFile := filepath.Join(home, ".omp", "agent", "coding-skill-gate.disabled")
	require.NoError(t, os.MkdirAll(filepath.Dir(disableFile), 0o755))
	require.NoError(t, os.WriteFile(disableFile, nil, 0o644))
	request, err := json.Marshal(cliInput{Results: []cliEvent{
		{ToolName: "read", Input: map[string]any{"path": "skill://coding-common"}, Details: completeRead()},
		{ToolName: "read", Input: map[string]any{"path": "skill://coding-go"}, Details: completeRead()},
	}})
	require.NoError(t, err)
	var output bytes.Buffer
	require.NoError(t, Run([]string{"reduce"}, bytes.NewReader(request), &output))
	var reduced struct {
		State State `json:"state"`
	}
	require.NoError(t, json.Unmarshal(output.Bytes(), &reduced))
	require.NoError(t, os.Remove(disableFile))

	// check runs one check of a write to path with the reduced state and returns its raw reply.
	//
	// Example: check("a.go") replies {"block":false} once coding-common and coding-go are credited.
	check := func(path string) string {
		request, err := json.Marshal(cliInput{State: reduced.State, Call: cliEvent{ToolName: "write", Input: map[string]any{"path": path}}})
		require.NoError(t, err)
		output.Reset()
		require.NoError(t, Run([]string{"check"}, bytes.NewReader(request), &output))
		return output.String()
	}
	require.JSONEq(t, `{"block":false}`, check("a.go"))
	require.Contains(t, check("b.py"), `requires skill://coding-python.`)
}

// TestCLICheckFailsWhenDisableFileIsUnknown fails the check when the disable file's presence cannot be determined, so the bridge blocks instead of guessing.
//
// Example: with HOME unset, check returns an error naming ~/.omp/agent and writes no verdict.
func TestCLICheckFailsWhenDisableFileIsUnknown(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notDir, nil, 0o644))
	request, err := json.Marshal(cliInput{Call: cliEvent{ToolName: "write", Input: map[string]any{"path": "a.go"}}})
	require.NoError(t, err)
	for home, want := range map[string]string{
		"":     "locating ~/.omp/agent for coding-skill-gate.disabled",
		notDir: "checking coding-skill-gate.disabled",
	} {
		t.Setenv("HOME", home)
		var output bytes.Buffer
		require.ErrorContains(t, Run([]string{"check"}, bytes.NewReader(request), &output), want, home)
		require.Empty(t, output.String(), home)
	}
}

// TestCLIAcceptsLegacyReadsObject keeps running bridges that still hold object-form state working.
//
// Example: {"reads":{"coding-common":true,"coding-go":true}} allows a Go write; without coding-go it blocks.
func TestCLIAcceptsLegacyReadsObject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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

// TestCLIRejectsMalformedReads names the state form that failed to decode and keeps the decoder's cause.
//
// Example: {"reads":-1} fails as a bitset; {"reads":{"coding-common":1}} fails as a skill-name object.
func TestCLIRejectsMalformedReads(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for reads, form := range map[string]string{
		`-1`:                  "reads is not a bitset: ",
		`{"coding-common":1}`: "reads is not a skill-name object: ",
	} {
		request := `{"state":{"reads":` + reads + `},"call":{"toolName":"write","input":{"path":"a.go"}}}`
		err := Run([]string{"check"}, bytes.NewReader([]byte(request)), io.Discard)
		require.ErrorContains(t, err, form, reads)
		var cause *json.UnmarshalTypeError
		require.ErrorAs(t, err, &cause, reads)
	}
}

// TestDescribeListsGatedExtensions keeps the TS prefilter on the Go extension sets.
//
// Example: describe lists ".cpp", ".md", and ".yml" because skillForPath can gate all three.
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
	require.Subset(t, described.GatedExtensions, []string{".cpp", ".md", ".mdc", ".yml", ".yaml"})
}

// TestCLICheckCarriesAgentName gates an ECI agent's task through check.
//
// Example: an orchestrator task call is blocked until its role reads finish.
func TestCLICheckCarriesAgentName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
