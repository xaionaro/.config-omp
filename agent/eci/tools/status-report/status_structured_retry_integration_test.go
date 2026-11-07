package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStructuredTaskUpdateRetriesAfterOwnerResolvesConflict checks atomic replay.
//
// Example: A manually edited effort blocks a paired impact change until restored.
func TestStructuredTaskUpdateRetriesAfterOwnerResolvesConflict(t *testing.T) {
	// first establishes the logged ownership baseline for the task effort.
	//
	// Example: Record a measured estimate before the owner edits the report.
	const first = `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"effort":{"min_minutes":5,"max_minutes":15,"kind":"elapsed","basis":"measured"}}}]}}}` + "\n"
	// second requests an atomic impact and effort change.
	//
	// Example: Keep both fields pending when the effort was manually changed.
	const second = `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"effort":{"min_minutes":8,"max_minutes":18,"kind":"elapsed","basis":"updated"},"impact":"Expanded impact"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	projected, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(projected), `"max_minutes":15`, `"max_minutes":14`, 1)
	require.NotEqual(t, string(projected), edited)
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	pending, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.EqualValues(t, 14, *report.Tasks[0].Effort.MaxMinutes)
	require.Equal(t, "Useful", report.Tasks[0].Impact)
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
	require.Len(t, report.StatusSync.Groups, 1)
	restored := strings.Replace(string(pending), `"max_minutes":14`, `"max_minutes":15`, 1)
	require.NotEqual(t, string(pending), restored)
	result, _, err := projectLogStatus(dir, []byte(restored), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.EqualValues(t, 18, *report.Tasks[0].Effort.MaxMinutes)
	require.Equal(t, "Expanded impact", report.Tasks[0].Impact)
	require.Empty(t, report.StatusSync.Groups)
}
