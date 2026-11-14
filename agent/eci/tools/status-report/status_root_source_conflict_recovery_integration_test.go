package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootCompletionRecoversAfterOwnerRestoresPrior checks source-backed retry.
//
// Example: A changed forecast delays one completion while an independent root completes.
func TestRootCompletionRecoversAfterOwnerRestoresPrior(t *testing.T) {
	t.Parallel()
	// roots are two independent canonical completions.
	//
	// Example: The second root is not blocked by the first root's owner edit.
	const roots = `  root-outcome-one: {section: progress, type: outcome, data: {description: First root}}
  root-outcome-two: {section: progress, type: outcome, data: {description: Second root}}
  root-one:
    section: progress
    type: work
    links: {outcomes: [root-outcome-one]}
    data: {owner: one, state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root-one'}
  root-two:
    section: progress
    type: work
    links: {outcomes: [root-outcome-two]}
    data: {owner: two, state: completed, completed_at: '2026-11-02T15:05:00Z', reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root-two'}
`
	// retired are the canonical forecasts no longer active after completion.
	//
	// Example: A completion may retire the exact matching report forecast.
	const retired = `  root-one: {kind: root, status: inactive}
  root-two: {kind: root, status: inactive}
`
	// events request both completions and their corresponding prior retirements.
	//
	// Example: An outdated prior remains pending until the owner resolves it.
	const events = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":{"forecast_ref":"forecasts.yaml#/records/root-one","outcome":"First root","target":"2026-11-02T16:00:00Z","assessment":"complete"},"record":{"work_ref":"project-understanding.yaml#/records/root-one","outcome":"First root","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":{"forecast_ref":"forecasts.yaml#/records/root-two","outcome":"Second root","target":"2026-11-02T17:00:00Z","assessment":"complete"},"record":{"work_ref":"project-understanding.yaml#/records/root-two","outcome":"Second root","completed_at":"2026-11-02T15:05:00Z"}}]}}}` + "\n"
	report := linkedReport + `# owner first
root_forecasts:
  - {forecast_ref: 'forecasts.yaml#/records/root-one', outcome: First root, target: '2026-11-02T16:30:00Z', assessment: complete}
  - {forecast_ref: 'forecasts.yaml#/records/root-two', outcome: Second root, target: '2026-11-02T17:00:00Z', assessment: complete}
`
	dir := writeLinkedSources(t, linkedLedger+roots, linkedForecasts+retired)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(events), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, parsed.RootForecasts, 1)
	require.Len(t, parsed.RootCompletions, 1)
	require.Equal(t, "project-understanding.yaml#/records/root-two", parsed.RootCompletions[0].WorkRef)
	require.Len(t, parsed.StatusSync.PendingRootAdditions, 1)
	fixed := strings.Replace(string(pending), "2026-11-02T16:30:00Z", "2026-11-02T16:00:00Z", 1)
	require.NotEqual(t, string(pending), fixed)
	require.NoError(t, os.WriteFile(path, []byte(fixed), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Empty(t, parsed.RootForecasts)
	require.Len(t, parsed.RootCompletions, 2)
	require.Empty(t, parsed.StatusSync.PendingRootAdditions)
	require.Contains(t, string(result), "# owner first")
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
