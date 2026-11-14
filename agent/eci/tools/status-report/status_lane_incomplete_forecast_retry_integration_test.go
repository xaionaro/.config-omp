package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneReopenWaitsForCompleteForecastOnDisk checks source-backed retry.
//
// Example: An active forecast needs both a target and outcome before reopening.
func TestLaneReopenWaitsForCompleteForecastOnDisk(t *testing.T) {
	t.Parallel()
	// closeEvent retires the first lane's completed forecast.
	//
	// Example: The lane becomes eligible for a later explicit reopening.
	const closeEvent = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	// reopen requests an active lane and its canonical forecast.
	//
	// Example: A missing source field must not create a partial forecast.
	const reopen = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	for _, tc := range []struct{ name, missing string }{
		{"missing target", "    target: '2026-11-02T16:00:00+00:00'\n"},
		{"missing outcome", "    outcome: First result\n"},
	} {
		t.Run(tc.name,
			// check persists an incomplete active forecast and later repairs it.
			//
			// Example: Restoring the missing target permits the pending reopening.
			func(t *testing.T) {
				completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish", 1)
				inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
				incomplete := strings.Replace(linkedForecasts, tc.missing, "", 1)
				require.NotEqual(t, linkedForecasts, incomplete)
				dir := writeLinkedSources(t, completed, inactive)
				path := filepath.Join(dir, "latest-status-report.yaml")
				logPath := filepath.Join(dir, "high_level_log.jsonl")
				forecastPath := filepath.Join(dir, "forecasts.yaml")
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(logPath, []byte(closeEvent), 0600))
				require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(forecastPath, []byte(incomplete), 0600))
				require.NoError(t, os.WriteFile(logPath, []byte(closeEvent+reopen), 0600))
				require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
				pending, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(pending)
				require.NoError(t, err)
				require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
				require.Nil(t, report.Lanes[0].Forecast)
				require.NotEmpty(t, report.StatusSync.Groups)
				require.NoError(t, os.WriteFile(forecastPath, []byte(linkedForecasts), 0600))
				require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err = parse(result)
				require.NoError(t, err)
				require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
				require.NotNil(t, report.Lanes[0].Forecast)
				require.Equal(t, "First result", report.Lanes[0].Forecast.Outcome)
				require.Empty(t, report.StatusSync.Groups)
				require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
				retry, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}
