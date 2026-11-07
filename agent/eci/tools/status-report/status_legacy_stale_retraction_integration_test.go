package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyStaleRetractionCannotRemoveNewerClaim checks replay ordering.
//
// Example: A pending correction to an old claim leaves a newer claim intact.
func TestLegacyStaleRetractionCannotRemoveNewerClaim(t *testing.T) {
	// saved records a stale pending retraction and a later applied text version.
	//
	// Example: Resume older saved metadata after a newer claim was published.
	const saved = `{"cursor":3,"applied":{"[\"progress\",\"work:one\",\"\",\"\"]/text":"newer claim"},"latest":{"[\"progress\",\"work:one\",\"\",\"\"]/text":3,"[\"progress\",\"work:one\",\"\",\"\"]/retract":2},"pending":{"[\"progress\",\"work:one\",\"\",\"\"]/retract":{"line":2,"update":{"target":"progress","id":"work:one","corrects":1,"retract":true,"fields":null}}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n{}\n{}\n"), 0600))
	input := strings.Replace(linkedReport, "progress: [Implementation started]", "progress: [Implementation started, {subject_ref: 'work:one', text: 'newer claim'}] # owner note", 1)
	require.NotEqual(t, linkedReport, input)
	initial, err := putSyncState([]byte(input), saved)
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "newer claim")
	require.Contains(t, report.Progress, "Implementation started")
	require.Contains(t, string(result), "# owner note")
	require.Empty(t, report.StatusSync.Pending)
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
