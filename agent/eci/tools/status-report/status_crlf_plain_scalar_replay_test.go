package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestCRLFPlainScalarUpdatePreservesReport checks replay on a block-style CRLF report.
//
// Example: An end-of-line state update preserves adjacent Unicode and comments.
func TestCRLFPlainScalarUpdatePreservesReport(t *testing.T) {
	input := strings.Replace(linkedReport, "state: Working\n", "state: Working λ\n", 1)
	input = strings.Replace(input, "progress: [Implementation started]", "progress: [Implementation started] # owner note", 1)
	input = strings.ReplaceAll(input, "\n", "\r\n")
	// event updates the plain report state and independent progress.
	//
	// Example: Move the report to review without discarding earlier notes.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"state":"Reviewing λ"}},{"target":"progress","id":"one","fields":{"text":"verified progress"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(input), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "Reviewing λ", report.State)
	require.Contains(t, report.Progress, "Implementation started")
	require.Contains(t, report.Progress, "verified progress")
	require.Contains(t, string(result), "# owner note")
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
	require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\n")
}

// TestCRLFLexerPositionsMapToOriginalBytes checks offsets across multibyte text.
//
// Example: A field after CRLF and Unicode still selects its exact original scalar.
func TestCRLFLexerPositionsMapToOriginalBytes(t *testing.T) {
	// input includes UTF-8 and two CRLF boundaries before the target field.
	//
	// Example: Resolve a nested mapping and scalar without shifting either byte span.
	const input = "first: {note: 'λ # }', list: [one, two]}\r\nsecond: {keep: true}\r\nstate: Working λ\r\n"
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(input), &doc))
	second := mapValue(doc.Content[0], "second")
	require.NotNil(t, second)
	start, err := yamlByteOffset([]byte(input), second.Line, second.Column)
	require.NoError(t, err)
	end, err := flowEnd([]byte(input), start, token.MappingStartType)
	require.NoError(t, err)
	require.Equal(t, "{keep: true}", input[start:end+1])
	state := mapValue(doc.Content[0], "state")
	require.NotNil(t, state)
	start, end, err = (&flowDocument{data: []byte(input)}).scalarSpan(state)
	require.NoError(t, err)
	require.Equal(t, "Working λ", input[start:end])
}
