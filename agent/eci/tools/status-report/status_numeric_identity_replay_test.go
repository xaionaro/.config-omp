package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNumericTaskIdentitiesReplayLexically checks the public refresh path.
//
// Example: Numeric-looking 1.10 and 1.1 remain distinct task identifiers.
func TestNumericTaskIdentitiesReplayLexically(t *testing.T) {
	report := strings.Replace(linkedReport, "id: '1', title: First", "id: 1, title: First", 1)
	report = strings.Replace(report, "id: '2', title: Second", "id: 1.1, parent_id: 1, title: Second", 1)
	report = strings.Replace(report, "top_priorities: ['1', '2']", "  - {id: 1.10, parent_id: 1, title: Third, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS, work_refs: ['project-understanding.yaml#/records/work:second']}\ntop_priorities: [1, 1.1, 1.10]", 1)
	report = strings.Replace(report, "task_id: '1'", "task_id: 1", 1)
	report = strings.Replace(report, "task_id: '2'", "task_id: 1.1", 1)
	_, err := parse([]byte(report))
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	event := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1.10","fields":{"owner":"selected owner"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "assignment_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, taskID("1.1"), parsed.Tasks[1].ID)
	require.Empty(t, parsed.Tasks[1].Owner)
	require.Equal(t, taskID("1.10"), parsed.Tasks[2].ID)
	require.Equal(t, "selected owner", parsed.Tasks[2].Owner)
	require.Equal(t, []taskID{"1", "1.1", "1.10"}, parsed.TopPriorities)
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "assignment_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}

// TestNumericTopPriorityCancellation checks the keyed removal effect.
//
// Example: Cancelling numeric task 1 removes only its own numeric priority entry.
func TestNumericTopPriorityCancellation(t *testing.T) {
	report := strings.Replace(linkedReport, "id: '1', title: First", "id: 1, title: First", 1)
	report = strings.Replace(report, "top_priorities: ['1', '2']", "top_priorities: [1, '2']", 1)
	report = strings.Replace(report, "task_id: '1'", "task_id: 1", 1)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	event := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"unknown","lifecycle":"cancelled","evidence":"withdrawn"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "lifecycle_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, taskLifecycleCancelled, parsed.Tasks[0].Lifecycle)
	require.Equal(t, []taskID{"2"}, parsed.TopPriorities)
	require.Contains(t, string(result), "top_priorities: ['2']")
}

// TestVerificationRetainsTopPriorityRepresentation checks unrelated scalar styles.
//
// Example: A task owner update cannot rewrite numeric priority 2 as a string.
func TestVerificationRetainsTopPriorityRepresentation(t *testing.T) {
	before := []byte("tasks: [{id: 1, owner: old}, {id: 2, owner: keep}]\ntop_priorities: [1, 2]\n")
	effects := []syncEffect{{Target: syncTargetTask, ID: "1", Field: "owner", Value: "new", Operation: syncEffectSet}}
	good := []byte("tasks: [{id: 1, owner: new}, {id: 2, owner: keep}]\ntop_priorities: [1, 2]\n")
	changed := []byte("tasks: [{id: 1, owner: new}, {id: 2, owner: keep}]\ntop_priorities: [1, '2']\n")
	require.NoError(t, verifyStatusSyncEffects(before, good, effects))
	require.Error(t, verifyStatusSyncEffects(before, changed, effects))
}

// TestExplicitTopPriorityReplacementCanChangeStyles checks authorized replacement.
//
// Example: A supplied list of string IDs may replace a numeric YAML list.
func TestExplicitTopPriorityReplacementCanChangeStyles(t *testing.T) {
	before := []byte("top_priorities: [1, 2]\n")
	after := []byte("top_priorities: ['2']\n")
	effects := []syncEffect{{Target: syncTargetReport, ID: "current", Field: "top_priorities", Value: json.RawMessage(`["2"]`), Operation: syncEffectSet}}
	require.NoError(t, verifyStatusSyncEffects(before, after, effects))
}

// TestVerificationRetainsUnchangedIdentityRepresentation checks owner-authored IDs.
//
// Example: Updating task 1 cannot silently rewrite another numeric ID as a string.
func TestVerificationRetainsUnchangedIdentityRepresentation(t *testing.T) {
	before := []byte("tasks: [{id: 1, owner: old}, {id: 2, owner: keep}]\n")
	effects := []syncEffect{{Target: syncTargetTask, ID: "1", Field: "owner", Value: "new", Operation: syncEffectSet}}
	good := []byte("tasks: [{id: 1, owner: new}, {id: 2, owner: keep}]\n")
	changed := []byte("tasks: [{id: 1, owner: new}, {id: '2', owner: keep}]\n")
	require.NoError(t, verifyStatusSyncEffects(before, good, effects))
	require.Error(t, verifyStatusSyncEffects(before, changed, effects))
}

