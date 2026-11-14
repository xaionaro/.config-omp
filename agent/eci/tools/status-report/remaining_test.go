package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeLinkedSources writes disposable source fixtures for reconciliation.
//
// Example: Compare a report against linked ledger and forecasts.
func writeLinkedSources(
	t *testing.T,
	ledger string,
	forecasts string,
) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(forecasts), 0600))
	return dir
}

// TestOpenWorkRequiresRemainingCoverage exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestOpenWorkRequiresRemainingCoverage(t *testing.T) {
	t.Parallel()
	reportText := strings.ReplaceAll(linkedReport, "status: IN PROGRESS, work_refs:", "status: CLOSED, evidence: accepted, work_refs:")
	reportText = strings.Replace(reportText, "  - task_id: '1'", "  - task_id: '1'", 1)
	r, err := parse([]byte(reportText))
	require.NoError(t, err)
	// The open lane represents remaining acceptance despite its closed package.
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	issues := strings.Join(testReconcile(r, dir), "\n")
	require.Contains(t, issues, "remaining acceptance")
	direct := strings.Replace(linkedLedger, "reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/first'", "reporting_roles: []", 1)
	r.Lanes = r.Lanes[1:]
	r.Tasks[1].Status = statusInProgress // An unrelated sibling cannot cover work:first.
	dir = writeLinkedSources(t, direct, linkedForecasts)
	issues = strings.Join(testReconcile(r, dir), "\n")
	require.Contains(t, issues, "no remaining package or open acceptance lane")
}

// TestCancelledLaneMayBeOmitted exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestCancelledLaneMayBeOmitted(t *testing.T) {
	t.Parallel()
	reportText := strings.Replace(linkedReport, "  - task_id: '1'", "  - task_id: '1'", 1)
	r, err := parse([]byte(reportText))
	require.NoError(t, err)
	r.Lanes = r.Lanes[1:]
	ledger := strings.Replace(linkedLedger, "state: active", "state: cancelled", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	issues := strings.Join(testReconcile(r, writeLinkedSources(t, ledger, forecasts)), "\n")
	require.NotContains(t, issues, "declared lane work:first missing")
	require.NotContains(t, issues, "completion")
}

// TestCompletedRootHasOwnCompletionProjection exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestCompletedRootHasOwnCompletionProjection(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Lanes = r.Lanes[1:]
	r.RootCompletions = []rootCompletion{{WorkRef: "project-understanding.yaml#/records/work:first", Outcome: "First result", CompletedAt: "2026-11-02T15:00:00+00:00"}}
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish, reporting_roles: [lane]", "state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [root]", 1)
	forecasts := strings.Replace(linkedForecasts, "kind: lane", "kind: root", 1)
	forecasts = strings.Replace(forecasts, "status: active", "status: inactive", 1)
	dir := writeLinkedSources(t, ledger, forecasts)
	require.Empty(t, testReconcile(r, dir))
	require.Contains(t, render(r), "Root outcome: First result")
	r.RootCompletions[0].Outcome = "wrong"
	require.Contains(t, strings.Join(testReconcile(r, dir), "\n"), "outcome unverified")
	r.RootCompletions[0].CompletedAt = "2026-11-02T15:01:00Z"
	require.Contains(t, strings.Join(testReconcile(r, dir), "\n"), "completed_at mismatch")
	r.RootCompletions = nil
	require.Contains(t, strings.Join(testReconcile(r, dir), "\n"), "completion missing from report")
}

// TestCancelledLaneRetainedHasNoCompletion exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestCancelledLaneRetainedHasNoCompletion(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Lanes[0].Forecast = nil
	r.Lanes[0].Lifecycle = laneLifecycleCancelled
	ledger := strings.Replace(linkedLedger, "state: active", "state: cancelled", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.Empty(t, testReconcile(r, writeLinkedSources(t, ledger, forecasts)))
	require.Contains(t, render(r), "Cancelled: First result; no active forecast deadline.")
	r.Lanes[0].CompletedAt = "2026-11-02T15:00:00Z"
	require.Contains(t, strings.Join(testReconcile(r, writeLinkedSources(t, ledger, forecasts)), "\n"), "must not claim completion")
}

