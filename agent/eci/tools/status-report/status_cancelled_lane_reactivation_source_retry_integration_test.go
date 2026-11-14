package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelledLaneReactivationWaitsForSources checks persisted lifecycle retry.
//
// Example: A withdrawn lane resumes only after its work and forecast reactivate.
func TestCancelledLaneReactivationWaitsForSources(t *testing.T) {
	t.Parallel()
	// cancel records the owner's withdrawal of the lane.
	//
	// Example: The inactive canonical forecast is removed from the report.
	const cancel = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"withdrawn","fields":{"lifecycle":"cancelled"}}]}}}` + "\n"
	// resume explicitly clears cancellation and records the paused state.
	//
	// Example: Active canonical work and forecast authorize the later transition.
	const resume = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"resumed","clear":["lifecycle"],"fields":{"implementation_status":"PAUSED"}}]}}}` + "\n"
	cancelled := strings.Replace(linkedLedger, "owner: one, state: active", "owner: one, state: cancelled", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, cancelled)
	require.NotEqual(t, linkedForecasts, inactive)
	dir := writeLinkedSources(t, cancelled, inactive)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(cancel), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	require.NoError(t, os.WriteFile(logPath, []byte(cancel+resume), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Equal(t, laneLifecycleCancelled, report.Lanes[0].Lifecycle)
	require.Nil(t, report.Lanes[0].Forecast)
	require.NotEmpty(t, report.StatusSync.Groups)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, laneLifecycleNone, report.Lanes[0].Lifecycle)
	require.Equal(t, statusPaused, report.Lanes[0].ImplementationStatus)
	require.NotNil(t, report.Lanes[0].Forecast)
	require.Empty(t, report.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
