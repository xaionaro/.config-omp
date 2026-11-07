package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncAddedTaskAllowsNullStatus checks the report's documented unknown state.
//
// Example: A new task with unknown status remains usable for later updates.
func TestSyncAddedTaskAllowsNullStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":null}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(added)
	require.NoError(t, err)
	require.Len(t, parsed.Tasks, 3)
	require.Equal(t, statusUnknown, parsed.Tasks[2].Status)
	second := `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"status":"IN PROGRESS"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	updated, _, err := projectLogStatus(dir, added, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err = parse(updated)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, parsed.Tasks[2].Status)
}

// TestSyncAddedReviewPreservesOwnerEdit checks reviewer identity ownership.
//
// Example: A later event does not replace a manually edited critic.
func TestSyncAddedReviewPreservesOwnerEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"review_result","details":{"status_update":{"additions":[{"target":"review","record":{"task_id":"1","reporter":"requester","critic":"reviewer","rounds":1,"estimate":"ten minutes","impact":"proof","priority":"high","unresolved":"none"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"critic":"reviewer"`, `"critic":"owner edit"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"review_result","details":{"status_update":{"updates":[{"target":"review","id":"1","values":{"critic":"later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Len(t, parsed.Reviews, 1)
	require.Equal(t, "owner edit", parsed.Reviews[0].Critic)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncAddedLanePreservesOwnerEdit checks source-backed insertion ownership.
//
// Example: An owner-edited lane coordinator survives a later assignment event.
func TestSyncAddedLanePreservesOwnerEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"lane","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","owner":"initial","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW","forecast":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "active", "kind": "lane"}}}
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(valid), ledger, forecasts)
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"owner":"initial"`, `"owner":"manual"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/w","required_environment":"implementation","fields":{"owner":"later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledger, forecasts)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Len(t, parsed.Lanes, 1)
	require.Equal(t, "manual", parsed.Lanes[0].Owner)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncAddedPriorityEstimatePreservesOwnerEdit checks grouped insertion ownership.
//
// Example: A later estimate update cannot overwrite a manually revised basis.
func TestSyncAddedPriorityEstimatePreservesOwnerEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"low","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}},{"target":"priority_estimate","record":{"priority":"low","min_minutes":1,"max_minutes":2,"as_of":"2026-11-02T15:24:00Z","basis":"initial basis","assumptions":"none","dependencies":"none","critical_path":"work"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"basis":"initial basis"`, `"basis":"owner edit"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"forecast_change","details":{"status_update":{"updates":[{"target":"priority_estimate","id":"low","values":{"basis":"later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "owner edit", parsed.PriorityEstimates[1].Basis)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncReprioritizationEstimatePreservesOwnerEdit checks its insertion path.
//
// Example: A revised estimate basis remains when a later event proposes another.
func TestSyncReprioritizationEstimatePreservesOwnerEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":"low"}}],"additions":[{"target":"priority_estimate","record":{"priority":"low","min_minutes":1,"max_minutes":2,"as_of":"2026-11-02T15:24:00Z","basis":"initial basis","assumptions":"none","dependencies":"none","critical_path":"work"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"basis":"initial basis"`, `"basis":"owner edit"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"forecast_change","details":{"status_update":{"updates":[{"target":"priority_estimate","id":"low","values":{"basis":"later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "owner edit", parsed.PriorityEstimates[1].Basis)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncExplicitNullStatusSpellings checks compatible legacy YAML forms.
//
// Example: A task marked with a tilde can be moved into progress.
func TestSyncExplicitNullStatusSpellings(t *testing.T) {
	for _, value := range []string{"null", "Null", "NULL", "~"} {
		t.Run(value,
			// Check one explicit YAML null spelling through replay.
			//
			// Example: Replace a tilde with an in-progress status.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "high_level_log.jsonl")
				report := strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", "status: "+value+", work_refs:", 1)
				entry := `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"IN PROGRESS"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(path, []byte(entry), 0600))
				result, _, err := projectLogStatus(dir, []byte(report), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, statusInProgress, parsed.Tasks[0].Status)
			})
	}
}
