package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPendingCorrectionSurvivesRefreshUntilTaskExists checks persisted retry order.
//
// Example: A correction waits across a restart until its missing task is created.
func TestPendingCorrectionSurvivesRefreshUntilTaskExists(t *testing.T) {
	t.Parallel()
	// first assigns a task that has not been created yet.
	//
	// Example: An assignment reaches the log before its task definition.
	const first = `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"first"}}]}}}` + "\n"
	// second corrects that pending assignment.
	//
	// Example: The corrected owner must not bypass its unresolved predecessor.
	const second = `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"3","corrects":1,"fields":{"owner":"second"}}]}}}` + "\n"
	// third introduces the missing task.
	//
	// Example: A later requirement event makes the queued updates applicable.
	const third = `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	pending, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, report.Tasks, 2)
	require.Len(t, report.StatusSync.Groups, 2)
	retry, _, err := projectLogStatus(dir, pending, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, string(pending), string(retry))
	require.NoError(t, os.WriteFile(path, []byte(first+second+third), 0600))
	result, _, err := projectLogStatus(dir, retry, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.Tasks, 3)
	require.Equal(t, "second", report.Tasks[2].Owner)
	require.Empty(t, report.StatusSync.Groups)
}
