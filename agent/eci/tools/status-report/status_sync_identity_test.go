package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncTaskRejectsIrrelevantSelector checks actual task identity.
//
// Example: A work reference cannot bypass protection of a task owner's edit.
func TestSyncTaskRejectsIrrelevantSelector(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"projected"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first)))
	original, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(original), `"owner": "projected"`, `"owner": "manual"`, 1)
	require.NotEqual(t, string(original), edited)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","work_ref":"project-understanding.yaml#/records/work:first","fields":{"owner":"next"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first+second)))
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
	t.Parallel()
	dir := t.TempDir()
	legacy := strings.Repeat(`{"event":"checkpoint","summary":"legacy"}`+"\n", 2000)
	require.NoError(t, writeSessionLog(dir, []byte(legacy)))
	first, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(first)
	require.NoError(t, err)
	require.Equal(t, 2000, parsed.StatusSync.Cursor)
	typed := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"worker"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(legacy+typed)))
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
				if err := writeSessionLog(dir, []byte(strings.Repeat(`{"event":"checkpoint","summary":"legacy"}`+"\n", count))); err != nil {
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

// TestSyncAddedTaskPreservesLaterOwnerEdits checks field ownership after insertion.
//
// Example: An owner-edited task impact is not replaced by a later event.
func TestSyncAddedTaskPreservesLaterOwnerEdits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Original","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first)))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"impact":"Original"`, `"impact":"Owner edit"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"3","values":{"impact":"Later"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first+second)))
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
	t.Parallel()
	dir := t.TempDir()
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Original","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first)))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"id":"3"`, `"owner":"manual","id":"3"`, 1)
	require.NotEqual(t, string(added), edited)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first+second)))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "manual", parsed.Tasks[2].Owner)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}
