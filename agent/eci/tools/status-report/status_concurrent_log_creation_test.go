package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshRetriesWhenMissingLogAppearsBeforePublication checks source snapshot integrity.
//
// Example: A newly created event log cannot be omitted by a successful stale refresh.
func TestRefreshRetriesWhenMissingLogAppearsBeforePublication(t *testing.T) {
	t.Parallel()
	// event is the first log entry, created after refresh reads the absent log.
	//
	// Example: A worker records progress during a forecast refresh.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"newly logged progress"}}]}}}` + "\n"
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	changed := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	require.NotEqual(t, linkedForecasts, changed)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(changed), 0600))
	observer := testRefreshObserver{Action:
	// Create the first event log immediately before report publication.
	//
	// Example: A concurrent writer wins after the absent-log read.
	func() { require.NoError(t, os.WriteFile(logPath, []byte(event), 0600)) }}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "forecast_change", observer), "retry needed")
	before, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(before))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "newly logged progress")
	require.Equal(t, 1, report.StatusSync.Cursor)
	require.Equal(t, "2026-11-02T16:30:00Z", report.Lanes[0].Forecast.Target)
}

// TestRefreshRetriesWhenEmptyLogChanges checks the empty-versus-absent distinction.
//
// Example: Appending to an existing empty log cannot publish a stale report.
func TestRefreshRetriesWhenEmptyLogChanges(t *testing.T) {
	t.Parallel()
	// event is appended to a log that existed but was empty when read.
	//
	// Example: A first worker update arrives while refresh is preparing publication.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first progress"}}]}}}` + "\n"
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte{}, 0600))
	observer := testRefreshObserver{Action:
	// Append the first event before the report publication check.
	//
	// Example: Distinguish a changed empty log from an absent source.
	func() {
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
		require.NoError(t, err)
		_, err = file.WriteString(event)
		require.NoError(t, err)
		require.NoError(t, file.Close())
	}}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "requirement_change", observer), "retry needed")
	before, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(before))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "first progress")
	require.Equal(t, 1, report.StatusSync.Cursor)
}
