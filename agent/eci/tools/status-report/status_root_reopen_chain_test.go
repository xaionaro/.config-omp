package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDelayedRootReopening checks delayed root reopening replay.
//
// Example: Reopening replaces a completed root with its forecast.
func TestDelayedRootReopening(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	forecast := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + completion + `}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w","expected_prior":` + completion + `,"record":` + forecast + `}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "active"}}}
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+second), 0600); err != nil {
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
	if len(r.RootForecasts) != 1 || len(r.RootCompletions) != 0 || len(r.StatusSync.PendingRootAdditions) != 0 {
		require.FailNowf(t, "assertion failed", "delayed root reopening unresolved: forecasts=%d completions=%d pending=%d", len(r.RootForecasts), len(r.RootCompletions), len(r.StatusSync.PendingRootAdditions))
	}
}
