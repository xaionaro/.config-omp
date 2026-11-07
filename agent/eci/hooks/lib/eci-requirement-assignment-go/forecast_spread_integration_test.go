package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForecastSpreadFailurePreservesLog checks the ECI append and refresh boundary.
//
// Example: An inconsistent scenario cannot rewrite the target or duplicate an event.
func TestForecastSpreadFailurePreservesLog(t *testing.T) {
	f := newCLIFixture(t)
	dir := filepath.Dir(f.Ledger)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	ledger := `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:first: {section: progress, type: outcome, data: {description: First result}}
  work:first:
    section: progress
    type: work
    links: {outcomes: ['outcome:first']}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/first'}
    evidence: []
`
	report := `timestamp: '2026-11-02T15:24:00Z'
state: Working
progress: []
decisions: []
blockers_risks: []
verification: []
next_focus: Finish
scope_complete: true
tasks:
  - {id: '1', title: First, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS, work_refs: ['project-understanding.yaml#/records/work:first']}
top_priorities: ['1']
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
    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2030-11-02T16:00:00Z', assessment: complete, baseline: '2030-11-02T16:00:00Z', evidence: initial}
`
	forecasts := `schema: forecasts/v1
records:
  first:
    kind: lane
    outcome: First result
    status: active
    assessment: complete
    target: '2030-11-02T16:00:00Z'
    original_baseline: '2030-11-02T16:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
    base_case:
      endpoint: '2030-11-02T15:45:00Z'
      packages: [{work: Finish, duration: 20 minutes, basis: guess}]
    scenarios:
      - {name: Delay, trigger: review, affected_work: Finish, evidence: unknown, assumptions: none, added_duration: 15 minutes, event_time: '2030-11-02T15:45:00Z', endpoint: '2030-11-02T16:30:00Z', basis: guess}
`
	require.NoError(t, os.WriteFile(f.Ledger, []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(reportPath, []byte(report), 0600))
	require.NoError(t, os.WriteFile(forecastPath, []byte(forecasts), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), cyclicTestDeadline)
	defer cancel()
	command := boundedECICommand(ctx, f.CLI, "ledger-append", "--json", `{"event":"forecast_change","summary":"Forecast updated"}`)
	command.Env = f.env(t)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.Output()
	require.NoError(t, ctx.Err(), stderr.String())
	require.NoError(t, err, stderr.String())
	require.Contains(t, string(stdout), "high-level log appended")
	require.Contains(t, stderr.String(), "log append succeeded; status refresh failed")
	require.Contains(t, stderr.String(), "forecast spread")
	logged, err := os.ReadFile(filepath.Join(dir, "high_level_log.jsonl"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(logged), "\n"))
	sourceAfter, err := os.ReadFile(forecastPath)
	require.NoError(t, err)
	require.Equal(t, forecasts, string(sourceAfter))
	reportAfter, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Contains(t, string(reportAfter), "scope_complete: false")
	require.Contains(t, string(reportAfter), "forecast spread")
	require.Contains(t, string(reportAfter), "target: '2030-11-02T16:00:00Z'")
	retry := boundedECICommand(ctx, f.CLI, "status-refresh", "--event", "forecast_change")
	retry.Env = command.Env
	retryOutput, retryErr := retry.CombinedOutput()
	require.NoError(t, ctx.Err(), string(retryOutput))
	require.Error(t, retryErr, string(retryOutput))
	require.Contains(t, string(retryOutput), "forecast spread")
	logAfter, err := os.ReadFile(filepath.Join(dir, "high_level_log.jsonl"))
	require.NoError(t, err)
	require.Equal(t, logged, logAfter)
	reportAfterRetry, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, reportAfter, reportAfterRetry)
}
