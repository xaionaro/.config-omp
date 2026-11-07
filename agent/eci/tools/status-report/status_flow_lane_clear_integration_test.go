package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFlowLaneClearLastFieldPreservesNeighbors checks an explicit blocker clear.
//
// Example: Clearing the final field keeps the forecast, comment, and second lane.
func TestFlowLaneClearLastFieldPreservesNeighbors(t *testing.T) {
	// lane is a compact lane whose final blocker contains a literal hash.
	//
	// Example: Clearing it retains the owner comment following the flow mapping.
	const lane = `  - {task_id: '1', work_ref: 'project-understanding.yaml#/records/work:first', outcome: First result, required_environment: implementation, implementation_status: IN PROGRESS, test_status: NEW, prod_status: NEW, forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}, blocker: 'issue #42'} # keep lane note` + "\n"
	// clear explicitly removes only the selected lane's blocker.
	//
	// Example: Resolve an implementation blocker without changing another lane.
	const clear = `{"event":"blocker_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","clear":["blocker"]}]}}}` + "\n"
	start := strings.Index(linkedReport, "  - task_id: '1'\n")
	end := strings.Index(linkedReport, "  - task_id: '2'\n")
	require.GreaterOrEqual(t, start, 0)
	require.Greater(t, end, start)
	input := linkedReport[:start] + lane + linkedReport[end:]
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(clear), 0600))
	result, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 2)
	require.Empty(t, report.Lanes[0].Blocker)
	require.NotNil(t, report.Lanes[0].Forecast)
	require.Equal(t, "2026-11-02T16:00:00Z", report.Lanes[0].Forecast.Target)
	require.Equal(t, "project-understanding.yaml#/records/work:second", report.Lanes[1].WorkRef)
	require.Contains(t, string(result), "# keep lane note")
	require.NotContains(t, string(result), "issue #42")
	require.Empty(t, report.StatusSync.Groups)
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
