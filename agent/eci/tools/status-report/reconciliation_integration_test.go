package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReconciliationMissingSources checks that a valid report cannot hide absent or malformed source records.
//
// Example: A session with no forecast source renders an incomplete report.
func TestReconciliationMissingSources(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		ledger string
	}{
		{name: "missing ledger"},
		{name: "malformed ledger", ledger: "[malformed"},
		{name: "multiple documents", ledger: linkedLedger + "\n---\nschema: other\n"},
		{name: "partial schema", ledger: "schema: project-understanding/v1\n"},
	} {
		t.Run(tc.name,
			// Exercise one source reconciliation case.
			//
			// Example: Report unavailable source evidence.
			func(t *testing.T) {
				dir := t.TempDir()
				reportPath := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(reportPath, []byte(valid), 0o600))
				if tc.ledger != "" {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(tc.ledger), 0o600))
				}
				var stdout, stderr bytes.Buffer
				code := testRun([]string{"render", reportPath}, strings.NewReader(""), &stdout, &stderr)
				require.Equal(t, 0, code, stderr.String())
				require.Contains(t, stdout.String(), "INCOMPLETE")
				stdout.Reset()
				stderr.Reset()
				code = testRun([]string{"validate", reportPath}, strings.NewReader(""), &stdout, &stderr)
				require.NotEqual(t, 0, code, stdout.String())
			})
	}
}

// linkedReport is a two-lane report with separately owned work and forecasts.
//
// Example: Reconcile two active lanes.
const linkedReport = `timestamp: '2026-11-02T15:24:00Z'
state: Working
progress: [Implementation started]
decisions: [Use YAML]
blockers_risks: []
verification: [pending]
next_focus: Finish
scope_complete: true
tasks:
  - {id: '1', title: First, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS, work_refs: ['project-understanding.yaml#/records/work:first']}
  - {id: '2', title: Second, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS, work_refs: ['project-understanding.yaml#/records/work:second']}
top_priorities: ['1', '2']
priority_estimates:
  - {priority: high, min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest}
completion_estimate: {min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest}
lanes:
  - task_id: '1'
    work_ref: 'project-understanding.yaml#/records/work:first'
    outcome: First result
    required_environment: implementation
    implementation_status: IN PROGRESS
    test_status: NEW
    prod_status: NEW
    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}
  - task_id: '2'
    work_ref: 'project-understanding.yaml#/records/work:second'
    outcome: Second result
    required_environment: implementation
    implementation_status: IN PROGRESS
    test_status: NEW
    prod_status: NEW
    forecast: {forecast_ref: 'forecasts.yaml#/records/second', outcome: Second result, target: '2026-11-02T17:00:00Z', assessment: complete, baseline: '2026-11-02T17:00:00Z', evidence: initial}
`

// linkedLedger contains the two active lane work records projected by linkedReport.
//
// Example: Resolve each lane to its work record.
const linkedLedger = `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:first:
    section: progress
    type: outcome
    data: {description: First result}
  outcome:second:
    section: progress
    type: outcome
    data: {description: Second result}
  work:first:
    section: progress
    type: work
    links: {outcomes: ['outcome:first']}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/first'}
    evidence: []
  work:second:
    section: progress
    type: work
    links: {outcomes: ['outcome:second']}
    data: {owner: two, state: active, next_action: Finish, reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/second'}
    evidence: []
`

// linkedForecasts contains current forecasts corresponding to linkedLedger.
//
// Example: Compare projected deadlines with canonical forecasts.
const linkedForecasts = `schema: forecasts/v1
records:
  first:
    kind: lane
    outcome: First result
    status: active
    assessment: complete
    target: '2026-11-02T16:00:00+00:00'
    original_baseline: '2026-11-02T16:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
  second:
    kind: lane
    outcome: Second result
    status: active
    assessment: complete
    target: '2026-11-02T17:00:00Z'
    original_baseline: '2026-11-02T17:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`

