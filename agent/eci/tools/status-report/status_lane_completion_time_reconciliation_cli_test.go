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

// TestLaneCompletionTimeMismatchSurfacesInCLI checks source-backed reconciliation.
//
// Example: A report completion must agree with the canonical work timestamp.
func TestLaneCompletionTimeMismatchSurfacesInCLI(t *testing.T) {
	t.Parallel()
	// forecast is retired from the report after the source work completes.
	//
	// Example: Terminal lane reconciliation compares actual completion times.
	const forecast = "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n"
	report := strings.Replace(linkedReport, forecast, "", 1)
	report = strings.Replace(report, "    implementation_status: IN PROGRESS\n", "    implementation_status: CLOSED\n    completed_at: '2026-11-02T15:01:00Z'\n", 1)
	require.NotEqual(t, linkedReport, report)
	ledger := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:00:00Z'", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, ledger)
	require.NotEqual(t, linkedForecasts, forecasts)
	for _, tc := range []struct {
		name, report string
		incomplete   bool
	}{
		{"different time", report, true},
		{"equivalent UTC offset", strings.Replace(report, "completed_at: '2026-11-02T15:01:00Z'", "completed_at: '2026-11-02T15:00:00+00:00'", 1), false},
	} {
		t.Run(tc.name,
			// check runs both public commands against the same source files.
			//
			// Example: Correcting the timestamp restores clean reconciliation.
			func(t *testing.T) {
				dir := writeLinkedSources(t, ledger, forecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tc.report), 0600))
				var out, errOut bytes.Buffer
				now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
				code := runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now)
				if tc.incomplete {
					require.Equal(t, 1, code, errOut.String())
					require.Contains(t, out.String(), "work work:first completed_at mismatch")
				} else {
					require.Equal(t, 0, code, errOut.String())
					require.Contains(t, out.String(), "source reconciliation complete")
				}
				out.Reset()
				errOut.Reset()
				require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
				if tc.incomplete {
					require.Contains(t, out.String(), "work work:first completed_at mismatch")
				} else {
					require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
				}
			})
	}
}
