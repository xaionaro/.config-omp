package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncBatchReplayMatchesIncrementalClosure checks causal event grouping.
//
// Example: Later evidence does not prevent an earlier accepted task from closing.
func TestSyncBatchReplayMatchesIncrementalClosure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted proof"}}]}}}` + "\n"
	second := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"evidence":"later proof"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	incremental, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	incremental, _, err = projectLogStatus(dir, incremental, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	batch, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	sequential, err := parse(incremental)
	require.NoError(t, err)
	replayed, err := parse(batch)
	require.NoError(t, err)
	require.Equal(t, statusClosed, sequential.Tasks[0].Status)
	require.Equal(t, sequential.Tasks[0].Status, replayed.Tasks[0].Status)
	require.Equal(t, sequential.Tasks[0].Evidence, replayed.Tasks[0].Evidence)
}

// TestSyncLegacyPendingSurvivesTransactionUpgrade checks existing session compatibility.
//
// Example: A saved field-wise assignment remains retryable after the upgrade.
func TestSyncLegacyPendingSurvivesTransactionUpgrade(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0600))
	update := syncUpdate{Target: syncTargetTask, ID: "1", Fields: map[string]string{"owner": "worker"}}
	key := syncTargetKey(update, "owner")
	state := syncState{Cursor: 1, Applied: map[string]string{}, Latest: map[string]int{key: 1}, Pending: map[string]syncPending{key: {Line: 1, Update: update}}}
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	report, err := putSyncState([]byte(linkedReport), string(encoded))
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, report, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "worker", parsed.Tasks[0].Owner)
	require.Empty(t, parsed.StatusSync.Pending)
}

// TestSyncSameEventTransactionsKeepSuppliedOrder checks target transaction ordering.
//
// Example: A later proof revision follows an earlier accepted closure.
func TestSyncSameEventTransactionsKeepSuppliedOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	entry := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}},{"target":"task","id":"1","fields":{"evidence":"revised"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(entry), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, parsed.Tasks[0].Status)
	require.Equal(t, "revised", parsed.Tasks[0].Evidence)
}

// TestSyncProjectsTypedTaskAndTimeline checks integrated supplied snapshots.
//
// Example: Update effort and a complete empty timeline without inferring facts.
func TestSyncProjectsTypedTaskAndTimeline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	entry := `{"event":"worker_progress","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"effort":{"min_minutes":1,"max_minutes":2,"kind":"work","basis":"measured"}}}],"timeline":{"as_of":"2026-11-02T15:24:00Z","state":"verified_empty","entries":[]}}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(entry), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.EqualValues(t, 2, *parsed.Tasks[0].Effort.MaxMinutes)
	require.Equal(t, timelineVerifiedEmpty, parsed.TimelineState)
	require.Equal(t, "2026-11-02T15:24:00Z", parsed.TimelineAsOf)
}

// TestSyncCorrectionUsesSameLineOrdinal checks exact event version identity.
//
// Example: A correction names the second update from one log entry.
func TestSyncCorrectionUsesSameLineOrdinal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"work","fields":{"text":"first"}},{"target":"progress","id":"work","fields":{"text":"second"}}]}}}` + "\n"
	second := `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work","corrects":1,"corrects_ordinal":1,"fields":{"text":"corrected"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, parsed.Progress, "corrected")
}

// TestSyncMixedTypedAndScalarFieldsStayAtomic checks a complete target update.
//
// Example: Task acceptance and its revised effort apply together.
func TestSyncMixedTypedAndScalarFieldsStayAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	entry := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"},"values":{"effort":{"min_minutes":1,"max_minutes":2,"kind":"work","basis":"measured"}}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(entry), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, parsed.Tasks[0].Status)
	require.EqualValues(t, 2, *parsed.Tasks[0].Effort.MaxMinutes)
}

// TestSyncLaneClearThenSetKeepsOwnership checks explicit removal and later update.
//
// Example: Resolve a blocker, then record a new blocker on the same lane.
func TestSyncLaneClearThenSetKeepsOwnership(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	report := strings.Replace(linkedReport, "    outcome: First result\n", "    outcome: First result\n    blocker: Waiting\n", 1)
	first := `{"event":"blocker_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","clear":["blocker"]}]}}}` + "\n"
	second := `{"event":"blocker_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"blocker":"new blocker"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(report), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "new blocker", parsed.Lanes[0].Blocker)
}

