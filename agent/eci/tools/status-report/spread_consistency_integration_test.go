package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForecastSpreadConsistency exercises canonical target and spread reconciliation through the public CLI.
//
// Example: A known scenario endpoint beyond the target makes the report incomplete.
func TestForecastSpreadConsistency(t *testing.T) {
	for _, tc := range []struct {
		name, base, scenario, second string
		incomplete                   bool
	}{
		{name: "equal latest UTC range", base: "2026-11-02T15:45:00Z", scenario: "2026-11-02T15:50:00Z–2026-11-02T16:00:00+00:00"},
		{name: "target before scenario", base: "2026-11-02T15:45:00Z", scenario: "2026-11-02T16:15:00Z", incomplete: true},
		{name: "target after known spread", base: "2026-11-02T15:45:00Z", scenario: "2026-11-02T15:55:00Z", incomplete: true},
		{name: "base beyond target", base: "2026-11-02T16:15:00Z", scenario: "2026-11-02T16:00:00Z", incomplete: true},
		{name: "unknown scenario", base: "2026-11-02T15:45:00Z", scenario: "unknown", incomplete: true},
		{name: "unknown plus beyond target", base: "2026-11-02T15:45:00Z", scenario: "unknown", second: "2026-11-02T16:15:00Z", incomplete: true},
		{name: "unknown base", base: "unknown", scenario: "2026-11-02T16:00:00Z", incomplete: true},
		{name: "missing base", scenario: "2026-11-02T16:00:00Z", incomplete: true},
		{name: "unsupported qualifier", base: "2026-11-02T15:45:00Z", scenario: "after 2026-11-02T16:00:00Z", incomplete: true},
		{name: "reversed range", base: "2026-11-02T15:45:00Z", scenario: "2026-11-02T16:00:00Z–2026-11-02T15:50:00Z", incomplete: true},
	} {
		t.Run(tc.name,
			// Compare one forecast target with its supplied spread.
			//
			// Example: UTC offsets for the same instant compare equally.
			func(t *testing.T) {
				base := ""
				if tc.base != "" {
					base = fmt.Sprintf("base_case: {endpoint: '%s', packages: [{work: Finish, duration: '20 minutes', basis: guess}]}, ", tc.base)
				}
				spread := fmt.Sprintf("%sscenarios: [{name: Delay, trigger: review, affected_work: Finish, evidence: known, assumptions: none, added_duration: '15 minutes', event_time: '2026-11-02T15:45:00Z', endpoint: '%s', basis: guess}]", base, tc.scenario)
				if tc.second != "" {
					spread = strings.TrimSuffix(spread, "]") + fmt.Sprintf(", {name: Second, trigger: review, affected_work: Finish, evidence: known, assumptions: none, added_duration: '15 minutes', event_time: '2026-11-02T15:45:00Z', endpoint: '%s', basis: guess}]", tc.second)
				}
				forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
				report := strings.Replace(linkedReport, forecast, strings.TrimSuffix(forecast, "}")+", source_projection: {"+spread+"}}", 1)
				source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    "+strings.ReplaceAll(spread, ", scenarios:", "\n    scenarios:")+"\n", 1)
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
				var stdout, stderr bytes.Buffer
				code := testRun([]string{"render", path}, strings.NewReader(""), &stdout, &stderr)
				require.Equal(t, 0, code, stderr.String())
				if tc.incomplete {
					require.Contains(t, stdout.String(), "INCOMPLETE")
					if tc.second != "" {
						require.Contains(t, stdout.String(), "exceeds target")
					}
				} else {
					require.NotContains(t, stdout.String(), "INCOMPLETE")
				}
				stdout.Reset()
				stderr.Reset()
				code = testRun([]string{"validate", path}, strings.NewReader(""), &stdout, &stderr)
				if tc.incomplete {
					require.NotEqual(t, 0, code)
				} else {
					require.Equal(t, 0, code, stdout.String(), stderr.String())
				}
			})
	}
}

// TestForecastWithoutSpreadRemainsValid preserves non-ECI forecasts without an applicable spread.
//
// Example: A direct active forecast with no scenarios is not treated as incomplete.
func TestForecastWithoutSpreadRemainsValid(t *testing.T) {
	out := lifecycleResult(t, linkedReport, linkedLedger, linkedForecasts)
	require.NotContains(t, out, "INCOMPLETE")
}
