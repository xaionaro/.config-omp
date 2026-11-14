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

// TestRefreshRejectsTruncatedLogAndRecoversAfterRestore checks persisted cursor safety.
//
// Example: A shortened event log cannot silently replay over the report's saved cursor.
func TestRefreshRejectsTruncatedLogAndRecoversAfterRestore(t *testing.T) {
	t.Parallel()
	// first records a prior projected claim.
	//
	// Example: Append a claim before an accidental log truncation.
	const first = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first claim"}}]}}}` + "\n"
	// second records work after the original log is restored.
	//
	// Example: Retry normally after repairing the event log.
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
	require.NoError(t, os.WriteFile(logPath, nil, 0600))
	diagnostics.Reset()
	require.Equal(t, 1, run([]string{"refresh", dir, "worker_progress"}, strings.NewReader(""), io.Discard, &diagnostics))
	require.Contains(t, diagnostics.String(), "status sync cursor exceeds log")
	data, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(data)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "first claim")
	require.NotContains(t, report.Progress, "later claim")
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
