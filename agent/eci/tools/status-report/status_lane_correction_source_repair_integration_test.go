package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneCorrectionWaitsForCanonicalCompletionOnDisk checks persisted causality.
//
// Example: A corrected closure stays pending until the source reaches its corrected time.
func TestLaneCorrectionWaitsForCanonicalCompletionOnDisk(t *testing.T) {
	t.Parallel()
	// first is an early completion reported before canonical work finishes.
	//
	// Example: The historical time must not be published from an active source.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	// correction records the verified completion time.
	//
	// Example: The correction names the first event while both remain pending.
	const correction = `{"event":"correction","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1,"evidence":"corrected","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first+correction), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
	require.NotEmpty(t, report.StatusSync.Groups)
	completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:45:00Z', next_action: Finish", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, completed)
	require.NotEqual(t, linkedForecasts, inactive)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(completed), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(inactive), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Equal(t, "2026-11-02T15:45:00Z", report.Lanes[0].CompletedAt)
	require.Nil(t, report.Lanes[0].Forecast)
	require.Empty(t, report.StatusSync.Groups)
	require.Equal(t, 2, report.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "correction"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
