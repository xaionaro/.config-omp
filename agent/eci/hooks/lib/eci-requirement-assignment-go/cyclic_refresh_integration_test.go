package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// cyclicTestDeadline bounds the append and retry commands.
	//
	// Example: Stop a cyclic parser that fails to return.
	cyclicTestDeadline = 30 * time.Second
	// cyclicWaitDelay bounds pipe cleanup after a command exits.
	//
	// Example: Stop waiting for an inherited output pipe.
	cyclicWaitDelay = 5 * time.Second
)

// testCommandGroup owns the process group of a running test command.
//
// Example: Retire the launcher and its go-run child after a deadline.
type testCommandGroup struct {
	Command *exec.Cmd
}

// Cancel kills the running command's private process group.
//
// Example: End a parser and launcher that have not returned before timeout.
func (g testCommandGroup) Cancel() error {
	if g.Command.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-g.Command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// boundedECICommand creates a command whose timeout retires its process group.
//
// Example: Bound a launcher that runs the status-report CLI as a child.
func boundedECICommand(
	ctx context.Context,
	path string,
	args ...string,
) *exec.Cmd {
	command := exec.CommandContext(ctx, path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = (testCommandGroup{Command: command}).Cancel
	command.WaitDelay = cyclicWaitDelay
	return command
}

// TestCyclicReportPreservesAppendedEvent checks recovery from an invalid report.
//
// Example: A cyclic YAML alias cannot undo a successful append or duplicate it on retry.
func TestCyclicReportPreservesAppendedEvent(t *testing.T) {
	f := newCLIFixture(t)
	dir := filepath.Dir(f.Ledger)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	original := []byte(`timestamp: '2026-11-02T15:24:00Z'
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
lanes: [&a {<<: *a}]
`)
	require.NoError(t, os.WriteFile(reportPath, original, 0600))
	ctx, cancel := context.WithTimeout(context.Background(), cyclicTestDeadline)
	defer cancel()
	command := boundedECICommand(ctx, f.CLI, "ledger-append", "--json", `{"event":"requirement_change","summary":"Scope extended"}`)
	command.Env = append(os.Environ(), "ECI_PROOF_ROOT="+filepath.Join(f.Root, "proof"), "ECI_SESSION_ID=test-capture")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.Output()
	require.NoError(t, ctx.Err(), stderr.String())
	require.NoError(t, err, stderr.String())
	require.Contains(t, string(stdout), "high-level log appended")
	require.Contains(t, stderr.String(), "log append succeeded; status refresh failed")
	require.Contains(t, stderr.String(), "invalid YAML/schema")
	require.NotContains(t, stderr.String(), "stack overflow")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(logged), "\n"))
	require.Contains(t, string(logged), `"event":"requirement_change"`)
	after, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, original, after)
	retry := boundedECICommand(ctx, f.CLI, "status-refresh", "--event", "requirement_change")
	retry.Env = command.Env
	retryOutput, retryErr := retry.CombinedOutput()
	require.NoError(t, ctx.Err(), string(retryOutput))
	require.Error(t, retryErr, string(retryOutput))
	require.Contains(t, string(retryOutput), "invalid YAML/schema")
	require.NotContains(t, string(retryOutput), "stack overflow")
	afterRetry, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, original, afterRetry)
	logAfterRetry, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, logged, logAfterRetry)
}
