package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestRefreshRequirementChangePreservesReport verifies the refresh behavior.
//
// Example: A closed task stays intact while scope becomes incomplete.
func TestRefreshRequirementChangePreservesReport(t *testing.T) {
	dir := t.TempDir()
	original := strings.Replace(linkedReport, "status: IN PROGRESS", "status: CLOSED, evidence: accepted", 1)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(original), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(first), "scope_complete: false")
	require.Contains(t, string(first), refreshDiagnostic)
	require.Contains(t, string(first), "status: CLOSED")
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// TestRefreshRejectsMissingOrInvalidReport verifies the refresh behavior.
//
// Example: A malformed report is rejected before publication.
func TestRefreshRejectsMissingOrInvalidReport(t *testing.T) {
	dir := t.TempDir()
	require.Error(t, refreshTestStatus(dir, "requirement_change"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "latest-status-report.yaml"), []byte("scope_complete: nope\n"), 0600))
	require.Error(t, refreshTestStatus(dir, "requirement_change"))
}

// TestRefreshDetectsConcurrentReportMutation verifies the refresh behavior.
//
// Example: A concurrent edit survives and the refresh requests a retry.
func TestRefreshDetectsConcurrentReportMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	changed := []byte(linkedReport + "# concurrent edit\n")
	observer := testRefreshObserver{Action:
	// Callback mutates the report before publication.
	//
	// Example: Simulate a concurrent report edit.
	func() { require.NoError(t, os.WriteFile(path, changed, 0600)) }}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "requirement_change", observer), "retry needed")
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, changed, actual)

	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
}

// TestRefreshProjectsLinkedForecast verifies the refresh behavior.
//
// Example: A linked lane receives the current canonical target.
func TestRefreshProjectsLinkedForecast(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	changed := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(changed), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "2026-11-02T16:30:00Z")
	require.Contains(t, string(result), "2026-11-02T17:00:00Z")
}

// TestForecastRefreshRejectsConcurrentSourceMutation verifies the refresh behavior.
//
// Example: A concurrent forecast edit prevents stale publication.
func TestForecastRefreshRejectsConcurrentSourceMutation(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	changed := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	require.NoError(t, os.WriteFile(forecastPath, []byte(changed), 0600))
	observer := testRefreshObserver{Action:
	// Callback mutates the forecast before publication.
	//
	// Example: Simulate a concurrent forecast edit.
	func() { require.NoError(t, os.WriteFile(forecastPath, []byte(changed+"# concurrent\n"), 0600)) }}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "forecast_change", observer), "retry needed")
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(result))
}

// TestRefreshProjectsForecastDetails verifies the refresh behavior.
//
// Example: A source outcome and target basis reach the report.
func TestRefreshProjectsForecastDetails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	source := strings.Replace(linkedForecasts, "outcome: First result", "outcome: Updated result", 1)
	source = strings.Replace(source, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: Conditional on review\n", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "Updated result")
	require.Contains(t, string(result), "Conditional on review")
}

// TestRefreshProjectsRootAndBlockForecast verifies the refresh behavior.
//
// Example: A block-style root forecast receives its source target.
func TestRefreshProjectsRootAndBlockForecast(t *testing.T) {
	dir := t.TempDir()
	report := linkedReport + "root_forecasts:\n  - forecast_ref: 'forecasts.yaml#/records/root'\n    outcome: Root result\n    target: '2026-11-02T16:00:00Z'\n    assessment: complete\n    baseline: '2026-11-02T16:00:00Z'\n    evidence: initial\n"
	ledger := linkedLedger + "  work:root:\n    section: progress\n    type: work\n    data: {owner: lead, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}\n    evidence: []\n"
	source := linkedForecasts + "  root:\n    kind: root\n    outcome: Root result\n    status: active\n    assessment: complete\n    target: '2026-11-02T16:30:00Z'\n    original_baseline: '2026-11-02T16:00:00Z'\n    baseline_evidence: initial\n    recalibration: {status: unchanged, evidence: initial}\n"
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "2026-11-02T16:30:00Z")
	require.Contains(t, string(result), "source_projection")
}

// TestRefreshUpdatesExistingProjectionAndPreservesComment verifies the refresh behavior.
//
// Example: An adjacent comment survives a projection update.
func TestRefreshUpdatesExistingProjectionAndPreservesComment(t *testing.T) {
	dir := t.TempDir()
	report := strings.Replace(linkedReport, "evidence: initial}", "evidence: initial, source_projection: {baseline_evidence: initial, target_basis: old}} # keep this comment", 1)
	source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new basis\n", 1)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "new basis")
	require.Contains(t, string(result), "# keep this comment")
}

// TestRefreshPersistsUnsupportedProjectionDiagnostic verifies the refresh behavior.
//
// Example: A comment inside a projection requires manual refresh.
func TestRefreshPersistsUnsupportedProjectionDiagnostic(t *testing.T) {
	dir := t.TempDir()
	report := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}", "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial\n      source_projection:\n        target_basis: old # retain this note", 1)
	source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new\n", 1)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "manual refresh required")
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "scope_complete: false")
	require.Contains(t, string(result), "forecasts.yaml#/records/first")
	require.Contains(t, string(result), "# retain this note")
}

