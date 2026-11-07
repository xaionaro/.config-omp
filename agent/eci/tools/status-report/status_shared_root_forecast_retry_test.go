package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSharedRootForecastWaitsForConsistentOutcomes checks shared source ownership.
//
// Example: Conflicting work outcomes keep a root forecast pending until reconciled.
func TestSharedRootForecastWaitsForConsistentOutcomes(t *testing.T) {
	// event supplies a root forecast shared by two active work records.
	//
	// Example: Add one common deadline after both outcomes agree.
	const event = `{"event":"forecast_change","details":{"status_update":{"additions":[{"target":"root_forecast","record":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}]}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	work := workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w1": {Type: "work", OutcomeRefs: []string{"o1"}, Data: work}, "w2": {Type: "work", OutcomeRefs: []string{"o2"}, Data: work}, "o1": {Type: "outcome", Outcome: "Result"}, "o2": {Type: "outcome", Outcome: "Different"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "active", "kind": "root"}}}
	pending, _, err := projectLogStatus(dir, []byte(valid), ledger, forecasts)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Empty(t, report.RootForecasts)
	require.Len(t, report.StatusSync.PendingRootAdditions, 1)
	ledger.Records["o2"] = sourceRecord{Type: "outcome", Outcome: "Result"}
	applied, _, err := projectLogStatus(dir, pending, ledger, forecasts)
	require.NoError(t, err)
	report, err = parse(applied)
	require.NoError(t, err)
	require.Len(t, report.RootForecasts, 1)
	require.Equal(t, "Result", report.RootForecasts[0].Outcome)
	require.Empty(t, report.StatusSync.PendingRootAdditions)
	retry, _, err := projectLogStatus(dir, applied, ledger, forecasts)
	require.NoError(t, err)
	require.Equal(t, applied, retry)
}
