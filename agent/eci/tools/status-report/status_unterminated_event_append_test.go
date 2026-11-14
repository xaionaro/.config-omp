package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCompleteUnterminatedEventThenAppendKeepsCursor checks normal log continuation.
//
// Example: Inserting the missing newline does not replay or skip either event.
func TestCompleteUnterminatedEventThenAppendKeepsCursor(t *testing.T) {
	t.Parallel()
	// first is a valid JSON event stored without a trailing newline.
	//
	// Example: Refresh a manually supplied final entry before another append.
	const first = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first claim"}}]}}}`
	// second is the later append after newline normalization.
	//
	// Example: Publish a separate claim without duplicating the first.
	const second = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"two","fields":{"text":"second claim"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	initial, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(initial)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "first claim")
	require.NotContains(t, report.Progress, "second claim")
	require.Equal(t, 1, report.StatusSync.Cursor)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = file.WriteString("\n" + second)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started", "first claim", "second claim"}, report.Progress)
	require.Equal(t, 2, report.StatusSync.Cursor)
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