// TestRequirementChangeAtomicallyProjectsForecast verifies the refresh behavior.
//
// Example: Scope invalidation and forecast changes publish together.
func TestRequirementChangeAtomicallyProjectsForecast(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	source := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "scope_complete: false")
	require.Contains(t, string(result), refreshDiagnostic)
	require.Contains(t, string(result), "2026-11-02T16:30:00Z")
}

// TestRefreshDirectWorkWithoutForecast verifies the refresh behavior.
//
// Example: Direct work refreshes without a forecasts file.
func TestRefreshDirectWorkWithoutForecast(t *testing.T) {
	dir := t.TempDir()
	report := strings.Replace(valid, "scope_complete: false", "scope_complete: true", 1)
	report = strings.Replace(report, "status: unknown", "status: IN PROGRESS", 1)
	report = strings.Replace(report, "title: Root", "title: Root\n    work_refs: ['project-understanding.yaml#/records/work:direct']", 1)
	ledger := "schema: project-understanding/v1\nsections: {progress: {title: Progress}}\nrecords:\n  outcome:direct:\n    section: progress\n    type: outcome\n    data: {description: Direct work}\n  work:direct:\n    section: progress\n    type: work\n    links: {outcomes: ['outcome:direct']}\n    data: {owner: one, state: active, next_action: Finish, reporting_roles: []}\n    evidence: []\n"
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, report, string(result))
	var out, errOut strings.Builder
	require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut))
	require.Empty(t, errOut.String())
}

// TestRefreshPersistsMissingWorkDiagnostic verifies the refresh behavior.
//
// Example: Missing linked work produces an incomplete diagnostic.
func TestRefreshPersistsMissingWorkDiagnostic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	ledger := strings.Replace(linkedLedger, "work:first:", "work:missing:", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "scope_complete: false")
	require.Contains(t, string(result), "Status refresh incomplete:")
	require.Contains(t, string(result), "work:first")
}

// TestRefreshRejectsInvalidForecastSources verifies the refresh behavior.
//
// Example: A source missing required forecast facts is rejected.
func TestRefreshRejectsInvalidForecastSources(t *testing.T) {
	cases := []struct{ name, from, to, issue string }{
		{"missing outcome", "    outcome: First result\n", "", "missing outcome"},
		{"bad target", "target: '2026-11-02T16:00:00+00:00'", "target: invalid", "invalid target"},
		{"unknown assessment", "assessment: complete", "assessment: unknown", "invalid assessment"},
		{"missing provisional basis", "assessment: complete", "assessment: provisional", "missing target_basis"},
		{"bad recalibration", "recalibration: {status: unchanged, evidence: initial}", "recalibration: {status: invalid, evidence: initial}", "invalid recalibration"},
		{"unsupported field", "    kind: lane\n", "    kind: lane\n    extra: value\n", "unsupported source field extra"},
		{"missing changed details", "recalibration: {status: unchanged, evidence: initial}", "recalibration: {status: changed, evidence: initial}", "recalibration missing prior"},
		{"changed current differs", "recalibration: {status: unchanged, evidence: initial}", "recalibration: {status: changed, prior: '2026-11-02T15:00:00Z', current: '2026-11-02T17:00:00Z', reason: review, evidence: initial}", "recalibration current does not match target"},
		{"missing lane forecast", "    kind: lane\n", "    kind: root\n", "not an active lane forecast"},
		{"invalid source scenario", "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    scenarios: [invalid]\n", "invalid scenario"},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(strings.Replace(linkedForecasts, tc.from, tc.to, 1)), 0600))
				require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), tc.issue)
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Contains(t, string(result), "scope_complete: false")
				require.Contains(t, string(result), tc.issue)
			})
	}
}

// TestRefreshCLIArgumentsAndFailures verifies the refresh behavior.
//
// Example: Missing command arguments return a usage error.
func TestRefreshCLIArgumentsAndFailures(t *testing.T) {
	var errOut strings.Builder
	require.Equal(t, 2, runRefresh(nil, &errOut))
	require.Contains(t, errOut.String(), "usage")
	broken := &testWriter{}
	require.Equal(t, 1, runRefresh(nil, broken))
	require.Equal(t, 1, broken.calls)
	short := &testWriter{short: true}
	require.Equal(t, 1, runRefresh(nil, short))
	require.Equal(t, 1, short.calls)
	errOut.Reset()
	require.Equal(t, 1, runRefresh([]string{t.TempDir(), "requirement_change"}, &errOut))
	require.Contains(t, errOut.String(), "status-report refresh")
}

