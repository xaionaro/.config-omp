package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSyncTimelineSnapshot checks that a complete source snapshot replaces its anchor and rows together.
//
// Example: Replace a historical four-hour timeline with a new complete snapshot.
func TestSyncTimelineSnapshot(t *testing.T) {
	t.Parallel()
	input := []byte(linkedReport + "timeline_as_of: '2026-11-02T15:24:00Z'\ntimeline_state: present\ntimeline:\n  - {job: old, parent_chain: old, start: '2026-11-02T14:54:00Z', end: '2026-11-02T15:24:00Z', fast: old, normal: old}\n")
	snapshot := syncTimelineSnapshot{AsOf: "2026-11-02T15:24:00Z", State: timelinePresent}
	anchor, err := time.Parse(time.RFC3339, snapshot.AsOf)
	require.NoError(t, err)
	for i := 0; i < 8; i++ {
		start := anchor.Add(-4*time.Hour + time.Duration(i)*30*time.Minute)
		snapshot.Entries = append(snapshot.Entries, timelineEntry{Job: "root", ParentChain: "root", Start: start.Format(time.RFC3339), End: start.Add(30 * time.Minute).Format(time.RFC3339), Fast: fmt.Sprint(i), Normal: "work"})
	}
	result, ok := applySyncTimeline(input, snapshot)
	require.True(t, ok)
	report, err := parse(result)
	require.NoError(t, err)
	require.Len(t, report.Timeline, 8)
	require.Equal(t, "root", report.Timeline[0].Job)
	require.NotContains(t, string(result), "job: old")
	require.Equal(t, snapshot.AsOf, report.TimelineAsOf)
	snapshot.Entries[0].Evidence = "project-understanding.yaml#/records/work:first"
	withReference, ok := applySyncTimeline(result, snapshot)
	require.True(t, ok)
	snapshot.Entries[0].Normal = "new work"
	refreshed, ok := applySyncTimeline(withReference, snapshot)
	require.True(t, ok)
	refreshedReport, err := parse(refreshed)
	require.NoError(t, err)
	require.Equal(t, "new work", refreshedReport.Timeline[0].Normal)
	require.Equal(t, snapshot.Entries[0].Evidence, refreshedReport.Timeline[0].Evidence)

	incomplete := snapshot
	incomplete.Entries = incomplete.Entries[:7]
	rejected, ok := applySyncTimeline(input, incomplete)
	require.False(t, ok)
	require.Equal(t, string(input), string(rejected))
	inconsistent := snapshot
	inconsistent.Entries = append([]timelineEntry(nil), snapshot.Entries...)
	inconsistent.Entries[0].ParentChain = "other"
	rejected, ok = applySyncTimeline(input, inconsistent)
	require.False(t, ok)
	require.True(t, strings.EqualFold(string(input), string(rejected)))
}

// TestSyncTimelineRejectsUnsupportedSnapshot preserves prior report bytes when source data is invalid.
//
// Example: Reject an incomplete snapshot or a commented timeline layout.
func TestSyncTimelineRejectsUnsupportedSnapshot(t *testing.T) {
	t.Parallel()
	input := []byte(linkedReport + "timeline_state: verified_empty\ntimeline: [] # owner comment\n")
	snapshot := syncTimelineSnapshot{AsOf: "2026-11-02T15:24:00Z", State: timelineVerifiedEmpty}
	got, ok := applySyncTimeline(input, snapshot)
	require.False(t, ok)
	require.Equal(t, input, got)
	input = []byte(linkedReport + "timeline_state: verified_empty\ntimeline: []\n")
	got, ok = applySyncTimeline(input, snapshot)
	require.True(t, ok)
	r, err := parse(got)
	require.NoError(t, err)
	require.Equal(t, timelineVerifiedEmpty, r.TimelineState)
	require.Empty(t, r.Timeline)
	require.Equal(t, snapshot.AsOf, r.TimelineAsOf)
	snapshot.AsOf = "2026-11-02T16:24:00Z"
	got, ok = applySyncTimeline(input, snapshot)
	require.False(t, ok)
	require.Equal(t, input, got)
}

// TestSyncTimelineJSON decodes source identity and path fields without inventing values.
//
// Example: A parent_chain key survives decoding into the snapshot.
func TestSyncTimelineJSON(t *testing.T) {
	t.Parallel()
	var snapshot syncTimelineSnapshot
	require.NoError(t, json.Unmarshal([]byte(`{"as_of":"2026-11-02T15:24:00Z","state":"present","entries":[{"job":"root","parent_chain":"root","fast":"work","normal":"unknown"}]}`), &snapshot))
	require.Equal(t, "root", snapshot.Entries[0].ParentChain)
	require.Equal(t, "unknown", snapshot.Entries[0].Normal)
}
