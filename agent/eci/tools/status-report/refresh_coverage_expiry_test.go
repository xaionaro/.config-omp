package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRefreshMarksUnlinkedScoredLeaf checks scored package coverage.
//
// Example: A closed task without an inherited work reference remains incomplete.
func TestRefreshMarksUnlinkedScoredLeaf(t *testing.T) {
	report := refreshCoverageReport("CLOSED", "evidence: accepted, ", "")
	result, parsedBefore, parsedAfter := runCoverageRefresh(t, report, time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC))
	require.False(t, *parsedAfter.ScopeComplete)
	require.Contains(t, string(result), "scored leaf 1 lacks a valid inherited work reference")
	require.Equal(t, parsedBefore.Tasks, parsedAfter.Tasks)
	require.Equal(t, parsedBefore.Timestamp, parsedAfter.Timestamp)
	require.Equal(t, parsedBefore.CompletionEstimate, parsedAfter.CompletionEstimate)
}

// TestRefreshMarksExpiredEstimateWithOpenReportWork checks report-side openness.
//
// Example: A completed canonical record does not hide an open task's expired estimate.
func TestRefreshMarksExpiredEstimateWithOpenReportWork(t *testing.T) {
	report := refreshCoverageReport("IN PROGRESS", "", "work_refs: ['project-understanding.yaml#/records/work:done'], ")
	cases := []struct {
		Name       string
		Evaluation time.Time
		Expired    bool
	}{
		{Name: "not elapsed", Evaluation: time.Date(2026, 11, 2, 15, 30, 0, 0, time.UTC)},
		{Name: "elapsed", Evaluation: time.Date(2026, 11, 2, 16, 15, 0, 0, time.UTC), Expired: true},
	}
	for _, tc := range cases {
		t.Run(tc.Name,
			// Callback evaluates one fixed completion-estimate instant.
			//
			// Example: Distinguish an elapsed endpoint from one still in the future.
			func(t *testing.T) {
				result, before, after := runCoverageRefresh(t, report, tc.Evaluation)
				require.Equal(t, before.Tasks, after.Tasks)
				require.Equal(t, before.Timestamp, after.Timestamp)
				require.Equal(t, before.CompletionEstimate, after.CompletionEstimate)
				if tc.Expired {
					require.False(t, *after.ScopeComplete)
					require.Contains(t, string(result), "endpoint elapsed while work open")
				} else {
					require.True(t, *after.ScopeComplete)
					require.NotContains(t, string(result), "endpoint elapsed while work open")
				}
			})
	}
}

// refreshCoverageReport constructs a report with one scored leaf package.
//
// Example: Represent a closed task with no canonical work coverage.
func refreshCoverageReport(
	status string,
	evidence string,
	refs string,
) string {
	return `timestamp: '2026-11-02T15:24:00Z'
state: Working
progress: []
decisions: []
blockers_risks: []
verification: []
next_focus: Finish
scope_complete: true
tasks:
  - {id: '1', title: Work, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, ` + refs + `status: ` + status + `, ` + evidence + `owner: one}
top_priorities: ['1']
priority_estimates:
  - {priority: high, min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
completion_estimate: {min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
`
}

// runCoverageRefresh writes isolated inputs and returns both report projections.
//
// Example: Evaluate a report while its canonical direct work is completed.
func runCoverageRefresh(
	t *testing.T,
	reportText string,
	evaluation time.Time,
) ([]byte, report, report) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	ledger := `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:done: {section: progress, type: outcome, data: {description: Work}}
  work:done:
    section: progress
    type: work
    links: {outcomes: ['outcome:done']}
    data: {owner: one, state: completed, reporting_roles: []}
    evidence: []
`
	before, err := parse([]byte(reportText))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(reportText), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	require.NoError(t, refreshStatusAt(dir, "decision", evaluation))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	after, err := parse(result)
	require.NoError(t, err)
	unchanged := after
	unchanged.ScopeComplete = before.ScopeComplete
	unchanged.Blockers = before.Blockers
	require.Equal(t, before, unchanged)
	sourceAfter, err := os.ReadFile(filepath.Join(dir, "project-understanding.yaml"))
	require.NoError(t, err)
	require.Equal(t, ledger, string(sourceAfter))
	return result, before, after
}
