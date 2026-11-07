package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshPreservesWrappedBlocker checks insertion after the complete YAML scalar.
//
// Example: A wrapped plain risk retains its text and gains a separate diagnostic.
func TestRefreshPreservesWrappedBlocker(t *testing.T) {
	cases := []struct {
		Name  string
		Block string
		Value string
	}{
		{Name: "plain wrapped", Block: "  - Long risk whose text\n    continues on the next line.\n", Value: "Long risk whose text continues on the next line."},
		{Name: "single quoted wrapped", Block: "  - 'Long risk whose text\n    continues on the next line.'\n", Value: "Long risk whose text continues on the next line."},
		{Name: "double quoted wrapped", Block: "  - \"Long risk whose text\n    continues on the next line.\"\n", Value: "Long risk whose text continues on the next line."},
		{Name: "single line", Block: "  - Existing risk\n", Value: "Existing risk"},
	}
	for _, tc := range cases {
		t.Run(tc.Name,
			// Callback exercises one YAML scalar layout.
			//
			// Example: Preserve a wrapped quoted risk and the following comment.
			func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "latest-status-report.yaml")
				report := strings.Replace(linkedReport, "blockers_risks: []\nverification:", "blockers_risks:\n"+tc.Block+"# verification note\nverification:", 1)
				require.Contains(t, report, tc.Block)
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
				require.NoError(t, refreshTestStatus(dir, "requirement_change"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, []string{tc.Value, refreshDiagnostic}, parsed.Blockers)
				require.False(t, *parsed.ScopeComplete)
				require.Contains(t, string(result), tc.Block)
				require.Contains(t, string(result), "# verification note\nverification:")
				require.NoError(t, refreshTestStatus(dir, "requirement_change"))
				again, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, again)
			})
	}
}

// TestRefreshPreservesLastWrappedBlocker checks a trailing block sequence.
//
// Example: Insert before a trailing comment without mixing CRLF line endings.
func TestRefreshPreservesLastWrappedBlocker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	report := strings.Replace(linkedReport, "blockers_risks: []\n", "", 1)
	report += "blockers_risks:\n  - Long trailing risk\n    continues here.\n# keep trailing note\n"
	report = strings.ReplaceAll(report, "\n", "\r\n")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, parsed.Blockers, "Long trailing risk continues here.")
	require.Contains(t, parsed.Blockers, refreshDiagnostic)
	require.False(t, *parsed.ScopeComplete)
	require.Contains(t, string(result), "    continues here.\r\n")
	require.Contains(t, string(result), "# keep trailing note\r\n")
	require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\n")
}
