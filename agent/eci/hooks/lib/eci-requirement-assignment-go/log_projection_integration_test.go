package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLedgerAppendProjectsOrderedTaskUpdates exercises the append and retry path.
//
// Example: Closing one task retains its sibling and retries do not replay stale status.
func TestLedgerAppendProjectsOrderedTaskUpdates(t *testing.T) {
	f := newCLIFixture(t)
	dir := filepath.Dir(f.Ledger)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	report := `# preserve report comment
timestamp: '2026-11-02T15:24:00Z'
state: Working
progress: []
decisions: []
blockers_risks: []
verification: []
next_focus: Finish
scope_complete: true
tasks:
  - {id: '1', title: First, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS}
  - {id: '2', title: Sibling, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS}
top_priorities: ['1', '2']
priority_estimates:
  - {priority: high, min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
completion_estimate: {min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
`
	require.NoError(t, os.WriteFile(reportPath, []byte(report), 0600))
	env := append(os.Environ(), "ECI_PROOF_ROOT="+filepath.Join(f.Root, "proof"), "ECI_SESSION_ID=test-capture")
	appendEvent := func(payload string) {
		t.Helper()
		command := exec.Command(f.CLI, "ledger-append", "--json", payload)
		command.Env = env
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
	}
	appendEvent(`{"event":"decision","summary":"Malformed optional update","details":{"status_update":{"updates":[{"target":"unknown","id":"1","fields":{"status":"CLOSED"}}]}}}`)
	appendEvent(`{"event":"verification_result","summary":"First accepted","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"acceptance proof"}}]}}}`)
	updated, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Regexp(t, `status: ["']?CLOSED["']?`, string(updated))
	require.Contains(t, string(updated), "acceptance proof")
	require.Contains(t, string(updated), "title: Sibling, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS")
	require.Contains(t, string(updated), "# preserve report comment")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(logged), "\n"))
	require.Contains(t, string(logged), "Malformed optional update")
	require.Contains(t, string(updated), "invalid status_update requires manual review")
	require.Contains(t, string(updated), "scope_complete: false")
	retry := exec.Command(f.CLI, "status-refresh", "--event", "verification_result")
	retry.Env = env
	output, err := retry.CombinedOutput()
	require.NoError(t, err, string(output))
	afterRetry, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, updated, afterRetry)
	logAfter, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, logged, logAfter)
}

// TestLedgerAppendRetryReplaysEventsInOrder preserves newer updates after a failed refresh.
//
// Example: Repairing invalid YAML applies the later closure instead of the earlier open state.
func TestLedgerAppendRetryReplaysEventsInOrder(t *testing.T) {
	f := newCLIFixture(t)
	dir := filepath.Dir(f.Ledger)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(reportPath, []byte("[malformed"), 0600))
	env := append(os.Environ(), "ECI_PROOF_ROOT="+filepath.Join(f.Root, "proof"), "ECI_SESSION_ID=test-capture")
	for _, payload := range []string{
		`{"event":"decision","summary":"Started","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"IN PROGRESS"}}]}}}`,
		`{"event":"verification_result","summary":"Accepted","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"later proof"}}]}}}`,
	} {
		command := exec.Command(f.CLI, "ledger-append", "--json", payload)
		command.Env = env
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		require.Contains(t, string(output), "status refresh failed")
	}
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	before, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(before), "\n"))
	report := `timestamp: '2026-11-02T15:24:00Z'
state: Working
progress: []
decisions: []
blockers_risks: []
verification: []
next_focus: Finish
scope_complete: true
tasks:
  - {id: '1', title: Work, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: NEW}
top_priorities: ['1']
priority_estimates:
  - {priority: high, min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
completion_estimate: {min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
`
	require.NoError(t, os.WriteFile(reportPath, []byte(report), 0600))
	retry := exec.Command(f.CLI, "status-refresh", "--event", "verification_result")
	retry.Env = env
	output, err := retry.CombinedOutput()
	require.NoError(t, err, string(output))
	updated, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Regexp(t, `status: ["']?CLOSED["']?`, string(updated))
	require.Contains(t, string(updated), "later proof")
	after, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
