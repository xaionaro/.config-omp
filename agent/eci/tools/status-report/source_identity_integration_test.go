package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReportEvaluationTime checks expiry against the actual evaluation time, preserving old estimate anchors.
//
// Example: A formerly future deadline becomes incomplete once evaluation passes it.
func TestReportEvaluationTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	for _, tc := range []struct {
		at         string
		incomplete bool
	}{
		{at: "2026-11-02T15:50:00Z"},
		{at: "2026-11-02T16:05:00Z", incomplete: true},
	} {
		t.Run(tc.at,
			// Check expiry at one fixed evaluation instant.
			//
			// Example: A future endpoint remains current.
			func(t *testing.T) {
				at, err := parseUTC(tc.at)
				require.NoError(t, err)
				var out, errOut bytes.Buffer
				require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, at), errOut.String())
				if tc.incomplete {
					require.Contains(t, out.String(), "INCOMPLETE")
					require.Contains(t, out.String(), "elapsed")
				} else {
					require.NotContains(t, out.String(), "INCOMPLETE")
				}
			})
	}
}

// TestSharedRootForecastOutcomeIdentity checks that sharing a forecast requires identical complete outcome sets.
//
// Example: Two roots with only one common outcome cannot share a forecast.
func TestSharedRootForecastOutcomeIdentity(t *testing.T) {
	report := linkedReport + `root_forecasts:
  - {forecast_ref: 'forecasts.yaml#/records/root', outcome: Root result, target: '2026-11-02T18:00:00Z', assessment: complete, baseline: '2026-11-02T18:00:00Z', evidence: initial}
`
	report = strings.Replace(report, "work_refs: ['project-understanding.yaml#/records/work:first']", "work_refs: ['project-understanding.yaml#/records/work:first', 'project-understanding.yaml#/records/root-one']", 1)
	report = strings.Replace(report, "work_refs: ['project-understanding.yaml#/records/work:second']", "work_refs: ['project-understanding.yaml#/records/work:second', 'project-understanding.yaml#/records/root-two']", 1)
	ledger := linkedLedger + `  a: {section: progress, type: outcome, data: {description: Root result}}
  b: {section: progress, type: outcome, data: {description: Root result}}
  c: {section: progress, type: outcome, data: {description: Root result}}
  root-one:
    section: progress
    type: work
    links: {outcomes: [a, b]}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
  root-two:
    section: progress
    type: work
    links: {outcomes: [a, b]}
    data: {owner: two, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	forecasts := linkedForecasts + `  root:
    kind: root
    outcome: Root result
    status: active
    assessment: complete
    target: '2026-11-02T18:00:00Z'
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	out := lifecycleResult(t, report, ledger, forecasts)
	require.NotContains(t, out, "INCOMPLETE")
	ledger = strings.Replace(ledger, "outcomes: [a, b]}", "outcomes: [a, c]}", 1)
	out = lifecycleResult(t, report, ledger, forecasts)
	require.Contains(t, out, "INCOMPLETE")
}
