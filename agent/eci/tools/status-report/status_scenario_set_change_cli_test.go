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

// TestScenarioSetChangeRefreshesWithoutTargetChange checks source-backed risk updates.
//
// Example: A new infrastructure scenario appears even when the deadline is unchanged.
func TestScenarioSetChangeRefreshesWithoutTargetChange(t *testing.T) {
	t.Parallel()
	oldSpread := `    base_case: {endpoint: '2026-11-02T15:45:00Z', packages: [{work: Finish, duration: '20 minutes', basis: measured}]}
    scenarios: [{name: Review, trigger: review, affected_work: Finish, evidence: observed, assumptions: none, added_duration: '15 minutes', event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', basis: measured}]
`
	newSpread := strings.Replace(oldSpread, "endpoint: '2026-11-02T16:00:00Z', basis: measured}]", "endpoint: '2026-11-02T16:00:00Z', basis: measured}, {name: Infrastructure, trigger: deployment queue, affected_work: Finish, evidence: queue measured, assumptions: parallel with review, added_duration: '15 minutes', event_time: '2026-11-02T15:45:00Z', endpoint: '2026-11-02T16:00:00Z', basis: measured}]", 1)
	require.NotEqual(t, oldSpread, newSpread)
	oldSource := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n"+oldSpread, 1)
	newSource := strings.Replace(oldSource, oldSpread, newSpread, 1)
	dir := writeLinkedSources(t, linkedLedger, oldSource)
	path := filepath.Join(dir, "latest-status-report.yaml")
	sourcePath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.NoError(t, refreshStatusAt(dir, "forecast_change", now))
	require.NoError(t, os.WriteFile(sourcePath, []byte(newSource), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first scenarios projection mismatch or missing")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first scenarios projection mismatch or missing")
	require.NoError(t, refreshStatusAt(dir, "forecast_change", now))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "Infrastructure")
	require.Contains(t, string(result), "queue measured")
	require.Contains(t, string(result), "parallel with review")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "First result — Infrastructure")
	require.Contains(t, out.String(), "Forecast deadline: First result will be finished by 2026-11-02T16:00:00Z")
	require.NoError(t, refreshStatusAt(dir, "forecast_change", now))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
	sourceAfter, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	require.Equal(t, newSource, string(sourceAfter))
}
