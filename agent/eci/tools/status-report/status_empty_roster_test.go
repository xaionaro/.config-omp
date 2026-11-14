package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmptyRosterReportsUnavailable checks that missing roster evidence is explicit.
//
// Example: An empty roster does not claim that no agents are active.
func TestEmptyRosterReportsUnavailable(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(valid))
	require.NoError(t, err)
	require.Contains(t, render(r), "Roster: unavailable (no roster evidence supplied).")
	r.Roster = map[string]any{"lead": "working"}
	require.Contains(t, render(r), "lead: working")
	require.NotContains(t, render(r), "Roster: unavailable")
}
