package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTerminalFollowupReconciliationInCLI checks public source-backed diagnostics.
//
// Example: Terminal work cannot retain a followup marker, while active work can.
func TestTerminalFollowupReconciliationInCLI(t *testing.T) {
	// forecast is removed from report lanes whose canonical work is terminal.
	//
	// Example: A completed or cancelled lane has no active report forecast.
	const forecast = "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n"
	terminal := strings.Replace(linkedReport, forecast, "", 1)
	require.NotEqual(t, linkedReport, terminal)
	completed := strings.Replace(terminal, "    implementation_status: IN PROGRESS\n", "    implementation_status: CLOSED\n    completed_at: '2026-11-02T15:00:00Z'\n", 1)
	cancelled := strings.Replace(terminal, "    implementation_status: IN PROGRESS\n", "    lifecycle: cancelled\n    implementation_status: IN PROGRESS\n", 1)
	completedLedger := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:00:00Z'", 1)
	cancelledLedger := strings.Replace(linkedLedger, "owner: one, state: active", "owner: one, state: cancelled", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	for _, tc := range []struct {
		name, report, ledger, forecast, marker string
		incomplete                             bool
	}{
		{"completed followup", completed, completedLedger, inactive, "FOLLOWUP", true},
		{"cancelled paused followup", cancelled, cancelledLedger, inactive, "FOLLOWUP_PAUSED", true},
		{"active followup", linkedReport, linkedLedger, linkedForecasts, "FOLLOWUP", false},
		{"completed resolved", completed, completedLedger, inactive, "", false},
	} {
		t.Run(tc.name,
			// check runs validation and rendering against the canonical sources.
			//
			// Example: Removing a stale marker restores clean reconciliation.
			func(t *testing.T) {
				input := tc.report
				if tc.marker != "" {
					input = strings.Replace(input, "    required_environment: implementation\n", "    required_environment: implementation\n    root_task_status: "+tc.marker+"\n", 1)
					require.NotEqual(t, tc.report, input)
				}
				dir := writeLinkedSources(t, tc.ledger, tc.forecast)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(input), 0600))
				var out, errOut bytes.Buffer
				now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
				code := runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now)
				if tc.incomplete {
					require.Equal(t, 1, code, errOut.String())
					require.Contains(t, out.String(), "terminal work retains followup: work:first")
				} else {
					require.Equal(t, 0, code, errOut.String())
					require.Contains(t, out.String(), "source reconciliation complete")
				}
				out.Reset()
				errOut.Reset()
				require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
				if tc.incomplete {
					require.Contains(t, out.String(), "terminal work retains followup: work:first")
				} else {
					require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
				}
			})
	}
}
