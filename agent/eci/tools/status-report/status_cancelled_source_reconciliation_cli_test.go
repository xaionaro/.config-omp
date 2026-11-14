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

// TestCancelledLaneSourceDiscrepanciesSurfaceInCLI checks reconciliation backstops.
//
// Example: Validate and render expose either lifecycle mismatch until the sources agree.
func TestCancelledLaneSourceDiscrepanciesSurfaceInCLI(t *testing.T) {
	t.Parallel()
	// forecast is the first lane's active report deadline.
	//
	// Example: A cancelled lane has no active report forecast.
	const forecast = "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n"
	withoutForecast := strings.Replace(linkedReport, forecast, "", 1)
	require.NotEqual(t, linkedReport, withoutForecast)
	cancelledReport := strings.Replace(withoutForecast, "    implementation_status: IN PROGRESS\n", "    lifecycle: cancelled\n    implementation_status: IN PROGRESS\n", 1)
	require.NotEqual(t, withoutForecast, cancelledReport)
	cancelledLedger := strings.Replace(linkedLedger, "owner: one, state: active", "owner: one, state: cancelled", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, cancelledLedger)
	require.NotEqual(t, linkedForecasts, inactive)
	for _, tc := range []struct {
		name, report, ledger, forecast, issue string
	}{
		{"source cancelled report active", withoutForecast, cancelledLedger, inactive, "cancelled lane work:first lifecycle is not marked cancelled"},
		{"source active report cancelled", cancelledReport, linkedLedger, linkedForecasts, "lane work:first lifecycle conflicts with source state"},
		{"matching cancellation", cancelledReport, cancelledLedger, inactive, ""},
	} {
		t.Run(tc.name,
			// check runs both public commands at a deterministic evaluation time.
			//
			// Example: A consistent cancellation validates and renders normally.
			func(t *testing.T) {
				dir := writeLinkedSources(t, tc.ledger, tc.forecast)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tc.report), 0600))
				var out, errOut bytes.Buffer
				now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
				code := runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now)
				if tc.issue == "" {
					require.Equal(t, 0, code, errOut.String())
					require.Contains(t, out.String(), "source reconciliation complete")
				} else {
					require.Equal(t, 1, code, errOut.String())
					require.Contains(t, out.String(), tc.issue)
				}
				out.Reset()
				errOut.Reset()
				require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
				if tc.issue == "" {
					require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
					require.Contains(t, out.String(), "Cancelled: First result")
				} else {
					require.Contains(t, out.String(), tc.issue)
				}
			})
	}
}
