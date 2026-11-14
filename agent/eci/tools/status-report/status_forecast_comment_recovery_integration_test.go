package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForecastProjectionRecoversAfterOwnerCommentResolution checks failed-refresh retry.
//
// Example: A source update waits until an annotated projection can be replaced safely.
func TestForecastProjectionRecoversAfterOwnerCommentResolution(t *testing.T) {
	t.Parallel()
	// event adds independent progress while the forecast projection is blocked.
	//
	// Example: Resolving the comment permits the retained event to apply once.
	const event = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"queued claim"}}]}}}` + "\n"
	// original is the first lane's compact forecast before adding owner annotation.
	//
	// Example: Replace its source projection with an annotated block layout.
	const original = "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	// annotated includes a comment inside the projection that cannot be discarded.
	//
	// Example: The owner resolves the annotation before the source update is retried.
	const annotated = "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial\n      source_projection:\n        target_basis: old # owner note"
	report := strings.Replace(linkedReport, original, annotated, 1)
	require.NotEqual(t, linkedReport, report)
	forecast := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new basis\n", 1)
	require.NotEqual(t, linkedForecasts, forecast)
	dir := writeLinkedSources(t, linkedLedger, forecast)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "contains comments")
	blocked, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(blocked)
	require.NoError(t, err)
	require.NotContains(t, parsed.Progress, "queued claim")
	require.Contains(t, string(blocked), "target_basis: old # owner note")
	resolved := strings.Replace(string(blocked), " # owner note", "", 1)
	require.NotEqual(t, string(blocked), resolved)
	require.NoError(t, os.WriteFile(path, []byte(resolved), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started", "queued claim"}, parsed.Progress)
	require.Contains(t, string(result), "new basis")
	require.Equal(t, 1, parsed.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
