package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMalformedLogEntryDoesNotDiscardFollowingProgress checks append replay isolation.
//
// Example: Diagnose an interrupted JSON entry and retain later independent work.
func TestMalformedLogEntryDoesNotDiscardFollowingProgress(t *testing.T) {
	// broken is a partially written status event followed by a newline.
	//
	// Example: Preserve a diagnostic for an interrupted append.
	const broken = `{"event":"worker_progress","details":{"status_update":{"updates":[` + "\n"
	// later is a complete independent event following the malformed line.
	//
	// Example: A later worker can still record completed progress.
	const later = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"other","fields":{"text":"later progress"}}]}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(broken+later), 0600))
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "Implementation started")
	require.Contains(t, report.Progress, "later progress")
	require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
	require.False(t, *report.ScopeComplete)
	require.Equal(t, 2, report.StatusSync.Cursor)
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
