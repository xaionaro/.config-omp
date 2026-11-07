package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTerminalLaneAdditionWaitsForMatchingSources checks independent source conflicts.
//
// Example: A changed completion time cannot publish a stale lane record.
func TestTerminalLaneAdditionWaitsForMatchingSources(t *testing.T) {
	// event supplies a completed lane without an active report forecast.
	//
	// Example: Add an accepted implementation completion.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"lane","evidence":"accepted","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","required_environment":"implementation","implementation_status":"CLOSED","test_status":"NEW","prod_status":"NEW","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n"
	cases := []struct {
		name     string
		work     workData
		forecast map[string]any
	}{
		{"wrong completion time", workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}, map[string]any{"status": "inactive", "kind": "lane"}},
		{"wrong reporting role", workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}, map[string]any{"status": "inactive", "kind": "lane"}},
		{"wrong forecast kind", workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}, map[string]any{"status": "inactive", "kind": "root"}},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Check one independently inconsistent canonical source.
			//
			// Example: A root forecast cannot authorize a lane completion.
			func(t *testing.T) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: tc.work}, "o": {Type: "outcome", Outcome: "Result"}}}
				forecasts := forecastSource{Records: map[string]map[string]any{"f": tc.forecast}}
				pending, _, err := projectLogStatus(dir, []byte(valid), ledger, forecasts)
				require.NoError(t, err)
				report, err := parse(pending)
				require.NoError(t, err)
				require.Empty(t, report.Lanes)
				require.Len(t, report.StatusSync.PendingAdditions, 1)
				ledger.Records["w"] = sourceRecord{Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}}
				forecasts.Records["f"] = map[string]any{"status": "inactive", "kind": "lane"}
				repaired, _, err := projectLogStatus(dir, pending, ledger, forecasts)
				require.NoError(t, err)
				report, err = parse(repaired)
				require.NoError(t, err)
				require.Len(t, report.Lanes, 1)
				require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
				require.Empty(t, report.StatusSync.PendingAdditions)
			})
	}
}
