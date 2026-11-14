package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelledRootWaitsForForecastRetirementOnDisk checks source-backed retry.
//
// Example: A cancelled root retains its report forecast while the source is still active.
func TestCancelledRootWaitsForForecastRetirementOnDisk(t *testing.T) {
	t.Parallel()
	// root is cancelled work whose canonical forecast has not yet been retired.
	//
	// Example: The report forecast cannot be removed until both sources agree.
	const root = `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  work:root:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: lead, state: cancelled, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	// rootForecast is initially active and is later retired by its owner.
	//
	// Example: Inactive status authorizes the pending forecast removal.
	const rootForecast = `  root:
    kind: root
    outcome: Root result
    status: active
    assessment: complete
    target: '2026-11-02T18:00:00Z'
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	// event requests retirement of the exact existing forecast.
	//
	// Example: A cancelled outcome carries explicit evidence and expected prior.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"root_forecast","id":"forecasts.yaml#/records/root","work_ref":"project-understanding.yaml#/records/work:root","evidence":"owner cancelled","values":{"expected_prior":{"forecast_ref":"forecasts.yaml#/records/root","outcome":"Root result","target":"2026-11-02T18:00:00Z","assessment":"complete"}},"clear":["record"]}]}}}` + "\n"
	report := linkedReport + `root_forecasts: [{forecast_ref: 'forecasts.yaml#/records/root', outcome: Root result, target: '2026-11-02T18:00:00Z', assessment: complete}]
`
	forecasts := linkedForecasts + rootForecast
	dir := writeLinkedSources(t, linkedLedger+root, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, parsed.RootForecasts, 1)
	require.NotEmpty(t, parsed.StatusSync.Groups)
	inactive := strings.Replace(forecasts, "outcome: Root result\n    status: active", "outcome: Root result\n    status: inactive", 1)
	require.NotEqual(t, forecasts, inactive)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(inactive), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Empty(t, parsed.RootForecasts)
	require.Empty(t, parsed.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
