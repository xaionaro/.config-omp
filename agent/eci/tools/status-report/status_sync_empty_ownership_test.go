package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncEmptyAndNullOwnersPreserveLaterEdits checks distinct ownership states.
//
// Example: An empty or null owner cannot be mistaken for unowned state.
func TestSyncEmptyAndNullOwnersPreserveLaterEdits(t *testing.T) {
	for _, owner := range []string{`""`, `null`} {
		t.Run(owner,
			// Check both unchanged and manually edited values for one representation.
			//
			// Example: An unchanged empty value permits the requested assignment.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "high_level_log.jsonl")
				first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW","owner":` + owner + `}}]}}}` + "\n"
				second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(path, []byte(first), 0600))
				added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
				unchanged, _, err := projectLogStatus(dir, added, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(unchanged)
				require.NoError(t, err)
				require.Equal(t, "later", report.Tasks[2].Owner)
				edited := strings.Replace(string(added), `"owner":`+owner, `"owner":"manual"`, 1)
				require.NotEqual(t, string(added), edited)
				result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err = parse(result)
				require.NoError(t, err)
				require.Equal(t, "manual", report.Tasks[2].Owner)
				require.NotEmpty(t, report.StatusSync.Groups)
			})
	}
}

// TestSyncLegacyEmptyOwnerRemainsProtected checks saved field-wise replay state.
//
// Example: A legacy pending assignment preserves an owner-added value.
func TestSyncLegacyEmptyOwnerRemainsProtected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0600))
	update := syncUpdate{Target: syncTargetTask, ID: "1", Fields: map[string]string{"owner": "later"}}
	key := syncTargetKey(update, "owner")
	state := syncState{Cursor: 1, Applied: map[string]string{key: ""}, Latest: map[string]int{key: 1}, Pending: map[string]syncPending{key: {Line: 1, Update: update}}}
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	report := strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", "status: IN PROGRESS, owner: manual, work_refs:", 1)
	initial, err := putSyncState([]byte(report), string(encoded))
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "manual", parsed.Tasks[0].Owner)
	require.NotEmpty(t, parsed.StatusSync.Pending)
}

// TestSyncNullOwnerAndLiteralNullAreDistinct checks YAML scalar identity.
//
// Example: An owner changing null to the string "null" retains that edit.
func TestSyncNullOwnerAndLiteralNullAreDistinct(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW","owner":null}}]}}}` + "\n"
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"owner":null`, `"owner":"null"`, 1)
	require.NotEqual(t, string(added), edited)
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "null", parsed.Tasks[2].Owner)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncLiteralNullOwnerAndNullAreDistinct checks the reverse type change.
//
// Example: An owner changing the string "null" to null retains that edit.
func TestSyncLiteralNullOwnerAndNullAreDistinct(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW","owner":"null"}}]}}}` + "\n"
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"owner":"null"`, `"owner":null`, 1)
	require.NotEqual(t, string(added), edited)
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, parsed.Tasks[2].Owner)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncAmbiguousLegacyNullOwnerStaysPending preserves older ambiguous metadata.
//
// Example: A legacy "null" baseline cannot establish whether an owner edited its type.
func TestSyncAmbiguousLegacyNullOwnerStaysPending(t *testing.T) {
	for _, owner := range []string{`null`, `"null"`} {
		t.Run(owner,
			// Keep each ambiguous historical representation unchanged.
			//
			// Example: A quoted null remains the owner's literal string.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "high_level_log.jsonl")
				first := `{}` + "\n"
				second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"later"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
				update := syncUpdate{Target: syncTargetTask, ID: "1"}
				key := syncTargetKey(update, "owner")
				state := syncState{Cursor: 1, Applied: map[string]string{key: "null"}, Latest: map[string]int{key: 1}, Pending: map[string]syncPending{}}
				encoded, err := json.Marshal(state)
				require.NoError(t, err)
				report := strings.Replace(linkedReport, "status: IN PROGRESS, work_refs:", "status: IN PROGRESS, owner: "+owner+", work_refs:", 1)
				initial, err := putSyncState([]byte(report), string(encoded))
				require.NoError(t, err)
				result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.NotEmpty(t, parsed.StatusSync.Groups)
				if owner == `"null"` {
					require.Equal(t, "null", parsed.Tasks[0].Owner)
				} else {
					require.Empty(t, parsed.Tasks[0].Owner)
				}
			})
	}
}
