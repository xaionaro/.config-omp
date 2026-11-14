package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Cycle test process deadlines bound parser and refresh regressions.
//
// Example: Stop a cyclic parser that fails to return.
const (
	// cycleTestDeadline bounds each built-CLI cycle probe.
	//
	// Example: Terminate a stalled parser within three seconds.
	cycleTestDeadline = 3 * time.Second
)

// TestYAMLCyclesTerminate checks cyclic aliases through the built CLI under a deadline.
//
// Example: A self-referential alias must fail rather than loop indefinitely.
func TestYAMLCyclesTerminate(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "status-report")
	build := exec.Command("go", "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	for _, tc := range []struct {
		name, extra string
		valid       bool
	}{
		{name: "self", extra: "lanes: [&a {<<: *a}]\n"},
		{name: "indirect", extra: "lanes: [&a {<<: &b {<<: *a}}]\n"},
		{name: "merge sequence", extra: "lanes: [&a {<<: [*a]}]\n"},
		{name: "alias DAG", extra: "lanes: []\ndetails: {one: &a [value], two: *a}\n", valid: true},
		{name: "shared mapping DAG", extra: "details: {defaults: &d {outcome: X, required_environment: implementation, implementation_status: NEW, test_status: NEW, prod_status: NEW}}\nlanes:\n  - {<<: *d, task_id: '1.1'}\n  - {<<: *d, task_id: '1.2'}\n", valid: true},
	} {
		t.Run(tc.name,
			// Run a fixture with a strict process deadline.
			//
			// Example: A valid alias graph renders normally.
			func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), cycleTestDeadline)
				defer cancel()
				command := exec.CommandContext(ctx, binary, "render")
				command.Stdin = strings.NewReader(strings.Replace(valid, "lanes: []\n", tc.extra, 1))
				output, err := command.CombinedOutput()
				require.NoError(t, ctx.Err(), "CLI timed out: %s", output)
				if tc.valid {
					require.NoError(t, err, string(output))
					require.Contains(t, string(output), "Full task tree")
				} else {
					require.Error(t, err, string(output))
					require.NotContains(t, string(output), "stack overflow")
					require.NotContains(t, string(output), "Full task tree")
				}
			})
	}
}

// TestRefreshRejectsCyclicReportWithoutWriting checks refresh leaves an invalid source report untouched.
//
// Example: A self-merged lane must fail before publication.
func TestRefreshRejectsCyclicReportWithoutWriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	original := strings.Replace(valid, "lanes: []\n", "lanes: [&a {<<: *a}]\n", 1)
	require.NoError(t, os.WriteFile(path, []byte(original), 0600))
	binary := filepath.Join(t.TempDir(), "status-report")
	build := exec.Command("go", "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	ctx, cancel := context.WithTimeout(context.Background(), cycleTestDeadline)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "refresh", dir, "requirement_change")
	output, err = command.CombinedOutput()
	require.NoError(t, ctx.Err(), string(output))
	require.Error(t, err)
	require.NotContains(t, string(output), "stack overflow")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, string(after))
}

// TestLifecycleMergeOverride checks resolved merge precedence and explicit null rejection.
//
// Example: An explicit valid lifecycle overrides a null merged default.
func TestLifecycleMergeOverride(t *testing.T) {
	t.Parallel()
	prefix := "details: {defaults: &defaults {lifecycle: null}}\n"
	lane := "lanes:\n  - {<<: *defaults, task_id: '1', outcome: X, required_environment: implementation, implementation_status: NEW, test_status: NEW, prod_status: NEW"
	_, err := parse([]byte(strings.Replace(valid, "lanes: []\n", prefix+lane+"}\n", 1)))
	require.Error(t, err)
	_, err = parse([]byte(strings.Replace(valid, "lanes: []\n", prefix+lane+", lifecycle: ''}\n", 1)))
	require.NoError(t, err)
	validDefault := strings.Replace(prefix, "lifecycle: null", "lifecycle: ''", 1)
	_, err = parse([]byte(strings.Replace(valid, "lanes: []\n", validDefault+lane+", lifecycle: null}\n", 1)))
	require.Error(t, err)
}
