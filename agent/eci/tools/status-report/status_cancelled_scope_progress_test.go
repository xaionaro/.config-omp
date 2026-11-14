package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCancelledLeavesDoNotHideRemainingAcceptance checks the scored scope.
//
// Example: A cancelled sibling cannot hide an open lane for a closed leaf.
func TestCancelledLeavesDoNotHideRemainingAcceptance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		cancelled bool
		first     workStatus
		wantIssue bool
	}{
		{"cancelled sibling", true, statusClosed, true},
		{"open sibling", false, statusClosed, false},
		{"open first", true, statusInProgress, false},
	} {
		t.Run(tc.name,
			// Check whether the selected leaf statuses expose remaining acceptance.
			//
			// Example: A cancelled sibling does not count as an open leaf.
			func(t *testing.T) {
				r, err := parse([]byte(linkedReport))
				require.NoError(t, err)
				r.Tasks[0].Status = tc.first
				if tc.cancelled {
					r.Tasks[1].Lifecycle = taskLifecycleCancelled
					r.Tasks[1].Status = statusUnknown
					r.Tasks[1].Evidence = "withdrawn"
				}
				issues := testReconcile(r, writeLinkedSources(t, linkedLedger, linkedForecasts))
				diagnostic := "work work:first remaining acceptance is represented by an open lane outside scored leaf packages"
				if tc.wantIssue {
					require.Contains(t, issues, diagnostic)
					r.Incomplete = issues
					require.Contains(t, render(r), "partial coverage")
				} else {
					require.NotContains(t, issues, diagnostic)
				}
			})
	}
}

// TestCancelledSiblingWithTerminalWorkKeepsCompleteProgress checks a terminal scope.
//
// Example: Accepted work and a withdrawn sibling need no remaining lane warning.
func TestCancelledSiblingWithTerminalWorkKeepsCompleteProgress(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(linkedReport))
	require.NoError(t, err)
	r.Tasks[0].Status = statusClosed
	r.Tasks[0].Evidence = "accepted"
	r.Tasks[1].Lifecycle = taskLifecycleCancelled
	r.Tasks[1].Status = statusUnknown
	r.Tasks[1].Evidence = "withdrawn"
	r.TopPriorities = []taskID{"1"}
	r.Lanes = r.Lanes[:1]
	r.Lanes[0].Forecast = nil
	r.Lanes[0].ImplementationStatus = statusClosed
	r.Lanes[0].CompletedAt = "2026-11-02T15:00:00Z"
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish", "state: completed, completed_at: '2026-11-02T15:00:00Z'", 1)
	ledger = strings.Replace(ledger, "state: active, next_action: Finish", "state: cancelled", 1)
	forecasts := strings.ReplaceAll(linkedForecasts, "status: active", "status: inactive")
	issues := testReconcile(r, writeLinkedSources(t, ledger, forecasts))
	require.Empty(t, issues)
	require.Contains(t, render(r), "100% (1/1 known leaf tasks")
	require.NotContains(t, render(r), "partial coverage")
}
