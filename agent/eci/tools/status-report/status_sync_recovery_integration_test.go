package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyTaskCancellationRecovery retries a persisted field-wise cancellation.
//
// Example: A cancelled task leaves its sibling open and exits the priority list.
func TestLegacyTaskCancellationRecovery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
	// saved is the historical wire format retained across upgrades.
	//
	// Example: Resume an update queued before grouped replay.
	const saved = `{"cursor":1,"applied":{},"latest":{"[\"task\",\"1\",\"\",\"\"]/evidence":1,"[\"task\",\"1\",\"\",\"\"]/lifecycle":1,"[\"task\",\"1\",\"\",\"\"]/status":1},"pending":{"[\"task\",\"1\",\"\",\"\"]/evidence":{"line":1,"update":{"target":"task","id":"1","fields":{"evidence":"owner withdrew"}}},"[\"task\",\"1\",\"\",\"\"]/lifecycle":{"line":1,"update":{"target":"task","id":"1","fields":{"lifecycle":"cancelled"}}},"[\"task\",\"1\",\"\",\"\"]/status":{"line":1,"update":{"target":"task","id":"1","fields":{"status":"unknown"}}}}}`
	initial, err := putSyncState([]byte(linkedReport), saved)
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, taskLifecycleCancelled, report.Tasks[0].Lifecycle)
	require.Equal(t, statusUnknown, report.Tasks[0].Status)
	require.Equal(t, "owner withdrew", report.Tasks[0].Evidence)
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
	require.NotContains(t, report.TopPriorities, taskID("1"))
	require.Empty(t, report.StatusSync.Pending)
}

// TestLegacyTaskClosureRecoveryRequiresEvidence retries a persisted acceptance.
//
// Example: Closure waits for matching evidence and preserves a manual replacement.
func TestLegacyTaskClosureRecoveryRequiresEvidence(t *testing.T) {
	t.Parallel()
	for _, evidence := range []string{"accepted", "manual replacement"} {
		t.Run(evidence,
			// Check one persisted evidence value.
			//
			// Example: An owner edit keeps closure pending.
			func(t *testing.T) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
				// saved is the historical wire format retained across upgrades.
				//
				// Example: Resume an update queued before grouped replay.
				const saved = `{"cursor":1,"applied":{},"latest":{"[\"task\",\"1\",\"\",\"\"]/status":1},"pending":{"[\"task\",\"1\",\"\",\"\"]/status":{"line":1,"update":{"target":"task","id":"1","fields":{"status":"CLOSED"}},"acceptance":"accepted"}}}`
				input := strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", "status: IN PROGRESS, evidence: '"+evidence+"', work_refs:", 1)
				require.NotEqual(t, linkedReport, input)
				initial, err := putSyncState([]byte(input), saved)
				require.NoError(t, err)
				result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, evidence, report.Tasks[0].Evidence)
				require.Equal(t, statusInProgress, report.Tasks[1].Status)
				if evidence == "accepted" {
					require.Equal(t, statusClosed, report.Tasks[0].Status)
					require.Empty(t, report.StatusSync.Pending)
				} else {
					require.Equal(t, statusInProgress, report.Tasks[0].Status)
					require.NotEmpty(t, report.StatusSync.Pending)
				}
			})
	}
}

// TestLegacyLaneCompletionRecoveryRequiresCanonicalCompletion retries a saved closure.
//
// Example: A completed source closes the named lane and leaves another lane open.
func TestLegacyLaneCompletionRecoveryRequiresCanonicalCompletion(t *testing.T) {
	t.Parallel()
	for _, completed := range []bool{true, false} {
		t.Run(fmt.Sprint(completed),
			// Check one canonical work state.
			//
			// Example: Active work keeps closure pending.
			func(t *testing.T) {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
				// saved is the historical wire format retained across upgrades.
				//
				// Example: Resume an update queued before grouped replay.
				const saved = `{"cursor":1,"applied":{},"latest":{"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/completed_at":1,"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/implementation_status":1},"pending":{"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/completed_at":{"line":1,"update":{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"completed_at":"2026-11-02T15:25:00Z"}}},"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/implementation_status":{"line":1,"update":{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED"}}}}}`
				input := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
				require.NotEqual(t, linkedReport, input)
				initial, err := putSyncState([]byte(input), saved)
				require.NoError(t, err)
				source := workData{State: workStateActive}
				if completed {
					source = workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z"}
				}
				ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: source}}}
				result, _, err := projectLogStatus(dir, initial, ledger, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, statusInProgress, report.Lanes[1].ImplementationStatus)
				if completed {
					require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
					require.Equal(t, "2026-11-02T15:25:00Z", report.Lanes[0].CompletedAt)
					require.Empty(t, report.StatusSync.Pending)
				} else {
					require.Equal(t, statusInProgress, report.Lanes[0].ImplementationStatus)
					require.Empty(t, report.Lanes[0].CompletedAt)
					require.Len(t, report.StatusSync.Pending, 2)
				}
			})
	}
}

// TestLegacyPendingTimelineRecovery publishes a saved complete snapshot.
//
// Example: A verified-empty timeline keeps its supplied anchor during migration.
func TestLegacyPendingTimelineRecovery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
	// saved is the historical wire format retained across upgrades.
	//
	// Example: Resume an update queued before grouped replay.
	const saved = `{"cursor":1,"applied":null,"latest":null,"pending":null,"pending_timeline":{"line":1,"snapshot":"{\"as_of\":\"2026-11-02T15:24:00Z\",\"state\":\"verified_empty\",\"entries\":[]}"}}`
	initial, err := putSyncState([]byte(linkedReport), saved)
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, timelineVerifiedEmpty, report.TimelineState)
	require.Equal(t, "2026-11-02T15:24:00Z", report.TimelineAsOf)
	require.Empty(t, report.Timeline)
	require.Nil(t, report.StatusSync.PendingTimeline)
	require.Empty(t, report.StatusSync.PendingTimelines)
}
