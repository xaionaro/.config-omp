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

// TestSwappedLaneForecastReferencesSurfaceInCLI checks canonical identity.
//
// Example: Two valid forecast references still fail when attached to the wrong lanes.
func TestSwappedLaneForecastReferencesSurfaceInCLI(t *testing.T) {
	t.Parallel()
	swapped := strings.Replace(linkedReport, "forecast_ref: 'forecasts.yaml#/records/first'", "forecast_ref: 'forecasts.yaml#/records/temp'", 1)
	swapped = strings.Replace(swapped, "forecast_ref: 'forecasts.yaml#/records/second'", "forecast_ref: 'forecasts.yaml#/records/first'", 1)
	swapped = strings.Replace(swapped, "forecast_ref: 'forecasts.yaml#/records/temp'", "forecast_ref: 'forecasts.yaml#/records/second'", 1)
	require.NotEqual(t, linkedReport, swapped)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(swapped), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first reference mismatch")
	require.Contains(t, out.String(), "forecast second reference mismatch")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first reference mismatch")
	require.Contains(t, out.String(), "forecast second reference mismatch")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
}
