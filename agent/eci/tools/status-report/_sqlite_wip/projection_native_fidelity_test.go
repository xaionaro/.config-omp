package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNativeProjectionUsesCanonicalForecastAndPreservesOpaqueValues checks one snapshot projection.
//
// Example: Refreshing a forecast keeps an unrelated exact numeric extension intact.
func TestNativeProjectionUsesCanonicalForecastAndPreservesOpaqueValues(t *testing.T) {
	t.Parallel()
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	ledger, err := readSource[ledgerSource](filepath.Join(dir, "project-understanding.yaml"))
	require.NoError(t, err)
	forecasts, err := readSource[forecastSource](filepath.Join(dir, "forecasts.yaml"))
	require.NoError(t, err)
	document, err := replayYAMLToReportJSON([]byte(linkedReport))
	require.NoError(t, err)
	value, err := decodeReportValue(document)
	require.NoError(t, err)
	root := value.(map[string]any)
	opaque, err := decodeReportValue([]byte(`{"values":[1e400,1.10,-0,1E+0,null,"null",[],{}]}`))
	require.NoError(t, err)
	root["owner_extension"] = opaque
	document, err = json.Marshal(root)
	require.NoError(t, err)
	forecasts.Records["first"]["target"] = "2026-11-02T18:00:00Z"
	require.NoError(t, os.Remove(filepath.Join(dir, "project-understanding.yaml")))
	require.NoError(t, os.Remove(filepath.Join(dir, "forecasts.yaml")))
	log, err := captureReplayLog(dir)
	require.NoError(t, err)
	snapshot := projectionSnapshot{Report: document, Sources: reconciliationSnapshot{ledger: ledger, ledgerPresent: true, forecasts: forecasts, forecastPresent: true}}
	result, err := projectDBReport(dir, "decision", snapshot, time.Date(2026, 11, 2, 15, 30, 0, 0, time.UTC), log)
	require.NoError(t, err)
	value, err = decodeReportValue(result)
	require.NoError(t, err)
	root = value.(map[string]any)
	require.Equal(t, opaque, root["owner_extension"])
	lanes := root["lanes"].([]any)
	first := lanes[0].(map[string]any)["forecast"].(map[string]any)
	second := lanes[1].(map[string]any)["forecast"].(map[string]any)
	require.Equal(t, "2026-11-02T18:00:00Z", first["target"])
	require.Equal(t, "2026-11-02T17:00:00Z", second["target"])
}
