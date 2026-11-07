package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshRetriesIncompleteFinalLogEntry preserves an interrupted event.
//
// Example: Completing a partial JSON line lets a later refresh project it.
func TestRefreshRetriesIncompleteFinalLogEntry(t *testing.T) {
	// prior is a complete event preceding the interrupted append.
	//
	// Example: Preserve already complete progress while deferring the final entry.
	const prior = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"earlier","fields":{"text":"earlier event"}}]}}}` + "\n"
	// event is a complete progress update split across two writes.
	//
	// Example: Complete the event after a refresh observes its prefix.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"event recovered"}}]}}}`
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	split := strings.Index(event, `"event recovered"`)
	require.Greater(t, split, 0)
	require.NoError(t, os.WriteFile(logPath, []byte(prior+event[:split]), 0600))
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	pending, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.NotContains(t, report.Progress, "event recovered")
	require.Contains(t, report.Progress, "earlier event")
	require.Contains(t, report.Blockers, "Status refresh incomplete: unterminated invalid final log entry; complete or terminate it before retry")
	require.NotNil(t, report.ScopeComplete)
	require.False(t, *report.ScopeComplete)
	require.Equal(t, 1, report.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	retried, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, pending, retried)
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = file.WriteString(event[split:] + "\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "event recovered")
	require.Contains(t, report.Progress, "earlier event")
	require.Contains(t, report.Progress, "Implementation started")
	require.Equal(t, 2, report.StatusSync.Cursor)
}

// TestLogFinalLineCompatibility distinguishes complete and terminated malformed entries.
//
// Example: A complete JSON event without a newline projects immediately.
func TestLogFinalLineCompatibility(t *testing.T) {
	// complete is a valid event without a trailing newline.
	//
	// Example: Replay a manually supplied complete final record.
	const complete = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"complete event"}}]}}}`
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(complete), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "complete event")
	require.Equal(t, 1, report.StatusSync.Cursor)
	// malformed is a complete physical line containing invalid JSON.
	//
	// Example: Diagnose a corrupted record rather than treating it as in progress.
	const malformed = `{"event":"decision","details":{"status_update":` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(malformed), 0600))
	result, _, err = projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
	require.Equal(t, 1, report.StatusSync.Cursor)
}

// TestIncompleteTailCompletionDuringRefreshRetries checks the publication boundary.
//
// Example: A writer completing the tail prevents a stale diagnostic from publishing.
func TestIncompleteTailCompletionDuringRefreshRetries(t *testing.T) {
	// event is one status update completed at the publication boundary.
	//
	// Example: Append its suffix while refresh is preparing the report.
	const event = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"completed concurrently"}}]}}}`
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	split := strings.Index(event, `"completed concurrently"`)
	require.Greater(t, split, 0)
	require.NoError(t, os.WriteFile(logPath, []byte(event[:split]), 0600))
	observer := testRefreshObserver{Action:
	// Complete the pending event at the publication boundary.
	//
	// Example: The source changes after refresh has read it.
	func() {
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
		require.NoError(t, err)
		_, err = file.WriteString(event[split:] + "\n")
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "decision", observer), "retry needed")
	before, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(before))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "completed concurrently")
	require.Equal(t, 1, report.StatusSync.Cursor)
}
