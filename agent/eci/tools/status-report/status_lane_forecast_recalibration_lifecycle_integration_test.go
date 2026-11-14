package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReopenedLaneTracksCanonicalForecastBeforeLaterClosure checks source ownership.
//
// Example: A source-updated forecast remains eligible for retirement on a later close.
func TestReopenedLaneTracksCanonicalForecastBeforeLaterClosure(t *testing.T) {
	t.Parallel()
	// closeEvent completes the lane against the canonical terminal work.
	//
	// Example: The forecast is retired each time the lane closes.
	const closeEvent = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	// reopen restores the lane using its active canonical forecast.
	//
	// Example: A later source target change must update ownership before reclosure.
	const reopen = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	changed := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	require.NotEqual(t, linkedLedger, completed)
	require.NotEqual(t, linkedForecasts, inactive)
	require.NotEqual(t, linkedForecasts, changed)
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
	require.NoError(t, os.WriteFile(forecastPath, []byte(changed), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(updated)
	require.NoError(t, err)
	require.NotNil(t, report.Lanes[0].Forecast)
	require.Equal(t, "2026-11-02T16:30:00Z", report.Lanes[0].Forecast.Target)
	inactiveChanged := strings.Replace(changed, "status: active", "status: inactive", 1)
	require.NotEqual(t, changed, inactiveChanged)
	require.NoError(t, os.WriteFile(ledgerPath, []byte(completed), 0600))
	require.NoError(t, os.WriteFile(forecastPath, []byte(inactiveChanged), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(closeEvent+reopen+closeEvent), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Nil(t, report.Lanes[0].Forecast)
	require.Empty(t, report.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
