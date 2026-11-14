package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSharedTaskLanesRequireDistinctWorkReferences checks lane identity in the CLI.
//
// Example: Two lanes can share a task while each projects a different work record.
func TestSharedTaskLanesRequireDistinctWorkReferences(t *testing.T) {
	t.Parallel()
	taskRef := "work_refs: ['project-understanding.yaml#/records/work:first']"
	sharedTask := "work_refs: ['project-understanding.yaml#/records/work:first', 'project-understanding.yaml#/records/work:second']"
	report := strings.Replace(linkedReport, taskRef, sharedTask, 1)
	report = strings.Replace(report, "  - task_id: '2'\n", "  - task_id: '1'\n", 1)
	require.NotEqual(t, linkedReport, report)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "First result")
	require.Contains(t, out.String(), "Second result")
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
	duplicate := strings.Replace(report, "    work_ref: 'project-understanding.yaml#/records/work:second'", "    work_ref: 'project-understanding.yaml#/records/work:first'", 1)
	require.NotEqual(t, report, duplicate)
	require.NoError(t, os.WriteFile(path, []byte(duplicate), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "duplicate lane for work:first")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "duplicate lane for work:first")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
}
