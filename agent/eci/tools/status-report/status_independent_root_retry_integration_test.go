package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIndependentRootCompletionSurvivesOtherPriorConflict checks root isolation.
//
// Example: A changed first forecast stays pending while a second root completes.
func TestIndependentRootCompletionSurvivesOtherPriorConflict(t *testing.T) {
	t.Parallel()
	// first is an owner-edited forecast and second is the independent forecast.
	//
	// Example: Only the second forecast still matches its requested retirement.
	const first = `{"forecast_ref":"forecasts.yaml#/records/f1","outcome":"First","target":"2026-11-02T16:30:00Z","assessment":"complete"}`
	// second is the unchanged second-root forecast.
	//
	// Example: The second root may retire it independently.
	const second = `{"forecast_ref":"forecasts.yaml#/records/f2","outcome":"Second","target":"2026-11-02T17:00:00Z","assessment":"complete"}`
	// events record two independent completions, one with an outdated first prior.
	//
	// Example: Both canonical sources complete after their work was accepted.
	const events = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":{"forecast_ref":"forecasts.yaml#/records/f1","outcome":"First","target":"2026-11-02T16:00:00Z","assessment":"complete"},"record":{"work_ref":"project-understanding.yaml#/records/w1","outcome":"First","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":{"forecast_ref":"forecasts.yaml#/records/f2","outcome":"Second","target":"2026-11-02T17:00:00Z","assessment":"complete"},"record":{"work_ref":"project-understanding.yaml#/records/w2","outcome":"Second","completed_at":"2026-11-02T15:05:00Z"}}]}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(events), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"w1": {Type: "work", OutcomeRefs: []string{"o1"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f1"}}, "w2": {Type: "work", OutcomeRefs: []string{"o2"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f2"}}, "o1": {Type: "outcome", Outcome: "First"}, "o2": {Type: "outcome", Outcome: "Second"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f1": {"status": "inactive", "kind": "root"}, "f2": {"status": "inactive", "kind": "root"}}}
	result, _, err := projectLogStatus(dir, []byte(valid+"root_forecasts: ["+first+","+second+"]\n"), ledger, forecasts)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Len(t, report.RootForecasts, 1)
	require.Equal(t, "forecasts.yaml#/records/f1", report.RootForecasts[0].ForecastRef)
	require.Equal(t, "2026-11-02T16:30:00Z", report.RootForecasts[0].Target)
	require.Len(t, report.RootCompletions, 1)
	require.Equal(t, "project-understanding.yaml#/records/w2", report.RootCompletions[0].WorkRef)
	require.Len(t, report.StatusSync.PendingRootAdditions, 1)
}