// TestRefreshProjectsFullScenario verifies the refresh behavior.
//
// Example: Scenario event times and duration bases reconcile.
func TestRefreshProjectsFullScenario(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	sourceProjection := "    base_case:\n      endpoint: '2026-11-02T15:45:00Z'\n      packages:\n        - {work: Finish, duration: 20 minutes, basis: guess}\n    scenarios:\n      - {name: Delay, trigger: review, affected_work: Finish, evidence: unknown, assumptions: none, added_duration: 15 minutes, event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', basis: guess}\n"
	source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n"+sourceProjection, 1)
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "added_duration")
	require.Contains(t, string(result), "event_time")
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, testReconcile(parsed, dir))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	again, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, again)
}

// TestInvalidatePreservesBlockListIndentation verifies the refresh behavior.
//
// Example: An indented blocker list retains its existing item.
func TestInvalidatePreservesBlockListIndentation(t *testing.T) {
	for _, indent := range []string{"", "  "} {
		t.Run("indent_"+strconv.Itoa(len(indent)),
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				report := strings.Replace(linkedReport, "blockers_risks: []", "blockers_risks:\n"+indent+"- existing", 1)
				updated, err := prepareInvalidation([]byte(report), refreshDiagnostic)
				require.NoError(t, err)
				parsed, err := parse(updated)
				require.NoError(t, err)
				require.Contains(t, parsed.Blockers, "existing")
				require.Contains(t, parsed.Blockers, refreshDiagnostic)
			})
	}
	report := strings.Replace(linkedReport, "blockers_risks: []", "blockers_risks:\n  - |\n    multiple\n    lines", 1)
	_, err := prepareInvalidation([]byte(report), refreshDiagnostic)
	require.ErrorContains(t, err, "multiline blocker")
}

// TestRefreshUpdatesBlockSourceProjection verifies the refresh behavior.
//
// Example: A block-style projection receives the new target basis.
func TestRefreshUpdatesBlockSourceProjection(t *testing.T) {
	dir := t.TempDir()
	report := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}", "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial\n      source_projection:\n        baseline_evidence: initial\n        target_basis: old", 1)
	source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new\n", 1)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "new")
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, testReconcile(parsed, dir))
}

// TestRefreshRejectsUnsafeOrMissingSources verifies the refresh behavior.
//
// Example: A symlink report is left untouched.
func TestRefreshRejectsUnsafeOrMissingSources(t *testing.T) {
	t.Run("symlink report",
		// Callback runs the named regression scenario.
		//
		// Example: Exercise one table-driven case.
		func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target")
			require.NoError(t, os.WriteFile(target, []byte(linkedReport), 0600))
			require.NoError(t, os.Symlink(target, filepath.Join(dir, "latest-status-report.yaml")))
			require.ErrorContains(t, refreshTestStatus(dir, "requirement_change"), "not a regular file")
			b, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, linkedReport, string(b))
		})
	t.Run("missing forecast",
		// Callback runs the named regression scenario.
		//
		// Example: Exercise one table-driven case.
		func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "latest-status-report.yaml")
			require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
			require.Error(t, refreshTestStatus(dir, "requirement_change"))
			b, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(b), refreshDiagnostic)
			require.Contains(t, string(b), "Status refresh incomplete:")
		})
	t.Run("malformed ledger",
		// Callback runs the named regression scenario.
		//
		// Example: Exercise one table-driven case.
		func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "latest-status-report.yaml")
			require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte("[invalid"), 0600))
			require.Error(t, refreshTestStatus(dir, "forecast_change"))
			b, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(b), "Status refresh incomplete:")
		})
}

// TestRefreshProjectsRecalibration verifies the refresh behavior.
//
// Example: A changed target retains its prior value and evidence.
func TestRefreshProjectsRecalibration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	source := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	source = strings.Replace(source, "assessment: complete", "assessment: provisional", 1)
	source = strings.Replace(source, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: conditional\n", 1)
	source = strings.Replace(source, "recalibration: {status: unchanged, evidence: initial}", "recalibration: {status: changed, prior: '2026-11-02T16:00:00Z', current: '2026-11-02T16:30:00Z', reason: review, evidence: evidence}", 1)
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, testReconcile(parsed, dir))
	require.Contains(t, string(result), "conditional")
	require.Contains(t, string(result), "review")
}

// TestRefreshMissingRootForecastPersistsDiagnostic verifies the refresh behavior.
//
// Example: A missing root forecast is named in the diagnostic.
func TestRefreshMissingRootForecastPersistsDiagnostic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	report := linkedReport + "root_forecasts:\n  - {forecast_ref: 'forecasts.yaml#/records/root', outcome: Root, target: '2026-11-02T16:00:00Z', assessment: complete}\n"
	ledger := linkedLedger + "  work:root:\n    section: progress\n    type: work\n    data: {owner: lead, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}\n    evidence: []\n"
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "not an active root forecast")
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "forecasts.yaml#/records/root")
	require.Contains(t, string(result), "scope_complete: false")
}

