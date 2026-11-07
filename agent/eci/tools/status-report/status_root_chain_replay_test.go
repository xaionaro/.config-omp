package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDelayedRootCloseReopenClose checks delayed root lifecycle replay.
//
// Example: The last closure replaces the reopened forecast.
func TestDelayedRootCloseReopenClose(t *testing.T) {
	forecast := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	later := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:10:00Z"}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + completion + `}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w","expected_prior":` + completion + `,"record":` + forecast + `}]}}}` + "\n"
	third := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted again","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + later + `}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:10:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+second+third), 0600); err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	out, _, err := projectLogStatus(dir, []byte(valid+"root_forecasts: ["+forecast+"]\n"), ledger, forecasts)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	r, err := parse(out)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	if len(r.RootCompletions) != 1 || r.RootCompletions[0].CompletedAt != "2026-11-02T15:10:00Z" || len(r.StatusSync.PendingRootAdditions) != 0 {
		require.FailNowf(t, "assertion failed", "final root closure not projected: completions=%+v forecasts=%d pending=%d", r.RootCompletions, len(r.RootForecasts), len(r.StatusSync.PendingRootAdditions))
	}
}

// TestRootChainReplacesChangedReopenedForecast checks exact same-kind boundaries.
//
// Example: A reopened forecast can carry a changed target when current sources agree.
func TestRootChainReplacesChangedReopenedForecast(t *testing.T) {
	old := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	changed := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T17:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + old + `,"record":` + completion + `}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w","expected_prior":` + completion + `,"record":` + changed + `}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "other": {Type: "work", OutcomeRefs: []string{"different"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}, "different": {Type: "outcome", Outcome: "Other"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "active"}}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+second), 0600); err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	out, _, err := projectLogStatus(dir, []byte(valid+"root_forecasts: ["+old+"]\n"), ledger, forecasts)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	r, err := parse(out)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	if len(r.RootForecasts) != 1 || r.RootForecasts[0].Target != "2026-11-02T17:00:00Z" || len(r.StatusSync.PendingRootAdditions) != 0 {
		require.FailNowf(t, "assertion failed", "replacement failed: forecasts=%+v pending=%d", r.RootForecasts, len(r.StatusSync.PendingRootAdditions))
	}
}

// TestRootChainRetiresForecastAfterCancellation checks a final retirement update.
//
// Example: A delayed completion and reopening end with an explicitly cancelled root.
func TestRootChainRetiresForecastAfterCancellation(t *testing.T) {
	forecast := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + completion + `}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w","expected_prior":` + completion + `,"record":` + forecast + `}]}}}` + "\n"
	third := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"root_forecast","id":"forecasts.yaml#/records/f","work_ref":"project-understanding.yaml#/records/w","evidence":"cancelled","values":{"expected_prior":` + forecast + `},"clear":["record"]}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCancelled, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+second+third), 0600); err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	out, _, err := projectLogStatus(dir, []byte(valid+"root_forecasts: ["+forecast+"]\n"), ledger, forecasts)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	r, err := parse(out)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	if len(r.RootForecasts) != 0 || len(r.RootCompletions) != 0 || len(r.StatusSync.PendingRootAdditions) != 0 || len(r.StatusSync.Groups) != 0 {
		require.FailNowf(t, "assertion failed", "cancellation stuck: forecasts=%d completions=%d pending=%d groups=%d", len(r.RootForecasts), len(r.RootCompletions), len(r.StatusSync.PendingRootAdditions), len(r.StatusSync.Groups))
	}
}