// TestReconciliationForecasts checks source projections and missing or inconsistent forecasts through the CLI.
//
// Example: A changed source target makes the old report incomplete.
func TestReconciliationForecasts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		report     string
		forecasts  string
		incomplete bool
	}{
		{name: "matching lanes", forecasts: linkedForecasts},
		{name: "closed package with active lane", report: strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", "status: CLOSED, evidence: accepted, work_refs:", 1), forecasts: linkedForecasts},
		{name: "missing forecasts", incomplete: true},
		{name: "inactive forecast", forecasts: strings.Replace(linkedForecasts, "status: active", "status: inactive", 1), incomplete: true},
		{name: "changed target", forecasts: strings.Replace(linkedForecasts, "2026-11-02T16:00:00+00:00", "2026-11-02T16:30:00Z", 1), incomplete: true},
	} {
		t.Run(tc.name,
			// Exercise one source reconciliation case.
			//
			// Example: Report unavailable source evidence.
			func(t *testing.T) {
				dir := t.TempDir()
				reportPath := filepath.Join(dir, "latest-status-report.yaml")
				report := tc.report
				if report == "" {
					report = linkedReport
				}
				require.NoError(t, os.WriteFile(reportPath, []byte(report), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0o600))
				if tc.forecasts != "" {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(tc.forecasts), 0o600))
				}
				var stdout, stderr bytes.Buffer
				require.Equal(t, 0, testRun([]string{"render", reportPath}, strings.NewReader(""), &stdout, &stderr), stderr.String())
				if tc.incomplete {
					require.Contains(t, stdout.String(), "INCOMPLETE")
				} else {
					require.NotContains(t, stdout.String(), "INCOMPLETE")
				}
				stdout.Reset()
				stderr.Reset()
				code := testRun([]string{"validate", reportPath}, strings.NewReader(""), &stdout, &stderr)
				if tc.incomplete {
					require.NotEqual(t, 0, code, stdout.String())
				} else {
					require.Equal(t, 0, code, stderr.String())
				}
			})
	}
}

// TestReconciliationDirectWorkWithoutForecast checks that nonforecast work does not require a forecast file.
//
// Example: Direct work can be fully represented without an applicable forecast.
func TestReconciliationDirectWorkWithoutForecast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	report := strings.Replace(valid, "scope_complete: false", "scope_complete: true", 1)
	report = strings.Replace(report, "status: unknown", "status: IN PROGRESS", 1)
	report = strings.Replace(report, "title: Root", "title: Root\n    work_refs: ['project-understanding.yaml#/records/work:direct']", 1)
	ledger := `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:direct: {section: progress, type: outcome}
  work:direct:
    section: progress
    type: work
    links: {outcomes: ["outcome:direct"]}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: []}
    evidence: []
`
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(reportPath, []byte(report), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0o600))
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, testRun([]string{"render", reportPath}, strings.NewReader(""), &stdout, &stderr), stderr.String())
	require.NotContains(t, stdout.String(), "INCOMPLETE")
	stdout.Reset()
	stderr.Reset()
	require.Equal(t, 0, testRun([]string{"validate", reportPath}, strings.NewReader(""), &stdout, &stderr), stderr.String())
}

// TestReconciliationRichForecast checks that ECI scenario details can be matched without loss.
//
// Example: A changed scenario endpoint makes the old projection incomplete.
func TestReconciliationRichForecast(t *testing.T) {
	t.Parallel()
	projection := `      source_projection:
        base_case:
          endpoint: '2026-11-02T15:45:00Z'
          packages:
            - {work: Finish, duration: 20 minutes, basis: guess}
        scenarios:
          - {name: Delay, trigger: review, affected_work: Finish, evidence: unknown, assumptions: none, added_duration: 15 minutes, event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', basis: guess}
`
	report := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "    forecast:\n      forecast_ref: 'forecasts.yaml#/records/first'\n      outcome: First result\n      target: '2026-11-02T16:00:00Z'\n      assessment: complete\n      baseline: '2026-11-02T16:00:00Z'\n      evidence: initial\n"+projection, 1)
	sourceProjection := `    base_case:
      endpoint: '2026-11-02T15:45:00Z'
      packages:
        - {work: Finish, duration: 20 minutes, basis: guess}
    scenarios:
      - {name: Delay, trigger: review, affected_work: Finish, evidence: unknown, assumptions: none, added_duration: 15 minutes, event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', basis: guess}
`
	forecasts := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n"+sourceProjection, 1)
	require.Contains(t, forecasts, "endpoint: '2026-11-02T16:00:00Z', basis: guess")
	for _, tc := range []struct {
		name       string
		source     string
		incomplete bool
	}{
		{name: "matching", source: forecasts},
		{name: "changed scenario", source: strings.Replace(forecasts, "endpoint: '2026-11-02T16:00:00Z', basis: guess", "endpoint: '2026-11-02T16:15:00Z', basis: guess", 1), incomplete: true},
	} {
		t.Run(tc.name,
			// Exercise one source reconciliation case.
			//
			// Example: Report unavailable source evidence.
			func(t *testing.T) {
				dir := t.TempDir()
				reportPath := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(reportPath, []byte(report), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(tc.source), 0o600))
				var stdout, stderr bytes.Buffer
				require.Equal(t, 0, testRun([]string{"render", reportPath}, strings.NewReader(""), &stdout, &stderr), stderr.String())
				if tc.incomplete {
					require.Contains(t, stdout.String(), "INCOMPLETE")
				} else {
					require.NotContains(t, stdout.String(), "INCOMPLETE")
					require.Contains(t, stdout.String(), "Delay")
				}
			})
	}
}

