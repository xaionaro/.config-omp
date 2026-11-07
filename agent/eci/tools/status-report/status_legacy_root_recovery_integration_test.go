package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyRootCorrectionRecovery restores the prior and corrected log entries.
//
// Example: An interrupted older report publishes only the source-backed correction.
func TestLegacyRootCorrectionRecovery(t *testing.T) {
	// first records the original completion.
	//
	// Example: Accept the original result at 15:00.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}}]}}}`
	// second corrects the original completion.
	//
	// Example: Correct the completion time to 15:05.
	const second = `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"},"record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}}]}}}`
	// saved is the legacy correction queue with its predecessor.
	//
	// Example: Recover a pending correction after an upgrade.
	const saved = `{"cursor":2,"applied":{},"latest":{},"pending":{},"pending_additions":{"addition/root_completion/project-understanding.yaml#/records/w":{"line":2,"target":"root_completion","id":"project-understanding.yaml#/records/w","evidence":"corrected","record":"{\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"completed_at\":\"2026-11-02T15:05:00Z\"}","expected_prior":"{\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"completed_at\":\"2026-11-02T15:00:00Z\"}","prior_line":1,"prior_record":"{\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"completed_at\":\"2026-11-02T15:00:00Z\"}"}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+"\n"+second+"\n"), 0600))
	initial, err := putSyncState([]byte(valid), saved)
	require.NoError(t, err)
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
	result, _, err := projectLogStatus(dir, initial, ledger, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootCompletions, 1)
	require.Equal(t, "2026-11-02T15:05:00Z", report.RootCompletions[0].CompletedAt)
	require.Empty(t, report.StatusSync.PendingAdditions)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	retried, _, err := projectLogStatus(dir, result, ledger, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retried)
}
