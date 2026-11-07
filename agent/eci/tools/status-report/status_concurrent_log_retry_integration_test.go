package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshRetriesAfterConcurrentLogAppend checks atomic cursor publication.
//
// Example: A new event arriving before publication remains visible on retry.
func TestRefreshRetriesAfterConcurrentLogAppend(t *testing.T) {
	// first is the event read by the initial refresh.
	//
	// Example: A worker reports its first progress update.
	const first = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first progress"}}]}}}` + "\n"
	// second arrives after the log was read but before the report is published.
	//
	// Example: Another worker appends independent progress during refresh.
	const second = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"two","fields":{"text":"second progress"}}]}}}` + "\n"
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	observer := testRefreshObserver{Action:
	// Append the second event at the publication boundary.
	//
	// Example: Model a writer updating the event log concurrently.
	func() {
		log, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
		require.NoError(t, err)
		_, err = log.WriteString(second)
		require.NoError(t, err)
		require.NoError(t, log.Close())
	}}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "worker_progress", observer), "retry needed")
	before, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(before))
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	after, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(after)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "first progress")
	require.Contains(t, report.Progress, "second progress")
	require.Contains(t, report.Progress, "Implementation started")
	require.Equal(t, 2, report.StatusSync.Cursor)
}
