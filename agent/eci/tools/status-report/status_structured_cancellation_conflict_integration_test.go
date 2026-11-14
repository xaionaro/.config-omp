package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStructuredCancellationWaitsForOwnerEvidence checks atomic recovery.
//
// Example: A changed reason blocks the whole cancellation until its owner restores it.
func TestStructuredCancellationWaitsForOwnerEvidence(t *testing.T) {
	t.Parallel()
	// prior records the reason that sync owns before the cancellation.
	//
	// Example: A later owner edit to this reason must not be overwritten.
	const prior = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"evidence":"original reason"}}]}}}` + "\n"
	// cancel groups the lifecycle change with its revised impact and effort.
	//
	// Example: No part of this update should publish if evidence conflicts.
	const cancel = `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"lifecycle":"cancelled","status":"unknown","evidence":"scope withdrawn"},"values":{"effort":{"min_minutes":0,"max_minutes":0,"kind":"work","basis":"cancelled"},"impact":"Removed from current scope"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(prior), 0600))
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.NoError(t, refreshStatusAt(dir, "worker_progress", now))
	owned, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(owned), `"evidence": "original reason"`, `"evidence": "owner changed reason"`, 1)
	require.NotEqual(t, string(owned), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(prior+cancel), 0600))
	require.NoError(t, refreshStatusAt(dir, "lifecycle_change", now))
	pendingBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	pending, err := parse(pendingBytes)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, pending.Tasks[0].Status)
	require.Equal(t, "owner changed reason", pending.Tasks[0].Evidence)
	require.Equal(t, "Useful", pending.Tasks[0].Impact)
	require.NotNil(t, pending.Tasks[0].Effort.MinMinutes)
	require.Equal(t, fractionalMinutes(10), *pending.Tasks[0].Effort.MinMinutes)
	require.Contains(t, pending.TopPriorities, taskID("1"))
	require.NotEmpty(t, pending.StatusSync.Groups)
	restored := strings.Replace(string(pendingBytes), `"evidence": "owner changed reason"`, `"evidence": "original reason"`, 1)
	require.NotEqual(t, string(pendingBytes), restored)
	require.NoError(t, os.WriteFile(path, []byte(restored), 0600))
	require.NoError(t, refreshStatusAt(dir, "lifecycle_change", now))
	resultBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	result, err := parse(resultBytes)
	require.NoError(t, err)
	require.Equal(t, taskLifecycleCancelled, result.Tasks[0].Lifecycle)
	require.Equal(t, statusUnknown, result.Tasks[0].Status)
	require.Equal(t, "scope withdrawn", result.Tasks[0].Evidence)
	require.Equal(t, "Removed from current scope", result.Tasks[0].Impact)
	require.NotNil(t, result.Tasks[0].Effort.MinMinutes)
	require.NotNil(t, result.Tasks[0].Effort.MaxMinutes)
	require.Equal(t, fractionalMinutes(0), *result.Tasks[0].Effort.MinMinutes)
	require.Equal(t, fractionalMinutes(0), *result.Tasks[0].Effort.MaxMinutes)
	require.Equal(t, []taskID{"2"}, result.TopPriorities)
	require.Equal(t, statusInProgress, result.Tasks[1].Status)
	require.Equal(t, "Useful", result.Tasks[1].Impact)
	require.Empty(t, result.StatusSync.Groups)
	require.NoError(t, refreshStatusAt(dir, "lifecycle_change", now))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, resultBytes, retry)
}
