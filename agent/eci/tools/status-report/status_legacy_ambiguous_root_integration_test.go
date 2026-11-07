package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyAmbiguousRootOccurrenceStaysPending protects ambiguous replay identity.
//
// Example: Two identical same-line additions cannot identify an old pending entry.
func TestLegacyAmbiguousRootOccurrenceStaysPending(t *testing.T) {
	// saved is a legacy root completion without an occurrence ordinal.
	//
	// Example: Recover an older report after duplicate additions were appended.
	const saved = `{"cursor":1,"applied":{},"latest":{"addition/root_completion/project-understanding.yaml#/records/w":1},"pending":{},"pending_additions":{"addition/root_completion/project-understanding.yaml#/records/w":{"line":1,"target":"root_completion","id":"project-understanding.yaml#/records/w","evidence":"accepted","record":"{\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"completed_at\":\"2026-11-02T15:05:00Z\"}"}}}`
	// entry is one source addition repeated in the same log event.
	//
	// Example: An imported event accidentally includes two identical additions.
	const entry = `{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}}`
	dir := t.TempDir()
	log := `{"event":"lifecycle_change","details":{"status_update":{"additions":[` + entry + `,` + entry + `]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	initial, err := putSyncState([]byte(valid), saved)
	require.NoError(t, err)
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
	result, _, err := projectLogStatus(dir, initial, ledger, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, report.RootCompletions)
	require.NotEmpty(t, report.StatusSync.PendingAdditions)
}
