package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootCorrectionWaitsForOwnerPriorRestoration checks source-backed ownership.
//
// Example: An owner-edited completion is preserved until its exact prior is restored.
func TestRootCorrectionWaitsForOwnerPriorRestoration(t *testing.T) {
	// source is completed canonical work backing the original root completion.
	//
	// Example: Its completion time changes when the correction is accepted.
	const source = `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  work:root:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: lead, state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [root]}
`
	// first publishes the source-backed original completion.
	//
	// Example: An owner may subsequently revise the visible report record.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n"
	// correction replaces exactly that original completion.
	//
	// Example: The owner's different value must not be overwritten automatically.
	const correction = `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:00:00Z"},"record":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:05:00Z"}}]}}}` + "\n"
	ledger := linkedLedger + source
	dir := writeLinkedSources(t, ledger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	initial, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(initial), `"completed_at":"2026-11-02T15:00:00Z"`, `"completed_at":"2026-11-02T15:01:00Z"`, 1)
	require.NotEqual(t, string(initial), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	corrected := strings.Replace(ledger, "completed_at: '2026-11-02T15:00:00Z'", "completed_at: '2026-11-02T15:05:00Z'", 1)
	require.NotEqual(t, ledger, corrected)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(corrected), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first+correction), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, report.RootCompletions, 1)
	require.Equal(t, "2026-11-02T15:01:00Z", report.RootCompletions[0].CompletedAt)
	require.Len(t, report.StatusSync.PendingRootAdditions, 1)
	restored := strings.Replace(string(pending), `"completed_at":"2026-11-02T15:01:00Z"`, `"completed_at":"2026-11-02T15:00:00Z"`, 1)
	require.NotEqual(t, string(pending), restored)
	require.NoError(t, os.WriteFile(path, []byte(restored), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootCompletions, 1)
	require.Equal(t, "2026-11-02T15:05:00Z", report.RootCompletions[0].CompletedAt)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	require.NoError(t, refreshTestStatus(dir, "correction"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
