package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootCorrectionWaitsForCanonicalCompletionOnDisk checks persisted lineage.
//
// Example: A corrected completion applies only after its source reaches the corrected time.
func TestRootCorrectionWaitsForCanonicalCompletionOnDisk(t *testing.T) {
	t.Parallel()
	// source starts active and later records the corrected completion time.
	//
	// Example: Neither historical nor corrected completion is published while active.
	const source = `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  work:root:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: lead, state: active, next_action: Finish, reporting_roles: [root]}
`
	// first records the historical completion that was not yet source-backed.
	//
	// Example: Its time is later corrected before either occurrence applies.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n"
	// correction names the prior occurrence and supplies the verified time.
	//
	// Example: Replay must preserve both records until current sources agree.
	const correction = `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:00:00Z"},"record":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:05:00Z"}}]}}}` + "\n"
	ledger := linkedLedger + source
	dir := writeLinkedSources(t, ledger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+correction), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Empty(t, report.RootCompletions)
	require.Len(t, report.StatusSync.PendingRootAdditions, 2)
	require.NoError(t, refreshTestStatus(dir, "correction"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, pending, retry)
	completed := strings.Replace(ledger, "owner: lead, state: active, next_action: Finish", "owner: lead, state: completed, completed_at: '2026-11-02T15:05:00Z', next_action: Finish", 1)
	require.NotEqual(t, ledger, completed)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(completed), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootCompletions, 1)
	require.Equal(t, "2026-11-02T15:05:00Z", report.RootCompletions[0].CompletedAt)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	require.NoError(t, refreshTestStatus(dir, "correction"))
	retry, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
