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

// TestFlowRootRefreshAddsDiagnosticWithMultilineComment checks publication.
//
// Example: A scope change preserves a commented flow root and applies logged progress.
func TestFlowRootRefreshAddsDiagnosticWithMultilineComment(t *testing.T) {
	t.Parallel()
	var source map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &source))
	source["timeline_state"] = "verified_empty"
	source["timeline"] = []any{}
	source["blockers_risks"] = []any{"existing ] risk"}
	encoded, err := json.Marshal(source)
	require.NoError(t, err)
	input := strings.TrimSuffix(string(encoded), "}") + ", # preserve owner note\n}\n"
	_, err = parse([]byte(input))
	require.NoError(t, err)
	// event supplies independent task and narrative progress.
	//
	// Example: Apply real updates while recording the scope-review diagnostic.
	const event = `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}},{"target":"progress","id":"one","fields":{"text":"verified progress"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(input), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Tasks[0].Status)
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
	require.Contains(t, report.Progress, "verified progress")
	require.Contains(t, report.Blockers, "existing ] risk")
	require.Contains(t, report.Blockers, refreshDiagnostic)
	require.Contains(t, string(result), "# preserve owner note")
	require.False(t, *report.ScopeComplete)
}
