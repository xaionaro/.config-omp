package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootChainKeepsMalformedSiblingDiagnostic checks valid root replay beside malformed input.
//
// Example: An invalid scalar record stays diagnosed without blocking an unrelated root chain.
func TestRootChainKeepsMalformedSiblingDiagnostic(t *testing.T) {
	t.Parallel()
	forecast := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	later := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:10:00Z"}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + completion + `}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","record":42},{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w","expected_prior":` + completion + `,"record":` + forecast + `}]}}}` + "\n"
	third := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted again","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + later + `}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:10:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+second+third), 0600))
	out, _, err := projectLogStatus(dir, []byte(valid+"root_forecasts: ["+forecast+"]\n"), ledger, forecasts)
	require.NoError(t, err)
	r, err := parse(out)
	require.NoError(t, err)
	require.Len(t, r.RootCompletions, 1)
	require.Equal(t, "2026-11-02T15:10:00Z", r.RootCompletions[0].CompletedAt)
	require.Empty(t, r.StatusSync.PendingRootAdditions)
	require.Contains(t, r.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
	require.NotNil(t, r.ScopeComplete)
	require.False(t, *r.ScopeComplete)
}
