package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReviewAndCriticWaitForTaskAcrossRefresh checks persisted dependency order.
//
// Example: A review and its later critic update apply after the missing task arrives.
func TestReviewAndCriticWaitForTaskAcrossRefresh(t *testing.T) {
	// review adds an assessment for a task not yet present in the report.
	//
	// Example: Its later critic update must not bypass the pending addition.
	const review = `{"event":"review_result","details":{"status_update":{"additions":[{"target":"review","record":{"task_id":"3","reporter":"requester","critic":"initial","rounds":1,"estimate":"ten minutes","impact":"proof","priority":"high","unresolved":"none"}}]}}}` + "\n"
	// critic changes the pending review's critic.
	//
	// Example: The later reviewer replaces the initial reviewer after insertion.
	const critic = `{"event":"review_result","details":{"status_update":{"updates":[{"target":"review","id":"3","values":{"critic":"later"}}]}}}` + "\n"
	// task supplies the missing task in a later event.
	//
	// Example: The pending review and update can then apply in order.
	const task = `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(review+critic), 0600))
	require.NoError(t, refreshTestStatus(dir, "review_result"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(pending)
	require.NoError(t, err)
	require.Empty(t, parsed.Reviews)
	require.Len(t, parsed.StatusSync.PendingAdditions, 1)
	require.Len(t, parsed.StatusSync.Groups, 1)
	require.NoError(t, refreshTestStatus(dir, "review_result"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, pending, retry)
	require.NoError(t, os.WriteFile(logPath, []byte(review+critic+task), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Len(t, parsed.Tasks, 3)
	require.Len(t, parsed.Reviews, 1)
	require.Equal(t, "later", parsed.Reviews[0].Critic)
	require.Empty(t, parsed.StatusSync.PendingAdditions)
	require.Empty(t, parsed.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	retry, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
