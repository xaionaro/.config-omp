package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTimelineSnapshotRetriesWhenReportTimestampAdvances checks temporal consistency.
//
// Example: A future snapshot waits until a later event advances the report.
func TestTimelineSnapshotRetriesWhenReportTimestampAdvances(t *testing.T) {
	t.Parallel()
	// first supplies a verified-empty snapshot newer than the report.
	//
	// Example: Keep the snapshot pending until its anchor is no longer in the future.
	const first = `{"event":"checkpoint","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"timeline":{"as_of":"2026-11-02T15:35:00Z","state":"verified_empty","entries":[]}}}}` + "\n"
	// second reports independent progress at the snapshot's anchor.
	//
	// Example: Advancing the report permits the earlier pending snapshot to publish.
	const second = `{"event":"worker_progress","timestamp":"2026-11-02T15:35:00Z","details":{"status_update":{"updates":[{"target":"progress","id":"work:one","fields":{"text":"progress advanced"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	pending, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.NotEqual(t, timelineVerifiedEmpty, report.TimelineState)
	require.Len(t, report.StatusSync.PendingTimelines, 1)
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, pending, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, timelineVerifiedEmpty, report.TimelineState)
	require.Equal(t, "2026-11-02T15:35:00Z", report.TimelineAsOf)
	require.Empty(t, report.StatusSync.PendingTimelines)
	require.Contains(t, report.Progress, "Implementation started")
	require.Contains(t, report.Progress, "progress advanced")
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