// TestRefreshHandlesConcurrentReplacementAndDeletion verifies the refresh behavior.
//
// Example: A deleted source prevents stale publication.
func TestRefreshHandlesConcurrentReplacementAndDeletion(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(
			*testing.T,
			string,
		)
	}{
		{"replace report",
			// Callback changes a file during a publication test.
			//
			// Example: Replace the report before publication.
			func(
				t *testing.T,
				dir string,
			) {
				path := filepath.Join(dir, "latest-status-report.yaml")
				replacement := filepath.Join(dir, "replacement")
				require.NoError(t, os.WriteFile(replacement, []byte(linkedReport), 0600))
				require.NoError(t, os.Rename(replacement, path))
			}},
		{"remove forecast",
			// Callback removes the forecast after it has been read.
			//
			// Example: Simulate a concurrent source deletion.
			func(
				t *testing.T,
				dir string,
			) {
				require.NoError(t, os.Remove(filepath.Join(dir, "forecasts.yaml")))
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
				observer := testRefreshObserver{Action:
				// Callback changes a source at the publication boundary.
				//
				// Example: Remove a forecast after it has been read.
				func() { tc.mutate(t, dir) }}
				require.Error(t, refreshTestLinkedStatus(dir, "requirement_change", observer))
				b, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, linkedReport, string(b))
			})
	}
}

// TestRefreshRejectsStaleRecalibrationFields verifies the refresh behavior.
//
// Example: A stale reason is preserved for manual review.
func TestRefreshRejectsStaleRecalibrationFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	report := strings.Replace(linkedReport, "evidence: initial}", "evidence: initial, reason: obsolete}", 1)
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "stale reason")
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "Status refresh incomplete:")
	require.Contains(t, string(result), "reason: obsolete")
}

// TestInvalidateFlowListPreservesQuotedClosingBracket verifies the refresh behavior.
//
// Example: A bracket inside a quoted blocker remains intact.
func TestInvalidateFlowListPreservesQuotedClosingBracket(t *testing.T) {
	original := strings.Replace(linkedReport, "blockers_risks: []", "blockers_risks: ['risk ] here', 'don''t ] split', \"quoted \\\" ] here\"]", 1)
	updated, err := prepareInvalidation([]byte(original), refreshDiagnostic)
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, []string{"risk ] here", "don't ] split", "quoted \" ] here", refreshDiagnostic}, parsed.Blockers)
	again, err := prepareInvalidation(updated, refreshDiagnostic)
	require.NoError(t, err)
	require.Equal(t, updated, again)
}

// TestRefreshChangesQuotedOutcomeWithoutDamagingOtherFields verifies the refresh behavior.
//
// Example: An apostrophe in an outcome does not damage another lane.
func TestRefreshChangesQuotedOutcomeWithoutDamagingOtherFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	report := strings.Replace(linkedReport, "outcome: First result, target:", "outcome: 'Worker''s {old}', target:", 1)
	source := strings.Replace(linkedForecasts, "    outcome: First result", "    outcome: 'Worker''s new result'", 1)
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "Worker's new result", parsed.Lanes[0].Forecast.Outcome)
	require.Equal(t, "2026-11-02T17:00:00Z", parsed.Lanes[1].Forecast.Target)
}

// TestRefreshDoesNotClearScopeOrPriorDiagnostic verifies the refresh behavior.
//
// Example: Restored links do not clear the scope review requirement.
func TestRefreshDoesNotClearScopeOrPriorDiagnostic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	ledgerPath := filepath.Join(dir, "project-understanding.yaml")
	require.NoError(t, os.WriteFile(ledgerPath, []byte(strings.Replace(linkedLedger, "work:first:", "work:missing:", 1)), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	require.NoError(t, os.WriteFile(ledgerPath, []byte(linkedLedger), 0600))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "scope_complete: false")
	require.Contains(t, string(result), refreshDiagnostic)
	require.Contains(t, string(result), "work:first")
}

// TestRequirementChangeSourceFailurePublishesOnce verifies the refresh behavior.
//
// Example: A missing source produces both diagnostics in one publication.
func TestRequirementChangeSourceFailurePublishesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	publications := 0
	observer := testRefreshObserver{Action:
	// Callback counts publication attempts.
	//
	// Example: Verify that failure diagnostics publish once.
	func() { publications++ }}
	require.Error(t, refreshTestLinkedStatus(dir, "requirement_change", observer))
	require.Equal(t, 1, publications)
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), refreshDiagnostic)
	require.Contains(t, string(result), "Status refresh incomplete:")
}

// TestRefreshPreservesMultilineForecastScalar verifies the refresh behavior.
//
// Example: An unsupported multiline outcome remains intact.
func TestRefreshPreservesMultilineForecastScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	old := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	block := "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: |-\n        First result\n        with detail\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial"
	report := strings.Replace(linkedReport, old, block, 1)
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.ErrorContains(t, refreshTestStatus(dir, "forecast_change"), "multiline forecast scalar")
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "First result\n        with detail")
	require.Contains(t, string(result), "Status refresh incomplete:")
}

