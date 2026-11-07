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

// TestFlowRootCRLFRefreshPreservesContent checks real refresh on a CRLF flow report.
//
// Example: A logged closure and diagnostic retain comments, UTF-8 and line endings.
func TestFlowRootCRLFRefreshPreservesContent(t *testing.T) {
	var source map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &source))
	source["state"] = "Working λ"
	source["timeline_state"] = "verified_empty"
	source["timeline"] = []any{}
	source["blockers_risks"] = []any{"existing ] risk"}
	encoded, err := json.Marshal(source)
	require.NoError(t, err)
	input := strings.TrimSuffix(string(encoded), "}") + ", # preserve owner note\r\n}\r\n"
	wrapped := strings.Replace(input, `,"tasks":`, ",\r\n\"tasks\":", 1)
	require.NotEqual(t, input, wrapped)
	input = wrapped
	beforeComments, err := yamlComments([]byte(input))
	require.NoError(t, err)
	// event closes one task and adds progress before refresh publishes a scope diagnostic.
	//
	// Example: Update the selected task without altering its sibling.
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
	require.Equal(t, "Working λ", report.State)
	require.Contains(t, report.Progress, "verified progress")
	require.Contains(t, report.Blockers, "existing ] risk")
	require.Contains(t, report.Blockers, refreshDiagnostic)
	require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\n")
	afterComments, err := yamlComments(result)
	require.NoError(t, err)
	require.Equal(t, beforeComments, afterComments)
}
