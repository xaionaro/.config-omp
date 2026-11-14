package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneCorrectionRequiresCorrectOrdinalForSameLineUpdates checks source locators.
//
// Example: The second update requires ordinal one, while omission means zero.
func TestLaneCorrectionRequiresCorrectOrdinalForSameLineUpdates(t *testing.T) {
	t.Parallel()
	// first contains an independent update before the original lane closure.
	//
	// Example: A lane completion at ordinal one follows another worker update.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"2","fields":{"owner":"other"}},{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	cases := []struct {
		name, ordinal string
		applies       bool
	}{
		{"correct", `,"corrects_ordinal":1`, true},
		{"omitted", ``, false},
		{"wrong", `,"corrects_ordinal":0`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Replay one correction against the same logged predecessor.
			//
			// Example: A wrong ordinal cannot publish the corrected closure.
			func(t *testing.T) {
				correction := `{"event":"correction","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1` + tc.ordinal + `,"evidence":"corrected","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+correction), 0600))
				ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
				forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
				result, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, "other", report.Tasks[1].Owner)
				if tc.applies {
					require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
					require.Equal(t, "2026-11-02T15:45:00Z", report.Lanes[0].CompletedAt)
					require.Empty(t, report.StatusSync.Groups)
				} else {
					require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
					require.Empty(t, report.Lanes[0].CompletedAt)
					require.NotEmpty(t, report.StatusSync.Groups)
				}
			})
	}
}
