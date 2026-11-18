package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIncompleteLaneSelectorsAreDiagnosedWhileProgressContinues checks producer input.
//
// Example: A lane without a work reference cannot be queued under an ambiguous identity.
func TestIncompleteLaneSelectorsAreDiagnosedWhileProgressContinues(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, record string }{
		{"missing work reference", `{"task_id":"1","outcome":"Result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW"}`},
		{"empty work reference", `{"task_id":"1","work_ref":"","outcome":"Result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW"}`},
		{"missing environment", `{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW"}`},
		{"missing task", `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Check a malformed lane selector beside an independent valid update.
			//
			// Example: An omitted environment is not converted into a lane identity.
			func(t *testing.T) {
				dir := t.TempDir()
				event := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"lane","record":` + tc.record + `}],"updates":[{"target":"progress","id":"other","fields":{"text":"independent progress"}}]}}}` + "\n"
				require.NoError(t, writeSessionLog(dir, []byte(event)))
				result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Len(t, report.Lanes, 2)
				require.Contains(t, report.Progress, "independent progress")
				require.Empty(t, report.StatusSync.PendingAdditions)
				require.Contains(t, report.StuckWorkRisks, "Status refresh incomplete: invalid status_update requires manual review")
			})
	}
}
