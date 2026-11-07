package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestLedgerAppendAnchorsHistoricalTimeline verifies the public append and retry path.
//
// Example: A fresh report timestamp keeps existing timeline bins at their old anchor.
func TestLedgerAppendAnchorsHistoricalTimeline(t *testing.T) {
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
timeline_state: present
timeline:
  - {job: root, parent_chain: root, start: '2026-11-02T14:54:00Z', end: '2026-11-02T15:24:00Z', fast: old proof, normal: old work}
`
	require.NoError(t, os.WriteFile(reportPath, []byte(report), 0600))
	env := f.env(t)
	command := exec.Command(f.CLI, "ledger-append", "--json", `{"event":"decision","summary":"Ready for review","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"state":"Reviewing"}}]}}}`)
	command.Env = env
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.NotContains(t, string(output), "status refresh failed")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	var event struct {
		Timestamp string `json:"timestamp"`
	}
	require.NoError(t, json.Unmarshal(logged, &event))
	updated, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	var parsed struct {
		Timestamp    string `yaml:"timestamp"`
		TimelineAsOf string `yaml:"timeline_as_of"`
		State        string `yaml:"state"`
		Timeline     []struct {
			Start  string `yaml:"start"`
			End    string `yaml:"end"`
			Fast   string `yaml:"fast"`
			Normal string `yaml:"normal"`
		} `yaml:"timeline"`
	}
	require.NoError(t, yaml.Unmarshal(updated, &parsed))
	require.Equal(t, event.Timestamp, parsed.Timestamp)
	require.NotEqual(t, "2026-11-02T15:24:00Z", parsed.Timestamp)
	require.Equal(t, "2026-11-02T15:24:00Z", parsed.TimelineAsOf)
	require.Equal(t, "Reviewing", parsed.State)
	require.Len(t, parsed.Timeline, 1)
	require.Equal(t, "2026-11-02T14:54:00Z", parsed.Timeline[0].Start)
	require.Equal(t, "2026-11-02T15:24:00Z", parsed.Timeline[0].End)
	require.Equal(t, "old proof", parsed.Timeline[0].Fast)
	require.Equal(t, "old work", parsed.Timeline[0].Normal)
	statusCLI, err := filepath.Abs("../../../bin/status-report")
	require.NoError(t, err)
	render := exec.Command(statusCLI, "render", reportPath)
	rendered, err := render.CombinedOutput()
	require.NoError(t, err, string(rendered))
	require.Contains(t, string(rendered), "Historical timeline as of 2026-11-02T15:24:00Z")
	retry := exec.Command(f.CLI, "status-refresh", "--event", "decision")
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
