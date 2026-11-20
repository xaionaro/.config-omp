package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSyncEmptyAndNullOwnersPreserveLaterEdits checks distinct ownership states.
//
// Example: An empty or null owner cannot be mistaken for unowned state.
func TestSyncEmptyAndNullOwnersPreserveLaterEdits(t *testing.T) {
	t.Parallel()
	for _, owner := range []string{`""`, `null`} {
		t.Run(owner,
			// Check both unchanged and manually edited values for one representation.
			//
			// Example: An unchanged empty value permits the requested assignment.
			func(t *testing.T) {
				dir := t.TempDir()
				first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW","owner":` + owner + `}}]}}}` + "\n"
				second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
				require.NoError(t, writeSessionLog(dir, []byte(first)))
				added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				require.NoError(t, writeSessionLog(dir, []byte(first+second)))
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

// TestSyncNullOwnerAndLiteralNullAreDistinct checks YAML scalar identity.
//
// Example: An owner changing null to the string "null" retains that edit.
func TestSyncNullOwnerAndLiteralNullAreDistinct(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW","owner":null}}]}}}` + "\n"
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first)))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"owner":null`, `"owner":"null"`, 1)
	require.NotEqual(t, string(added), edited)
	require.NoError(t, writeSessionLog(dir, []byte(first+second)))
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
	t.Parallel()
	dir := t.TempDir()
	first := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW","owner":"null"}}]}}}` + "\n"
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"3","fields":{"owner":"later"}}]}}}` + "\n"
	require.NoError(t, writeSessionLog(dir, []byte(first)))
	added, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(added), `"owner":"null"`, `"owner":null`, 1)
	require.NotEqual(t, string(added), edited)
	require.NoError(t, writeSessionLog(dir, []byte(first+second)))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, parsed.Tasks[2].Owner)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}
