package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootChainKeepsScalarPayloadDiagnostic checks invalid unrelated payload isolation.
//
// Example: A scalar status update does not block a valid root lifecycle chain.
func TestRootChainKeepsScalarPayloadDiagnostic(t *testing.T) {
	t.Parallel()
	forecast := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + forecast + `,"record":` + completion + `}]}}}` + "\n"
	invalid := `{"event":"decision","details":{"status_update":42}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w","expected_prior":` + completion + `,"record":` + forecast + `}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "active"}}}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+invalid+second), 0600))
	out, _, err := projectLogStatus(dir, []byte(valid+"root_forecasts: ["+forecast+"]\n"), ledger, forecasts)
	require.NoError(t, err)
	r, err := parse(out)
	require.NoError(t, err)
	require.Len(t, r.RootForecasts, 1)
	require.Empty(t, r.StatusSync.PendingRootAdditions)
	require.Contains(t, r.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
	require.NotNil(t, r.ScopeComplete)
	require.False(t, *r.ScopeComplete)
}
