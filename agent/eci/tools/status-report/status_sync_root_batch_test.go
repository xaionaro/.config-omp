package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncRootCompletionCorrectionReplaysWithoutHistoricalSource checks causal replay.
//
// Example: A corrected completion matches current canonical work without fabricating its prior state.
func TestSyncRootCompletionCorrectionReplaysWithoutHistoricalSource(t *testing.T) {
	t.Parallel()
	old := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	corrected := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}`
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":` + old + `}]}}}` + "\n"
	second := `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":` + old + `,"record":` + corrected + `}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(valid), ledger, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootCompletions, 1)
	require.Equal(t, "2026-11-02T15:05:00Z", report.RootCompletions[0].CompletedAt)
}