// TestReconciliationCompletedPriorityDoesNotExpire checks that another priority's open work does not stale a completed priority.
//
// Example: A past high-priority endpoint is harmless after high-priority work completes.
func TestReconciliationCompletedPriorityDoesNotExpire(t *testing.T) {
	t.Parallel()
	report := strings.Replace(valid, "scope_complete: false", "scope_complete: true", 1)
	report = strings.Replace(report, "title: Root", "title: Root\n    work_refs: ['project-understanding.yaml#/records/work:done']", 1)
	report = strings.Replace(report, "status: IN PROGRESS\n  - id: \"1.1\"", "status: CLOSED\n    evidence: accepted\n  - id: \"1.1\"", 1)
	report = strings.Replace(report, "title: Unknown", "title: Unknown\n    work_refs: ['project-understanding.yaml#/records/work:open']", 1)
	report = strings.Replace(report, "status: unknown", "status: IN PROGRESS", 1)
	report = strings.Replace(report, "priority: high, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T15:24:00Z\"", "priority: high, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T14:00:00Z\"", 1)
	ledger := `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:direct: {section: progress, type: outcome}
  work:done:
    section: progress
    type: work
    data: {owner: one, state: completed, next_action: none, reporting_roles: []}
    evidence: []
  work:open:
    section: progress
    type: work
    links: {outcomes: ["outcome:direct"]}
    data: {owner: two, state: active, next_action: Finish, reporting_roles: []}
    evidence: []
`
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(reportPath, []byte(report), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0o600))
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, testRun([]string{"render", reportPath}, strings.NewReader(""), &stdout, &stderr), stderr.String())
	require.NotContains(t, stdout.String(), "INCOMPLETE")
	require.Contains(t, stdout.String(), "Source freshness: unverified")
}

// TestReconciliationClosedLaneCompletion checks the actual completion time against the work record.
//
// Example: A different completion instant makes the report incomplete.
func TestReconciliationClosedLaneCompletion(t *testing.T) {
	t.Parallel()
	report := strings.Replace(linkedReport, "implementation_status: IN PROGRESS", "implementation_status: CLOSED\n    completed_at: '2026-11-02T15:00:00Z'", 1)
	report = strings.Replace(report, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish, reporting_roles: [lane]", "state: completed, completed_at: '2026-11-02T15:00:00+00:00', next_action: none, reporting_roles: [lane]", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	forecasts = strings.Replace(forecasts, "    target: '2026-11-02T16:00:00+00:00'\n", "", 1)
	for _, tc := range []struct {
		name       string
		ledger     string
		forecast   string
		incomplete bool
	}{
		{name: "unknown forecast status", ledger: ledger, forecast: strings.Replace(forecasts, "status: inactive", "status: unknown", 1), incomplete: true},
		{name: "matching instant", ledger: ledger},
		{name: "different instant", ledger: strings.Replace(ledger, "2026-11-02T15:00:00+00:00", "2026-11-02T15:10:00Z", 1), incomplete: true},
		{name: "missing completion", ledger: strings.Replace(ledger, "completed_at: '2026-11-02T15:00:00+00:00', ", "", 1), incomplete: true},
	} {
		t.Run(tc.name,
			// Exercise one source reconciliation case.
			//
			// Example: Report unavailable source evidence.
			func(t *testing.T) {
				dir := t.TempDir()
				reportPath := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(reportPath, []byte(report), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(tc.ledger), 0o600))
				forecast := tc.forecast
				if forecast == "" {
					forecast = forecasts
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(forecast), 0o600))
				var stdout, stderr bytes.Buffer
				require.Equal(t, 0, testRun([]string{"render", reportPath}, strings.NewReader(""), &stdout, &stderr), stderr.String())
				if tc.incomplete {
					require.Contains(t, stdout.String(), "INCOMPLETE")
				} else {
					require.NotContains(t, stdout.String(), "INCOMPLETE")
					require.Contains(t, stdout.String(), "Completed: 2026-11-02T15:00:00Z")
				}
			})
	}
}
