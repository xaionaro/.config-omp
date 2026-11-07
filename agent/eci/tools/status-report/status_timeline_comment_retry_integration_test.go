package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTimelineSnapshotWaitsForOwnerCommentResolution checks persisted replacement.
//
// Example: A commented timeline stays intact until the owner removes its annotation.
func TestTimelineSnapshotWaitsForOwnerCommentResolution(t *testing.T) {
	// event contains a complete four-hour timeline with source evidence.
	//
	// Example: A saved snapshot can be retried without appending a second event.
	const event = `{"event":"checkpoint","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"timeline":{"as_of":"2026-11-02T15:24:00Z","state":"present","entries":[{"job":"root","parent_chain":"root","start":"2026-11-02T11:24:00Z","end":"2026-11-02T11:54:00Z","fast":"new","normal":"work","evidence":"project-understanding.yaml#/records/work:first"},{"job":"root","parent_chain":"root","start":"2026-11-02T11:54:00Z","end":"2026-11-02T12:24:00Z","fast":"new","normal":"work"},{"job":"root","parent_chain":"root","start":"2026-11-02T12:24:00Z","end":"2026-11-02T12:54:00Z","fast":"new","normal":"work"},{"job":"root","parent_chain":"root","start":"2026-11-02T12:54:00Z","end":"2026-11-02T13:24:00Z","fast":"new","normal":"work"},{"job":"root","parent_chain":"root","start":"2026-11-02T13:24:00Z","end":"2026-11-02T13:54:00Z","fast":"new","normal":"work"},{"job":"root","parent_chain":"root","start":"2026-11-02T13:54:00Z","end":"2026-11-02T14:24:00Z","fast":"new","normal":"work"},{"job":"root","parent_chain":"root","start":"2026-11-02T14:24:00Z","end":"2026-11-02T14:54:00Z","fast":"new","normal":"work"},{"job":"root","parent_chain":"root","start":"2026-11-02T14:54:00Z","end":"2026-11-02T15:24:00Z","fast":"new","normal":"work"}]}}}}` + "\n"
	report := linkedReport + `timeline_as_of: '2026-11-02T15:24:00Z'
timeline_state: present
timeline:
  - {job: old, parent_chain: old, start: '2026-11-02T14:54:00Z', end: '2026-11-02T15:24:00Z', fast: old, normal: old} # owner note
`
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "checkpoint"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, parsed.Timeline, 1)
	require.Equal(t, "old", parsed.Timeline[0].Job)
	require.Len(t, parsed.StatusSync.PendingTimelines, 1)
	require.Contains(t, string(pending), "# owner note")
	resolved := strings.Replace(string(pending), " # owner note", "", 1)
	require.NotEqual(t, string(pending), resolved)
	require.NoError(t, os.WriteFile(path, []byte(resolved), 0600))
	require.NoError(t, refreshTestStatus(dir, "checkpoint"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Len(t, parsed.Timeline, 8)
	require.Equal(t, "root", parsed.Timeline[0].Job)
	require.Equal(t, "project-understanding.yaml#/records/work:first", parsed.Timeline[0].Evidence)
	require.Empty(t, parsed.StatusSync.PendingTimelines)
	require.Equal(t, []string{"Implementation started"}, parsed.Progress)
	require.Equal(t, 1, parsed.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "checkpoint"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
