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

// TestStaleRootCompletionIsReportedInCLI checks a reopened source against its report.
//
// Example: A stale completion must be replaced with the active root forecast.
func TestStaleRootCompletionIsReportedInCLI(t *testing.T) {
	t.Parallel()
	// firstLane is the existing lane projection converted to a root in this fixture.
	//
	// Example: Remove it before adding the stale root completion.
	const firstLane = `  - task_id: '1'
    work_ref: 'project-understanding.yaml#/records/work:first'
    outcome: First result
    required_environment: implementation
    implementation_status: IN PROGRESS
    test_status: NEW
    prod_status: NEW
    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}
`
	// completion is the stale report projection for the reopened root.
	//
	// Example: Validation identifies this completion as inconsistent with active work.
	const completion = "root_completions:\n  - {work_ref: 'project-understanding.yaml#/records/work:first', outcome: First result, completed_at: '2026-11-02T15:00:00Z'}\n"
	// rootForecast is the active projection supported by the canonical forecast.
	//
	// Example: Replace the stale completion to reconcile the report.
	const rootForecast = "root_forecasts:\n  - {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n"
	report := strings.Replace(linkedReport, firstLane, "", 1) + completion
	require.NotEqual(t, linkedReport+completion, report)
	ledger := strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: [root]", 1)
	forecasts := strings.Replace(linkedForecasts, "kind: lane", "kind: root", 1)
	dir := writeLinkedSources(t, ledger, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "root completion work:first does not reference completed root work")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "root completion work:first does not reference completed root work")
	fixed := strings.Replace(report, completion, rootForecast, 1)
	require.NotEqual(t, report, fixed)
	require.NoError(t, os.WriteFile(path, []byte(fixed), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
	require.Contains(t, out.String(), "2026-11-02T16:00:00Z")
}
