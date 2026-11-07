package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAnchoredNullRootForecastRetires checks the source-backed lifecycle.
//
// Example: A first forecast initialized from null can later be retired on completion.
func TestAnchoredNullRootForecastRetires(t *testing.T) {
	forecast := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	first := `{"event":"forecast_change","details":{"status_update":{"additions":[{"target":"root_forecast","record":` + forecast + `}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + completion + `}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "active", "kind": "root"}}}
	initial := []byte(valid + "root_forecasts: &owner null # forecast note\nroot_completions: null # completion note\n")
	projected, _, err := projectLogStatus(dir, initial, ledger, forecasts)
	require.NoError(t, err)
	report, err := parse(projected)
	require.NoError(t, err)
	require.Len(t, report.RootForecasts, 1)
	work := ledger.Records["w"]
	work.Data.State = workStateCompleted
	work.Data.CompletedAt = "2026-11-02T15:00:00Z"
	ledger.Records["w"] = work
	forecasts.Records["f"]["status"] = "inactive"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, projected, ledger, forecasts)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Empty(t, report.RootForecasts)
	require.Len(t, report.RootCompletions, 1)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	require.Contains(t, string(result), "root_forecasts: &owner [] # forecast note")
	require.Contains(t, string(result), "# completion note")
	retry, _, err := projectLogStatus(dir, result, ledger, forecasts)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}

// TestAnchoredCollectionRemovalProtectsOwners checks exact removal boundaries.
//
// Example: An owner-edited forecast remains intact and a matching last item retires.
func TestAnchoredCollectionRemovalProtectsOwners(t *testing.T) {
	record := json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`)
	original := []byte("root_forecasts: &owner null # owner note\n")
	initialized, ok := insertSyncAddition(original, syncAddition{Target: syncTargetRootForecast, Record: record})
	require.True(t, ok)
	changed := strings.Replace(string(initialized), `"outcome":"Result"`, `"outcome":"Owner change"`, 1)
	require.NotEqual(t, string(initialized), changed)
	got, ok := removeRootSyncRecord([]byte(changed), syncTargetRootForecast, "forecasts.yaml#/records/f", record)
	require.False(t, ok)
	require.Equal(t, changed, string(got))
	got, ok = removeRootSyncRecord(initialized, syncTargetRootForecast, "forecasts.yaml#/records/f", record)
	require.True(t, ok)
	require.Contains(t, string(got), "root_forecasts: &owner [] # owner note")
}

// TestAnchoredTopPrioritiesAllowCancellation checks the related removal consumer.
//
// Example: A cancelled task is removed while its list anchor and comment survive.
func TestAnchoredTopPrioritiesAllowCancellation(t *testing.T) {
	input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities: &owner ['1', '2'] # owner note", 1)
	out, ok := removeCancelledPriority([]byte(input), taskID("1"))
	require.True(t, ok)
	require.Contains(t, string(out), "top_priorities: &owner ['2'] # owner note")
	_, err := parse(out)
	require.NoError(t, err)
}

// TestAnchoredFlowRemovalPreservesOtherRecords checks each list position.
//
// Example: Removing either forecast keeps the other forecast and owner note.
func TestAnchoredFlowRemovalPreservesOtherRecords(t *testing.T) {
	first := `{"forecast_ref":"forecasts.yaml#/records/f1","outcome":"First","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	second := `{"forecast_ref":"forecasts.yaml#/records/f2","outcome":"Second","target":"2026-11-02T17:00:00Z","assessment":"complete"}`
	input := []byte("root_forecasts: &owner [" + first + ", " + second + "] # owner note\n")
	for _, tc := range []struct{ id, prior, survivor string }{{"f1", first, "Second"}, {"f2", second, "First"}} {
		t.Run(tc.id,
			// Remove one record from the anchored flow list.
			//
			// Example: The sibling record remains after the target is retired.
			func(t *testing.T) {
				out, ok := removeRootSyncRecord(input, syncTargetRootForecast, "forecasts.yaml#/records/"+tc.id, json.RawMessage(tc.prior))
				require.True(t, ok)
				require.Contains(t, string(out), "root_forecasts: &owner [")
				require.Contains(t, string(out), tc.survivor)
				require.Contains(t, string(out), "# owner note")
			})
	}
}

// TestAnchoredStructuredListReplacementKeepsAnchor checks explicit typed updates.
//
// Example: Replacing top priorities preserves the anchor and adjacent note.
func TestAnchoredStructuredListReplacementKeepsAnchor(t *testing.T) {
	input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities: &owner ['1', '2'] # owner note", 1)
	out, ok := editStructuredField([]byte(input), syncUpdate{Target: syncTargetReport, ID: "current"}, "top_priorities", `["2"]`)
	require.True(t, ok)
	require.Contains(t, string(out), `top_priorities: &owner ["2"] # owner note`)
	_, err := parse(out)
	require.NoError(t, err)
}
