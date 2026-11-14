package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelledLaneAdditionRecoversAfterSourceCancellation checks persisted retry.
//
// Example: A withdrawn lane is added only after canonical work is cancelled.
func TestCancelledLaneAdditionRecoversAfterSourceCancellation(t *testing.T) {
	t.Parallel()
	// source is a third active work record linked to its outcome.
	//
	// Example: Its later cancellation authorizes the pending lane addition.
	const source = `  outcome:third: {section: progress, type: outcome, data: {description: Third result}}
  work:third:
    section: progress
    type: work
    links: {outcomes: ['outcome:third']}
    data: {owner: three, state: active, next_action: Finish, reporting_roles: [lane]}
`
	// event records the owner's decision to withdraw that lane.
	//
	// Example: The event remains pending while canonical work remains active.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"lane","evidence":"owner withdrew","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/work:third","outcome":"Third result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW","lifecycle":"cancelled"}}]}}}` + "\n"
	ledger := linkedLedger + source
	dir := writeLinkedSources(t, ledger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 2)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	cancelled := strings.Replace(ledger, "owner: three, state: active", "owner: three, state: cancelled", 1)
	require.NotEqual(t, ledger, cancelled)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(cancelled), 0600))
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
