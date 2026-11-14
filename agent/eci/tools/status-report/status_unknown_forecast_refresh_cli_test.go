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

// TestUnknownForecastRefreshUsesCanonicalSource checks public placeholder recovery.
//
// Example: Refresh replaces an unknown deadline with the active canonical target.
func TestUnknownForecastRefreshUsesCanonicalSource(t *testing.T) {
	t.Parallel()
	original := "target: '2026-11-02T16:00:00Z', assessment: complete"
	report := strings.Replace(linkedReport, original, "target: unknown, assessment: complete", 1)
	require.NotEqual(t, linkedReport, report)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first has unknown target or assessment")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "Forecast deadline: First result — unknown")
	out.Reset()
	errOut.Reset()
	require.NoError(t, refreshStatusAt(dir, "forecast_change", now))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "Forecast deadline: First result will be finished by 2026-11-02T16:00:00+00:00")
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
}
