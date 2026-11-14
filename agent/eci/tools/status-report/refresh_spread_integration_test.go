package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshForecastSpread checks the canonical target against scenario endpoints.
//
// Example: A scenario later than the target cannot replace the report's forecast.
func TestRefreshForecastSpread(t *testing.T) {
	t.Parallel()
	cases := []struct {
		Name     string
		Endpoint string
		Invalid  bool
	}{
		{Name: "target covers spread", Endpoint: "2026-11-02T16:00:00Z"},
		{Name: "scenario exceeds target", Endpoint: "2026-11-02T16:30:00Z", Invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.Name,
			// Callback exercises one forecast spread.
			//
			// Example: Reject a scenario beyond the declared target.
			func(t *testing.T) {
				dir := t.TempDir()
				reportPath := filepath.Join(dir, "latest-status-report.yaml")
				forecastPath := filepath.Join(dir, "forecasts.yaml")
				projection := "    base_case:\n      endpoint: '2026-11-02T15:45:00Z'\n      packages:\n        - {work: Finish, duration: 20 minutes, basis: guess}\n    scenarios:\n      - {name: Delay, trigger: review, affected_work: Finish, evidence: unknown, assumptions: none, added_duration: 15 minutes, event_time: '2026-11-02T15:45:00Z', endpoint: '" + tc.Endpoint + "', basis: guess}\n"
				source := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n"+projection, 1)
				require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(forecastPath, []byte(source), 0600))
				err := refreshTestStatus(dir, "forecast_change")
				result, readErr := os.ReadFile(reportPath)
				require.NoError(t, readErr)
				report, parseErr := parse(result)
				require.NoError(t, parseErr)
				afterSource, sourceErr := os.ReadFile(forecastPath)
				require.NoError(t, sourceErr)
				require.Equal(t, source, string(afterSource))
				require.Equal(t, "2026-11-02T16:00:00Z", report.Lanes[0].Forecast.Target)
				if tc.Invalid {
					require.ErrorContains(t, err, "forecast spread")
					require.False(t, *report.ScopeComplete)
					require.Contains(t, string(result), "forecast spread")
				} else {
					require.NoError(t, err)
					require.True(t, *report.ScopeComplete)
					require.NotContains(t, string(result), "forecast spread")
				}
			})
	}
}
