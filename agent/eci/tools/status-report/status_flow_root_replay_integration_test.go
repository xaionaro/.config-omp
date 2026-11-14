package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestJSONRootReplaysUpdatesAndAddsMissingSections checks flow-root synchronization.
//
// Example: A task closure, review, and timeline coexist with existing report fields.
func TestJSONRootReplaysUpdatesAndAddsMissingSections(t *testing.T) {
	t.Parallel()
	var source map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &source))
	input, err := json.Marshal(source)
	require.NoError(t, err)
	// event changes a task and adds a review and complete empty timeline.
	//
	// Example: Publish all supplied data in the same JSON-root report.
	const event = `{"event":"worker_progress","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}},{"target":"progress","id":"work:one","fields":{"text":"verified progress"}}],"additions":[{"target":"review","record":{"task_id":"1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}}],"timeline":{"as_of":"2026-11-02T15:24:00Z","state":"verified_empty","entries":[]}}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	result, _, err := projectLogStatus(dir, input, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.True(t, json.Valid(result), string(result))
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Tasks[0].Status)
	require.Equal(t, "accepted", report.Tasks[0].Evidence)
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
	require.Contains(t, report.Progress, "Implementation started")
	require.Contains(t, report.Progress, "verified progress")
	require.Len(t, report.Reviews, 1)
	require.Equal(t, timelineVerifiedEmpty, report.TimelineState)
	require.Equal(t, "2026-11-02T15:24:00Z", report.TimelineAsOf)
	require.Equal(t, 1, report.StatusSync.Cursor)
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}

// TestFlowRootRefreshPreservesHistoricalTimelineAndComment checks the real refresh path.
//
// Example: Advancing a report anchors an existing verified-empty timeline.
func TestFlowRootRefreshPreservesHistoricalTimelineAndComment(t *testing.T) {
	t.Parallel()
	var source map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &source))
	source["timeline_state"] = "verified_empty"
	source["timeline"] = []any{}
	input, err := json.Marshal(source)
	require.NoError(t, err)
	input = append(input, []byte(" # owner note\n")...)
	// event advances the report timestamp while adding independent progress.
	//
	// Example: Keep the old empty timeline's original time coverage.
	const event = `{"event":"worker_progress","timestamp":"2026-11-02T15:34:00Z","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"later progress"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, input, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T15:34:00Z", report.Timestamp)
	require.Equal(t, "2026-11-02T15:24:00Z", report.TimelineAsOf)
	require.Equal(t, timelineVerifiedEmpty, report.TimelineState)
	require.Contains(t, report.Progress, "later progress")
	require.Contains(t, string(result), "# owner note")
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
}

// TestJSONRootTimelineReplacementPreservesOtherSections checks existing flow fields.
//
// Example: Replace an older empty timeline without dropping neighboring fields.
func TestJSONRootTimelineReplacementPreservesOtherSections(t *testing.T) {
	t.Parallel()
	var source map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &source))
	source["timeline_as_of"] = "2026-11-02T15:14:00Z"
	source["timeline_state"] = "verified_empty"
	source["timeline"] = []any{}
	input, err := json.Marshal(source)
	require.NoError(t, err)
	// event replaces the full timeline with a later verified-empty snapshot.
	//
	// Example: Publish an updated anchor in an existing JSON report.
	const event = `{"event":"checkpoint","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"timeline":{"as_of":"2026-11-02T15:24:00Z","state":"verified_empty","entries":[]}}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	result, _, err := projectLogStatus(dir, input, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.True(t, json.Valid(result), string(result))
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T15:24:00Z", report.TimelineAsOf)
	require.Equal(t, timelineVerifiedEmpty, report.TimelineState)
	require.Len(t, report.Tasks, 2)
	require.Contains(t, report.Decisions, "Use YAML")
	require.Equal(t, 1, report.StatusSync.Cursor)
}

// TestJSONRootAcceptsPopulatedTimelineSnapshot checks JSON-compatible timeline rows.
//
// Example: Eight supplied intervals replace an older empty timeline without changing tasks.
func TestJSONRootAcceptsPopulatedTimelineSnapshot(t *testing.T) {
	t.Parallel()
	var source map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &source))
	source["timeline_as_of"] = "2026-11-02T15:14:00Z"
	source["timeline_state"] = "verified_empty"
	source["timeline"] = []any{}
	input, err := json.Marshal(source)
	require.NoError(t, err)
	anchor, err := time.Parse(time.RFC3339, "2026-11-02T15:24:00Z")
	require.NoError(t, err)
	entries := make([]timelineEntry, 0, 8)
	for i := 0; i < 8; i++ {
		start := anchor.Add(-4*time.Hour + time.Duration(i)*30*time.Minute)
		entries = append(entries, timelineEntry{Job: "root", ParentChain: "root", Start: start.Format(time.RFC3339), End: start.Add(30 * time.Minute).Format(time.RFC3339), Fast: "work", Normal: "work"})
	}
	encoded, err := json.Marshal(syncTimelineSnapshot{AsOf: anchor.Format(time.RFC3339), State: timelinePresent, Entries: entries})
	require.NoError(t, err)
	event := `{"event":"checkpoint","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"timeline":` + string(encoded) + `}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	result, _, err := projectLogStatus(dir, input, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.True(t, json.Valid(result), string(result))
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, timelinePresent, report.TimelineState)
	require.Len(t, report.Timeline, 8)
	require.Equal(t, "root", report.Timeline[0].Job)
	require.Len(t, report.Tasks, 2)
}
