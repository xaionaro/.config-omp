package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaterAssignmentsDoNotAwaitInvalidCorrectionLocators checks replay ordering.
//
// Example: A later owner never awaits a self-referencing or future correction.
func TestLaterAssignmentsDoNotAwaitInvalidCorrectionLocators(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, locator string
	}{
		{"self", `"corrects":1`},
		{"future", `"corrects":2`},
		{"same ordinal", `"corrects":1,"corrects_ordinal":0`},
		{"later ordinal", `"corrects":1,"corrects_ordinal":1`},
	} {
		t.Run(tc.name,
			// Verify an impossible locator is diagnosed while valid work proceeds.
			//
			// Example: The log retains both the invalid correction and assignment.
			func(t *testing.T) {
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				log := `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1",` + tc.locator + `,"fields":{"owner":"mistaken"}}]}}}` + "\n" + `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"actual"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, writeSessionLog(dir, []byte(log)))
				require.NoError(t, refreshTestStatus(dir, "assignment_change"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, "actual", report.Tasks[0].Owner)
				require.Empty(t, report.StatusSync.Groups)
				require.Contains(t, report.StuckWorkRisks, "Status refresh incomplete: invalid status_update requires manual review")
				require.NoError(t, refreshTestStatus(dir, "assignment_change"))
				retry, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}

// TestRetryDoesNotAwaitPersistedSelfCorrection checks existing queued state.
//
// Example: A previously saved impossible locator is diagnosed and removed.
func TestRetryDoesNotAwaitPersistedSelfCorrection(t *testing.T) {
	t.Parallel()
	// saved is a previously persisted impossible correction and later assignment.
	//
	// Example: Retry removes the invalid group without discarding the assignment.
	const saved = `status_sync: {"cursor":2,"applied":{},"latest":{},"groups":{"[1,0]":{"line":1,"ordinal":0,"update":"{\"target\":\"task\",\"id\":\"1\",\"corrects\":1,\"fields\":{\"owner\":\"mistaken\"}}"},"[2,0]":{"line":2,"ordinal":0,"update":"{\"target\":\"task\",\"id\":\"1\",\"fields\":{\"owner\":\"actual\"}}"}}}`
	// log preserves the two immutable events referenced by the saved state.
	//
	// Example: The correction cannot precede itself.
	const log = `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"mistaken"}}]}}}` + "\n" + `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"actual"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport+saved+"\n"), 0600))
	require.NoError(t, writeSessionLog(dir, []byte(log)))
	require.NoError(t, refreshTestStatus(dir, "assignment_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "actual", report.Tasks[0].Owner)
	require.Empty(t, report.StatusSync.Groups)
	require.Contains(t, report.StuckWorkRisks, "Status refresh incomplete: invalid status_update requires manual review")
}

// TestCorrectionCanNameEarlierSameLineOrdinal checks valid intra-event ordering.
//
// Example: A second correction may name the first correction on the same line.
func TestCorrectionCanNameEarlierSameLineOrdinal(t *testing.T) {
	t.Parallel()
	// log contains two ordered corrections in one canonical event.
	//
	// Example: The last correction names ordinal zero of its own line.
	const log = `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"first"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"second"}},{"target":"task","id":"1","corrects":2,"corrects_ordinal":0,"fields":{"owner":"third"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, writeSessionLog(dir, []byte(log)))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "third", report.Tasks[0].Owner)
	require.Empty(t, report.StatusSync.Groups)
}