// TestRootCompletionRequiresIdentityAndTimestamp exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestRootCompletionRequiresIdentityAndTimestamp(t *testing.T) {
	t.Parallel()
	for _, extra := range []string{
		"root_completions: [{work_ref: '', outcome: Root, completed_at: '2026-11-02T15:00:00Z'}]\n",
		"root_completions: [{work_ref: ref, outcome: '', completed_at: '2026-11-02T15:00:00Z'}]\n",
		"root_completions: [{work_ref: ref, outcome: Root, completed_at: invalid}]\n",
	} {
		_, err := parse([]byte(linkedReport + extra))
		require.Error(t, err)
	}
}

// TestCancelledLifecycleValidation exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestCancelledLifecycleValidation(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	l := r.Lanes[0]
	l.Lifecycle = laneLifecycleCancelled
	l.Forecast = nil
	l.ImplementationStatus = statusClosed
	require.NoError(t, validateLane(l, map[taskID]task{l.TaskID: r.Tasks[0]}))
	l.CompletedAt = "2026-11-02T15:00:00Z"
	require.Error(t, validateLane(l, map[taskID]task{l.TaskID: r.Tasks[0]}))
	l.Lifecycle = laneLifecycle(99)
	require.Error(t, validateLane(l, map[taskID]task{l.TaskID: r.Tasks[0]}))
}

// TestTerminalReportForecastIsIncomplete exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestTerminalReportForecastIsIncomplete(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Lanes = r.Lanes[1:]
	r.RootForecasts = []forecast{{ForecastRef: "forecasts.yaml#/records/first", Outcome: "First result", Target: "2026-11-02T16:00:00Z", Assessment: "complete"}}
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish, reporting_roles: [lane]", "state: cancelled, reporting_roles: [root]", 1)
	forecasts := strings.Replace(strings.Replace(linkedForecasts, "kind: lane", "kind: root", 1), "status: active", "status: inactive", 1)
	require.Contains(t, strings.Join(testReconcile(r, writeLinkedSources(t, ledger, forecasts)), "\n"), "terminal work has report forecast")
}

// TestRolelessWorkCannotClaimForecast exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestRolelessWorkCannotClaimForecast(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Lanes = r.Lanes[1:]
	ledger := strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: []", 1)
	require.Contains(t, strings.Join(testReconcile(r, writeLinkedSources(t, ledger, linkedForecasts)), "\n"), "forecast has no applicable reporting role")
}

// TestInheritedWorkPriorityUsesRemainingLeaves exercises source reconciliation behavior.
//
// Example: Detect an inconsistent status projection.
func TestInheritedWorkPriorityUsesRemainingLeaves(t *testing.T) {
	t.Parallel()
	r := report{Tasks: []task{
		{ID: "1", WorkRefs: []string{"project-understanding.yaml#/records/work:first"}},
		{ID: "1.1", ParentID: "1", Priority: "high", Status: statusClosed},
		{ID: "1.2", ParentID: "1", Priority: "low", Status: statusInProgress},
		{ID: "2", Priority: "other", Status: statusInProgress},
	}}
	idx := buildTaskWorkIndex(r.Tasks, map[string]sourceRecord{"work:first": {Data: workData{State: workStateActive}}})
	require.True(t, idx.remaining["work:first"])
	require.False(t, idx.priorityOpen("high", nil))
	require.True(t, idx.priorityOpen("low", nil))
	require.True(t, idx.priorityOpen("other", nil))
}

// TestLaneWorkMustMatchTaskScope verifies the reconciliation boundary.
//
// Example: Reject a contradictory report projection.
func TestLaneWorkMustMatchTaskScope(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	require.Empty(t, testReconcile(r, dir))
	r.Lanes[0].TaskID = "2"
	require.Contains(t, strings.Join(testReconcile(r, dir), "\n"), "outside task scope")
}

