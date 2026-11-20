package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSQLiteAssignmentTransaction exercises changed and unchanged capture and rollback.
//
// Example: a missing linked record rejects an edit without publishing the changed row or an assignment.
func TestSQLiteAssignmentTransaction(t *testing.T) {
	f := newSQLiteAssignmentFixture(t)
	database := filepath.Join(filepath.Dir(f.Ledger), "ledger.db")
	query :=
		// query reads the committed session state without mutating it.
		//
		// Example: retrieve the generation after a capture.
		func(statement string) string {
		t.Helper()
		result, err := exec.Command("sqlite3", "-readonly", database, statement).CombinedOutput()
		require.NoError(t, err, string(result))
		return strings.TrimSpace(string(result))
	}
	request := `{"ops":[{"op":"patch","table":"records","id":"requirement:metrics","expect":{"type":"requirement"},"value":{"data":{"source_text":"User: corrected"}}}],"requirements":["requirement:metrics"],"outcome":"outcome:metrics","target":"/root/receiver","instructions":"Implement locally; preserve every binding condition.","actor":{"id":"/root","role":"Supervisor"},"reason":"material correction"}`
	require.NoError(t, os.WriteFile(f.Request, []byte(request), 0600))
	payload, stderr, err := f.invoke(t)
	require.NoError(t, err, stderr)
	var assignment Payload
	require.NoError(t, json.Unmarshal([]byte(payload), &assignment))
	require.Equal(t, "/root/receiver", assignment.Target)
	require.Contains(t, assignment.Message, "User: corrected")
	require.Contains(t, assignment.Message, "requirement:preserved")
	require.Contains(t, assignment.Message, "must survive projection")
	require.NotContains(t, assignment.Message, "User: old compactness")
	require.Equal(t, "User: corrected", query(`SELECT json_extract(value,'$.data.source_text') FROM records WHERE id='requirement:metrics'`))
	generation := query("SELECT generation FROM ledger_meta WHERE id=1")
	logPath := filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl")
	log, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(log), `"event":"requirement_change"`)

	reuse := strings.Replace(request, `[{"op":"patch","table":"records","id":"requirement:metrics","expect":{"type":"requirement"},"value":{"data":{"source_text":"User: corrected"}}}]`, `[]`, 1)
	require.NoError(t, os.WriteFile(f.Request, []byte(reuse), 0600))
	payload, stderr, err = f.invoke(t)
	require.NoError(t, err, stderr)
	require.Contains(t, payload, "requirement:preserved")
	require.Equal(t, generation, query("SELECT generation FROM ledger_meta WHERE id=1"))
	log, err = os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(log), `"event":"decision"`)

	invalid := strings.Replace(request, `"source_text":"User: corrected"`, `"acceptance":[]`, 1)
	require.NoError(t, os.WriteFile(f.Request, []byte(invalid), 0600))
	before := string(log)
	payload, _, err = f.invoke(t)
	require.Error(t, err)
	require.Empty(t, payload)
	require.Equal(t, "User: corrected", query(`SELECT json_extract(value,'$.data.source_text') FROM records WHERE id='requirement:metrics'`))
	require.Equal(t, generation, query("SELECT generation FROM ledger_meta WHERE id=1"))
	log, err = os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, before, string(log))
}

// newSQLiteAssignmentFixture bootstraps a session through its ordinary ledger-edit command.
//
// Example: populate requirement records without touching the account's real session.
func newSQLiteAssignmentFixture(t *testing.T) cliFixture {
	t.Helper()
	f := newCLIFixture(t)
	require.NoError(t, os.Remove(f.Ledger))
	var snapshot struct {
		Sections map[string]json.RawMessage `json:"sections"`
		Records map[string]json.RawMessage `json:"records"`
	}
	legacy := strings.Replace(snapshotFixture, "  unrelated:\n    type:", "  unrelated:\n    section: task\n    type:", 1)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(legacy), &document))
	contents, err := json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(contents, &snapshot))
	// operation describes one initial row in the session.
	//
	// Example: a section put creates the parent for its records.
	type operation struct {
		Op string `json:"op"`
		Table string `json:"table"`
		ID string `json:"id"`
		Value json.RawMessage `json:"value"`
	}
	var ops []operation
	for id, value := range snapshot.Sections {
		ops = append(ops, operation{Op: "put", Table: "sections", ID: id, Value: value})
	}
	for id, value := range snapshot.Records {
		ops = append(ops, operation{Op: "put", Table: "records", ID: id, Value: value})
	}
	batch, err := json.Marshal(struct { Ops []operation `json:"ops"` }{Ops: ops})
	require.NoError(t, err)
	seed := exec.Command(f.CLI, "ledger-edit", "--json", string(batch))
	seed.Env = f.env(t)
	output, err := seed.CombinedOutput()
	require.NoError(t, err, string(output))
	require.FileExists(t, filepath.Join(filepath.Dir(f.Ledger), "ledger-activation.json"))
	require.FileExists(t, filepath.Join(filepath.Dir(f.Ledger), "ledger.db"))
	return f
}

// TestSQLiteAssignmentAfterBootstrap exercises a newly created session without legacy snapshots.
//
// Example: the first ledger edit creates the store before an unchanged capture.
func TestSQLiteAssignmentAfterBootstrap(t *testing.T) {
	f := newSQLiteAssignmentFixture(t)
	request := `{"ops":[],"requirements":["requirement:metrics"],"outcome":"outcome:metrics","target":"/root/receiver","instructions":"Implement locally","actor":{"id":"/root","role":"Supervisor"},"reason":"reuse"}`
	require.NoError(t, os.WriteFile(f.Request, []byte(request), 0600))
	payload, stderr, err := f.invoke(t)
	require.NoError(t, err, stderr)
	require.Contains(t, payload, "requirement:preserved")
	require.Contains(t, payload, "User: old compactness")
	log, err := os.ReadFile(filepath.Join(filepath.Dir(f.Ledger), "high_level_log.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(log), `"event":"decision"`)
	require.NotContains(t, string(log), `"event":"requirement_change"`)
}
