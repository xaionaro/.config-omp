package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAssignmentRefreshFailureRecovery checks that retrying status refresh does not duplicate an accepted history event.
//
// Example: A malformed report leaves the assignment and log intact until the report is repaired.
func TestAssignmentRefreshFailureRecovery(t *testing.T) {
	f := newCLIFixture(t)
	dir := filepath.Dir(f.Ledger)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(reportPath, []byte("[malformed"), 0600))
	output, stderr, err := f.invoke(t)
	require.NoError(t, err, stderr)
	require.Contains(t, output, "/root/receiver")
	require.Contains(t, stderr, "log append succeeded; status refresh failed")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	before, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(before), "\n"))
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
	require.NoError(t, os.WriteFile(reportPath, []byte(report), 0600))
	command := exec.Command(f.CLI, "status-refresh", "--event", "requirement_change")
	command.Env = f.env()
	combined, err := command.CombinedOutput()
	require.NoError(t, err, string(combined))
	after, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, before, after)
	updated, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Contains(t, string(updated), "scope_complete: false")
}
