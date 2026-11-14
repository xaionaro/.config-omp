package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLegacyScenarioRequiresNativeProjectionInCLI checks source verification.
//
// Example: An unverified legacy scenario remains visible until its owner migrates it.
func TestLegacyScenarioRequiresNativeProjectionInCLI(t *testing.T) {
	t.Parallel()
	// sourceSpread is the canonical native forecast spread.
	//
	// Example: The report's source projection must match these scenario details.
	const sourceSpread = `    target_basis: conditional on review
    base_case:
      endpoint: '2026-11-02T15:45:00Z'
      packages:
        - {work: Finish work, duration: '20 minutes', basis: measured}
    scenarios:
      - {name: Delay, trigger: review delay, affected_work: Finish work, added_duration: '15 minutes', basis: guessed, evidence: review pending, assumptions: reviewer available, event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', evidence_gap: review duration, next_action: obtain timing}
`
	// native contains the same fields as the canonical source.
	//
	// Example: The native scenario is rendered and source-verified.
	const native = `source_projection: {target_basis: conditional on review, base_case: {endpoint: '2026-11-02T15:45:00Z', packages: [{work: Finish work, duration: '20 minutes', basis: measured}]}, scenarios: [{name: Delay, trigger: review delay, affected_work: Finish work, added_duration: '15 minutes', basis: guessed, evidence: review pending, assumptions: reviewer available, event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', evidence_gap: review duration, next_action: obtain timing}]}`
	// legacy is a separate historical report-only scenario.
	//
	// Example: It remains visibly unverified even when the native spread matches.
	const legacy = `, scenarios: [{case: legacy, trigger: old review, affected_work: Finish work, duration_basis: guess, evidence_assumptions: pending, endpoint: unknown, evidence_gap_next_action: measure}]`
	// oldForecast identifies the first lane's compact forecast.
	//
	// Example: Preserve all unrelated lane and report fields while adding scenarios.
	const oldForecast = "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	forecast := strings.Replace(strings.TrimSuffix(oldForecast, "}"), "assessment: complete", "assessment: provisional", 1) + ", " + native + legacy + "}"
	report := strings.Replace(linkedReport, oldForecast, forecast, 1)
	require.NotEqual(t, linkedReport, report)
	source := strings.Replace(linkedForecasts, "assessment: complete", "assessment: provisional", 1)
	source = strings.Replace(source, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n"+sourceSpread, 1)
	dir := writeLinkedSources(t, linkedLedger, source)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first report scenarios cannot be verified against canonical scenarios; use source_projection")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first report scenarios cannot be verified against canonical scenarios; use source_projection")
	require.Contains(t, out.String(), "| First result — legacy |")
	migrated := strings.Replace(report, legacy, "", 1)
	require.NotEqual(t, report, migrated)
	require.NoError(t, os.WriteFile(path, []byte(migrated), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
	require.Contains(t, out.String(), "| First result — Delay |")
	require.NotContains(t, out.String(), "| First result — legacy |")
}
