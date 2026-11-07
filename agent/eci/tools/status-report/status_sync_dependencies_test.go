package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncOwnerConflictDoesNotBlockDisjointTaskClosure checks independent fields.
//
// Example: Preserve an owner edit while accepting the same task with evidence.
func TestSyncOwnerConflictDoesNotBlockDisjointTaskClosure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"projected"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	initial, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(initial), `"owner": "projected"`, `"owner": "manual"`, 1)
	require.NotEqual(t, string(initial), edited)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"later"}}]}}}` + "\n"
	third := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second+third), 0600))
	out, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, "manual", report.Tasks[0].Owner)
	require.Equal(t, statusClosed, report.Tasks[0].Status)
	require.Equal(t, "accepted", report.Tasks[0].Evidence)
	require.NotEmpty(t, report.StatusSync.Groups)
}

// TestSyncEvidenceConflictBlocksDependentTaskClosure checks atomic acceptance.
//
// Example: An owner-edited proof prevents a later closure from replacing it.
func TestSyncEvidenceConflictBlocksDependentTaskClosure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"evidence":"projected"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	initial, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(initial), `"evidence": "projected"`, `"evidence": "manual"`, 1)
	require.NotEqual(t, string(initial), edited)
	second := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"evidence":"later"}}]}}}` + "\n"
	third := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second+third), 0600))
	out, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, "manual", report.Tasks[0].Evidence)
	require.Equal(t, statusInProgress, report.Tasks[0].Status)
	require.Len(t, report.StatusSync.Groups, 2)
}
