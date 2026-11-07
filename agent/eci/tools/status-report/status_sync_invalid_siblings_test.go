package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMalformedStatusSiblingDoesNotDiscardValidClosure checks independent decoding.
//
// Example: A malformed target remains diagnosed while a later sibling closes its task.
func TestMalformedStatusSiblingDoesNotDiscardValidClosure(t *testing.T) {
	dir := t.TempDir()
	event := `{"event":"decision","details":{"status_update":{"updates":[{"target":42,"id":"bad","fields":{"owner":"x"}},{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted safe sibling"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	r, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, statusClosed, r.Tasks[0].Status)
	require.Equal(t, "accepted safe sibling", r.Tasks[0].Evidence)
	require.NotNil(t, r.ScopeComplete)
	require.False(t, *r.ScopeComplete)
	require.Contains(t, r.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
	require.Equal(t, 1, r.StatusSync.Cursor)
	again, _, err := projectLogStatus(dir, out, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, string(out), string(again))
}

// TestMalformedStatusItemsRetainOrdinalsAndAtomicity checks decoding at item boundaries.
//
// Example: A partially decoded item is discarded while later valid additions retain their position.
func TestMalformedStatusItemsRetainOrdinalsAndAtomicity(t *testing.T) {
	var event syncEvent
	raw := `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","owner":[1]}},{"target":"task","id":"2","fields":{"owner":"next"}}],"additions":[{"target":3,"record":{}},{"target":"task","record":{"id":"3"}}]}}}`
	require.NoError(t, json.Unmarshal([]byte(raw), &event))
	require.True(t, event.InvalidStatus)
	require.Len(t, event.Details.StatusUpdate.Updates, 2)
	require.Equal(t, syncUpdate{}, event.Details.StatusUpdate.Updates[0])
	require.Equal(t, "2", event.Details.StatusUpdate.Updates[1].ID)
	require.Len(t, event.Details.StatusUpdate.Additions, 2)
	require.Equal(t, syncAddition{}, event.Details.StatusUpdate.Additions[0])
	require.Equal(t, syncTargetTask, event.Details.StatusUpdate.Additions[1].Target)

	var partial syncEvent
	require.NoError(t, json.Unmarshal([]byte(`{"details":{"status_update":{"updates":7,"timeline":false,"additions":[{"target":"task","record":{"id":"3"}}]}}}`), &partial))
	require.True(t, partial.InvalidStatus)
	require.Len(t, partial.Details.StatusUpdate.Additions, 1)
	require.Equal(t, syncTargetTask, partial.Details.StatusUpdate.Additions[0].Target)
}
