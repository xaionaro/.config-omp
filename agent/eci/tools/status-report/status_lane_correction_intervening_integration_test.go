package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneCorrectionAcrossInterveningUpdate checks source ordering and isolation.
//
// Example: A disjoint owner assignment survives while a malformed dependent update blocks closure.
func TestLaneCorrectionAcrossInterveningUpdate(t *testing.T) {
	// first is an old completion which does not match current canonical work.
	//
	// Example: Keep the old closure pending for an explicit correction.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	// last corrects the old completion to the canonical time.
	//
	// Example: Replace the old completion after checking intervening events.
	const last = `{"event":"correction","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1,"evidence":"corrected","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	cases := []struct {
		name, middle string
		applies      bool
	}{
		{"disjoint owner", `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"owner":"assigned"}}]}}}` + "\n", true},
		{"malformed dependent", `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":42,"fields":{"implementation_status":"PAUSED"}}]}}}` + "\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Replay one intervening lane update between the two completions.
			//
			// Example: Do not bypass a malformed same-lane lifecycle update.
			func(t *testing.T) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+tc.middle+last), 0600))
				ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
				forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
				result, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				if tc.applies {
					require.Equal(t, "assigned", report.Lanes[0].Owner)
					require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
					require.Equal(t, "2026-11-02T15:45:00Z", report.Lanes[0].CompletedAt)
					require.Empty(t, report.StatusSync.Groups)
				} else {
					require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
					require.Empty(t, report.Lanes[0].CompletedAt)
					require.NotEmpty(t, report.StatusSync.Groups)
					require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
				}
			})
	}
}
