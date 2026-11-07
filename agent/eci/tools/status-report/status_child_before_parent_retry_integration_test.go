package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChildAndAssignmentWaitForParentAcrossRefresh checks persisted hierarchy order.
//
// Example: A child and its later owner assignment apply after the parent is created.
func TestChildAndAssignmentWaitForParentAcrossRefresh(t *testing.T) {
	// child is a valid nested task whose parent does not yet exist.
	//
	// Example: Its addition remains pending until task 3 is present.
	const child = `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3.1","parent_id":"3","title":"Child","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	// assignment follows the child before its parent is available.
	//
	// Example: The owner assignment must wait for the child record.
	const assignment = `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3.1","fields":{"owner":"child owner"}}]}}}` + "\n"
	// parent introduces the missing task without reappending earlier events.
	//
	// Example: Both queued child operations can now apply in order.
	const parent = `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"Parent","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(child+assignment), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, report.Tasks, 2)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	require.Len(t, report.StatusSync.Groups, 1)
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, pending, retry)
	require.NoError(t, os.WriteFile(logPath, []byte(child+assignment+parent), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.Tasks, 4)
	require.Equal(t, taskID("3"), report.Tasks[2].ID)
	require.Equal(t, taskID("3.1"), report.Tasks[3].ID)
	require.Equal(t, taskID("3"), report.Tasks[3].ParentID)
	require.Equal(t, "child owner", report.Tasks[3].Owner)
	require.Equal(t, []string{"Implementation started"}, report.Progress)
	require.NotNil(t, report.ScopeComplete)
	require.False(t, *report.ScopeComplete)
	require.Empty(t, report.StatusSync.PendingAdditions)
	require.Empty(t, report.StatusSync.Groups)
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	retry, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
