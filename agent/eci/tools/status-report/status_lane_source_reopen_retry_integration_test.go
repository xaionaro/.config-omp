package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneReopenWaitsForActiveForecastOnDisk checks source-backed retry.
//
// Example: A reopened work item remains closed until its forecast is reactivated.
func TestLaneReopenWaitsForActiveForecastOnDisk(t *testing.T) {
	t.Parallel()
	// closeEvent records a completed lane whose canonical work and forecast are terminal.
	//
	// Example: Completion retires the report's active lane forecast.
	const closeEvent = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	// reopen requests a later return to active work.
	//
	// Example: The matching active canonical forecast is needed to reopen safely.
	const reopen = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, completed)
	require.NotEqual(t, linkedForecasts, inactive)
	dir := writeLinkedSources(t, completed, inactive)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(closeEvent), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	closed, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(closed)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Nil(t, report.Lanes[0].Forecast)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(closeEvent+reopen), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(pending)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Nil(t, report.Lanes[0].Forecast)
	require.NotEmpty(t, report.StatusSync.Groups)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
	require.Empty(t, report.Lanes[0].CompletedAt)
	require.NotNil(t, report.Lanes[0].Forecast)
	require.Empty(t, report.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
