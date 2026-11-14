package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLeafWorkCoverage checks that each scored leaf has its own or inherited canonical work reference.
//
// Example: A sibling's reference does not cover another task.
func TestLeafWorkCoverage(t *testing.T) {
	t.Parallel()
	report := strings.Replace(valid, "scope_complete: false", "scope_complete: true", 1)
	report = strings.Replace(report, "status: unknown", "status: IN PROGRESS", 1)
	ledger := `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:one: {section: progress, type: outcome, data: {description: Work}}
  work:one:
    section: progress
    type: work
    links: {outcomes: ['outcome:one']}
    data: {owner: one, state: active, next_action: Finish, reporting_roles: []}
`
	ref := "work_refs: ['project-understanding.yaml#/records/work:one']"
	for _, tc := range []struct {
		name, doc, diagnostic string
	}{
		{name: "sibling cannot cover open leaf", doc: strings.Replace(report, "title: Complete", "title: Complete\n    "+ref, 1), diagnostic: "scored leaf 1.2 lacks a valid inherited work reference"},
		{name: "sibling cannot cover closed leaf", doc: strings.Replace(report, "title: Unknown", "title: Unknown\n    "+ref, 1), diagnostic: "scored leaf 1.1 lacks a valid inherited work reference"},
		{name: "ancestor covers both", doc: strings.Replace(report, "title: Root", "title: Root\n    "+ref, 1)},
	} {
		t.Run(tc.name,
			// Check coverage for one task tree.
			//
			// Example: Both leaves inherit an ancestor's source reference.
			func(t *testing.T) {
				out := lifecycleResult(t, tc.doc, ledger, "")
				if tc.diagnostic != "" {
					require.Contains(t, out, "INCOMPLETE")
					require.Contains(t, out, tc.diagnostic)
				} else {
					require.NotContains(t, out, "INCOMPLETE")
				}
			})
	}
}

// TestOpenReportedLeafExpiresEstimate checks remaining report work even when its broader source record is closed.
//
// Example: A nonclosed child retains an elapsed-priority warning.
func TestOpenReportedLeafExpiresEstimate(t *testing.T) {
	t.Parallel()
	report := strings.Replace(valid, "scope_complete: false", "scope_complete: true", 1)
	report = strings.Replace(report, "title: Root", "title: Root\n    work_refs: ['project-understanding.yaml#/records/work:one']", 1)
	report = strings.Replace(report, "status: unknown", "status: IN PROGRESS", 1)
	report = strings.Replace(report, "priority: unknown, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T15:24:00Z\"", "priority: unknown, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T14:00:00Z\"", 1)
	ledger := `schema: project-understanding/v1
sections: {progress: {title: Progress}}
records:
  outcome:one: {section: progress, type: outcome, data: {description: Work}}
  work:one:
    section: progress
    type: work
    links: {outcomes: ['outcome:one']}
    data: {owner: one, state: completed, next_action: none, reporting_roles: []}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(ledger), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, testRun([]string{"render", path}, strings.NewReader(""), &out, &errOut), errOut.String())
	require.Contains(t, out.String(), "INCOMPLETE")
	require.Contains(t, out.String(), "endpoint elapsed")
	out.Reset()
	errOut.Reset()
	require.NotEqual(t, 0, testRun([]string{"validate", path}, strings.NewReader(""), &out, &errOut))
}
