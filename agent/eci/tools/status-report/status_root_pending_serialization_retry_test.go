package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPendingRootCompletionRetryIsStableUntilSourceCompletes checks persisted metadata.
//
// Example: A pending completion survives an unchanged retry, then applies after source completion.
func TestPendingRootCompletionRetryIsStableUntilSourceCompletes(t *testing.T) {
	t.Parallel()
	// root is the active source backing the pending completion.
	//
	// Example: The root cannot complete until its canonical state changes.
	const root = `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  work:root:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: lead, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	// rootForecast supplies the active report deadline.
	//
	// Example: Completion retires this forecast only after the source changes.
	const rootForecast = `  root:
    kind: root
    outcome: Root result
    status: active
    assessment: complete
    target: '2026-11-02T16:00:00Z'
    original_baseline: '2026-11-02T16:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	// event carries intentionally noncanonical object key ordering.
	//
	// Example: Persist the exact completion and prior while it awaits canonical work.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","record":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:00:00Z"},"evidence":"accepted","retire_forecast":true,"expected_prior":{"forecast_ref":"forecasts.yaml#/records/root","outcome":"Root result","target":"2026-11-02T16:00:00Z","assessment":"complete","baseline":"2026-11-02T16:00:00Z","evidence":"initial","source_projection":{"baseline_evidence":"initial"}}}]}}}` + "\n"
	report := linkedReport + `root_forecasts: [{forecast_ref: 'forecasts.yaml#/records/root', outcome: Root result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial, source_projection: {baseline_evidence: initial}}]
`
	ledger := linkedLedger + root
	forecasts := linkedForecasts + rootForecast
	dir := writeLinkedSources(t, ledger, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, parsed.StatusSync.PendingRootAdditions, 1)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(pending), string(retry))
	completed := strings.Replace(ledger, "owner: lead, state: active, next_action: Finish", "owner: lead, state: completed, completed_at: '2026-11-02T15:00:00Z', next_action: Finish", 1)
	inactive := strings.Replace(forecasts, "outcome: Root result\n    status: active", "outcome: Root result\n    status: inactive", 1)
	require.NotEqual(t, ledger, completed)
	require.NotEqual(t, forecasts, inactive)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(completed), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(inactive), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Empty(t, parsed.StatusSync.PendingRootAdditions)
	require.Len(t, parsed.RootCompletions, 1)
	require.Equal(t, "2026-11-02T15:00:00Z", parsed.RootCompletions[0].CompletedAt)
	require.Empty(t, parsed.RootForecasts)
}
