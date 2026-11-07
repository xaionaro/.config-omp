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

// TestUnlinkedActiveForecastSurfacesInCLI checks canonical source coverage.
//
// Example: An orphan active forecast is diagnosed until its owner retires it.
func TestUnlinkedActiveForecastSurfacesInCLI(t *testing.T) {
	// orphan is a valid forecast that no work record references.
	//
	// Example: An inactive orphan does not represent untracked active work.
	const orphan = `  orphan:
    kind: root
    outcome: Untracked result
    status: active
    assessment: complete
    target: '2026-11-02T18:00:00Z'
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	forecasts := linkedForecasts + orphan
	dir := writeLinkedSources(t, linkedLedger, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "active canonical forecast orphan is not linked to work")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "active canonical forecast orphan is not linked to work")
	retired := strings.Replace(forecasts, "outcome: Untracked result\n    status: active", "outcome: Untracked result\n    status: inactive", 1)
	require.NotEqual(t, forecasts, retired)
	require.NoError(t, os.WriteFile(forecastPath, []byte(retired), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
}
