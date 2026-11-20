package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TODO: this package's tests do not compile: project (line 22) is undefined (pre-existing; seen by
// work:no-historical-code). Point this test at the current projection entry point or delete it.
//
// TestTransactionProjectionUsesSavedChainAndChangeState checks both capture alternatives.
//
// Example: a changed transaction emits a requirement change with all linked records.
func TestTransactionProjectionUsesSavedChainAndChangeState(t *testing.T) {
	t.Parallel()
	request := Request{Requirements: []RecordID{"requirement:r"}, Outcome: "outcome:o", Target: "worker", Instructions: "Execute", Actor: Identity{ID: "owner", Role: "Supervisor"}, Reason: "capture"}
	snapshot := json.RawMessage(`{"sections":{},"records":{"outcome:o":{"type":"outcome","data":{"description":"result"},"links":{"requirements":["requirement:r"],"scope":["scope:s"]},"evidence":[]},"requirement:r":{"type":"requirement","data":{"source_text":"original","acceptance":["preserve"]},"evidence":[]},"scope:s":{"type":"scope","data":{"include":[],"exclude":[]},"evidence":[]}}}`)
	for _, changed := range []bool{false, true} {
		result, err := project(snapshot, changed, request)
		require.NoError(t, err)
		require.Equal(t, changed, result.Event.Details.SnapshotChanged)
		if changed {
			require.Equal(t, CaptureRequirementChange, result.Event.Event)
		} else {
			require.Equal(t, CaptureDecision, result.Event.Event)
		}
		require.Contains(t, result.Payload.Message, "original")
		require.Contains(t, result.Payload.Message, "scope:s")
		require.NotContains(t, result.Payload.Message, "```json")
	}
}

// TestProjectionProcess exercises the real CLI with a transaction snapshot on stdin.
//
// Example: a failed chain validation emits no assignment envelope.
func TestProjectionProcess(t *testing.T) {
	if os.Getenv("REQUIREMENT_PROJECTION_CHILD") == "1" {
		os.Args = []string{"requirement-assignment"}
		main()
		os.Exit(0)
	}
	t.Parallel()
	request := `{"ops":[{"op":"patch","table":"records","id":"requirement:metrics","expect":{"type":"requirement"},"value":{"data":{"source_text":"corrected"}}}],"requirements":["requirement:metrics"],"outcome":"outcome:metrics","target":"worker","instructions":"execute","actor":{"id":"owner","role":"Supervisor"},"reason":"correction"}`
	var snapshot map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(snapshotFixture), &snapshot))
	delete(snapshot, "schema")
	snapshot["snapshot_changed"] = true
	records := snapshot["records"].(map[string]any)
	record := records["requirement:metrics"].(map[string]any)
	record["data"].(map[string]any)["source_text"] = "corrected\nexact multiline source"
	input, err := json.Marshal(snapshot)
	require.NoError(t, err)
	command := exec.Command(os.Args[0], "-test.run=^TestProjectionProcess$")
	command.Env = append(os.Environ(), "REQUIREMENT_PROJECTION_CHILD=1")
	command.Stdin = strings.NewReader(`{"request":` + request + `,"snapshot":` + string(input) + `}`)
	output, err := command.Output()
	require.NoError(t, err)
	var result struct {
		Payload Payload `json:"payload"`
		Event struct {
			Event string `json:"event"`
			Details struct {
				Changed bool `json:"snapshot_changed"`
				Ops []json.RawMessage `json:"ops"`
			} `json:"details"`
		} `json:"event"`
	}
	require.NoError(t, json.Unmarshal(output, &result))
	require.Equal(t, "worker", result.Payload.Target)
	for _, text := range []string{"corrected", "exact multiline source", "requirement:preserved", "must survive projection", "scope:preserved", "zero versus unavailable"} {
		require.Contains(t, result.Payload.Message, text)
	}
	require.NotContains(t, result.Payload.Message, "User: old compactness")
	require.Equal(t, "requirement_change", result.Event.Event)
	require.True(t, result.Event.Details.Changed)
	require.Len(t, result.Event.Details.Ops, 1)
	require.JSONEq(t, `{"op":"patch","table":"records","id":"requirement:metrics","expect":{"type":"requirement"},"value":{"data":{"source_text":"corrected"}}}`, string(result.Event.Details.Ops[0]))

	snapshot["snapshot_changed"] = false
	input, err = json.Marshal(snapshot)
	require.NoError(t, err)
	command = exec.Command(os.Args[0], "-test.run=^TestProjectionProcess$")
	command.Env = append(os.Environ(), "REQUIREMENT_PROJECTION_CHILD=1")
	command.Stdin = strings.NewReader(`{"request":` + request + `,"snapshot":` + string(input) + `}`)
	output, err = command.Output()
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(output, &result))
	require.Equal(t, "decision", result.Event.Event)
	require.False(t, result.Event.Details.Changed)
	require.Contains(t, result.Payload.Message, "requirement:preserved")

	delete(records, "requirement:preserved")
	input, err = json.Marshal(snapshot)
	require.NoError(t, err)
	command = exec.Command(os.Args[0], "-test.run=^TestProjectionProcess$")
	command.Env = append(os.Environ(), "REQUIREMENT_PROJECTION_CHILD=1")
	command.Stdin = strings.NewReader(`{"request":` + request + `,"snapshot":` + string(input) + `}`)
	output, err = command.Output()
	require.Error(t, err)
	require.Empty(t, output)
}

// TestProjectionRejectsInvalidEnvelope exercises stdin validation at the CLI boundary.
//
// Example: a missing change flag cannot be mistaken for an unchanged transaction.
func TestProjectionRejectsInvalidEnvelope(t *testing.T) {
	t.Parallel()
	request := `{"ops":[],"requirements":["requirement:metrics"],"outcome":"outcome:metrics","target":"worker","instructions":"execute","actor":{"id":"owner","role":"Supervisor"},"reason":"reuse"}`
	for _, input := range []string{
		`{"sections":{},"records":{}}`,
		`{"sections":{},"records":{},"snapshot_changed":null}`,
		`{"sections":{},"records":{},"snapshot_changed":false} {}`,
		`{"sections":{},"records":{},"snapshot_changed":false,"unexpected":1}`,
		`{"sections":{},"records":{},"Snapshot_Changed":false}`,
		`{"sections":{},"records":{},"snapshot_changed":false,"snapshot_changed":true}`,
		`{"sections":{},"records":{"r":{"data":{"x":1,"x":2}}},"snapshot_changed":false}`,
	} {
		command := exec.Command(os.Args[0], "-test.run=^TestProjectionProcess$")
		command.Env = append(os.Environ(), "REQUIREMENT_PROJECTION_CHILD=1")
		command.Stdin = strings.NewReader(`{"request":` + request + `,"snapshot":` + input + `}`)
		output, err := command.Output()
		require.Error(t, err)
		require.Empty(t, output)
	}
}
