package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIncompleteTailRecoveryPreservesIndependentOwnerEdit checks retry isolation.
//
// Example: A completed log tail applies after the owner revises earlier progress.
func TestIncompleteTailRecoveryPreservesIndependentOwnerEdit(t *testing.T) {
	t.Parallel()
	// prior creates managed progress before an interrupted append.
	//
	// Example: The owner may later revise this independently of the new event.
	const prior = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"initial claim"}}]}}}` + "\n"
	// next adds a different progress item when its partial log line is completed.
	//
	// Example: Recovery must not overwrite an owner edit to the earlier item.
	const next = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"two","fields":{"text":"recovered claim"}}]}}}`
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	split := strings.Index(next, `"recovered claim"`)
	require.Greater(t, split, 0)
	require.NoError(t, os.WriteFile(logPath, []byte(prior+next[:split]), 0600))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	pending, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	edited := strings.Replace(string(pending), `text: "initial claim"`, `text: "owner revision"`, 1)
	require.NotEqual(t, string(pending), edited)
	require.NoError(t, os.WriteFile(reportPath, []byte(edited), 0600))
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = file.WriteString(next[split:] + "\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started", "owner revision", "recovered claim"}, report.Progress)
	require.Equal(t, 2, report.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "decision"))
	retry, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