// TestRefreshPreservesFlowCommentWithClosingBrace verifies the refresh behavior.
//
// Example: A brace inside a flow comment is not a closing delimiter.
func TestRefreshPreservesFlowCommentWithClosingBrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	before := "forecast: {forecast_ref: 'forecasts.yaml#/records/first'"
	after := "forecast: {\n      # context } untouched\n      forecast_ref: 'forecasts.yaml#/records/first'"
	report := strings.Replace(linkedReport, before, after, 1)
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "# context } untouched")
	parsed, err := parse(result)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.Lanes[0].Forecast.SourceProjection)
	require.Empty(t, testReconcile(parsed, dir))
}

// TestLexerFlowBoundaryIgnoresCommentsAndUnicode verifies the refresh behavior.
//
// Example: A multibyte character before a flow node preserves its offset.
func TestLexerFlowBoundaryIgnoresCommentsAndUnicode(t *testing.T) {
	for _, text := range []string{"label: {# comment }\n key: 'text }', nested: [one, {two: three}]}\n", "label: {\n # comment }\n key: 'text }', nested: [one, {two: three}]}\n", "label: {clé: '😀 }', nested: [one, {two: three}]}\n"} {
		var doc yaml.Node
		require.NoError(t, yaml.Unmarshal([]byte(text), &doc))
		node := mapValue(doc.Content[0], "label")
		end, err := flowMappingEnd([]byte(text), node)
		require.NoError(t, err)
		require.Equal(t, strings.LastIndex(text, "}"), end)
	}
	text := "{lead: 'λ', label: {key: '😀 }'}}\n"
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(text), &doc))
	end, err := flowMappingEnd([]byte(text), mapValue(doc.Content[0], "label"))
	require.NoError(t, err)
	require.Equal(t, strings.LastIndex(text, "}}"), end)
}

// TestVerifyRefreshRejectsUnrelatedChangesAndCommentLoss verifies the refresh behavior.
//
// Example: A changed task or removed comment prevents publication.
func TestVerifyRefreshRejectsUnrelatedChangesAndCommentLoss(t *testing.T) {
	original := strings.Replace(linkedReport, "state: Working", "state: Working # preserve me", 1)
	cases := []struct{ name, candidate, issue string }{
		{"comment changed", strings.Replace(original, "# preserve me", "# damaged", 1), "comments"},
		{"task changed", strings.Replace(original, "status: IN PROGRESS", "status: PAUSED", 1), "unprojected"},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				require.ErrorContains(t, verifyRefresh([]byte(original), []byte(tc.candidate), ledgerSource{}, forecastSource{}), tc.issue)
			})
	}
}

// TestVerifyRefreshRejectsUnsupportedForecastChanges verifies the refresh behavior.
//
// Example: A target unsupported by the canonical source is rejected.
func TestVerifyRefreshRejectsUnsupportedForecastChanges(t *testing.T) {
	var ledger ledgerSource
	var forecasts forecastSource
	require.NoError(t, yaml.Unmarshal([]byte(linkedLedger), &ledger))
	require.NoError(t, yaml.Unmarshal([]byte(linkedForecasts), &forecasts))
	tests := []struct{ name, candidate, issue string }{
		{"unbacked target", strings.Replace(linkedReport, "target: '2026-11-02T16:00:00Z'", "target: '2026-11-02T19:00:00Z'", 1), "unexpected projected target"},
		{"readiness", strings.Replace(linkedReport, "test_status: NEW", "test_status: PAUSED", 1), "unprojected"},
		{"unlinked reference", strings.Replace(linkedReport, "forecasts.yaml#/records/first", "forecasts.yaml#/records/other", 1), "unprojected"},
		{"invented diagnostic", strings.Replace(linkedReport, "blockers_risks: []", "blockers_risks: [invented]", 1), "invalid refresh diagnostic"},
		{"scope revival", strings.Replace(linkedReport, "scope_complete: true", "scope_complete: false", 1), "invalid scope change"},
	}
	for _, tc := range tests {
		t.Run(tc.name,
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				before, after := linkedReport, tc.candidate
				if tc.name == "scope revival" {
					before, after = after, before
				}
				require.ErrorContains(t, verifyRefresh([]byte(before), []byte(after), ledger, forecasts), tc.issue)
			})
	}
}

// TestVerifyRefreshPreservesBlockersAndLinkedFields verifies the refresh behavior.
//
// Example: Existing blockers cannot be replaced by a refresh.
func TestVerifyRefreshPreservesBlockersAndLinkedFields(t *testing.T) {
	var ledger ledgerSource
	var forecasts forecastSource
	require.NoError(t, yaml.Unmarshal([]byte(linkedLedger), &ledger))
	require.NoError(t, yaml.Unmarshal([]byte(linkedForecasts), &forecasts))
	original := strings.Replace(linkedReport, "blockers_risks: []", "blockers_risks: [existing]", 1)
	candidates := []struct{ name, text, errorText string }{
		{"remove blocker", strings.Replace(original, "blockers_risks: [existing]", "blockers_risks: []", 1), "invalid diagnostic change"},
		{"replace blocker", strings.Replace(original, "blockers_risks: [existing]", "blockers_risks: [different]", 1), "existing blockers"},
		{"forecast evidence", strings.Replace(original, "evidence: initial}", "evidence: fabricated}", 1), "unexpected projected evidence"},
		{"unlinked task", strings.Replace(original, "work:first", "work:unknown", 1), "unprojected"},
	}
	for _, tc := range candidates {
		t.Run(tc.name,
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				require.ErrorContains(t, verifyRefresh([]byte(original), []byte(tc.text), ledger, forecasts), tc.errorText)
			})
	}
}

