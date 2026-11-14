package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSourceReconciliationBoundaries exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestSourceReconciliationBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, report, ledger, forecasts, diagnostic string }{
		{"omitted active work", strings.Replace(linkedReport, "work_refs: ['project-understanding.yaml#/records/work:second']", "work_refs: []", 1), linkedLedger, linkedForecasts, "active work work:second missing"},
		{"unknown role", linkedReport, strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: [mystery]", 1), linkedForecasts, "unknown reporting role"},
		{"root and lane overlap", linkedReport, strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: [lane, root]", 1), linkedForecasts, ""},
		{"future estimate", strings.ReplaceAll(linkedReport, "as_of: '2026-11-02T15:24:00Z'", "as_of: '2026-11-02T15:30:00Z'"), linkedLedger, linkedForecasts, "as_of 2026-11-02T15:30:00Z is in the future"},
		{"elapsed estimate", strings.ReplaceAll(linkedReport, "as_of: '2026-11-02T15:24:00Z'", "as_of: '2026-11-02T14:00:00Z'"), linkedLedger, linkedForecasts, "endpoint elapsed while work open"},
		{"missing canonical baseline", strings.Replace(linkedReport, ", baseline: '2026-11-02T16:00:00Z'", "", 1), linkedLedger, strings.Replace(linkedForecasts, "    original_baseline: '2026-11-02T16:00:00Z'\n", "", 1), "source missing required original_baseline"},
		{"missing recalibration", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "    recalibration: {status: unchanged, evidence: initial}\n", "", 1), "source missing recalibration"},
		{"missing lane", strings.Replace(linkedReport, "work_ref: 'project-understanding.yaml#/records/work:first'", "work_ref: 'project-understanding.yaml#/records/bad'", 1), linkedLedger, linkedForecasts, "declared lane work:first missing"},
		{"missing owner", linkedReport, strings.Replace(linkedLedger, "owner: one", "owner: ", 1), linkedForecasts, "missing owner"},
		{"unknown state", linkedReport, strings.Replace(linkedLedger, "state: active", "state: mystery", 1), linkedForecasts, "unknown state"},
		{"missing action", linkedReport, strings.Replace(linkedLedger, "next_action: Finish", "next_action: ", 1), linkedForecasts, "missing next action"},
		{"missing role", linkedReport, strings.Replace(linkedLedger, "reporting_roles: [lane], ", "", 1), linkedForecasts, "reporting roles unverified"},
		{"wrong kind", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "kind: lane", "kind: root", 1), "kind mismatch"},
		{"missing forecast record", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "  first:", "  renamed:", 1), "forecast first missing"},
		{"unsupported source", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "    kind: lane", "    extra: value\n    kind: lane", 1), "source field extra is unsupported"},
		{"invalid recalibration", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "status: unchanged", "status: mystery", 1), "invalid recalibration status"},
		{"changed recalibration without evidence", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "status: unchanged, evidence: initial", "status: changed", 1), "source missing recalibration evidence"},
		{"elapsed forecast", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T15:00:00Z'", 1), "target elapsed while work open"},
		{"invalid assessment", linkedReport, linkedLedger, strings.Replace(linkedForecasts, "assessment: complete", "assessment: mystery", 1), "invalid assessment"},
		{"provisional without basis", strings.Replace(linkedReport, "assessment: complete", "assessment: provisional", 1), linkedLedger, strings.Replace(linkedForecasts, "assessment: complete", "assessment: provisional", 1), "provisional source missing target_basis"},
		{"invented recalibration", strings.Replace(linkedReport, "assessment: complete, baseline: '2026-11-02T16:00:00Z'", "assessment: complete, prior: '2026-11-02T15:00:00Z', reason: invented, baseline: '2026-11-02T16:00:00Z'", 1), linkedLedger, linkedForecasts, "recalibration prior mismatch"},
		{"duplicate forecast", linkedReport + "root_forecasts:\n  - {forecast_ref: 'forecasts.yaml#/records/first', outcome: Conflict, target: '2026-11-02T18:00:00Z', assessment: complete}\n", linkedLedger, linkedForecasts, "duplicate report forecast reference"},
		{"orphan canonical forecast", linkedReport, linkedLedger, linkedForecasts + "  orphan:\n    status: active\n", "active canonical forecast orphan is not linked"},
		{"orphan forecast", linkedReport + "root_forecasts:\n  - {forecast_ref: 'forecasts.yaml#/records/orphan', outcome: Orphan, target: '2026-11-02T18:00:00Z', assessment: complete}\n", linkedLedger, linkedForecasts, "root forecast \"forecasts.yaml#/records/orphan\" is not linked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, // The callback checks one source consistency boundary.
			//
			// Example: Detect a missing work reference.
			func(t *testing.T) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(tc.ledger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(tc.forecasts), 0600))
				r, err := parse([]byte(tc.report))
				require.NoError(t, err)
				issues := testReconcile(r, dir)
				if tc.diagnostic == "" {
					require.Empty(t, issues)
				} else {
					require.Contains(t, strings.Join(issues, "\n"), tc.diagnostic)
				}
			})
	}
}

// TestForecastStructureRejectsIncompleteDetail exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestForecastStructureRejectsIncompleteDetail(t *testing.T) {
	t.Parallel()
	issues := forecastStructure(map[string]any{
		"base_case": map[string]any{"packages": []any{map[string]any{"work": "work"}}},
		"scenarios": []any{map[string]any{"name": "delay"}},
	})
	require.Contains(t, issues, "base_case missing endpoint")
	require.Contains(t, issues, "base_case package missing basis")
	require.Contains(t, issues, "scenario missing event_time")
	require.Contains(t, issues, "scenario missing basis")
}
