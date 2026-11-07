package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKeyedNarrativeRendersText checks structured and legacy narrative entries together.
//
// Example: Stable subjects support updates without exposing map formatting to readers.
func TestKeyedNarrativeRendersText(t *testing.T) {
	input := strings.Replace(linkedReport, "progress: [Implementation started]", "progress: [legacy, {subject_ref: 'work:one', text: Done, evidence: proof}]", 1)
	input = strings.Replace(input, "verification: [pending]", "verification: [{subject_ref: 'work:one', text: Passed}]", 1)
	r, err := parse([]byte(input))
	require.NoError(t, err)
	out := render(r)
	require.Contains(t, out, "legacy")
	require.Contains(t, out, "Done")
	require.Contains(t, out, "proof")
	require.Contains(t, out, "Passed")
	require.NotContains(t, out, "map[")
}

// TestTimelineAsOfPreservesHistoricalBins checks the independent timeline anchor.
//
// Example: A later report timestamp retains an older window and labels it historical.
func TestTimelineAsOfPreservesHistoricalBins(t *testing.T) {
	input := linkedReport + "timeline_as_of: '2026-11-02T15:24:00Z'\ntimeline_state: present\ntimeline:\n  - {job: root, parent_chain: root, start: '2026-11-02T14:54:00Z', end: '2026-11-02T15:24:00Z', fast: work, normal: work}\n"
	input = strings.Replace(input, "timestamp: '2026-11-02T15:24:00Z'", "timestamp: '2026-11-02T16:24:00Z'", 1)
	r, err := parse([]byte(input))
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(render(r)), "historical")
	bad := strings.Replace(input, "timeline_as_of: '2026-11-02T15:24:00Z'", "timeline_as_of: '2026-11-02T17:24:00Z'", 1)
	_, err = parse([]byte(bad))
	require.Error(t, err)
}

// TestKeyedNarrativeRejectsTrailingDocument protects the single-document contract.
//
// Example: Rendering a keyed report followed by another YAML document fails.
func TestKeyedNarrativeRejectsTrailingDocument(t *testing.T) {
	input := strings.Replace(linkedReport, "progress: [Implementation started]", "progress: [{subject_ref: 'work:one', text: Done}]", 1)
	_, err := parse([]byte(input))
	require.NoError(t, err)
	invalid := input + "\n---\nstate: concealed\n"
	_, err = parse([]byte(invalid))
	require.Error(t, err)
	binary := filepath.Join(t.TempDir(), "status-report")
	build := exec.Command("go", "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	command := exec.Command(binary, "render")
	command.Stdin = strings.NewReader(invalid)
	output, err = command.CombinedOutput()
	require.Error(t, err, string(output))
	require.NotContains(t, string(output), "Full task tree")
}