// TestFlowLexerRejectsUnprovableBoundaries verifies the refresh behavior.
//
// Example: Unbalanced flow boundaries and bare carriage returns are rejected.
func TestFlowLexerRejectsUnprovableBoundaries(t *testing.T) {
	_, err := flowEnd([]byte("label: {a: [one}\n"), 7, token.MappingStartType)
	require.Error(t, err)
	end, err := flowEnd([]byte("label: {a: one}\r\n"), 7, token.MappingStartType)
	require.NoError(t, err)
	require.Equal(t, 14, end)
	_, err = flowEnd([]byte("label: {a: one}\r"), 7, token.MappingStartType)
	require.ErrorContains(t, err, "bare CR")
	_, err = yamlByteOffset([]byte("label: x\n"), 1, 30)
	require.ErrorContains(t, err, "exceeds")
}

// TestRequirementChangePreservesSingleLineReportWithUnicode verifies the refresh behavior.
//
// Example: A single-line report retains its multibyte state text.
func TestRequirementChangePreservesSingleLineReportWithUnicode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	var object any
	require.NoError(t, yaml.Unmarshal([]byte(strings.Replace(linkedReport, "state: Working", "state: 'Working λ'", 1)), &object))
	original, err := json.Marshal(object)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(original, '\n'), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, "Working λ", parsed.State)
	require.False(t, *parsed.ScopeComplete)
	require.Contains(t, parsed.Blockers, refreshDiagnostic)
}

// TestVerifyRefreshRejectsUnlinkedForecastEdit verifies the refresh behavior.
//
// Example: An unlinked forecast outcome cannot change.
func TestVerifyRefreshRejectsUnlinkedForecastEdit(t *testing.T) {
	changed := strings.Replace(linkedReport, "outcome: First result, target:", "outcome: Changed, target:", 1)
	require.ErrorContains(t, verifyRefresh([]byte(linkedReport), []byte(changed), ledgerSource{}, forecastSource{}), "unprojected")
}

// TestVerifyRefreshRejectsDiagnosticAndCommentLoss verifies the refresh behavior.
//
// Example: Existing diagnostic and comment text remains present.
func TestVerifyRefreshRejectsDiagnosticAndCommentLoss(t *testing.T) {
	original := strings.Replace(linkedReport, "blockers_risks: []", "blockers_risks: [existing] # important note", 1)
	cases := []struct{ name, text, issue string }{
		{"diagnostic removed", strings.Replace(original, "[existing]", "[]", 1), "invalid diagnostic change"},
		{"comment removed", strings.Replace(original, " # important note", "", 1), "comments"},
		{"comment added", original + "# added unexpectedly\n", "comments"},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				require.ErrorContains(t, verifyRefresh([]byte(original), []byte(tc.text), ledgerSource{}, forecastSource{}), tc.issue)
			})
	}
}

// TestYAMLPositionsRejectInvalidEncoding verifies the refresh behavior.
//
// Example: Invalid UTF-8 cannot produce an offset.
func TestYAMLPositionsRejectInvalidEncoding(t *testing.T) {
	_, err := yamlByteOffset([]byte{0xff}, 1, 1)
	require.ErrorContains(t, err, "UTF-8")
	_, err = yamlByteOffset([]byte("line\n"), 3, 1)
	require.ErrorContains(t, err, "invalid YAML position")
}

// TestVerifyRefreshRejectsDroppedLane verifies the refresh behavior.
//
// Example: A candidate cannot silently drop a lane.
func TestVerifyRefreshRejectsDroppedLane(t *testing.T) {
	original := linkedReport
	offset := strings.Index(original, "  - task_id: '2'\n")
	require.Greater(t, offset, 0)
	candidate := original[:offset]
	require.ErrorContains(t, verifyRefresh([]byte(original), []byte(candidate), ledgerSource{}, forecastSource{}), "forecast groups")
}

// TestFlowDocumentCachesNestedPairs verifies the refresh behavior.
//
// Example: Nested braces and brackets retain distinct closing positions.
func TestFlowDocumentCachesNestedPairs(t *testing.T) {
	data := []byte("{outer: {inner: [one, two]}, other: [three]}\n")
	doc := &flowDocument{data: data}
	outer, err := doc.end(0, token.MappingStartType)
	require.NoError(t, err)
	require.Equal(t, strings.LastIndex(string(data), "}"), outer)
	nested := strings.Index(string(data), "{inner")
	end, err := doc.end(nested, token.MappingStartType)
	require.NoError(t, err)
	require.Equal(t, strings.Index(string(data), "}, other"), end)
	_, err = doc.end(nested, token.SequenceStartType)
	require.ErrorContains(t, err, "position mismatch")
}

