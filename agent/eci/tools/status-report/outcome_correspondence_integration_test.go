package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTerminalLaneOutcomeCorrespondence checks completed and cancelled lane labels against canonical outcomes.
//
// Example: A cancelled lane cannot silently describe another outcome.
func TestTerminalLaneOutcomeCorrespondence(t *testing.T) {
	forecast := "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n"
	for _, tc := range []struct {
		name, laneState, workState string
	}{
		{name: "completed", laneState: "implementation_status: CLOSED\n    completed_at: '2026-11-02T15:00:00Z'", workState: "state: completed, completed_at: '2026-11-02T15:00:00Z'"},
		{name: "cancelled", laneState: "lifecycle: cancelled\n    implementation_status: IN PROGRESS", workState: "state: cancelled"},
	} {
		t.Run(tc.name,
			// Check one terminal lane against its linked outcome.
			//
			// Example: Preserve the matched cancellation outcome.
			func(t *testing.T) {
				report := strings.Replace(linkedReport, "implementation_status: IN PROGRESS", tc.laneState, 1)
				report = strings.Replace(report, forecast, "", 1)
				ledger := strings.Replace(linkedLedger, "state: active", tc.workState, 1)
				forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
				matched := lifecycleResult(t, report, ledger, forecasts)
				require.NotContains(t, matched, "INCOMPLETE")
				report = strings.Replace(report, "    outcome: First result", "    outcome: Unrelated result", 1)
				mismatched := lifecycleResult(t, report, ledger, forecasts)
				require.Contains(t, mismatched, "INCOMPLETE")
				require.Contains(t, mismatched, "outcome")
			})
	}
}

// TestFutureActualCompletionWithoutSources checks actual time independently of source availability.
//
// Example: Missing ledger evidence cannot hide a future completion timestamp.
func TestFutureActualCompletionWithoutSources(t *testing.T) {
	report := strings.Replace(linkedReport, "implementation_status: IN PROGRESS", "implementation_status: CLOSED\n    completed_at: '2026-11-02T16:00:00Z'", 1)
	report = strings.Replace(report, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	at, err := parseUTC("2026-11-02T15:24:00Z")
	require.NoError(t, err)
	var out, errOut bytes.Buffer
	require.Equal(t, 0, runAt([]string{"render"}, strings.NewReader(report), &out, &errOut, at), errOut.String())
	require.Contains(t, out.String(), "INCOMPLETE")
	require.Contains(t, out.String(), "completed_at")
	require.Contains(t, out.String(), "future")
	out.Reset()
	errOut.Reset()
	root := linkedReport + "root_completions:\n  - {work_ref: 'project-understanding.yaml#/records/root', outcome: Root result, completed_at: '2026-11-02T16:00:00Z'}\n"
	require.Equal(t, 0, runAt([]string{"render"}, strings.NewReader(root), &out, &errOut, at), errOut.String())
	require.Contains(t, out.String(), "completed_at")
	require.Contains(t, out.String(), "future")
	require.NotContains(t, lifecycleResult(t, linkedReport, linkedLedger, linkedForecasts), "INCOMPLETE")
}

// TestLaneLifecycleScalarValidation preserves omission and rejects unsupported null or unknown values.
//
// Example: Explicit null is not a cancellation state.
func TestLaneLifecycleScalarValidation(t *testing.T) {
	for _, value := range []string{"null", "unknown", "42", "!custom cancelled"} {
		doc := strings.Replace(linkedReport, "    required_environment: implementation", "    lifecycle: "+value+"\n    required_environment: implementation", 1)
		_, err := parse([]byte(doc))
		require.Error(t, err, value)
	}
	_, err := parse([]byte(linkedReport))
	require.NoError(t, err)
}

// TestRootForecastOutcomeCorrespondence checks root labels and full composite identity against canonical outcomes.
//
// Example: One matching child does not represent a two-outcome root.
func TestRootForecastOutcomeCorrespondence(t *testing.T) {
	report := linkedReport + `root_forecasts:
  - {forecast_ref: 'forecasts.yaml#/records/root', outcome: Root result, target: '2026-11-02T18:00:00Z', assessment: complete, baseline: '2026-11-02T18:00:00Z', evidence: initial}
`
	report = strings.Replace(report, "work_refs: ['project-understanding.yaml#/records/work:first']", "work_refs: ['project-understanding.yaml#/records/work:first', 'project-understanding.yaml#/records/root-work']", 1)
	ledger := linkedLedger + `  root-outcome: {section: progress, type: outcome, data: {description: Root result}}
  child-outcome: {section: progress, type: outcome, data: {description: Other child}}
  root-work:
    section: progress
    type: work
    links: {outcomes: [root-outcome]}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: [root], forecast_ref: 'forecasts.yaml#/records/root'}
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
	matched := lifecycleResult(t, report, ledger, forecasts)
	require.NotContains(t, matched, "INCOMPLETE")
	mismatchedReport := strings.Replace(report, "outcome: Root result, target:", "outcome: Unrelated, target:", 1)
	mismatchedSource := strings.Replace(forecasts, "    outcome: Root result", "    outcome: Unrelated", 1)
	require.Contains(t, lifecycleResult(t, mismatchedReport, ledger, mismatchedSource), "INCOMPLETE")
	partial := strings.Replace(ledger, "outcomes: [root-outcome]", "outcomes: [root-outcome, child-outcome]", 1)
	require.Contains(t, lifecycleResult(t, report, partial, forecasts), "INCOMPLETE")
}

// TestRootCompletionOutcomeCorrespondence checks completed root descriptions against their canonical record.
//
// Example: A completed root cannot claim an unrelated outcome.
func TestRootCompletionOutcomeCorrespondence(t *testing.T) {
	start := strings.Index(linkedReport, "  - task_id: '1'\n")
	end := strings.Index(linkedReport, "  - task_id: '2'\n")
	require.Greater(t, start, 0)
	require.Greater(t, end, start)
	report := linkedReport[:start] + linkedReport[end:] + `root_completions:
  - {work_ref: 'project-understanding.yaml#/records/work:first', outcome: First result, completed_at: '2026-11-02T15:00:00Z'}
`
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish, reporting_roles: [lane]", "state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [root]", 1)
	forecasts := strings.Replace(linkedForecasts, "kind: lane", "kind: root", 1)
	forecasts = strings.Replace(forecasts, "status: active", "status: inactive", 1)
	require.NotContains(t, lifecycleResult(t, report, ledger, forecasts), "INCOMPLETE")
	report = strings.Replace(report, "outcome: First result, completed_at:", "outcome: Unrelated, completed_at:", 1)
	require.Contains(t, lifecycleResult(t, report, ledger, forecasts), "INCOMPLETE")
}