// TestSyncCorrectionWaitsForItsPendingTarget checks causal retry ordering.
//
// Example: An assignment correction waits until the missing task is added.
func TestSyncCorrectionWaitsForItsPendingTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"first"}}]}}}` + "\n"
	second := `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"3","corrects":1,"fields":{"owner":"second"}}]}}}` + "\n"
	third := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second+third), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Len(t, parsed.Tasks, 3)
	require.Equal(t, "second", parsed.Tasks[2].Owner)
	require.Empty(t, parsed.StatusSync.Groups)
}

// TestSyncNewTimelineSupersedesUnappliedSnapshot checks snapshot replay order.
//
// Example: A later complete snapshot replaces an earlier incomplete snapshot.
func TestSyncNewTimelineSupersedesUnappliedSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"checkpoint","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"timeline":{"as_of":"2026-11-02T15:24:00Z","state":"present","entries":[]}}}}` + "\n"
	second := `{"event":"checkpoint","timestamp":"2026-11-02T15:24:00Z","details":{"status_update":{"timeline":{"as_of":"2026-11-02T15:24:00Z","state":"verified_empty","entries":[]}}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, timelineVerifiedEmpty, parsed.TimelineState)
	require.Empty(t, parsed.StatusSync.PendingTimelines)
}

// TestSyncTaskCancellationCanBeReversed checks scalar-to-structured ownership.
//
// Example: Explicitly reopen a task that the projection previously cancelled.
func TestSyncTaskCancellationCanBeReversed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"lifecycle":"cancelled","status":"unknown","evidence":"cancelled"}}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"IN PROGRESS"},"clear":["lifecycle"]}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, taskLifecycleNone, parsed.Tasks[0].Lifecycle)
	require.Equal(t, statusInProgress, parsed.Tasks[0].Status)
}

// TestSyncRetractionUsesOrdinalOfLatestLine checks version ordering.
//
// Example: A newer evidence update supersedes an older text ordinal.
func TestSyncRetractionUsesOrdinalOfLatestLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"state":"Updated"}},{"target":"progress","id":"work","fields":{"text":"managed"}}]}}}` + "\n"
	second := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"progress","id":"work","fields":{"evidence":"latest proof"}}]}}}` + "\n"
	third := `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work","corrects":2,"retract":true}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second+third), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started"}, parsed.Progress)
}

// TestSyncReprioritizationAddsItsEstimateAtomically checks existing tasks.
//
// Example: A task moves to a new priority with its supplied estimate.
func TestSyncReprioritizationAddsItsEstimateAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	entry := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":"low"}}],"additions":[{"target":"priority_estimate","record":{"priority":"low","min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(entry), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("low"), parsed.Tasks[0].Priority)
	require.Len(t, parsed.PriorityEstimates, 2)
	require.Empty(t, parsed.StatusSync.Groups)
	require.Empty(t, parsed.StatusSync.PendingAdditions)
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}

// TestSyncReopenRecordsImplicitCompletionRemoval checks provenance watermarks.
//
// Example: An old completion correction cannot target the pre-reopen version.
func TestSyncReopenRecordsImplicitCompletionRemoval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	report := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	first := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	second := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	third := `{"event":"correction","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1,"evidence":"old proof","fields":{"completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second+third), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z"}}}}
	result, _, err := projectLogStatus(dir, []byte(report), ledger, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	selector := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	key := syncTargetKey(selector, "completed_at")
	require.Equal(t, 2, parsed.StatusSync.Latest[key])
	require.Equal(t, syncAbsentValue, parsed.StatusSync.Applied[key])
	require.Empty(t, parsed.Lanes[0].CompletedAt)
	require.Contains(t, strings.Join(parsed.Blockers, " "), "invalid status_update requires manual review")
}

// TestSyncLastPriorityRetirementRequiresExactPrior checks explicit removal.
//
// Example: Replace the last high-priority estimate while moving its task to low.
func TestSyncLastPriorityRetirementRequiresExactPrior(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	report := `timestamp: '2026-11-02T15:24:00Z'
state: Working
progress: []
decisions: []
blockers_risks: []
verification: []
next_focus: Finish
scope_complete: true
tasks:
  - {id: '1', title: Work, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS}
top_priorities: ['1']
priority_estimates:
  - {priority: high, min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
completion_estimate: {min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
`
	prior := `{"priority":"high","min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}`
	entry := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":"low"}}],"additions":[{"target":"priority_estimate","retire_priority":"high","expected_prior":` + prior + `,"record":{"priority":"low","min_minutes":1,"max_minutes":2,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(entry), 0600))
	result, _, err := projectLogStatus(dir, []byte(report), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("low"), parsed.Tasks[0].Priority)
	require.Len(t, parsed.PriorityEstimates, 1)
	require.Equal(t, priorityLabel("low"), parsed.PriorityEstimates[0].Priority)
	mismatch := strings.Replace(entry, `"basis":"guess","assumptions"`, `"basis":"wrong","assumptions"`, 1)
	require.NoError(t, os.WriteFile(path, []byte(mismatch), 0600))
	result, _, err = projectLogStatus(dir, []byte(report), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("high"), parsed.Tasks[0].Priority)
	require.Len(t, parsed.StatusSync.Groups, 1)
	require.Len(t, parsed.StatusSync.PendingAdditions, 1)
}