// TestFlowDocumentRefusesInvalidTokenStreams verifies the refresh behavior.
//
// Example: An unterminated quoted value fails safely.
func TestFlowDocumentRefusesInvalidTokenStreams(t *testing.T) {
	tests := []struct {
		name  string
		text  []byte
		issue string
	}{
		{"invalid utf8", []byte{0xff}, "UTF-8"},
		{"unterminated quote", []byte("{a: 'unterminated}\n"), "token"},
		{"unmatched close", []byte("}\n"), "position mismatch"},
		{"unmatched open", []byte("{a: one\n"), "unbalanced"},
	}
	for _, tc := range tests {
		t.Run(tc.name,
			// Callback runs the named regression scenario.
			//
			// Example: Exercise one table-driven case.
			func(t *testing.T) {
				doc := &flowDocument{data: tc.text}
				_, err := doc.end(0, token.MappingStartType)
				require.ErrorContains(t, err, tc.issue)
			})
	}
}

// TestVerifyRefreshRejectsMalformedCandidate verifies the refresh behavior.
//
// Example: A malformed candidate cannot pass semantic verification.
func TestVerifyRefreshRejectsMalformedCandidate(t *testing.T) {
	malformed := strings.Replace(linkedReport, "state: Working", "state: 'unterminated", 1)
	require.Error(t, verifyRefresh([]byte(linkedReport), []byte(malformed), ledgerSource{}, forecastSource{}))
}

// TestVerifyForecastGroupRejectsNonMapping verifies the refresh behavior.
//
// Example: A non-mapping lane cannot pass forecast verification.
func TestVerifyForecastGroupRejectsNonMapping(t *testing.T) {
	err := verifyForecastGroup(forecastGroupLanes, []any{"unexpected"}, []any{"unexpected"}, ledgerSource{}, forecastSource{}, nil)
	require.ErrorContains(t, err, "unsupported forecast group")
}

// TestYAMLCommentVerificationRejectsInvalidLexerStream verifies the refresh behavior.
//
// Example: An invalid lexer stream cannot establish comment preservation.
func TestYAMLCommentVerificationRejectsInvalidLexerStream(t *testing.T) {
	_, err := yamlComments([]byte("state: 'unterminated\n"))
	require.ErrorContains(t, err, "unsupported YAML token")
}

// TestRefreshProjectsPlainScalarPunctuation checks lexer-backed scalar spans.
//
// Example: A comma or embedded hash in a block scalar is replaced in full.
func TestRefreshProjectsPlainScalarPunctuation(t *testing.T) {
	for _, old := range []string{"First, second", "issue#123"} {
		t.Run(old,
			// Callback runs a plain-scalar projection case.
			//
			// Example: Update an outcome containing a comma.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				original := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
				block := "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: " + old + " # preserve comment\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial"
				report := strings.Replace(linkedReport, original, block, 1)
				report = strings.Replace(report, "    outcome: First result\n", "    outcome: Updated result\n", 1)
				ledger := strings.Replace(linkedLedger, "data: {description: First result}", "data: {description: Updated result}", 1)
				source := strings.Replace(linkedForecasts, "outcome: First result", "outcome: Updated result", 1)
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
				require.NoError(t, refreshTestStatus(dir, "forecast_change"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, "Updated result", parsed.Lanes[0].Forecast.Outcome)
				require.Contains(t, string(result), "# preserve comment")
				require.Empty(t, testReconcile(parsed, dir))
				require.NoError(t, refreshTestStatus(dir, "forecast_change"))
				again, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, again)
			})
	}
}

// TestRefreshRefusesMultilineQuotedScalar preserves unsupported scalar text.
//
// Example: A quoted outcome spanning lines remains available for manual review.
func TestRefreshRefusesMultilineQuotedScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	original := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	block := "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: 'First\n        result'\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial"
	report := strings.Replace(linkedReport, original, block, 1)
	source := strings.Replace(linkedForecasts, "outcome: First result", "outcome: Updated result", 1)
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
	require.Error(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "outcome: 'First\n        result'")
	require.Contains(t, string(result), "scope_complete: false")
	require.Contains(t, string(result), "Status refresh incomplete:")
}

// TestScalarSpanRejectsParserLexerDisagreement checks the scalar identity guard.
//
// Example: A node value that differs from its lexer token cannot be replaced.
func TestScalarSpanRejectsParserLexerDisagreement(t *testing.T) {
	data := []byte("outcome: one\n")
	var parsed yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	node := mapValue(parsed.Content[0], "outcome")
	node.Value = "different"
	_, _, err := (&flowDocument{data: data}).scalarSpan(node)
	require.ErrorContains(t, err, "token mismatch")
}

