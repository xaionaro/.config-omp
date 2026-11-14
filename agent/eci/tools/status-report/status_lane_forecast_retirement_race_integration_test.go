package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneClosureRetriesWhenForecastRetiresDuringRefresh checks source races.
//
// Example: A late forecast retirement is observed on retry before the lane closes.
func TestLaneClosureRetriesWhenForecastRetiresDuringRefresh(t *testing.T) {
	t.Parallel()
	// event requests closure of a source-backed implementation lane.
	//
	// Example: Closure waits for the canonical forecast to become inactive.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, completed)
	require.NotEqual(t, linkedForecasts, inactive)
	dir := writeLinkedSources(t, completed, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	observer := testRefreshObserver{Action:
	// retire changes the source after it was read but before publication.
	//
	// Example: A producer retires the canonical forecast concurrently.
	func() { require.NoError(t, os.WriteFile(forecastPath, []byte(inactive), 0600)) }}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "lifecycle_change", observer), "retry needed")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(unchanged))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Equal(t, "2026-11-02T15:25:00Z", report.Lanes[0].CompletedAt)
	require.Nil(t, report.Lanes[0].Forecast)
	require.Empty(t, report.StatusSync.Groups)
	require.Equal(t, 1, report.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
