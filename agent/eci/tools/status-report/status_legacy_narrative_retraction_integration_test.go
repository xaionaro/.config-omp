package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyNarrativeRetractionRecoveryPreservesOwnerChanges checks a saved correction.
//
// Example: Withdraw only an unchanged managed claim and preserve adjacent prose.
func TestLegacyNarrativeRetractionRecoveryPreservesOwnerChanges(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"old managed text", "manual edit"} {
		t.Run(text,
			// Check one current owner-visible narrative value.
			//
			// Example: An owner edit prevents the pending retraction.
			func(t *testing.T) {
				// saved is a pending legacy retraction and its original owned text.
				//
				// Example: Resume a correction after the report format is upgraded.
				const saved = `{"cursor":2,"applied":{"[\"progress\",\"work:one\",\"\",\"\"]/text":"old managed text"},"latest":{"[\"progress\",\"work:one\",\"\",\"\"]/text":1,"[\"progress\",\"work:one\",\"\",\"\"]/retract":2},"pending":{"[\"progress\",\"work:one\",\"\",\"\"]/retract":{"line":2,"update":{"target":"progress","id":"work:one","corrects":1,"retract":true,"fields":null}}}}`
				dir := t.TempDir()
				log := `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"work:one","fields":{"text":"old managed text"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work:one","corrects":1,"retract":true}]}}}` + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
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
					require.NotContains(t, report.Progress, text)
					require.Empty(t, report.StatusSync.Pending)
				} else {
					require.Contains(t, report.Progress, text)
					require.NotEmpty(t, report.StatusSync.Pending)
				}
			})
	}
}