// testRefreshObserver carries a test-only publication callback.
//
// Example: Mutate a report immediately before its final check.
type testRefreshObserver struct{ Action func() }

// BeforePublish runs the test's publication callback.
//
// Example: Simulate a concurrent source edit.
func (o testRefreshObserver) BeforePublish() { o.Action() }

// TestRefreshDoesNotProjectUndeclaredForecastRole checks declared role applicability.
//
// Example: A roleless work reference cannot authorize a lane forecast update.
func TestRefreshDoesNotProjectUndeclaredForecastRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	ledger := strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: []", 1)
	forecasts := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(forecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T16:00:00Z", parsed.Lanes[0].Forecast.Target)
	require.False(t, *parsed.ScopeComplete)
}

// TestRefreshDoesNotProjectRedundantRoot checks root and lane representation.
//
// Example: A root already represented by a lane keeps its existing projection.
func TestRefreshDoesNotProjectRedundantRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	report := linkedReport + "root_forecasts:\n  - {forecast_ref: 'forecasts.yaml#/records/first', outcome: Root result, target: '2026-11-02T15:00:00Z', assessment: complete}\n"
	ledger := strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: [lane, root]", 1)
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T15:00:00Z", parsed.RootForecasts[0].Target)
	require.False(t, *parsed.ScopeComplete)
}

// TestRemoveUnpublishedReportsUnexpectedFailure checks cleanup error handling.
//
// Example: A nonempty directory cannot be silently treated as a removed candidate.
func TestRemoveUnpublishedReportsUnexpectedFailure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, removeUnpublished(filepath.Join(dir, "already-renamed")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "child"), []byte("keep"), 0600))
	require.Error(t, removeUnpublished(dir))
}

// TestRefreshEvaluatesElapsedForecastsAtCapturedTime checks refresh-time evaluation.
//
// Example: An expired target is marked incomplete without changing authored timestamps.
func TestRefreshEvaluatesElapsedForecastsAtCapturedTime(t *testing.T) {
	cases := []struct {
		Name       string
		Evaluation time.Time
		Incomplete bool
	}{
		{Name: "future target", Evaluation: time.Date(2026, 11, 2, 15, 30, 0, 0, time.UTC)},
		{Name: "expired target", Evaluation: time.Date(2026, 11, 2, 16, 15, 0, 0, time.UTC), Incomplete: true},
	}
	for _, tc := range cases {
		t.Run(tc.Name,
			// Callback checks one explicit evaluation instant.
			//
			// Example: Evaluate an already elapsed forecast.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
				require.NoError(t, refreshStatusAt(dir, "forecast_change", tc.Evaluation))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, !tc.Incomplete, *report.ScopeComplete, string(result))
				if tc.Incomplete {
					require.Contains(t, string(result), "target elapsed while work open")
				} else {
					require.NotContains(t, string(result), "target elapsed while work open")
				}
				require.Contains(t, string(result), "timestamp: '2026-11-02T15:24:00Z'")
				require.Contains(t, string(result), "as_of: '2026-11-02T15:24:00Z'")
			})
	}
}

// TestRefreshRejectsContradictoryUnchangedRecalibration checks source truth.
//
// Example: A prior target differing from an unchanged current target is incomplete.
func TestRefreshRejectsContradictoryUnchangedRecalibration(t *testing.T) {
	cases := []struct {
		Name, Prior string
		Invalid     bool
	}{
		{Name: "matching prior", Prior: "2026-11-02T16:00:00Z"},
		{Name: "different prior", Prior: "2026-11-02T15:30:00Z", Invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.Name,
			// Callback checks one canonical recalibration record.
			//
			// Example: Reject an unchanged forecast with a different prior target.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				source := strings.Replace(linkedForecasts, "recalibration: {status: unchanged, evidence: initial}", "recalibration: {status: unchanged, prior: '"+tc.Prior+"', current: '2026-11-02T16:00:00Z', evidence: initial}", 1)
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(source), 0600))
				err := refreshStatusAt(dir, "forecast_change", time.Date(2026, 11, 2, 15, 30, 0, 0, time.UTC))
				result, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				report, parseErr := parse(result)
				require.NoError(t, parseErr)
				if tc.Invalid {
					require.Error(t, err)
					require.False(t, *report.ScopeComplete)
					require.Contains(t, string(result), "recalibration")
				} else {
					require.NoError(t, err)
					require.True(t, *report.ScopeComplete, string(result))
				}
			})
	}
}

// refreshTestStatus evaluates a fixture at its fixed report time.
//
// Example: Keep a forecast fixture independent of the wall clock.
func refreshTestStatus(
	dir string,
	event string,
) error {
	return refreshStatusAt(dir, event, time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC))
}

// refreshTestLinkedStatus evaluates a fixture using a test publication observer.
//
// Example: Simulate a concurrent write at a deterministic evaluation time.
func refreshTestLinkedStatus(
	dir string,
	event string,
	observer refreshObserver,
) error {
	return refreshLinkedStatusAt(dir, event, observer, time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC))
}
