package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyRootCorrectionWithAlteredPredecessorPreservesUnrelatedProgress checks recovery isolation.
//
// Example: An inconsistent saved predecessor stays pending while another update applies.
func TestLegacyRootCorrectionWithAlteredPredecessorPreservesUnrelatedProgress(t *testing.T) {
	// first is the original completed root event.
	//
	// Example: Publish the first accepted completion at 15:00.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n"
	// correction changes that root completion to match the canonical source.
	//
	// Example: Record an accepted timestamp correction at 15:05.
	const correction = `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"},"record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}}]}}}` + "\n"
	// later is a disjoint progress update following the saved cursor.
	//
	// Example: Record a separate worker's successful verification.
	const later = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"work:other","fields":{"text":"other work advanced"}}]}}}` + "\n"
	// saved contains a legacy correction whose stored predecessor disagrees with the log.
	//
	// Example: Preserve conflicting historical metadata for manual reconciliation.
	const saved = `{"cursor":2,"applied":{},"latest":{"addition/root_completion/project-understanding.yaml#/records/w":2},"pending":{},"pending_additions":{"addition/root_completion/project-understanding.yaml#/records/w":{"line":2,"target":"root_completion","id":"project-understanding.yaml#/records/w","evidence":"corrected","record":"{\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"completed_at\":\"2026-11-02T15:05:00Z\"}","expected_prior":"{\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"completed_at\":\"2026-11-02T15:00:00Z\"}","prior_line":1,"prior_record":"{\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"completed_at\":\"2026-11-02T14:59:00Z\"}"}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+correction+later), 0600))
	initial, err := putSyncState([]byte(valid), saved)
	require.NoError(t, err)
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
	result, _, err := projectLogStatus(dir, initial, ledger, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, report.RootCompletions)
	require.NotEmpty(t, report.StatusSync.PendingAdditions)
	require.Contains(t, report.Progress, "other work advanced")
	require.Contains(t, report.Progress, "Implementation started")
	require.Equal(t, 3, report.StatusSync.Cursor)
}
