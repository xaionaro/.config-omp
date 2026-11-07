package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyCancellationWaitsForOwnerEvidenceResolution checks atomic retry.
//
// Example: An edited reason prevents cancellation until the owned value is restored.
func TestLegacyCancellationWaitsForOwnerEvidenceResolution(t *testing.T) {
	// saved is a historical field-wise cancellation and its evidence baseline.
	//
	// Example: Resume a cancellation that was interrupted before publication.
	const saved = `{"cursor":1,"applied":{"[\"task\",\"1\",\"\",\"\"]/evidence":"original reason"},"latest":{"[\"task\",\"1\",\"\",\"\"]/evidence":1,"[\"task\",\"1\",\"\",\"\"]/lifecycle":1,"[\"task\",\"1\",\"\",\"\"]/status":1},"pending":{"[\"task\",\"1\",\"\",\"\"]/evidence":{"line":1,"update":{"target":"task","id":"1","fields":{"evidence":"owner withdrew"}}},"[\"task\",\"1\",\"\",\"\"]/lifecycle":{"line":1,"update":{"target":"task","id":"1","fields":{"lifecycle":"cancelled"}}},"[\"task\",\"1\",\"\",\"\"]/status":{"line":1,"update":{"target":"task","id":"1","fields":{"status":"unknown"}}}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
	input := strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", "status: IN PROGRESS, evidence: 'manual reason', work_refs:", 1)
	require.NotEqual(t, linkedReport, input)
	initial, err := putSyncState([]byte(input), saved)
	require.NoError(t, err)
	pending, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Tasks[0].Status)
	require.Equal(t, "manual reason", report.Tasks[0].Evidence)
	require.Contains(t, report.TopPriorities, taskID("1"))
	require.Len(t, report.StatusSync.Pending, 3)
	restored := strings.Replace(string(pending), "evidence: 'manual reason'", "evidence: 'original reason'", 1)
	require.NotEqual(t, string(pending), restored)
	result, _, err := projectLogStatus(dir, []byte(restored), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, taskLifecycleCancelled, report.Tasks[0].Lifecycle)
	require.Equal(t, statusUnknown, report.Tasks[0].Status)
	require.Equal(t, "owner withdrew", report.Tasks[0].Evidence)
	require.NotContains(t, report.TopPriorities, taskID("1"))
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
	require.Empty(t, report.StatusSync.Pending)
}
