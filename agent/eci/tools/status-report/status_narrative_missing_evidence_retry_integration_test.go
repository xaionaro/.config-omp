package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNarrativeRetractionWaitsForOwnerEvidenceRestoration checks persisted ownership.
//
// Example: A deleted proof prevents retraction until the owner restores it.
func TestNarrativeRetractionWaitsForOwnerEvidenceRestoration(t *testing.T) {
	t.Parallel()
	// first publishes one managed claim with its supporting evidence.
	//
	// Example: Later removal requires both owned fields to remain intact.
	const first = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"managed claim","evidence":"original proof"}}]}}}` + "\n"
	// second requests an exact retraction of that prior claim.
	//
	// Example: A missing owner-visible proof makes the retraction wait.
	const second = `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one","corrects":1,"retract":true}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	initial, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(initial), `, "evidence": "original proof"`, "", 1)
	require.NotEqual(t, string(initial), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first+second), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "managed claim")
	require.NotEmpty(t, report.StatusSync.Groups)
	restored := strings.Replace(string(pending), `text: "managed claim"`, `text: "managed claim", evidence: "original proof"`, 1)
	require.NotEqual(t, string(pending), restored)
	require.NoError(t, os.WriteFile(path, []byte(restored), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started"}, report.Progress)
	require.Empty(t, report.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "correction"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
