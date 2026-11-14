package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootForecastWaitsForCorrectCanonicalKind checks source-backed retry.
//
// Example: A root forecast remains pending while its source is classified as a lane.
func TestRootForecastWaitsForCorrectCanonicalKind(t *testing.T) {
	t.Parallel()
	// root is active work that explicitly uses a root forecast.
	//
	// Example: Its linked forecast must have the matching canonical kind.
	const root = `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  work:root:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: lead, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	// forecast initially has the wrong source kind.
	//
	// Example: Correcting its kind later authorizes the pending root forecast.
	const forecast = `  root:
    kind: lane
    outcome: Root result
    status: active
    assessment: complete
    target: '2026-11-02T18:00:00Z'
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	// event asks to add the root forecast with the canonical target.
	//
	// Example: A lane-kind source must not produce a root forecast.
	const event = `{"event":"forecast_change","details":{"status_update":{"additions":[{"target":"root_forecast","record":{"forecast_ref":"forecasts.yaml#/records/root","outcome":"Root result","target":"2026-11-02T18:00:00Z","assessment":"complete"}}]}}}` + "\n"
	forecasts := linkedForecasts + forecast
	dir := writeLinkedSources(t, linkedLedger+root, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Empty(t, report.RootForecasts)
	require.Len(t, report.StatusSync.PendingRootAdditions, 1)
	fixed := strings.Replace(forecasts, "  root:\n    kind: lane", "  root:\n    kind: root", 1)
	require.NotEqual(t, forecasts, fixed)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(fixed), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootForecasts, 1)
	require.Equal(t, "forecasts.yaml#/records/root", report.RootForecasts[0].ForecastRef)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