// TestNumericLaneAndReviewSelectorsReplay checks other task-keyed targets.
//
// Example: A numeric lane or review task ID matches its explicit string selector.
func TestNumericLaneAndReviewSelectorsReplay(t *testing.T) {
	base := strings.Replace(linkedReport, "id: '1', title: First", "id: 1, title: First", 1)
	base = strings.Replace(base, "top_priorities: ['1', '2']", "top_priorities: [1, '2']", 1)
	base = strings.Replace(base, "task_id: '1'", "task_id: 1", 1)
	for _, tc := range []struct{ name, input, event string }{
		{"lane", base, `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"owner":"lane owner"}}]}}}`},
		{"review", base + "reviews: [{task_id: 1, reporter: A, critic: B, rounds: 1, estimate: checked, impact: checked, priority: checked, unresolved: none}]\n", `{"event":"review_result","details":{"status_update":{"updates":[{"target":"review","id":"1","values":{"critic":"new critic"}}]}}}`},
	} {
		t.Run(tc.name,
			// Apply a targeted lane or review event through refresh.
			//
			// Example: Numeric task ID 1 matches exactly one section entry.
			func(t *testing.T) {
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tc.input), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(tc.event+"\n"), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				if tc.name == "lane" {
					require.Equal(t, "lane owner", parsed.Lanes[0].Owner)
				} else {
					require.Equal(t, "new critic", parsed.Reviews[0].Critic)
				}
				require.Empty(t, parsed.StatusSync.Groups)
			})
	}
}

// TestVerificationRetainsOtherSectionIdentityStyles checks unrelated record IDs.
//
// Example: A task update cannot rewrite a lane or review ID's YAML type.
func TestVerificationRetainsOtherSectionIdentityStyles(t *testing.T) {
	before := []byte("tasks: [{id: 1, owner: old}]\nlanes: [{task_id: 1, work_ref: work, required_environment: implementation}]\nreviews: [{task_id: 1, critic: B}]\n")
	effects := []syncEffect{{Target: syncTargetTask, ID: "1", Field: "owner", Value: "new", Operation: syncEffectSet}}
	good := strings.Replace(string(before), "owner: old", "owner: new", 1)
	require.NoError(t, verifyStatusSyncEffects(before, []byte(good), effects))
	changedLane := strings.Replace(good, "lanes: [{task_id: 1,", "lanes: [{task_id: '1',", 1)
	changedReview := strings.Replace(good, "reviews: [{task_id: 1,", "reviews: [{task_id: '1',", 1)
	require.Error(t, verifyStatusSyncEffects(before, []byte(changedLane), effects))
	require.Error(t, verifyStatusSyncEffects(before, []byte(changedReview), effects))
}

// TestVerificationRetainsDuplicateReviewIdentityStyles checks every occurrence.
//
// Example: Rewriting the first of two review IDs is still detected.
func TestVerificationRetainsDuplicateReviewIdentityStyles(t *testing.T) {
	before := strings.Replace(linkedReport, "id: '1', title: First", "id: '1', owner: old, title: First", 1)
	before += "reviews: [{task_id: 1, reporter: A, critic: B, rounds: 1, estimate: checked, impact: checked, priority: checked, unresolved: none}, {task_id: '1', reporter: C, critic: D, rounds: 1, estimate: checked, impact: checked, priority: checked, unresolved: none}]\n"
	_, err := parse([]byte(before))
	require.NoError(t, err)
	candidate := strings.Replace(strings.Replace(before, "owner: old", "owner: new", 1), "task_id: 1,", "task_id: '1',", 1)
	effects := []syncEffect{{Target: syncTargetTask, ID: "1", Field: "owner", Value: "new", Operation: syncEffectSet}}
	require.Error(t, verifyStatusSyncEffects([]byte(before), []byte(candidate), effects))
}

// TestVerificationRetainsParentAliasIdentity checks an aliased parent ID.
//
// Example: Updating a task owner cannot replace a parent alias with a string.
func TestVerificationRetainsParentAliasIdentity(t *testing.T) {
	before := strings.Replace(linkedReport, "id: '1', title: First", "id: &id 1, owner: old, title: First", 1)
	before = strings.Replace(before, "id: '2', title: Second", "id: '1.1', parent_id: *id, title: Second", 1)
	before = strings.Replace(before, "task_id: '2'", "task_id: '1.1'", 1)
	before = strings.Replace(before, "top_priorities: ['1', '2']", "top_priorities: ['1', '1.1']", 1)
	_, err := parse([]byte(before))
	require.NoError(t, err)
	candidate := strings.Replace(strings.Replace(before, "owner: old", "owner: new", 1), "parent_id: *id", "parent_id: '1'", 1)
	effects := []syncEffect{{Target: syncTargetTask, ID: "1", Field: "owner", Value: "new", Operation: syncEffectSet}}
	require.Error(t, verifyStatusSyncEffects([]byte(before), []byte(candidate), effects))
}

