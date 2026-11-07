package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelledTaskLifecycle checks cancellation rendering and validation.
//
// Example: A cancelled task is excluded from remaining work.
func TestCancelledTaskLifecycle(t *testing.T) {
	cancelled := strings.Replace(valid, "status: unknown\ntop_priorities:", "status: unknown\n    lifecycle: cancelled\n    evidence: user withdrew\ntop_priorities:", 1)
	cancelled = strings.Replace(cancelled, `top_priorities: ["1.1"]`, `top_priorities: ["1.2"]`, 1)
	r, err := parse([]byte(cancelled))
	require.NoError(t, err)
	out := render(r)
	require.Contains(t, out, "lifecycle: cancelled")
	require.Contains(t, out, "1/1 known leaf tasks")
	require.NotContains(t, strings.Split(out, "Full task tree:")[0], "1.2 Unknown")
	require.False(t, buildTaskWorkIndex(r.Tasks, nil).reportOpen)
	for _, text := range []string{
		strings.Replace(cancelled, "    evidence: user withdrew\n", "", 1),
		strings.Replace(cancelled, "status: unknown\n    lifecycle: cancelled", "status: CLOSED\n    lifecycle: cancelled", 1),
		strings.Replace(cancelled, "lifecycle: cancelled", "lifecycle: mysterious", 1),
	} {
		_, err := parse([]byte(text))
		require.Error(t, err)
	}
}

// TestCancelledParentKeepsChildAndReportsContradiction checks child preservation.
//
// Example: An active child of a cancelled parent remains visible.
func TestCancelledParentKeepsChildAndReportsContradiction(t *testing.T) {
	doc := strings.Replace(valid, "status: IN PROGRESS\n  - id:", "status: unknown\n    lifecycle: cancelled\n    evidence: user withdrew\n  - id:", 1)
	r, err := parse([]byte(doc))
	require.NoError(t, err)
	out := render(r)
	require.Contains(t, out, "1.2 Unknown")
	require.Contains(t, out, "cancelled task 1 has nonterminal child 1.2")
	require.True(t, buildTaskWorkIndex(r.Tasks, nil).reportOpen)
}
