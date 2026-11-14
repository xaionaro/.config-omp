package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyNarrativeRecoveryPreservesOwnerChanges retries a saved narrative edit.
//
// Example: A matching entry changes while a manually edited entry remains pending.
func TestLegacyNarrativeRecoveryPreservesOwnerChanges(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"old managed text", "manual edit"} {
		t.Run(text,
			// Check one current owner-visible narrative value.
			//
			// Example: An owner edit must not be overwritten.
			func(t *testing.T) {
				// saved is a legacy field-wise narrative update and its owned baseline.
				//
				// Example: Resume a pending text change after an upgrade.
				const saved = `{"cursor":1,"applied":{"[\"progress\",\"work:one\",\"\",\"\"]/text":"old managed text"},"latest":{"[\"progress\",\"work:one\",\"\",\"\"]/text":1},"pending":{"[\"progress\",\"work:one\",\"\",\"\"]/text":{"line":1,"update":{"target":"progress","id":"work:one","fields":{"text":"new managed text"}}}}}`
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
				input := strings.Replace(linkedReport, "progress: [Implementation started]", "progress: [Implementation started, {subject_ref: 'work:one', text: '"+text+"'}] # owner comment", 1)
				require.NotEqual(t, linkedReport, input)
				initial, err := putSyncState([]byte(input), saved)
				require.NoError(t, err)
				result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, string(result), "# owner comment")
				require.Contains(t, report.Progress, "Implementation started")
				if text == "old managed text" {
					require.Contains(t, report.Progress, "new managed text")
					require.NotContains(t, report.Progress, "old managed text")
					require.Empty(t, report.StatusSync.Pending)
				} else {
					require.Contains(t, report.Progress, "manual edit")
					require.NotContains(t, report.Progress, "new managed text")
					require.NotEmpty(t, report.StatusSync.Pending)
				}
			})
	}
}
