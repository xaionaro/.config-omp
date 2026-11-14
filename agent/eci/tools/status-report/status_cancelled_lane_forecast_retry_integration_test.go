package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelledLaneWaitsForForecastRetirementOnDisk checks both source transitions.
//
// Example: Cancelling work alone does not authorize a lane while its forecast is active.
func TestCancelledLaneWaitsForForecastRetirementOnDisk(t *testing.T) {
	t.Parallel()
	// source is active canonical lane work with a linked forecast.
	//
	// Example: Both work and forecast must become terminal before replay applies.
	const source = `  outcome:third: {section: progress, type: outcome, data: {description: Third result}}
  work:third:
    section: progress
    type: work
    links: {outcomes: ['outcome:third']}
    data: {owner: three, state: active, next_action: Finish, reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/third'}
`
	// forecast is initially active and later retired on disk.
	//
	// Example: A cancelled lane must not coexist with an active canonical forecast.
	const forecast = `  third:
    kind: lane
    outcome: Third result
    status: active
    assessment: complete
    target: '2026-11-02T18:00:00Z'
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	// event requests the cancelled lane without an active report forecast.
	//
	// Example: Its evidence permits projection after both sources are terminal.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"lane","evidence":"owner withdrew","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/work:third","outcome":"Third result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW","lifecycle":"cancelled"}}]}}}` + "\n"
	ledger := linkedLedger + source
	forecasts := linkedForecasts + forecast
	dir := writeLinkedSources(t, ledger, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	cancelled := strings.Replace(ledger, "owner: three, state: active", "owner: three, state: cancelled", 1)
	require.NotEqual(t, ledger, cancelled)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(cancelled), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 2)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	inactive := strings.Replace(forecasts, "outcome: Third result\n    status: active", "outcome: Third result\n    status: inactive", 1)
	require.NotEqual(t, forecasts, inactive)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(inactive), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 3)
	require.Equal(t, laneLifecycleCancelled, report.Lanes[2].Lifecycle)
	require.Nil(t, report.Lanes[2].Forecast)
	require.Empty(t, report.StatusSync.PendingAdditions)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
