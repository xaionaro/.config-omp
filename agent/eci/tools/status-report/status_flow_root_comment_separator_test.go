package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestFlowRootReplayPreservesCommentsAroundSeparators checks YAML flow punctuation.
//
// Example: A trailing comma before a comment is not duplicated during replay.
func TestFlowRootReplayPreservesCommentsAroundSeparators(t *testing.T) {
	// event supplies a status update and requires new sync metadata.
	//
	// Example: Replay a completed task in an annotated flow-style report.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}}]}}}` + "\n"
	var source map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &source))
	input, err := json.Marshal(source)
	require.NoError(t, err)
	for _, tc := range []struct{ name, suffix string }{
		{"comma before comment", ", # keep\n}"},
		{"comma inside comment", " # keep,\n}"},
	} {
		t.Run(tc.name,
			// Replay into one commented flow mapping.
			//
			// Example: Preserve the existing comment and both tasks.
			func(t *testing.T) {
				reportText := strings.TrimSuffix(string(input), "}") + tc.suffix + "\n"
				_, err := parse([]byte(reportText))
				require.NoError(t, err)
				comments, err := yamlComments([]byte(reportText))
				require.NoError(t, err)
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				result, _, err := projectLogStatus(dir, []byte(reportText), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, statusClosed, report.Tasks[0].Status)
				require.Equal(t, "accepted", report.Tasks[0].Evidence)
				require.Equal(t, statusInProgress, report.Tasks[1].Status)
				require.Contains(t, string(result), "# keep")
				afterComments, err := yamlComments(result)
				require.NoError(t, err)
				require.Equal(t, comments, afterComments)
				require.Equal(t, 1, report.StatusSync.Cursor)
			})
	}
}