// TestTerminalLaneCannotRetainFollowup verifies the reconciliation boundary.
//
// Example: Reject a contradictory report projection.
func TestTerminalLaneCannotRetainFollowup(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Lanes[0].Forecast = nil
	r.Lanes[0].Lifecycle = laneLifecycleCancelled
	r.Lanes[0].RootTaskStatus = rootTaskFollowup
	ledger := strings.Replace(linkedLedger, "state: active", "state: cancelled", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.Contains(t, strings.Join(testReconcile(r, writeLinkedSources(t, ledger, forecasts)), "\n"), "terminal work retains followup")
	r.Lanes[0].RootTaskStatus = rootTaskFollowupPaused
	require.Contains(t, strings.Join(testReconcile(r, writeLinkedSources(t, ledger, forecasts)), "\n"), "terminal work retains followup")
	r.Lanes[0].RootTaskStatus = rootTaskNone
	require.Empty(t, testReconcile(r, writeLinkedSources(t, ledger, forecasts)))
}

// TestTaskScopeUsesOnlyRelatedBranches verifies the reconciliation boundary.
//
// Example: Reject a contradictory report projection.
func TestTaskScopeUsesOnlyRelatedBranches(t *testing.T) {
	t.Parallel()
	tasks := []task{{ID: "1", WorkRefs: []string{"project-understanding.yaml#/records/work:a"}}, {ID: "1.1", ParentID: "1"}, {ID: "2"}, {ID: "2.1", ParentID: "2", WorkRefs: []string{"project-understanding.yaml#/records/work:b"}}}
	idx := buildTaskWorkIndex(tasks, map[string]sourceRecord{"work:a": {}, "work:b": {}})
	require.True(t, idx.containsWork("1.1", "work:a"))
	require.True(t, idx.containsWork("2", "work:b"))
	require.False(t, idx.containsWork("2.1", "work:a"))
}

// TestCompletedLaneCannotRetainFollowup checks completion against follow-up status.
//
// Example: Accepted work cannot still claim post-Fast follow-up.
func TestCompletedLaneCannotRetainFollowup(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Lanes[0].Forecast = nil
	r.Lanes[0].ImplementationStatus = statusClosed
	r.Lanes[0].CompletedAt = "2026-11-02T15:00:00Z"
	r.Lanes[0].RootTaskStatus = rootTaskFollowup
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish", "state: completed, completed_at: '2026-11-02T15:00:00Z'", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	dir := writeLinkedSources(t, ledger, forecasts)
	require.Contains(t, strings.Join(testReconcile(r, dir), "\n"), "terminal work retains followup")
	r.Lanes[0].RootTaskStatus = rootTaskNone
	require.Empty(t, testReconcile(r, dir))
}

// TestActiveLaneMayRetainFollowup preserves ongoing post-Fast work.
//
// Example: An active source lane may report FOLLOWUP.
func TestActiveLaneMayRetainFollowup(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Lanes[0].RootTaskStatus = rootTaskFollowup
	require.Empty(t, testReconcile(r, writeLinkedSources(t, linkedLedger, linkedForecasts)))
}

// TestScoredLeavesNeedTheirOwnInheritedWork checks link scope at leaf granularity.
//
// Example: A sibling's work reference cannot cover a closed package.
func TestScoredLeavesNeedTheirOwnInheritedWork(t *testing.T) {
	t.Parallel()
	tasks := []task{{ID: "1", WorkRefs: []string{"project-understanding.yaml#/records/a"}}, {ID: "1.1", ParentID: "1", Status: statusClosed}, {ID: "2", Status: statusInProgress}, {ID: "3", Status: statusUnknown}}
	idx := buildTaskWorkIndex(tasks, map[string]sourceRecord{"a": {}})
	issues := strings.Join(unlinkedScoredLeaves(tasks, idx), "\n")
	require.Contains(t, issues, "scored leaf 2 lacks a valid inherited work reference")
	require.NotContains(t, issues, "scored leaf 1.1")
	require.NotContains(t, issues, "scored leaf 3")
}

// TestReportOpenWorkAffectsPriorityExpiry checks report packages independently of source lifecycle.
//
// Example: A new report leaf can keep a priority open after an earlier work record closed.
func TestReportOpenWorkAffectsPriorityExpiry(t *testing.T) {
	t.Parallel()
	tasks := []task{{ID: "1", Priority: "high", Status: statusClosed}, {ID: "2", Priority: "low", Status: statusInProgress}}
	idx := buildTaskWorkIndex(tasks, nil)
	require.False(t, idx.priorityOpen("high", nil))
	require.True(t, idx.priorityOpen("low", nil))
	require.True(t, idx.reportOpen)
}

// TestEmptyTaskScopeNeedsNoInventedWorkReference checks an empty inventory.
//
// Example: No scored leaves means no leaf references are required.
func TestEmptyTaskScopeNeedsNoInventedWorkReference(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Tasks = []task{}
	r.Lanes = nil
	r.TopPriorities = []taskID{}
	r.PriorityEstimates = nil
	ledger := "schema: project-understanding/v1\nsections: {}\nrecords: {}\n"
	dir := writeLinkedSources(t, ledger, "schema: forecasts/v1\nrecords: {}\n")
	require.Empty(t, testReconcile(r, dir))
}
