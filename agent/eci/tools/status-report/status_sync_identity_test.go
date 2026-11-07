package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncTaskRejectsIrrelevantSelector checks actual task identity.
//
// Example: A work reference cannot bypass protection of a task owner's edit.
func TestSyncTaskRejectsIrrelevantSelector(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"projected"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	original, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(original), `"owner": "projected"`, `"owner": "manual"`, 1)
	require.NotEqual(t, string(original), edited)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","work_ref":"project-understanding.yaml#/records/work:first","fields":{"owner":"next"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "manual", report.Tasks[0].Owner)
}

// TestSyncLegacyBatchAdvancesCursor checks ordered recovery after many legacy events.
//
// Example: A later typed assignment follows two thousand previously untyped events.
func TestSyncLegacyBatchAdvancesCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	legacy := strings.Repeat(`{"event":"checkpoint","summary":"legacy"}`+"\n", 2000)
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0600))
	first, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(first)
	require.NoError(t, err)
	require.Equal(t, 2000, parsed.StatusSync.Cursor)
	typed := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"worker"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(legacy+typed), 0600))
	updated, _, err := projectLogStatus(dir, first, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err = parse(updated)
	require.NoError(t, err)
	require.Equal(t, 2001, parsed.StatusSync.Cursor)
	require.Equal(t, "worker", parsed.Tasks[0].Owner)
	retry, _, err := projectLogStatus(dir, updated, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, updated, retry)
}

// BenchmarkSyncLegacyBatch measures per-refresh scanning of legacy entries.
//
// Example: Compare one- and two-thousand-entry batches on the same runner.
func BenchmarkSyncLegacyBatch(b *testing.B) {
	for _, count := range []int{1000, 2000} {
		b.Run(fmt.Sprint(count),
			// Measure a fixed-size source log against the same report snapshot.
			//
			// Example: Replay a two-thousand-entry legacy log.
			func(b *testing.B) {
				dir := b.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(strings.Repeat(`{"event":"checkpoint","summary":"legacy"}`+"\n", count)), 0600); err != nil {
					b.Fatal(err)
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{}); err != nil {
						b.Fatal(err)
					}
				}
			})
	}
}

// TestSyncMigratesLegacyTaskSelectorOwnership checks existing session protection.
//
// Example: Previously recorded aliases cannot bypass later owner edits.
func TestSyncMigratesLegacyTaskSelectorOwnership(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0600))
	selector := syncUpdate{Target: syncTargetTask, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first"}
	// A prior version incorrectly included the irrelevant work reference in this key.
	encoded, _ := json.Marshal([]string{"task", "1", selector.WorkRef, ""})
	alias := string(encoded) + "/owner"
	state := syncState{Cursor: 1, Applied: map[string]string{alias: "projected"}, Latest: map[string]int{alias: 1}, Pending: map[string]syncPending{}}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	report := strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", `status: IN PROGRESS, owner: manual, work_refs:`, 1)
	reportBytes, err := putSyncState([]byte(report), string(raw))
	require.NoError(t, err)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"next"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte("{}\n"+second), 0600))
	result, _, err := projectLogStatus(dir, reportBytes, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "manual", parsed.Tasks[0].Owner)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncLegacyAliasConflictDoesNotDiscardOwnership checks ambiguous history.
//
// Example: Conflicting task aliases require reconciliation instead of guessing.
func TestSyncLegacyAliasConflictDoesNotDiscardOwnership(t *testing.T) {
	canonical := syncTargetKey(syncUpdate{Target: syncTargetTask, ID: "1"}, "owner")
	encoded, err := json.Marshal([]string{"task", "1", "legacy work reference", ""})
	require.NoError(t, err)
	alias := string(encoded) + "/owner"
	state := syncState{Applied: map[string]string{canonical: "first", alias: "second"}}
	require.ErrorContains(t, normalizeSyncOwnership(&state), "conflicting legacy status ownership")
	require.Len(t, state.Applied, 2)
}

// TestSyncLegacyQueuedAliasUsesActualTaskIdentity checks pending compatibility.
//
// Example: A previously queued update retains its intent after alias migration.
func TestSyncLegacyQueuedAliasUsesActualTaskIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n{}\n"), 0600))
	update := syncUpdate{Target: syncTargetTask, ID: "1", WorkRef: "legacy reference", Fields: map[string]string{"owner": "next"}}
	encodedUpdate, err := encodeSyncGroup(update)
	require.NoError(t, err)
	selector, _ := json.Marshal([]string{"task", "1", "legacy reference", ""})
	alias := string(selector) + "/owner"
	state := syncState{Cursor: 2, Applied: map[string]string{alias: "projected"}, Latest: map[string]int{alias: 1}, Pending: map[string]syncPending{}, Groups: map[string]syncPendingGroup{"legacy": {Line: 2, Update: encodedUpdate}}}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	report := strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", "status: IN PROGRESS, owner: projected, work_refs:", 1)
	initial, err := putSyncState([]byte(report), string(raw))
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "next", parsed.Tasks[0].Owner)
	require.Empty(t, parsed.StatusSync.Groups)
}

// TestSyncAddedTaskPreservesLaterOwnerEdits checks field ownership after insertion.
//
// Example: An owner-edited task impact is not replaced by a later event.
func TestSyncAddedTaskPreservesLaterOwnerEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Original","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"impact":"Original"`, `"impact":"Owner edit"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"3","values":{"impact":"Later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "Owner edit", parsed.Tasks[2].Impact)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncAddedTaskPreservesOwnerAddedField checks initially absent fields.
//
// Example: A manually assigned owner is not replaced by a later event.
func TestSyncAddedTaskPreservesOwnerAddedField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Original","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"id":"3"`, `"owner":"manual","id":"3"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "manual", parsed.Tasks[2].Owner)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}
