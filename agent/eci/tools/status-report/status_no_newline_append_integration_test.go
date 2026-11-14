package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshAppendsAfterCompleteUnterminatedLogRecord checks cursor continuity.
//
// Example: A later writer adds a separator and a second event after a prior refresh.
func TestRefreshAppendsAfterCompleteUnterminatedLogRecord(t *testing.T) {
	t.Parallel()
	// first is a valid manually written event without a final newline.
	//
	// Example: A complete log entry is projected before the file is extended.
	const first = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first claim"}}]}}}`
	// second is appended as a separate physical record.
	//
	// Example: A subsequent event must not replay the first record twice.
	const second = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"two","fields":{"text":"second claim"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	initial, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	parsed, err := parse(initial)
	require.NoError(t, err)
	require.Equal(t, 1, parsed.StatusSync.Cursor)
	require.Contains(t, parsed.Progress, "first claim")
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = file.WriteString("\n" + second)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, 2, parsed.StatusSync.Cursor)
	require.Equal(t, []string{"Implementation started", "first claim", "second claim"}, parsed.Progress)
	require.NoError(t, refreshTestStatus(dir, "decision"))
	retry, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
