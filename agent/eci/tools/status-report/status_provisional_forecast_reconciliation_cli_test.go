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

// TestProvisionalForecastNeedsCanonicalBasisInCLI checks uncertainty disclosure.
//
// Example: A provisional forecast is incomplete until its source and projection carry a basis.
func TestProvisionalForecastNeedsCanonicalBasisInCLI(t *testing.T) {
	t.Parallel()
	// original identifies the first lane's complete forecast.
	//
	// Example: Make its assessment provisional while retaining all other fields.
	const original = "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}"
	provisional := strings.Replace(original, "assessment: complete", "assessment: provisional", 1)
	report := strings.Replace(linkedReport, original, provisional, 1)
	require.NotEqual(t, linkedReport, report)
	forecasts := strings.Replace(linkedForecasts, "assessment: complete", "assessment: provisional", 1)
	require.NotEqual(t, linkedForecasts, forecasts)
	dir := writeLinkedSources(t, linkedLedger, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	var out, errOut bytes.Buffer
	now := time.Date(2026, 11, 2, 15, 24, 0, 0, time.UTC)
	require.Equal(t, 1, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first provisional source missing target_basis")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "forecast first provisional source missing target_basis")
	fixedSource := strings.Replace(forecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: conditional on review\n", 1)
	require.NotEqual(t, forecasts, fixedSource)
	fixedForecast := strings.TrimSuffix(provisional, "}") + ", source_projection: {target_basis: conditional on review}}"
	fixedReport := strings.Replace(report, provisional, fixedForecast, 1)
	require.NotEqual(t, report, fixedReport)
	require.NoError(t, os.WriteFile(forecastPath, []byte(fixedSource), 0600))
	require.NoError(t, os.WriteFile(path, []byte(fixedReport), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"validate", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.Contains(t, out.String(), "source reconciliation complete")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, runAt([]string{"render", path}, strings.NewReader(""), &out, &errOut, now), errOut.String())
	require.NotContains(t, out.String(), "INCOMPLETE — source reconciliation")
	require.Contains(t, out.String(), "conditional on review")
}
