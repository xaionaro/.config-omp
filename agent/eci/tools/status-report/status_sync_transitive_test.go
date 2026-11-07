package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReviewTransitiveDependency keeps overlapping transactions in order.
//
// Example: An owner conflict blocks an atomic owner and evidence update and its dependent closure.
func TestReviewTransitiveDependency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"projected"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	initial, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(initial), `"owner": "projected"`, `"owner": "manual"`, 1)
	require.NotEqual(t, string(initial), edited)
	second := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"later"}}]}}}` + "\n"
	third := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"third","evidence":"waiting"}}]}}}` + "\n"
	fourth := `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+second+third+fourth), 0600))
	out, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Tasks[0].Status, "closure must wait for the preceding pending evidence transaction")
	require.Equal(t, "manual", report.Tasks[0].Owner)
	require.Len(t, report.StatusSync.Groups, 3)
}
