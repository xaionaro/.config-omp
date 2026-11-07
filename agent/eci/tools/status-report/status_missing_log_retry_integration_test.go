package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshRejectsMissingPreviouslyConsumedLog checks cursor durability.
//
// Example: A deleted event log must not be mistaken for a fresh empty history.
func TestRefreshRejectsMissingPreviouslyConsumedLog(t *testing.T) {
	// first is already reflected in the saved report.
	//
	// Example: Project one event before its source log is removed.
	const first = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first claim"}}]}}}` + "\n"
	// second is appended after the original event log is restored.
	//
	// Example: Continue replay without losing the saved claim.
	const second = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"two","fields":{"text":"later claim"}}]}}}` + "\n"
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	var diagnostics bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "worker_progress"}, strings.NewReader(""), io.Discard, &diagnostics), diagnostics.String())
	require.NoError(t, os.Remove(logPath))
	diagnostics.Reset()
	require.Equal(t, 1, run([]string{"refresh", dir, "worker_progress"}, strings.NewReader(""), io.Discard, &diagnostics))
	require.Contains(t, diagnostics.String(), "status sync cursor exceeds log")
	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(data)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "first claim")
	require.Equal(t, 1, report.StatusSync.Cursor)
	require.NoError(t, os.WriteFile(logPath, []byte(first+second), 0600))
	diagnostics.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "worker_progress"}, strings.NewReader(""), io.Discard, &diagnostics), diagnostics.String())
	data, err = os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err = parse(data)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "first claim")
	require.Contains(t, report.Progress, "later claim")
	require.Equal(t, 2, report.StatusSync.Cursor)
}

// TestMissingLogWithoutConsumedEventsRemainsOptional checks fresh sessions.
//
// Example: A report with no synced events can refresh before a log exists.
func TestMissingLogWithoutConsumedEventsRemainsOptional(t *testing.T) {
	dir := t.TempDir()
	result, logBytes, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(result))
	require.Nil(t, logBytes)
	// saved is a zero cursor from a prior empty synchronization.
	//
	// Example: An empty saved cursor still permits a missing fresh log.
	const saved = `{"cursor":0,"applied":{},"latest":{},"pending":{}}`
	initial, err := putSyncState([]byte(linkedReport), saved)
	require.NoError(t, err)
	result, logBytes, err = projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, initial, result)
	require.Nil(t, logBytes)
}
