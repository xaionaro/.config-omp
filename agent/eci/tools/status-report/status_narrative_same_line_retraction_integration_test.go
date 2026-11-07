package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNarrativeRetractionUsesLatestSameLineOrdinal checks persisted replay locators.
//
// Example: Evidence at ordinal two must be named when retracting an earlier text item.
func TestNarrativeRetractionUsesLatestSameLineOrdinal(t *testing.T) {
	// first records state, narrative text, and its evidence in a single event.
	//
	// Example: The evidence is the latest version of the managed narrative item.
	const first = `{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"state":"Updated"}},{"target":"progress","id":"work","fields":{"text":"managed claim"}},{"target":"progress","id":"work","fields":{"evidence":"latest proof"}}]}}}` + "\n"
	for _, tc := range []struct {
		name, ordinal string
		removes       bool
	}{
		{"correct evidence ordinal", `,"corrects_ordinal":2`, true},
		{"omitted ordinal", "", false},
		{"older text ordinal", `,"corrects_ordinal":1`, false},
	} {
		t.Run(tc.name,
			// check replays one locator against the already persisted source event.
			//
			// Example: A stale locator leaves the managed claim intact.
			func(t *testing.T) {
				second := `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work","corrects":1` + tc.ordinal + `,"retract":true}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				logPath := filepath.Join(dir, "high_level_log.jsonl")
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
				require.NoError(t, refreshTestStatus(dir, "decision"))
				initial, err := os.ReadFile(path)
				require.NoError(t, err)
				before, err := parse(initial)
				require.NoError(t, err)
				require.Contains(t, before.Progress, "managed claim (evidence: latest proof)")
				require.NoError(t, os.WriteFile(logPath, []byte(first+second), 0600))
				require.NoError(t, refreshTestStatus(dir, "correction"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				after, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, "Updated", after.State)
				require.Equal(t, 2, after.StatusSync.Cursor)
				if tc.removes {
					require.Equal(t, []string{"Implementation started"}, after.Progress)
				} else {
					require.Contains(t, after.Progress, "managed claim (evidence: latest proof)")
					require.Contains(t, after.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
				}
				require.NoError(t, refreshTestStatus(dir, "correction"))
				retry, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}
