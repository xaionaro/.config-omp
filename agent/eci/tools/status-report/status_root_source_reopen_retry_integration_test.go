package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootReopenWaitsForCanonicalSourcesOnDisk checks a persisted lifecycle retry.
//
// Example: A completed root is reopened only after work and forecast become active.
func TestRootReopenWaitsForCanonicalSourcesOnDisk(t *testing.T) {
	// root is completed canonical work with its forecast retired.
	//
	// Example: Reopening waits until this work becomes active again.
	const root = `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  work:root:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: lead, state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	// rootForecast begins inactive and is later restored to active.
	//
	// Example: A forecast cannot be reopened while its source remains retired.
	const rootForecast = `  root:
    kind: root
    outcome: Root result
    status: inactive
    assessment: complete
    target: '2026-11-02T18:00:00Z'
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	// event replaces the exact completion with a newly active root forecast.
	//
	// Example: The pending event is retried after both source files are updated.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/work:root","expected_prior":{"work_ref":"project-understanding.yaml#/records/work:root","outcome":"Root result","completed_at":"2026-11-02T15:00:00Z"},"record":{"forecast_ref":"forecasts.yaml#/records/root","outcome":"Root result","target":"2026-11-02T18:00:00Z","assessment":"complete"}}]}}}` + "\n"
	report := linkedReport + `root_completions: [{work_ref: 'project-understanding.yaml#/records/work:root', outcome: Root result, completed_at: '2026-11-02T15:00:00Z'}]
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
	require.Len(t, parsed.RootCompletions, 1)
	require.Empty(t, parsed.RootForecasts)
	require.Len(t, parsed.StatusSync.PendingRootAdditions, 1)
	active := strings.Replace(ledger, "owner: lead, state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles", "owner: lead, state: active, next_action: Finish, reporting_roles", 1)
	activeForecast := strings.Replace(forecasts, "outcome: Root result\n    status: inactive", "outcome: Root result\n    status: active", 1)
	require.NotEqual(t, ledger, active)
	require.NotEqual(t, forecasts, activeForecast)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(active), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(activeForecast), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Empty(t, parsed.RootCompletions)
	require.Len(t, parsed.RootForecasts, 1)
	require.Equal(t, "Root result", parsed.RootForecasts[0].Outcome)
	require.Empty(t, parsed.StatusSync.PendingRootAdditions)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
