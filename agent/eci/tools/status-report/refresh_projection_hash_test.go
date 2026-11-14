package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshProjectionAllowsQuotedHash checks source projection replacement.
//
// Example: A quoted issue reference is data, while adjacent comments are retained.
func TestRefreshProjectionAllowsQuotedHash(t *testing.T) {
	t.Parallel()
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	for _, tc := range []struct{ name, projection string }{
		{"flow single", "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial, source_projection: {baseline_evidence: initial, target_basis: 'issue #42'}} # adjacent owner comment"},
		{"flow double", "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial, source_projection: {baseline_evidence: initial, target_basis: \"issue #42\"}} # adjacent owner comment"},
		{"block", "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial\n      source_projection:\n        baseline_evidence: initial\n        target_basis: 'issue #42'\n    # adjacent owner comment"},
	} {
		t.Run(tc.name,
			// Refresh one YAML representation and preserve its unrelated comment.
			//
			// Example: A hash inside a quoted scalar does not block the edit.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				report := strings.Replace(linkedReport, forecast, tc.projection, 1)
				require.NotEqual(t, linkedReport, report)
				source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new basis\n", 1)
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
				require.NoError(t, refreshTestStatus(dir, "forecast_change"))
				updated, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Contains(t, string(updated), "new basis")
				require.Contains(t, string(updated), "# adjacent owner comment")
				require.NotContains(t, string(updated), "source_projection contains comments; manual refresh required")
			})
	}
}

// TestRefreshProjectionKeepsRealInnerComment checks a true flow comment.
//
// Example: A source update remains pending rather than consuming an owner note.
func TestRefreshProjectionKeepsRealInnerComment(t *testing.T) {
	t.Parallel()
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	replacement := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial, source_projection: {baseline_evidence: initial, # preserve inner note\n      target_basis: 'issue #42'}}"
	report := strings.Replace(linkedReport, forecast, replacement, 1)
	require.NotEqual(t, linkedReport, report)
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new basis\n", 1)
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "manual refresh required")
	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(updated), "# preserve inner note")
	require.Contains(t, string(updated), "issue #42")
	require.NotContains(t, string(updated), "new basis")
	before, err := yamlComments([]byte(report))
	require.NoError(t, err)
	after, err := yamlComments(updated)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