// TestNumericDottedPriorityCancellationPreservesSibling checks lexical separation.
//
// Example: Cancelling 1.1 does not rewrite or remove numeric-looking 1.10.
func TestNumericDottedPriorityCancellationPreservesSibling(t *testing.T) {
	report := strings.Replace(linkedReport, "id: '1', title: First", "id: 1, title: First", 1)
	report = strings.Replace(report, "id: '2', title: Second", "id: 1.1, parent_id: 1, title: Second", 1)
	report = strings.Replace(report, "top_priorities: ['1', '2']", "  - {id: 1.10, parent_id: 1, title: Third, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS, work_refs: ['project-understanding.yaml#/records/work:second']}\ntop_priorities: [1, 1.1, 1.10]", 1)
	report = strings.Replace(report, "task_id: '1'", "task_id: 1", 1)
	report = strings.Replace(report, "task_id: '2'", "task_id: 1.1", 1)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	event := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1.1","fields":{"status":"unknown","lifecycle":"cancelled","evidence":"withdrawn"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "lifecycle_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, []taskID{"1", "1.10"}, parsed.TopPriorities)
	require.Contains(t, string(result), "top_priorities: [1, 1.10]")
	require.Equal(t, taskLifecycleCancelled, parsed.Tasks[1].Lifecycle)
	require.Equal(t, statusInProgress, parsed.Tasks[2].Status)
}

// TestCancellationRetainsDecoratedPrioritySurvivors checks valid owner syntax.
//
// Example: A surviving tagged or anchored priority retains its original spelling.
func TestCancellationRetainsDecoratedPrioritySurvivors(t *testing.T) {
	for _, tc := range []struct {
		name, priority string
		alias          bool
	}{
		{"anchor", "&owner '1'", false},
		{"tag", "!!str '1'", false},
		{"tag and anchor", "!!str &owner '1'", false},
		{"alias", "*owner", true},
	} {
		t.Run(tc.name,
			// Replay a cancellation while retaining a decorated survivor.
			//
			// Example: A task ID alias still resolves to its original anchor.
			func(t *testing.T) {
				report := linkedReport
				if tc.alias {
					report = strings.Replace(report, "id: '1', title: First", "id: &owner '1', title: First", 1)
				}
				report = strings.Replace(report, "top_priorities: ['1', '2']", "top_priorities: ["+tc.priority+", '2'] # owner note", 1)
				_, err := parse([]byte(report))
				require.NoError(t, err)
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				event := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"2","fields":{"status":"unknown","lifecycle":"cancelled","evidence":"withdrawn"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "lifecycle_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, taskLifecycleCancelled, parsed.Tasks[1].Lifecycle)
				require.Equal(t, []taskID{"1"}, parsed.TopPriorities)
				require.Contains(t, string(result), "top_priorities: ["+tc.priority+"] # owner note")
			})
	}
}

// TestCancellationDoesNotDeleteReferencedPriorityAnchor checks alias safety.
//
// Example: A live review alias prevents removing its referenced priority anchor.
func TestCancellationDoesNotDeleteReferencedPriorityAnchor(t *testing.T) {
	report := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities: [&owner '1', '2'] # owner note", 1)
	report += "reviews: [{task_id: *owner, reporter: A, critic: B, rounds: 1, estimate: checked, impact: checked, priority: checked, unresolved: none}]\n"
	_, err := parse([]byte(report))
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	event := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"unknown","lifecycle":"cancelled","evidence":"withdrawn"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	code := run([]string{"refresh", dir, "lifecycle_change"}, strings.NewReader(""), &out, &errOut)
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, parsed.Tasks[0].Status)
	require.Equal(t, []taskID{"1", "2"}, parsed.TopPriorities)
	require.Contains(t, string(result), "top_priorities: [&owner '1', '2'] # owner note")
	require.Equal(t, taskID("1"), parsed.Reviews[0].TaskID)
	if code == 0 {
		require.NotNil(t, parsed.StatusSync)
		require.NotEmpty(t, parsed.StatusSync.Groups)
	}
}

// TestVerificationProtectsPriorityAnchorIdentity checks owner-authored anchors.
//
// Example: An unrelated task edit cannot rename a surviving priority anchor.
func TestVerificationProtectsPriorityAnchorIdentity(t *testing.T) {
	before := []byte("tasks: [{id: 1, owner: old}]\ntop_priorities: [&owner '1']\n")
	after := []byte("tasks: [{id: 1, owner: new}]\ntop_priorities: [&other '1']\n")
	effects := []syncEffect{{Target: syncTargetTask, ID: "1", Field: "owner", Value: "new", Operation: syncEffectSet}}
	require.Error(t, verifyStatusSyncEffects(before, after, effects))
}
