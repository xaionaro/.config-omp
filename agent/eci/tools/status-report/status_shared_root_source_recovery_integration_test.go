package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSharedRootForecastRecoversAfterSourceRepair checks persisted source retry.
//
// Example: A shared root forecast waits until both work outcomes agree on disk.
func TestSharedRootForecastRecoversAfterSourceRepair(t *testing.T) {
	t.Parallel()
	// event requests a forecast shared by two active root work records.
	//
	// Example: An inconsistent source must not be projected prematurely.
	const event = `{"event":"forecast_change","details":{"status_update":{"additions":[{"target":"root_forecast","record":{"forecast_ref":"forecasts.yaml#/records/root","outcome":"Root result","target":"2026-11-02T18:00:00Z","assessment":"complete"}}]}}}` + "\n"
	// roots supplies conflicting outcomes for the shared forecast.
	//
	// Example: The second work record is relinked to the shared outcome.
	const roots = `  root-outcome-one: {section: progress, type: outcome, data: {description: Root result}}
  root-outcome-two: {section: progress, type: outcome, data: {description: Wrong result}}
  root-one:
    section: progress
    type: work
    links: {outcomes: [root-outcome-one]}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
  root-two:
    section: progress
    type: work
    links: {outcomes: [root-outcome-two]}
    data: {owner: two, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	// rootForecast is the canonical forecast referenced by both work records.
	//
	// Example: Its target and assessment remain stable through the retry.
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
	ledger := linkedLedger + roots
	dir := writeLinkedSources(t, ledger, linkedForecasts+rootForecast)
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
	fixed := strings.Replace(ledger, "outcomes: [root-outcome-two]", "outcomes: [root-outcome-one]", 1)
	require.NotEqual(t, ledger, fixed)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(fixed), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootForecasts, 1)
	require.Equal(t, "Root result", report.RootForecasts[0].Outcome)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
