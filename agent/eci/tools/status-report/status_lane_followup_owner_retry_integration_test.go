package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneClosureWaitsForOwnerFollowupRestoration checks atomic source-backed clearing.
//
// Example: An edited followup marker blocks closure until the owner restores its prior value.
func TestLaneClosureWaitsForOwnerFollowupRestoration(t *testing.T) {
	t.Parallel()
	// followup records the managed marker on an active lane.
	//
	// Example: A later explicit clear owns the originally projected value.
	const followup = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"root_task_status":"FOLLOWUP"}}]}}}` + "\n"
	// closeEvent explicitly clears that marker while closing canonical work.
	//
	// Example: A changed marker prevents the complete lane transition.
	const closeEvent = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","clear":["root_task_status"],"fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(followup), 0600))
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	initial, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(initial), `"root_task_status": "FOLLOWUP"`, `"root_task_status": "FOLLOWUP_PAUSED"`, 1)
	require.NotEqual(t, string(initial), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, completed)
	require.NotEqual(t, linkedForecasts, inactive)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(completed), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(inactive), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(followup+closeEvent), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
	require.Equal(t, rootTaskFollowupPaused, report.Lanes[0].RootTaskStatus)
	require.NotEmpty(t, report.StatusSync.Groups)
	restored := strings.Replace(string(pending), `"root_task_status": "FOLLOWUP_PAUSED"`, `"root_task_status": "FOLLOWUP"`, 1)
	require.NotEqual(t, string(pending), restored)
	require.NoError(t, os.WriteFile(path, []byte(restored), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Equal(t, rootTaskNone, report.Lanes[0].RootTaskStatus)
	require.Empty(t, report.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
