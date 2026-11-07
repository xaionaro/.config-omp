package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestLedgerAppendProjectsValidSiblingOfMalformedUpdate exercises independent event updates.
//
// Example: An invalid target remains diagnosed while a sibling closes its task.
func TestLedgerAppendProjectsValidSiblingOfMalformedUpdate(t *testing.T) {
	f := newCLIFixture(t)
	dir := filepath.Dir(f.Ledger)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
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
	env := append(os.Environ(), "ECI_PROOF_ROOT="+filepath.Join(f.Root, "proof"), "ECI_SESSION_ID=test-capture")
	payload := `{"event":"verification_result","summary":"Accepted with malformed sibling","details":{"status_update":{"updates":[{"target":42,"id":"bad","fields":{"owner":"x"}},{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted safe sibling"}}]}}}`
	command := exec.Command(f.CLI, "ledger-append", "--json", payload)
	command.Env = env
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "high-level log appended")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(logged), "\n"))
	var event map[string]any
	require.NoError(t, json.Unmarshal(logged, &event))
	require.Equal(t, "verification_result", event["event"])
	require.Contains(t, string(logged), `"target":42`)
	require.Contains(t, string(logged), "accepted safe sibling")
	updated, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var parsed struct {
		ScopeComplete bool     `yaml:"scope_complete"`
		Blockers      []string `yaml:"blockers_risks"`
		Tasks         []struct {
			ID       string `yaml:"id"`
			Status   string `yaml:"status"`
			Evidence string `yaml:"evidence"`
		} `yaml:"tasks"`
	}
	require.NoError(t, yaml.Unmarshal(updated, &parsed))
	require.False(t, parsed.ScopeComplete)
	require.Contains(t, parsed.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
	require.Len(t, parsed.Tasks, 1)
	require.Equal(t, "1", parsed.Tasks[0].ID)
	require.Equal(t, "CLOSED", parsed.Tasks[0].Status)
	require.Equal(t, "accepted safe sibling", parsed.Tasks[0].Evidence)
	retry := exec.Command(f.CLI, "status-refresh", "--event", "verification_result")
	retry.Env = env
	output, err = retry.CombinedOutput()
	require.NoError(t, err, string(output))
	after, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, updated, after)
	logAfter, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, logged, logAfter)
}
