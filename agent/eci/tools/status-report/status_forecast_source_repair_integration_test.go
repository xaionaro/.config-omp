package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRefreshRecoversLogAfterForecastSourceRepair checks failed-refresh replay.
//
// Example: An invalid forecast file is repaired before the retained event is retried.
func TestRefreshRecoversLogAfterForecastSourceRepair(t *testing.T) {
	t.Parallel()
	// event records progress while a required canonical source is temporarily invalid.
	//
	// Example: Repairing the source permits the same event to project once.
	const event = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"queued while source broken"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	forecastPath := filepath.Join(dir, "forecasts.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, os.WriteFile(forecastPath, []byte("schema: forecasts/v1\nrecords: [broken\n"), 0600))
	require.Error(t, refreshTestStatus(dir, "decision"))
	failed, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(failed)
	require.NoError(t, err)
	require.NotContains(t, report.Progress, "queued while source broken")
	require.NotEmpty(t, report.Blockers)
	require.NoError(t, os.WriteFile(forecastPath, []byte(linkedForecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "decision"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started", "queued while source broken"}, report.Progress)
	require.Equal(t, 1, report.StatusSync.Cursor)
	require.NoError(t, refreshTestStatus(dir, "decision"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
