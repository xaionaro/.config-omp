package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// lifecycleResult renders a report against disposable canonical records.
//
// Example: Compare a stale projection with its source ledger.
func lifecycleResult(
	t *testing.T,
	report string,
	ledger string,
	forecasts string,
) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	if forecasts != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(forecasts), 0600))
	}
	var out, errOut bytes.Buffer
	require.Equal(t, 0, testRun([]string{"render", path}, strings.NewReader(""), &out, &errOut), errOut.String())
	return out.String()
}

// TestTerminalRootForecastIsIncomplete checks that an inactive source does not validate a retained deadline for terminal work.
//
// Example: A completed root cannot retain a projected active deadline.
func TestTerminalRootForecastIsIncomplete(t *testing.T) {
	report := linkedReport + `root_forecasts:
  - {forecast_ref: 'forecasts.yaml#/records/root', outcome: Root result, target: '2026-11-02T18:00:00Z', assessment: complete}
`
	ledger := linkedLedger + `  work:root:
    section: progress
    type: work
    data: {owner: lead, next_action: none, state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
`
	forecasts := linkedForecasts + `  root:
    kind: root
    outcome: Root result
    status: inactive
    assessment: complete
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	out := lifecycleResult(t, report, ledger, forecasts)
	require.Contains(t, out, "INCOMPLETE")
	require.Contains(t, out, "root")
}

// TestRolelessForecastIsIncomplete distinguishes a missing role from direct work without a forecast.
//
// Example: A direct work record cannot silently retain an unrepresented forecast reference.
func TestRolelessForecastIsIncomplete(t *testing.T) {
	ledger := strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: []", 1)
	out := lifecycleResult(t, linkedReport, ledger, linkedForecasts)
	require.Contains(t, out, "INCOMPLETE")
	require.Contains(t, out, "work:first")
}

// TestClosedLaneWithoutForecastChecksCompletion compares timestamps even when no forecast applies.
//
// Example: A missing completion timestamp remains incomplete without a forecast reference.
func TestClosedLaneWithoutForecastChecksCompletion(t *testing.T) {
	report := strings.Replace(linkedReport, "implementation_status: IN PROGRESS", "implementation_status: CLOSED\n    completed_at: '2026-11-02T15:00:00Z'", 1)
	report = strings.Replace(report, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish, reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/first'", "state: completed, next_action: none, reporting_roles: [lane]", 1)
	out := lifecycleResult(t, report, ledger, linkedForecasts)
	require.Contains(t, out, "INCOMPLETE")
	require.Contains(t, out, "work:first")
	ledger = strings.Replace(ledger, "state: completed, next_action", "state: completed, completed_at: '2026-11-02T15:00:00+00:00', next_action", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	out = lifecycleResult(t, report, ledger, forecasts)
	require.NotContains(t, out, "INCOMPLETE")
}

// TestInheritedWorkPriorityExpiry checks leaf priority against work referenced by an ancestor.
//
// Example: An open low-priority child inherits its root's work reference.
func TestInheritedWorkPriorityExpiry(t *testing.T) {
	report := strings.Replace(valid, "scope_complete: false", "scope_complete: true", 1)
	report = strings.Replace(report, "title: Root", "title: Root\n    work_refs: ['project-understanding.yaml#/records/work:direct']", 1)
	report = strings.Replace(report, "status: unknown", "status: IN PROGRESS", 1)
	report = strings.Replace(report, "priority: unknown, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T15:24:00Z\"", "priority: unknown, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T14:00:00Z\"", 1)
	ledger := `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:direct: {section: progress, type: outcome}
  work:direct:
    section: progress
    type: work
    links: {outcomes: ["outcome:direct"]}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: []}
`
	out := lifecycleResult(t, report, ledger, "")
	require.Contains(t, out, "INCOMPLETE")
	require.Contains(t, out, "endpoint elapsed")
	report = strings.Replace(report, "priority: unknown, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T14:00:00Z\"", "priority: unknown, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T15:24:00Z\"", 1)
	out = lifecycleResult(t, report, ledger, "")
	require.NotContains(t, out, "INCOMPLETE")
}
