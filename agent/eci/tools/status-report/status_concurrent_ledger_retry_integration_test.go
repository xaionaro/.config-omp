package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshRetriesWhenLedgerChangesDuringLogReplay checks source snapshot integrity.
//
// Example: A concurrent source edit prevents a stale log projection from publishing.
func TestRefreshRetriesWhenLedgerChangesDuringLogReplay(t *testing.T) {
	t.Parallel()
	// event adds progress while refresh reads its canonical sources.
	//
	// Example: A retry must project the retained event exactly once.
	const event = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"queued claim"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	ledgerPath := filepath.Join(dir, "project-understanding.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	changed := linkedLedger + "# concurrent source note\n"
	observer := testRefreshObserver{Action:
	// mutate changes the source immediately before candidate publication.
	//
	// Example: A concurrent writer adds an owner note to the ledger.
	func() { require.NoError(t, os.WriteFile(ledgerPath, []byte(changed), 0600)) }}
	require.ErrorContains(t, refreshTestLinkedStatus(dir, "decision", observer), "retry needed")
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, linkedReport, string(original))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started", "queued claim"}, report.Progress)
	require.Equal(t, 1, report.StatusSync.Cursor)
	ledger, err := os.ReadFile(ledgerPath)
	require.NoError(t, err)
	require.Equal(t, changed, string(ledger))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
