package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelledLaneAdditionWaitsForCancelledSource checks source-backed retry.
//
// Example: A cancellation remains pending while the source still reports active work.
func TestCancelledLaneAdditionWaitsForCancelledSource(t *testing.T) {
	t.Parallel()
	// event adds a cancelled lane with explicit evidence.
	//
	// Example: Record the owner's withdrawn implementation lane.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"lane","evidence":"owner withdrew","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW","lifecycle":"cancelled"}}]}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	work := workData{State: workStateActive, Roles: []reportingRole{reportingRoleLane}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: work}, "o": {Type: "outcome", Outcome: "Result"}}}
	pending, _, err := projectLogStatus(dir, []byte(valid), ledger, forecastSource{})
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Empty(t, report.Lanes)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	work.State = workStateCancelled
	ledger.Records["w"] = sourceRecord{Type: "work", OutcomeRefs: []string{"o"}, Data: work}
	result, _, err := projectLogStatus(dir, pending, ledger, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 1)
	require.Equal(t, laneLifecycleCancelled, report.Lanes[0].Lifecycle)
	require.Nil(t, report.Lanes[0].Forecast)
	require.Empty(t, report.StatusSync.PendingAdditions)
	retry, _, err := projectLogStatus(dir, result, ledger, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
