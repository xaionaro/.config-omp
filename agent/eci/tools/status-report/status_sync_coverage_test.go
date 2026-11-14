package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSyncWireSelectorsRoundTrip checks supported and unsupported selectors.
//
// Example: Production remains distinct from implementation through metadata.
func TestSyncWireSelectorsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"task", "lane", "report", "progress", "decisions", "blockers_risks", "verification", "review", "priority_estimate"} {
		var target syncTarget
		require.NoError(t, json.Unmarshal([]byte(`"`+name+`"`), &target))
		require.NotEqual(t, syncTargetUnknown, target)
		require.Equal(t, name, target.String())
		var fromYAML syncTarget
		require.NoError(t, yaml.Unmarshal([]byte(name), &fromYAML))
		require.Equal(t, target, fromYAML)
	}
	for _, name := range []string{"implementation", "test", "prod"} {
		var environment syncEnvironment
		require.NoError(t, json.Unmarshal([]byte(`"`+name+`"`), &environment))
		require.NotEqual(t, syncEnvironmentUnknown, environment)
		require.Equal(t, name, environment.String())
		var fromYAML syncEnvironment
		require.NoError(t, yaml.Unmarshal([]byte(name), &fromYAML))
		require.Equal(t, environment, fromYAML)
	}
	var target syncTarget
	require.NoError(t, json.Unmarshal([]byte(`"unsupported"`), &target))
	require.Equal(t, syncTargetUnknown, target)
	require.Error(t, json.Unmarshal([]byte(`[]`), &target))
	require.Error(t, yaml.Unmarshal([]byte(`[]`), &target))
	var environment syncEnvironment
	require.NoError(t, json.Unmarshal([]byte(`"unsupported"`), &environment))
	require.Equal(t, syncEnvironmentUnknown, environment)
	require.Error(t, json.Unmarshal([]byte(`[]`), &environment))
	require.Error(t, yaml.Unmarshal([]byte(`[]`), &environment))
}

// TestSyncOwnerEditsAndNewerUpdates checks conflict and supersession semantics.
//
// Example: A later update cannot overwrite an owner's changed field.
func TestSyncOwnerEditsAndNewerUpdates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"first"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	report, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report = []byte(strings.Replace(string(report), `"owner": "first"`, `"owner": "manual"`, 1))
	require.Contains(t, string(report), `"owner": "manual"`)
	newer := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"newer"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+newer), 0600))
	result, _, err := projectLogStatus(dir, report, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "manual", parsed.Tasks[0].Owner)
	require.Contains(t, string(result), "status updates remain pending")
}

// TestSyncPendingRetractionDoesNotInsertOldText checks unapplied event ordering.
//
// Example: A retracted progress event never appears after a delayed refresh.
func TestSyncPendingRetractionDoesNotInsertOldText(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"work","fields":{"text":"old"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work","corrects":1,"retract":true}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started"}, parsed.Progress)
}

// TestSyncCancellationKeepsBlockPriorities checks a common YAML layout.
//
// Example: Removing one ID from a block list preserves its sibling.
func TestSyncCancellationKeepsBlockPriorities(t *testing.T) {
	t.Parallel()
	report := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities:\n  - '1'\n  - '2'", 1)
	updated, ok := removeCancelledPriority([]byte(report), taskID("1"))
	require.True(t, ok)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, []taskID{"2"}, parsed.TopPriorities)
	unchanged, ok := removeCancelledPriority(updated, taskID("missing"))
	require.True(t, ok)
	require.Equal(t, updated, unchanged)
}

// TestSyncValidatesExplicitUpdates checks selectors and evidence boundaries.
//
// Example: Production completion requires a time and acceptance evidence.
func TestSyncValidatesExplicitUpdates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		update syncUpdate
		valid  bool
	}{
		{syncUpdate{}, false},
		{syncUpdate{Target: syncTargetTask, ID: "1", Fields: map[string]string{"status": "CLOSED"}}, false},
		{syncUpdate{Target: syncTargetTask, ID: "1", Fields: map[string]string{"status": "CLOSED", "evidence": "accepted"}}, true},
		{syncUpdate{Target: syncTargetTask, ID: "1", Fields: map[string]string{"status": "bad"}}, false},
		{syncUpdate{Target: syncTargetTask, ID: "1", Fields: map[string]string{"lifecycle": "cancelled", "status": "unknown"}}, false},
		{syncUpdate{Target: syncTargetTask, ID: "1", Fields: map[string]string{"lifecycle": "cancelled", "status": "unknown", "evidence": "reason"}}, true},
		{syncUpdate{Target: syncTargetReport, ID: "other", Fields: map[string]string{"state": "working"}}, false},
		{syncUpdate{Target: syncTargetReport, ID: "current", Fields: map[string]string{"next_focus": "proof"}}, true},
		{syncUpdate{Target: syncTargetLane, ID: "1", Fields: map[string]string{"owner": "executor"}}, false},
		{syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "work", RequiredEnvironment: syncEnvironmentProd, Fields: map[string]string{"prod_status": "CLOSED"}}, false},
		{syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "work", RequiredEnvironment: syncEnvironmentProd, Evidence: "accepted", Fields: map[string]string{"prod_status": "CLOSED", "completed_at": "2026-11-02T15:25:00Z"}}, true},
		{syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "work", RequiredEnvironment: syncEnvironmentTest, Fields: map[string]string{"lifecycle": "cancelled"}}, false},
		{syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "work", RequiredEnvironment: syncEnvironmentTest, Evidence: "cancelled", Fields: map[string]string{"lifecycle": "cancelled"}}, true},
		{syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "work", RequiredEnvironment: syncEnvironmentTest, Fields: map[string]string{"unsupported": "x"}}, false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.valid, validSyncUpdate(tc.update), "%+v", tc.update)
	}
}

