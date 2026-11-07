package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEarlierPendingRootConflictPreservesOrder checks root transaction isolation.
//
// Example: A later reopening waits for an earlier unresolved retirement of its work.
func TestEarlierPendingRootConflictPreservesOrder(t *testing.T) {
	update := syncUpdate{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", WorkRef: "project-understanding.yaml#/records/w", Evidence: "cancelled", Values: map[string]json.RawMessage{"expected_prior": json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f"}`)}, Clear: []string{"record"}}
	encoded, err := encodeSyncGroup(update)
	require.NoError(t, err)
	state := syncState{Groups: map[string]syncPendingGroup{"first": {Line: 1, Ordinal: 0, Update: encoded}}}
	start := syncPendingRootAddition{Locator: rootLifecycleLocator{Line: 2, Ordinal: 0, Target: syncTargetRootForecast}, Addition: syncAddition{Target: syncTargetRootForecast, ReopenWorkRef: update.WorkRef, Record: json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f"}`)}}
	require.True(t, earlierPendingRootConflict(state, start))
	start.Addition.ReopenWorkRef = "project-understanding.yaml#/records/other"
	require.False(t, earlierPendingRootConflict(state, start))
	state.Groups = nil
	state.PendingAdditions = map[string]syncPendingAddition{"legacy": {Line: 1, Target: syncTargetRootCompletion, Record: `{"work_ref":"project-understanding.yaml#/records/w"}`}}
	start.Addition.ReopenWorkRef = update.WorkRef
	require.True(t, earlierPendingRootConflict(state, start))
}
