package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForecastRecalibrationRecoversAfterSourceCorrection checks history consistency.
//
// Example: A mismatched current time blocks publication until the source is repaired.
func TestForecastRecalibrationRecoversAfterSourceCorrection(t *testing.T) {
	t.Parallel()
	// event records independent progress while canonical history is inconsistent.
	//
	// Example: Repairing the source permits this retained event to apply once.
	const event = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"queued claim"}}]}}}` + "\n"
	forecast := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	forecast = strings.Replace(forecast, "recalibration: {status: unchanged, evidence: initial}", "recalibration: {status: changed, prior: '2026-11-02T16:00:00Z', current: '2026-11-02T16:45:00Z', reason: review, evidence: verified}", 1)
	require.NotEqual(t, linkedForecasts, forecast)
	dir := writeLinkedSources(t, linkedLedger, forecast)
	path := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "current does not match target")
	blocked, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(blocked)
	require.NoError(t, err)
	require.NotContains(t, report.Progress, "queued claim")
	require.Equal(t, "2026-11-02T16:00:00Z", report.Lanes[0].Forecast.Target)
	fixed := strings.Replace(forecast, "current: '2026-11-02T16:45:00Z'", "current: '2026-11-02T16:30:00Z'", 1)
	require.NotEqual(t, forecast, fixed)
	require.NoError(t, os.WriteFile(forecastPath, []byte(fixed), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T16:30:00Z", report.Lanes[0].Forecast.Target)
	require.Equal(t, "2026-11-02T16:00:00Z", report.Lanes[0].Forecast.Prior)
	require.Equal(t, "review", report.Lanes[0].Forecast.Reason)
	require.Equal(t, "verified", report.Lanes[0].Forecast.Evidence)
	require.Equal(t, []string{"Implementation started", "queued claim"}, report.Progress)
	require.Equal(t, 1, report.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