// TestSyncLaneTerminalChecksCanonicalFacts checks source authority.
//
// Example: An active forecast prevents a source-completed lane from closing.
func TestSyncLaneTerminalChecksCanonicalFacts(t *testing.T) {
	t.Parallel()
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Evidence: "accepted", Fields: map[string]string{"completed_at": "2026-11-02T15:25:00Z"}}
	work := sourceRecord{Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": work}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "active"}}}
	require.False(t, laneSyncAllowed([]byte(linkedReport), ledger, forecasts, update, "completed_at", "2026-11-02T15:25:00Z"))
	forecasts.Records["first"]["status"] = "inactive"
	require.True(t, laneSyncAllowed([]byte(linkedReport), ledger, forecasts, update, "completed_at", "2026-11-02T15:25:00Z"))
	require.False(t, laneSyncAllowed([]byte(linkedReport), ledger, forecasts, update, "completed_at", "2026-11-02T15:26:00Z"))
	work.Data.State = workStateCancelled
	ledger.Records["work:first"] = work
	require.True(t, laneSyncAllowed([]byte(linkedReport), ledger, forecasts, update, "lifecycle", "cancelled"))
	work.Data.State = workStateActive
	ledger.Records["work:first"] = work
	require.False(t, laneSyncAllowed([]byte(linkedReport), ledger, forecasts, update, "lifecycle", "cancelled"))
	update.WorkRef = "bad"
	require.False(t, laneSyncAllowed([]byte(linkedReport), ledger, forecasts, update, "lifecycle", "cancelled"))
}

// TestSyncTimestampKeepsTimelineAnchor checks temporal preservation.
//
// Example: A new report time leaves a historical timeline anchored to its original time.
func TestSyncTimestampKeepsTimelineAnchor(t *testing.T) {
	t.Parallel()
	report := linkedReport + "timeline_state: present\ntimeline:\n  - {job: job, parent_chain: root, start: '2026-11-02T14:54:00Z', end: '2026-11-02T15:24:00Z', state: working}\n"
	updated, err := advanceSyncTimestamp([]byte(report), "2026-11-02T15:34:00Z")
	require.NoError(t, err)
	require.Contains(t, string(updated), `timeline_as_of: "2026-11-02T15:24:00Z"`)
	require.Contains(t, string(updated), "2026-11-02T14:54:00Z")
	same, err := advanceSyncTimestamp(updated, "2026-11-02T15:30:00Z")
	require.NoError(t, err)
	require.Equal(t, updated, same)
	_, err = advanceSyncTimestamp(updated, "bad")
	require.Error(t, err)
}

// TestSyncRetriesAfterTaskAddition checks pending replay against a new target.
//
// Example: An assignment is applied once its task record exists.
func TestSyncRetriesAfterTaskAddition(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"worker"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	pending, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Contains(t, string(pending), "status updates remain pending")
	addition := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+addition), 0600))
	updated, _, err := projectLogStatus(dir, pending, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Len(t, parsed.Tasks, 3)
	require.Equal(t, "worker", parsed.Tasks[2].Owner)
	require.Empty(t, parsed.StatusSync.Pending)
}

// TestSyncUpdatesDistinctLaneAndReportFields checks explicit selectors.
//
// Example: A lane coordinator changes without modifying a sibling lane.
func TestSyncUpdatesDistinctLaneAndReportFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"owner":"coordinator","next_action":"review"}},{"target":"report","id":"current","fields":{"state":"Reviewing","next_focus":"Check evidence"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	updated, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, "coordinator", parsed.Lanes[0].Owner)
	require.Equal(t, "review", parsed.Lanes[0].NextAction)
	require.Empty(t, parsed.Lanes[1].Owner)
	require.Equal(t, "Reviewing", parsed.State)
	require.Equal(t, "Check evidence", parsed.NextFocus)
}

// TestSyncDefersTerminalLaneWithActiveForecast checks paired completion safety.
//
// Example: Report and source forecast remain open until retirement is recorded.
func TestSyncDefersTerminalLaneWithActiveForecast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "active"}}}
	updated, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, parsed.Lanes[0].ImplementationStatus)
	require.Empty(t, parsed.Lanes[0].CompletedAt)
	require.Len(t, parsed.StatusSync.Groups, 1)
}
