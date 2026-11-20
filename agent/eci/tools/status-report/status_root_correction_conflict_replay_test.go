package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootCorrectionReplayPreservesConflicts checks the production correction path.
//
// Example: Owner edits, comments and changed source time prevent a replacement.
func TestRootCorrectionReplayPreservesConflicts(t *testing.T) {
	t.Parallel()
	old := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	newer := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":` + old + `}]}}}` + "\n"
	second := `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":` + old + `,"record":` + newer + `}]}}}` + "\n"
	saved := `{"cursor":1,"applied":{"addition/root_completion/project-understanding.yaml#/records/w":` + strconv.Quote(old) + `},"latest":{"addition/root_completion/project-understanding.yaml#/records/w":1}}`
	for _, mode := range []string{"owner edit", "comment", "source mismatch"} {
		t.Run(mode,
			// Check one reason an explicit correction must remain pending.
			//
			// Example: Preserve a comment on the existing completion.
			func(t *testing.T) {
				prior := old
				expectedTime := "2026-11-02T15:00:00Z"
				section := "root_completions: [" + prior + "]\n"
				sourceTime := "2026-11-02T15:05:00Z"
				switch mode {
				case "owner edit":
					prior = strings.Replace(old, "15:00:00Z", "15:01:00Z", 1)
					expectedTime = "2026-11-02T15:01:00Z"
					section = "root_completions: [" + prior + "]\n"
				case "comment":
					section = "root_completions:\n  - " + prior + " # keep owner comment\n"
				case "source mismatch":
					sourceTime = "2026-11-02T15:06:00Z"
				}
				initial, err := putSyncState([]byte(valid+section), saved)
				require.NoError(t, err)
				dir := t.TempDir()
				require.NoError(t, writeSessionLog(dir, []byte(first+second)))
				ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: sourceTime, Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
				out, _, err := projectLogStatus(dir, initial, ledger, forecastSource{})
				require.NoError(t, err)
				report, err := parse(out)
				require.NoError(t, err)
				require.Len(t, report.RootCompletions, 1)
				require.Equal(t, expectedTime, report.RootCompletions[0].CompletedAt)
				require.NotEmpty(t, report.StatusSync.PendingRootAdditions)
				if mode == "comment" {
					require.Contains(t, string(out), "# keep owner comment")
				}
			})
	}
}
