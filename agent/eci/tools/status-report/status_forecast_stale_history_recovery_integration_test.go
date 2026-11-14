package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForecastUnchangedSourceWaitsForStaleHistoryResolution checks recovery.
//
// Example: An owner removes obsolete recalibration fields before a retained event replays.
func TestForecastUnchangedSourceWaitsForStaleHistoryResolution(t *testing.T) {
	t.Parallel()
	// event is independent progress retained while forecast history is unresolved.
	//
	// Example: A successful retry applies it once after manual reconciliation.
	const event = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"queued claim"}}]}}}` + "\n"
	changed := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	changed = strings.Replace(changed, "recalibration: {status: unchanged, evidence: initial}", "recalibration: {status: changed, prior: '2026-11-02T16:00:00Z', current: '2026-11-02T16:30:00Z', reason: review, evidence: verified}", 1)
	require.NotEqual(t, linkedForecasts, changed)
	dir := writeLinkedSources(t, linkedLedger, changed)
	path := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	initial, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(initial)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T16:00:00Z", report.Lanes[0].Forecast.Prior)
	require.Equal(t, "review", report.Lanes[0].Forecast.Reason)
	unchanged := strings.Replace(changed, "recalibration: {status: changed, prior: '2026-11-02T16:00:00Z', current: '2026-11-02T16:30:00Z', reason: review, evidence: verified}", "recalibration: {status: unchanged, evidence: current}", 1)
	require.NotEqual(t, changed, unchanged)
	require.NoError(t, os.WriteFile(forecastPath, []byte(unchanged), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "stale")
	blocked, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(blocked)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T16:00:00Z", report.Lanes[0].Forecast.Prior)
	require.Equal(t, "review", report.Lanes[0].Forecast.Reason)
	require.NotContains(t, report.Progress, "queued claim")
	resolved := strings.Replace(string(blocked), `, "prior": "2026-11-02T16:00:00Z"`, "", 1)
	resolved = strings.Replace(resolved, `, "reason": "review"`, "", 1)
	require.NotEqual(t, string(blocked), resolved)
	require.NotContains(t, resolved, `"reason": "review"`)
	require.NoError(t, os.WriteFile(path, []byte(resolved), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Empty(t, report.Lanes[0].Forecast.Prior)
	require.Empty(t, report.Lanes[0].Forecast.Reason)
	require.Equal(t, "current", report.Lanes[0].Forecast.Evidence)
	require.Equal(t, []string{"Implementation started", "queued claim"}, report.Progress)
	require.Equal(t, 1, report.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
