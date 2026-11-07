package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneClosureRetriesAgainstChangedCompletionTime checks source snapshot integrity.
//
// Example: A changed canonical completion time blocks stale closure until corrected.
func TestLaneClosureRetriesAgainstChangedCompletionTime(t *testing.T) {
	// first requests a closure at the initially observed source time.
	//
	// Example: A concurrent source correction makes that event stale.
	const first = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	// correction names that closure and supplies the current canonical time.
	//
	// Example: A later event authorizes the corrected completion.
	const correction = `{"event":"correction","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1,"evidence":"corrected","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	completed := strings.Replace(linkedLedger, "owner: one, state: active, next_action: Finish", "owner: one, state: completed, completed_at: '2026-11-02T15:25:00Z', next_action: Finish", 1)
	corrected := strings.Replace(completed, "completed_at: '2026-11-02T15:25:00Z'", "completed_at: '2026-11-02T15:45:00Z'", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, completed)
	require.NotEqual(t, completed, corrected)
	require.NotEqual(t, linkedForecasts, inactive)
	dir := writeLinkedSources(t, completed, inactive)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	ledgerPath := filepath.Join(dir, "project-understanding.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	observer := testRefreshObserver{Action:
	// revise changes canonical completion after the original snapshot is read.
	//
	// Example: A producer corrects the source while refresh prepares publication.
	func() { require.NoError(t, os.WriteFile(ledgerPath, []byte(corrected), 0600)) }}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "lifecycle_change", observer), "retry needed")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(unchanged))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
	require.NotEmpty(t, report.StatusSync.Groups)
	require.NoError(t, os.WriteFile(logPath, []byte(first+correction), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Equal(t, "2026-11-02T15:45:00Z", report.Lanes[0].CompletedAt)
	require.Empty(t, report.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "correction"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
