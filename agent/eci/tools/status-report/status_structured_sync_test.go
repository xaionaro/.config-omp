package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStructuredGroupAtomicity checks typed updates and owner conflict preservation.
//
// Example: An effort and impact change is published as one group.
func TestStructuredGroupAtomicity(t *testing.T) {
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1.2","values":{"effort":{"min_minutes":5,"max_minutes":15,"kind":"elapsed","basis":"measured"},"impact":"Useful"}}`), &update))
	require.True(t, validStructuredSyncUpdate(update))
	before, ok := structuredSyncValues([]byte(valid), update)
	require.True(t, ok)
	changed, ok := applyStructuredSyncUpdate([]byte(valid), update, before)
	require.True(t, ok)
	parsed, err := parse(changed)
	require.NoError(t, err)
	require.Equal(t, "Useful", parsed.Tasks[2].Impact)
	require.EqualValues(t, 15, *parsed.Tasks[2].Effort.MaxMinutes)
	_, ok = applyStructuredSyncUpdate(changed, update, before)
	require.False(t, ok, "owner-diverged values must not be overwritten")
	bad := update
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1.2","values":{"effort":{"min_minutes":50,"max_minutes":15,"kind":"elapsed","basis":"guess"},"impact":"Changed"}}`), &bad))
	_, ok = applyStructuredSyncUpdate([]byte(valid), bad, nil)
	require.False(t, ok, "invalid grouped values must remain unapplied")
}

// TestStructuredReportFieldsPreserveUnrelatedBytes checks typed report changes.
//
// Example: Reorder priorities without altering an owner's neighboring comment.
func TestStructuredReportFieldsPreserveUnrelatedBytes(t *testing.T) {
	input := strings.Replace(valid, `top_priorities: ["1.1"]`, `top_priorities: ["1.1"] # owner note`, 1)
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"report","id":"current","values":{"top_priorities":["1.2"],"roster":{"lead":"A"}}}`), &update))
	changed, ok := applyStructuredSyncUpdate([]byte(input), update, nil)
	require.True(t, ok)
	require.Contains(t, string(changed), `top_priorities: ["1.2"] # owner note`)
	require.Contains(t, string(changed), `roster: {"lead":"A"}`)
	require.Contains(t, string(changed), "    title: Unknown\n")
	_, err := parse(changed)
	require.NoError(t, err)
}

// TestStructuredCommentedValueRemainsPending checks unsafe nested replacement.
//
// Example: Preserve a comment inside an effort mapping.
func TestStructuredCommentedValueRemainsPending(t *testing.T) {
	input := strings.Replace(valid, "effort: {min_minutes: 10, max_minutes: 30, kind: elapsed, basis: educated guess}", "effort:\n      min_minutes: 10 # owner note\n      max_minutes: 30\n      kind: elapsed\n      basis: educated guess", 1)
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1.2","values":{"effort":{"min_minutes":5,"max_minutes":15,"kind":"elapsed","basis":"measured"}}}`), &update))
	changed, ok := applyStructuredSyncUpdate([]byte(input), update, nil)
	require.False(t, ok)
	require.Equal(t, input, string(changed))
}

// TestStructuredCancellationReversalRequiresStatus checks atomic restoration.
//
// Example: Reopen a cancelled task by clearing lifecycle and supplying its new status.
func TestStructuredCancellationReversalRequiresStatus(t *testing.T) {
	input := strings.Replace(valid, "    status: unknown\ntop_priorities:", "    status: unknown\n    lifecycle: cancelled\n    evidence: withdrawn\ntop_priorities:", 1)
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1.2","fields":{"status":"IN PROGRESS","context":"resumed"},"clear":["lifecycle"]}`), &update))
	changed, ok := applyStructuredSyncUpdate([]byte(input), update, nil)
	require.True(t, ok)
	parsed, err := parse(changed)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, parsed.Tasks[2].Status)
	require.NotContains(t, string(changed), "lifecycle: cancelled")
	delete(update.Fields, "status")
	_, ok = applyStructuredSyncUpdate([]byte(input), update, nil)
	require.False(t, ok)
}

// TestStructuredClearFlowMapping checks removal and comment safety in inline tasks.
//
// Example: Reopen an inline cancelled task while preserving an owner's comment.
func TestStructuredClearFlowMapping(t *testing.T) {
	start := strings.Index(valid, "  - id: \"1.2\"")
	end := strings.Index(valid, "top_priorities:")
	require.Greater(t, end, start)
	inline := `  - {id: "1.2", parent_id: "1", title: Unknown, priority: unknown, impact: unknown, effort: {min_minutes: 10, max_minutes: 30, kind: elapsed, basis: educated guess}, status: unknown, lifecycle: cancelled, evidence: withdrawn}` + "\n"
	input := valid[:start] + inline + valid[end:]
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1.2","fields":{"status":"IN PROGRESS"},"clear":["lifecycle"]}`), &update))
	changed, ok := applyStructuredSyncUpdate([]byte(input), update, nil)
	require.True(t, ok)
	require.NotContains(t, string(changed), "lifecycle: cancelled")
	require.Contains(t, string(changed), "evidence: withdrawn")
	_, err := parse(changed)
	require.NoError(t, err)
	commented := strings.Replace(input, "lifecycle: cancelled, evidence:", "lifecycle: cancelled, # owner note\n      evidence:", 1)
	changed, ok = applyStructuredSyncUpdate([]byte(commented), update, nil)
	require.False(t, ok)
	require.Equal(t, commented, string(changed))
}

// TestStructuredClearAcceptsPriorScalarOwnership checks cross-format replay ownership.
//
// Example: Reopen a previously projected cancellation without overriding owner changes.
func TestStructuredClearAcceptsPriorScalarOwnership(t *testing.T) {
	input := strings.Replace(valid, "    status: unknown\ntop_priorities:", "    status: unknown\n    lifecycle: cancelled\n    evidence: withdrawn\ntop_priorities:", 1)
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1.2","fields":{"status":"IN PROGRESS"},"clear":["lifecycle"]}`), &update))
	changed, ok := applyStructuredSyncUpdate([]byte(input), update, map[string]string{"lifecycle": "cancelled"})
	require.True(t, ok)
	require.NotContains(t, string(changed), "lifecycle: cancelled")
	_, ok = applyStructuredSyncUpdate([]byte(input), update, map[string]string{"lifecycle": "different"})
	require.False(t, ok)
}

// TestStructuredWorkRefsAcceptsHash checks references are not mistaken for comments.
//
// Example: Replace an existing project record reference.
func TestStructuredWorkRefsAcceptsHash(t *testing.T) {
	var update syncUpdate
	require.NoError(t, json.Unmarshal([]byte(`{"target":"task","id":"1","values":{"work_refs":["project-understanding.yaml#/records/work:second"]}}`), &update))
	changed, ok := applyStructuredSyncUpdate([]byte(linkedReport), update, nil)
	require.True(t, ok)
	parsed, err := parse(changed)
	require.NoError(t, err)
	require.Equal(t, []string{"project-understanding.yaml#/records/work:second"}, parsed.Tasks[0].WorkRefs)
}
