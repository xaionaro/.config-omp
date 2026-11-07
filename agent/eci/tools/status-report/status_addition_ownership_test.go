package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskAdditionOwnershipPreservesSuppliedAndAbsentValues checks replay baselines.
//
// Example: A later update detects edits to a newly added task's owner and effort.
func TestTaskAdditionOwnershipPreservesSuppliedAndAbsentValues(t *testing.T) {
	record := json.RawMessage(`{"id":"1.2","title":"Unknown","priority":"unknown","impact":"unknown","effort":{"kind":"elapsed","max_minutes":30,"min_minutes":10,"basis":"educated guess"},"status":"unknown","owner":"Alice"}`)
	values, ok := taskAdditionOwnership(record)
	require.True(t, ok)
	require.Equal(t, "Alice", values["owner"])
	require.Equal(t, `{"basis":"educated guess","kind":"elapsed","max_minutes":30,"min_minutes":10}`, values["effort"])
	require.Equal(t, syncAbsentValue, values["context"])
	require.NotContains(t, values, "id")
	_, ok = taskAdditionOwnership(json.RawMessage(`{"owner":[]}`))
	require.False(t, ok)
}

// TestTaskAdditionOwnershipRejectsLaterOwnerEdit checks its use by projection.
//
// Example: A manual effort edit blocks a subsequent logged effort update.
func TestTaskAdditionOwnershipRejectsLaterOwnerEdit(t *testing.T) {
	record := json.RawMessage(`{"effort":{"min_minutes":10,"max_minutes":30,"kind":"elapsed","basis":"educated guess"}}`)
	previous, ok := taskAdditionOwnership(record)
	require.True(t, ok)
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1.2","values":{"effort":{"min_minutes":5,"max_minutes":15,"kind":"elapsed","basis":"measured"}}}`), &update))
	_, ok = applyStructuredSyncUpdate([]byte(valid), update, map[string]string{"effort": previous["effort"]})
	require.True(t, ok)
	edited := strings.Replace(valid, "max_minutes: 30, kind: elapsed, basis: educated guess", "max_minutes: 29, kind: elapsed, basis: educated guess", 1)
	_, ok = applyStructuredSyncUpdate([]byte(edited), update, map[string]string{"effort": previous["effort"]})
	require.False(t, ok)
}

// TestAdditionOwnershipMatchesTargetEncodings checks each updateable addition target.
//
// Example: Remember a review critic and a lane blocker as separate owned fields.
func TestAdditionOwnershipMatchesTargetEncodings(t *testing.T) {
	cases := []struct {
		target                  syncTarget
		record, field, expected string
	}{
		{syncTargetTask, `{"status":null}`, "status", syncNullValue},
		{syncTargetReview, `{"critic":"reviewer","rounds":2}`, "critic", `"reviewer"`},
		{syncTargetPriorityEstimate, `{"min_minutes":2}`, "min_minutes", `2`},
		{syncTargetLane, `{"blocker":"waiting"}`, "blocker", "waiting"},
		{syncTargetLane, `{"stage":"implementation"}`, "stage", "implementation"},
	}
	for _, tc := range cases {
		values, ok := additionOwnership(tc.target, json.RawMessage(tc.record))
		require.True(t, ok)
		require.Equal(t, tc.expected, values[tc.field])
	}
}

// TestReviewAdditionOwnershipRejectsLaterOwnerEdit checks structured review updates.
//
// Example: Preserve a manually changed critic after review insertion.
func TestReviewAdditionOwnershipRejectsLaterOwnerEdit(t *testing.T) {
	record := `{"task_id":"1.1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}`
	previous, ok := additionOwnership(syncTargetReview, json.RawMessage(record))
	require.True(t, ok)
	input := strings.Replace(valid, "reviews: []", "reviews: ["+record+"]", 1)
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"review","id":"1.1","values":{"critic":"C"}}`), &update))
	_, ok = applyStructuredSyncUpdate([]byte(input), update, map[string]string{"critic": previous["critic"]})
	require.True(t, ok)
	edited := strings.Replace(input, `"critic":"B"`, `"critic":"owner"`, 1)
	_, ok = applyStructuredSyncUpdate([]byte(edited), update, map[string]string{"critic": previous["critic"]})
	require.False(t, ok)
}

// TestScalarSyncOwnershipDistinguishesNull checks stored scalar identity.
//
// Example: Preserve an owner's literal "null" after a projected null.
func TestScalarSyncOwnershipDistinguishesNull(t *testing.T) {
	nullValues, ok := additionOwnership(syncTargetTask, json.RawMessage(`{"owner":null}`))
	require.True(t, ok)
	textValues, ok := additionOwnership(syncTargetTask, json.RawMessage(`{"owner":"null"}`))
	require.True(t, ok)
	require.Equal(t, syncNullValue, nullValues["owner"])
	require.Equal(t, syncLiteralNullValue, textValues["owner"])
	require.NotEqual(t, nullValues["owner"], textValues["owner"])
	require.Equal(t, syncLiteralNullValue, scalarSyncStoredValue("null"))
	require.Equal(t, "other", scalarSyncStoredValue("other"))
}
