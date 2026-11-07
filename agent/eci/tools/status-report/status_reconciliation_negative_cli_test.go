package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateDetectsMissingCompletedLane checks the public validation path.
//
// Example: Completed source work needs its matching lane completion in the report.
func TestValidateDetectsMissingCompletedLane(t *testing.T) {
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish", "state: completed, completed_at: '2026-11-02T15:00:00Z'", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	dir := writeLinkedSources(t, ledger, forecasts)
	first := strings.Index(linkedReport, "  - task_id: '1'\n")
	second := strings.Index(linkedReport, "  - task_id: '2'\n")
	require.GreaterOrEqual(t, first, 0)
	require.Greater(t, second, first)
	missing := linkedReport[:first] + linkedReport[second:]
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(missing), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 1, testRun([]string{"validate", path}, strings.NewReader(""), &out, &errOut), errOut.String())
	require.Contains(t, out.String(), "declared lane work:first missing from report")
	require.Contains(t, out.String(), "completed forecasted work work:first completion projection unverified")
	completedLane := `  - task_id: '1'
    work_ref: 'project-understanding.yaml#/records/work:first'
    outcome: First result
    required_environment: implementation
    implementation_status: CLOSED
    test_status: NEW
    prod_status: NEW
    completed_at: '2026-11-02T15:00:00Z'
`
	require.NoError(t, os.WriteFile(path, []byte(linkedReport[:first]+completedLane+linkedReport[second:]), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, testRun([]string{"validate", path}, strings.NewReader(""), &out, &errOut), out.String(), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
}

// TestValidateDetectsDuplicateOutcomeLinks checks canonical work links.
//
// Example: A repeated outcome is rejected until the source link is deduplicated.
func TestValidateDetectsDuplicateOutcomeLinks(t *testing.T) {
	duplicate := strings.Replace(linkedLedger, "outcomes: ['outcome:first']", "outcomes: ['outcome:first', 'outcome:first']", 1)
	require.NotEqual(t, linkedLedger, duplicate)
	dir := writeLinkedSources(t, duplicate, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 1, testRun([]string{"validate", path}, strings.NewReader(""), &out, &errOut), errOut.String())
	require.Contains(t, out.String(), "work work:first has duplicate outcome link outcome:first")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, testRun([]string{"validate", path}, strings.NewReader(""), &out, &errOut), out.String(), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
}
