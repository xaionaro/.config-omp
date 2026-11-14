package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPriorityRetirementRetriesAfterOwnerRestoresEstimate verifies atomic recovery.
//
// Example: A manually edited estimate keeps both changes pending until restored.
func TestPriorityRetirementRetriesAfterOwnerRestoresEstimate(t *testing.T) {
	t.Parallel()
	// report has one active task and one matching priority estimate.
	//
	// Example: Reprioritize the last task in the high group.
	const report = `timestamp: '2026-11-02T15:24:00Z'
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
	// event changes the task and replaces the exact estimate together.
	//
	// Example: Retire high only when its supplied prior remains unchanged.
	const event = `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":"low"}}],"additions":[{"target":"priority_estimate","retire_priority":"high","expected_prior":{"priority":"high","min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"},"record":{"priority":"low","min_minutes":1,"max_minutes":2,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}]}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	edited := strings.Replace(report, "basis: guess, assumptions: none", "basis: manually revised, assumptions: none", 1)
	require.NotEqual(t, report, edited)
	pending, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	r, err := parse(pending)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("high"), r.Tasks[0].Priority)
	require.Equal(t, priorityLabel("high"), r.PriorityEstimates[0].Priority)
	require.Len(t, r.StatusSync.Groups, 1)
	require.Len(t, r.StatusSync.PendingAdditions, 1)
	restored := strings.Replace(string(pending), "basis: manually revised, assumptions: none", "basis: guess, assumptions: none", 1)
	require.NotEqual(t, string(pending), restored)
	applied, _, err := projectLogStatus(dir, []byte(restored), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	r, err = parse(applied)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("low"), r.Tasks[0].Priority)
	require.Len(t, r.PriorityEstimates, 1)
	require.Equal(t, priorityLabel("low"), r.PriorityEstimates[0].Priority)
	require.Empty(t, r.StatusSync.Groups)
	require.Empty(t, r.StatusSync.PendingAdditions)
	again, _, err := projectLogStatus(dir, applied, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, applied, again)
}
