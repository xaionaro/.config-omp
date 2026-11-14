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

// TestCancelledDualRoleWorkReconcilesWithoutProjection checks lifecycle transitions.
//
// Example: Cancelled work needs no lane or root projection until it resumes.
func TestCancelledDualRoleWorkReconcilesWithoutProjection(t *testing.T) {
	t.Parallel()
	first := strings.Index(linkedReport, "  - task_id: '1'\n")
	second := strings.Index(linkedReport, "  - task_id: '2'\n")
	require.GreaterOrEqual(t, first, 0)
	require.Greater(t, second, first)
	omitted := linkedReport[:first] + linkedReport[second:]
	activeLedger := strings.Replace(linkedLedger, "reporting_roles: [lane]", "reporting_roles: [lane, root]", 1)
	cancelledLedger := strings.Replace(activeLedger, "owner: one, state: active", "owner: one, state: cancelled", 1)
	inactive := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	require.NotEqual(t, linkedLedger, activeLedger)
	require.NotEqual(t, activeLedger, cancelledLedger)
	require.NotEqual(t, linkedForecasts, inactive)
	dir := writeLinkedSources(t, cancelledLedger, inactive)
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	ledgerPath := filepath.Join(dir, "project-understanding.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(reportPath, []byte(omitted), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 0, runAt([]string{"validate", reportPath}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", reportPath}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
	require.NoError(t, os.WriteFile(forecastPath, []byte(linkedForecasts), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 1, runAt([]string{"validate", reportPath}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "closed work has active forecast first")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", reportPath}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "closed work has active forecast first")
	require.NoError(t, os.WriteFile(ledgerPath, []byte(activeLedger), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 1, runAt([]string{"validate", reportPath}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "declared lane work:first missing from report")
	require.Contains(t, out.String(), "active root work:first forecast missing from report")
	require.NoError(t, os.WriteFile(reportPath, []byte(linkedReport), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", reportPath}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", reportPath}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
	require.Contains(t, out.String(), "Forecast deadline: First result will be finished by 2026-11-02T16:00:00Z")
}
