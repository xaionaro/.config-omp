package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshPreservesYAMLContent checks quoted delimiters, comments, and Unicode through the public refresh command.
//
// Example: A bracket inside a quoted risk is not the end of its flow sequence.
func TestRefreshPreservesYAMLContent(t *testing.T) {
	t.Parallel()
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	for _, tc := range []struct {
		name   string
		report string
		keep   []string
	}{
		{name: "quoted blocker bracket", report: strings.Replace(linkedReport, "blockers_risks: []", "blockers_risks: ['risk ] here']", 1), keep: []string{"'risk ] here'"}},
		{name: "flow comment before fields", report: strings.Replace(linkedReport, forecast, strings.Replace(forecast, "forecast: {", "forecast: {\n      # context } untouched\n      ", 1), 1), keep: []string{"# context } untouched"}},
		{name: "flow comment after opening", report: strings.Replace(linkedReport, forecast, strings.Replace(forecast, "forecast: {", "forecast: {# context } untouched\n      ", 1), 1), keep: []string{"# context } untouched"}},
		{name: "unicode and quoted delimiters", report: strings.Replace(linkedReport, forecast, strings.Replace(forecast, "outcome: First result", "outcome: 'λ, ] # ''quoted'''", 1)+" # keep forecast comment", 1), keep: []string{"λ, ] # ''quoted''", "# keep forecast comment"}},
		{name: "CRLF report", report: strings.ReplaceAll(linkedReport, "\n", "\r\n"), keep: []string{"\r\n"}},
		{name: "ordinary block forecast", report: strings.Replace(linkedReport, forecast, "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial # keep block comment", 1), keep: []string{"# keep block comment"}},
	} {
		t.Run(tc.name,
			// Execute one preservation case through refresh.
			//
			// Example: Preserve a comment adjacent to a flow mapping.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				report := tc.report + "details:\n  unrelated: 'keep ] # λ' # keep unrelated\n"
				if tc.name == "CRLF report" {
					report = strings.ReplaceAll(strings.ReplaceAll(report, "\r\n", "\n"), "\n", "\r\n")
				}
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new basis\n", 1)
				if tc.name == "unicode and quoted delimiters" {
					source = strings.Replace(source, "outcome: First result", "outcome: 'λ, ] # ''quoted'''", 1)
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
				var out, errOut bytes.Buffer
				code := run([]string{"refresh", dir, "requirement_change"}, strings.NewReader(""), &out, &errOut)
				updated, err := os.ReadFile(path)
				require.NoError(t, err)
				_, err = parse(updated)
				require.NoError(t, err, "refresh must retain valid YAML")
				if tc.name == "CRLF report" {
					require.NotContains(t, strings.ReplaceAll(string(updated), "\r\n", ""), "\n")
				}
				for _, s := range append(tc.keep, "unrelated: 'keep ] # λ' # keep unrelated") {
					require.Contains(t, string(updated), s)
				}
				if code != 0 {
					require.Contains(t, string(updated), "scope_complete: false")
					require.Contains(t, string(updated), "forecast")
				} else {
					require.Contains(t, string(updated), "new basis")
				}
			})
	}
}

// TestRefreshRefusesCommentedProjection checks that unsupported edits preserve the original content and mark the report incomplete.
//
// Example: A comment inside a projection survives an unsafe rewrite.
func TestRefreshRefusesCommentedProjection(t *testing.T) {
	t.Parallel()
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	block := "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial\n      source_projection:\n        target_basis: old # preserve this note"
	report := strings.Replace(linkedReport, forecast, block, 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new basis\n", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	var out, errOut bytes.Buffer
	code := run([]string{"refresh", dir, "forecast_change"}, strings.NewReader(""), &out, &errOut)
	require.NotEqual(t, 0, code)
	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	_, err = parse(updated)
	require.NoError(t, err)
	require.Contains(t, string(updated), "target_basis: old # preserve this note")
	require.NotContains(t, string(updated), "target_basis: new basis")
	require.Contains(t, string(updated), "scope_complete: false")
	require.Contains(t, string(updated), "forecasts.yaml#/records/first")
}
