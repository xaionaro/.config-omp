package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyLaneClosureRetriesAfterStatusConflict checks paired publication.
//
// Example: A manually paused lane cannot receive a completion time by itself.
func TestLegacyLaneClosureRetriesAfterStatusConflict(t *testing.T) {
	t.Parallel()
	// saved contains a legacy closure and its owned status baseline.
	//
	// Example: Retry a saved status and completion-time pair after an upgrade.
	const saved = `{"cursor":1,"applied":{"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/implementation_status":"IN PROGRESS"},"latest":{"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/completed_at":1,"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/implementation_status":1},"pending":{"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/completed_at":{"line":1,"update":{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"completed_at":"2026-11-02T15:25:00Z"}}},"[\"lane\",\"1\",\"project-understanding.yaml#/records/work:first\",\"implementation\"]/implementation_status":{"line":1,"update":{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED"}}}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
	input := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	require.NotEqual(t, linkedReport, input)
	edited := strings.Replace(input, "    implementation_status: IN PROGRESS", "    implementation_status: PAUSED", 1)
	require.NotEqual(t, input, edited)
	initial, err := putSyncState([]byte(edited), saved)
	require.NoError(t, err)
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z"}}}}
	pending, _, err := projectLogStatus(dir, initial, ledger, forecastSource{})
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Equal(t, statusPaused, report.Lanes[0].ImplementationStatus)
	require.Empty(t, report.Lanes[0].CompletedAt)
	require.Len(t, report.StatusSync.Pending, 2)
	require.Equal(t, statusInProgress, report.Lanes[1].ImplementationStatus)
	restored := strings.Replace(string(pending), "    implementation_status: PAUSED", "    implementation_status: IN PROGRESS", 1)
	require.NotEqual(t, string(pending), restored)
	result, _, err := projectLogStatus(dir, []byte(restored), ledger, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Equal(t, "2026-11-02T15:25:00Z", report.Lanes[0].CompletedAt)
	require.Equal(t, statusInProgress, report.Lanes[1].ImplementationStatus)
	require.Empty(t, report.StatusSync.Pending)
}
