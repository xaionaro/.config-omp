package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReclosedLanePreservesOwnerEditedRestoredForecast checks full-refresh ownership.
//
// Example: An owner edit to a restored forecast prevents a later closure from removing it.
func TestReclosedLanePreservesOwnerEditedRestoredForecast(t *testing.T) {
	t.Parallel()
	// closeEvent retires the lane's forecast when its canonical work completes.
	//
	// Example: The same closure can later be recorded after a reopening.
	const closeEvent = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	// reopen restores the active canonical forecast on that lane.
	//
	// Example: A user can subsequently edit the visible restored forecast.
	const reopen = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, completed)
	require.NotEqual(t, linkedForecasts, inactive)
	dir := writeLinkedSources(t, completed, inactive)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	ledgerPath := filepath.Join(dir, "project-understanding.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(closeEvent), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	require.NoError(t, os.WriteFile(ledgerPath, []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(forecastPath, []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(closeEvent+reopen), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	reopened, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(reopened), `"outcome":"First result"`, `"outcome":"owner edit"`, 1)
	require.NotEqual(t, string(reopened), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	require.NoError(t, os.WriteFile(ledgerPath, []byte(completed), 0600))
	require.NoError(t, os.WriteFile(forecastPath, []byte(inactive), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(closeEvent+reopen+closeEvent), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	report, parseErr := parse(pending)
	require.NoError(t, parseErr)
	require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
	require.NotNil(t, report.Lanes[0].Forecast)
	require.Equal(t, "owner edit", report.Lanes[0].Forecast.Outcome)
	require.NotEmpty(t, report.StatusSync.Groups)
	restored := strings.Replace(string(pending), `"outcome":"owner edit"`, `"outcome":"First result"`, 1)
	require.NotEqual(t, string(pending), restored)
	require.NoError(t, os.WriteFile(path, []byte(restored), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Nil(t, report.Lanes[0].Forecast)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
