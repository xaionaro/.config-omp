package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyNarrativeEvidenceWaitsForOwnedText checks source attribution on retry.
//
// Example: Preserve a manual claim edit before applying its pending proof update.
func TestLegacyNarrativeEvidenceWaitsForOwnedText(t *testing.T) {
	t.Parallel()
	// saved contains the managed claim and a pending proof change.
	//
	// Example: Restore evidence ownership after an interrupted upgrade.
	const saved = `{"cursor":1,"applied":{"[\"verification\",\"work:one\",\"\",\"\"]/text":"verified claim","[\"verification\",\"work:one\",\"\",\"\"]/evidence":"old proof"},"latest":{"[\"verification\",\"work:one\",\"\",\"\"]/evidence":1},"pending":{"[\"verification\",\"work:one\",\"\",\"\"]/evidence":{"line":1,"update":{"target":"verification","id":"work:one","fields":{"evidence":"new proof"}}}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
	original := strings.Replace(linkedReport, "verification: [pending]", "verification: [pending, {subject_ref: 'work:one', text: 'manual edit', evidence: 'old proof'}] # owner note", 1)
	require.NotEqual(t, linkedReport, original)
	initial, err := putSyncState([]byte(original), saved)
	require.NoError(t, err)
	pending, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Contains(t, report.Verification, "manual edit (evidence: old proof)")
	require.Contains(t, string(pending), "old proof")
	require.NotContains(t, string(pending), `"evidence": "new proof"`)
	require.NotEmpty(t, report.StatusSync.Pending)
	restored := strings.Replace(string(pending), "text: 'manual edit'", "text: 'verified claim'", 1)
	require.NotEqual(t, string(pending), restored)
	applied, _, err := projectLogStatus(dir, []byte(restored), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err = parse(applied)
	require.NoError(t, err)
	require.Contains(t, report.Verification, "verified claim (evidence: new proof)")
	require.Contains(t, string(applied), "new proof")
	require.Contains(t, string(applied), "# owner note")
	require.Empty(t, report.StatusSync.Pending)
}
