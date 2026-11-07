package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInvalidAdditionModesDoNotBlockIndependentProgress checks malformed producer input.
//
// Example: A task cannot retire a forecast while a valid sibling update still applies.
func TestInvalidAdditionModesDoNotBlockIndependentProgress(t *testing.T) {
	cases := []struct{ name, addition string }{
		{"task retires forecast", `{"target":"task","retire_forecast":true,"record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}`},
		{"review retires priority", `{"target":"review","retire_priority":"high","record":{"task_id":"1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}}`},
		{"estimate has unpaired prior", `{"target":"priority_estimate","expected_prior":{"priority":"high"},"record":{"priority":"low","min_minutes":1,"max_minutes":2,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}`},
		{"root forecast retires forecast", `{"target":"root_forecast","retire_forecast":true,"record":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Check one malformed addition and a separate valid update.
			//
			// Example: A contradictory operation is diagnosed without suppressing progress.
			func(t *testing.T) {
				dir := t.TempDir()
				event := `{"event":"decision","details":{"status_update":{"additions":[` + tc.addition + `],"updates":[{"target":"progress","id":"other","fields":{"text":"independent progress"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, report.Progress, "independent progress")
				require.Len(t, report.Tasks, 2)
				require.Empty(t, report.Reviews)
				require.Empty(t, report.RootForecasts)
				require.Len(t, report.PriorityEstimates, 1)
				require.Empty(t, report.StatusSync.PendingAdditions)
				require.Empty(t, report.StatusSync.PendingRootAdditions)
				require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
			})
	}
}
