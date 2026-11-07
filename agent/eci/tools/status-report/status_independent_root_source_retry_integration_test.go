package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIndependentRootCompletionWaitsForItsOwnSource checks source isolation.
//
// Example: One ready root completes while a sibling waits for canonical completion.
func TestIndependentRootCompletionWaitsForItsOwnSource(t *testing.T) {
	// sources contain one completed and one still-active root.
	//
	// Example: The second source changes only after the first completion is published.
	const sources = `  root-outcome-one: {section: progress, type: outcome, data: {description: First root}}
  root-outcome-two: {section: progress, type: outcome, data: {description: Second root}}
  root-one:
    section: progress
    type: work
    links: {outcomes: [root-outcome-one]}
    data: {owner: one, state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [root]}
  root-two:
    section: progress
    type: work
    links: {outcomes: [root-outcome-two]}
    data: {owner: two, state: active, next_action: Finish, reporting_roles: [root]}
`
	// event records the independent root completions in one log occurrence.
	//
	// Example: A pending sibling must not prevent the ready completion from applying.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/root-one","outcome":"First root","completed_at":"2026-11-02T15:00:00Z"}},{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/root-two","outcome":"Second root","completed_at":"2026-11-02T15:05:00Z"}}]}}}` + "\n"
	ledger := linkedLedger + sources
	dir := writeLinkedSources(t, ledger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, report.RootCompletions, 1)
	require.Equal(t, "project-understanding.yaml#/records/root-one", report.RootCompletions[0].WorkRef)
	require.Len(t, report.StatusSync.PendingRootAdditions, 1)
	completed := strings.Replace(ledger, "owner: two, state: active, next_action: Finish, reporting_roles: [root]", "owner: two, state: completed, completed_at: '2026-11-02T15:05:00Z', reporting_roles: [root]", 1)
	require.NotEqual(t, ledger, completed)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(completed), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootCompletions, 2)
	require.Equal(t, "project-understanding.yaml#/records/root-one", report.RootCompletions[0].WorkRef)
	require.Equal(t, "project-understanding.yaml#/records/root-two", report.RootCompletions[1].WorkRef)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
