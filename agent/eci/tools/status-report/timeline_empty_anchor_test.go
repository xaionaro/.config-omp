package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVerifiedEmptyTimelineRemainsHistorical checks the empty timeline's actual coverage interval.
//
// Example: Advancing a report cannot present yesterday's verified absence as current.
func TestVerifiedEmptyTimelineRemainsHistorical(t *testing.T) {
	input := linkedReport + "timeline_state: verified_empty\ntimeline: []\n"
	updated, err := advanceSyncTimestamp([]byte(input), "2026-11-02T15:34:00Z")
	require.NoError(t, err)
	r, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T15:24:00Z", r.TimelineAsOf)
	require.Equal(t, timelineVerifiedEmpty, r.TimelineState)
	out := render(r)
	require.Contains(t, out, "verified no evidenced activity")
	require.Contains(t, out, "Historical timeline as of 2026-11-02T15:24:00Z")
	require.Contains(t, out, "coverage may be incomplete")
}

// TestUnknownEmptyTimelineRemainsUnknown checks empty data does not invent verified coverage.
//
// Example: A missing timeline anchor remains missing when its state is unknown.
func TestUnknownEmptyTimelineRemainsUnknown(t *testing.T) {
	input := linkedReport + "timeline: []\n"
	updated, err := advanceSyncTimestamp([]byte(input), "2026-11-02T15:34:00Z")
	require.NoError(t, err)
	r, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, timelineUnknown, r.TimelineState)
	require.Empty(t, r.TimelineAsOf)
	require.NotContains(t, strings.ToLower(render(r)), "verified no evidenced activity")
}

// TestTimelineStateJSONPreservesLabels verifies snapshot enum wire compatibility.
//
// Example: Unknown wire labels fail rather than becoming a valid state.
func TestTimelineStateJSONPreservesLabels(t *testing.T) {
	for _, state := range []timelineState{timelinePresent, timelineVerifiedEmpty} {
		encoded, err := json.Marshal(state)
		require.NoError(t, err)
		require.JSONEq(t, `"`+state.String()+`"`, string(encoded))
		var decoded timelineState
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Equal(t, state, decoded)
	}
	var state timelineState
	require.Error(t, json.Unmarshal([]byte(`"unexpected"`), &state))
	require.Error(t, json.Unmarshal([]byte(`42`), &state))
}
