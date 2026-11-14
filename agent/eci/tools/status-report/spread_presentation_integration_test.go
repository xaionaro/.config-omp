package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCanonicalSpreadPresentation checks the native base and scenario projection through the CLI.
//
// Example: Lane and root forecasts each show a seven-column spread table.
func TestCanonicalSpreadPresentation(t *testing.T) {
	t.Parallel()
	projection := `source_projection:
        target_basis: conditional on review
        base_case:
          endpoint: '2026-11-02T15:45:00Z'
          packages:
            - {work: Finish work, duration: '20 minutes', basis: measured}
        scenarios:
          - {name: Delay, trigger: review delay, affected_work: Finish work, added_duration: '15 minutes', basis: guessed, evidence: review pending, assumptions: reviewer available, event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', evidence_gap: review duration, next_action: obtain timing}
`
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n"
	lane := "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: provisional\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial\n      " + projection
	report := strings.Replace(linkedReport, forecast, lane, 1)
	rootProjection := strings.ReplaceAll(projection, "        ", "      ")
	report += "root_forecasts:\n  - forecast_ref: 'forecasts.yaml#/records/root'\n    outcome: Root result\n    target: '2026-11-02T16:00:00Z'\n    assessment: provisional\n    baseline: '2026-11-02T16:00:00Z'\n    evidence: initial\n    " + rootProjection
	report = strings.Replace(report, "work_refs: ['project-understanding.yaml#/records/work:first']", "work_refs: ['project-understanding.yaml#/records/work:first', 'project-understanding.yaml#/records/root']", 1)
	ledger := linkedLedger + `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  root:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: lead, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	sourceSpread := `    target_basis: conditional on review
    base_case:
      endpoint: '2026-11-02T15:45:00Z'
      packages:
        - {work: Finish work, duration: '20 minutes', basis: measured}
    scenarios:
      - {name: Delay, trigger: review delay, affected_work: Finish work, added_duration: '15 minutes', basis: guessed, evidence: review pending, assumptions: reviewer available, event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', evidence_gap: review duration, next_action: obtain timing}
`
	forecasts := strings.Replace(linkedForecasts, "assessment: complete", "assessment: provisional", 1)
	forecasts = strings.Replace(forecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n"+sourceSpread, 1)
	forecasts += "  root:\n    kind: root\n    outcome: Root result\n    status: active\n    assessment: provisional\n    target: '2026-11-02T16:00:00Z'\n    original_baseline: '2026-11-02T16:00:00Z'\n    baseline_evidence: initial\n" + sourceSpread + "    recalibration: {status: unchanged, evidence: initial}\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(forecasts), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, testRun([]string{"render", path}, strings.NewReader(""), &out, &errOut), errOut.String())
	text := out.String()
	require.NotContains(t, text, "INCOMPLETE")
	require.Contains(t, text, "| Lane/root and case | Trigger/evidence | Affected work/dependency | Added duration/range and basis | Evidence assessed; assumptions/limits | Endpoint/range | Evidence gap / next fact-finding action |")
	for _, row := range []string{"First result — base case", "First result — Delay", "Root result — base case", "Root result — Delay"} {
		assertSpreadRow(t, text, row)
	}
	for _, value := range []string{"Finish work", "20 minutes", "measured", "review delay", "15 minutes", "guessed", "review pending", "reviewer available", "review duration", "obtain timing", "conditional on review"} {
		require.Contains(t, text, value)
	}
	require.Contains(t, text, "Forecast assessment: provisional")
	require.Contains(t, text, "target conditional")
}

// assertSpreadRow checks that a named case has exactly seven table cells.
//
// Example: A base-case row includes every required column.
func assertSpreadRow(
	t *testing.T,
	text string,
	name string,
) {
	t.Helper()
	prefix := "| " + name + " |"
	require.Contains(t, text, prefix)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			require.Len(t, strings.Split(line, "|"), 9, line)
		}
	}
}

// TestSpreadPresentationMissingAndLegacy checks unknown fields and preserved old presentation.
//
// Example: An unsupported scenario field is not invented.
func TestSpreadPresentationMissingAndLegacy(t *testing.T) {
	t.Parallel()
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	report := strings.Replace(linkedReport, forecast, strings.TrimSuffix(forecast, "}")+", source_projection: {base_case: {endpoint: unknown}, scenarios: [{name: Gap, endpoint: unknown, extra: preserve-me}]}}", 1)
	var out, errOut bytes.Buffer
	require.Equal(t, 0, testRun([]string{"render"}, strings.NewReader(report), &out, &errOut), errOut.String())
	require.Contains(t, out.String(), "| First result — Gap | unknown | unknown | unknown | unknown | unknown | unknown |")
	require.Contains(t, out.String(), "preserve-me")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, testRun([]string{"render"}, strings.NewReader(linkedReport), &out, &errOut), errOut.String())
	require.NotContains(t, out.String(), "| Lane/root and case |")
	out.Reset()
	errOut.Reset()
	legacy := strings.Replace(linkedReport, forecast, strings.TrimSuffix(forecast, "}")+", scenarios: [{case: legacy, trigger: old, affected_work: work, duration_basis: guess, evidence_assumptions: pending, endpoint: unknown, evidence_gap_next_action: next}]}", 1)
	require.Equal(t, 0, testRun([]string{"render"}, strings.NewReader(legacy), &out, &errOut), errOut.String())
	require.Contains(t, out.String(), "| First result — legacy |")
}

// TestNativeSpreadRetainsLegacy checks coexisting native and legacy information.
//
// Example: An unverified legacy scenario remains visible beside the native spread.
func TestNativeSpreadRetainsLegacy(t *testing.T) {
	t.Parallel()
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	report := strings.Replace(linkedReport, forecast, strings.TrimSuffix(forecast, "}")+", source_projection: {base_case: {endpoint: unknown}}, scenarios: [{case: legacy, trigger: legacy-only-proof-42, affected_work: legacy-work-42, duration_basis: legacy-duration-42, evidence_assumptions: legacy-evidence-42, endpoint: legacy-endpoint-42, evidence_gap_next_action: legacy-next-42}]}", 1)
	var out, errOut bytes.Buffer
	require.Equal(t, 0, testRun([]string{"render"}, strings.NewReader(report), &out, &errOut), errOut.String())
	require.Contains(t, out.String(), "| First result — base case |")
	require.Contains(t, out.String(), "Legacy scenarios (unverified)")
	require.Contains(t, out.String(), "| First result — legacy |")
	require.Contains(t, out.String(), "| First result — legacy | legacy-only-proof-42 | legacy-work-42 | legacy-duration-42 | legacy-evidence-42 | legacy-endpoint-42 | legacy-next-42 |")
}
