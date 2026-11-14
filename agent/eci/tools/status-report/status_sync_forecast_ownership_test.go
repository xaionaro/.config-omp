package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSyncForecastOwnershipSurvivesRetireAndRestore checks implicit version tracking.
//
// Example: A later closure preserves an owner's edit to a restored forecast.
func TestSyncForecastOwnershipSurvivesRetireAndRestore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	closeEvent := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	reopen := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	var ledger ledgerSource
	var forecasts forecastSource
	require.NoError(t, yaml.Unmarshal([]byte(linkedLedger), &ledger))
	require.NoError(t, yaml.Unmarshal([]byte(linkedForecasts), &forecasts))
	work := ledger.Records["work:first"]
	work.Data.State, work.Data.CompletedAt = workStateCompleted, "2026-11-02T15:25:00Z"
	ledger.Records["work:first"] = work
	forecasts.Records["first"]["status"] = "inactive"
	require.NoError(t, os.WriteFile(path, []byte(closeEvent), 0600))
	closed, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	require.NoError(t, err)
	parsed, err := parse(closed)
	require.NoError(t, err)
	selector := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	key := syncTargetKey(selector, "forecast")
	require.Equal(t, syncAbsentValue, parsed.StatusSync.Applied[key])
	require.Equal(t, 1, parsed.StatusSync.Latest[key])
	work.Data.State, work.Data.CompletedAt = workStateActive, ""
	ledger.Records["work:first"] = work
	forecasts.Records["first"]["status"] = "active"
	require.NoError(t, os.WriteFile(path, []byte(closeEvent+reopen), 0600))
	reopened, _, err := projectLogStatus(dir, closed, ledger, forecasts)
	require.NoError(t, err)
	parsed, err = parse(reopened)
	require.NoError(t, err)
	require.Equal(t, 2, parsed.StatusSync.Latest[key])
	require.Contains(t, parsed.StatusSync.Applied[key], "forecast_ref")
	edited := strings.Replace(string(reopened), `"outcome":"First result"`, `"outcome":"owner edit"`, 1)
	require.NotEqual(t, string(reopened), edited)
	work.Data.State, work.Data.CompletedAt = workStateCompleted, "2026-11-02T15:25:00Z"
	ledger.Records["work:first"] = work
	forecasts.Records["first"]["status"] = "inactive"
	require.NoError(t, os.WriteFile(path, []byte(closeEvent+reopen+closeEvent), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledger, forecasts)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, parsed.Lanes[0].ImplementationStatus)
	require.Equal(t, "owner edit", parsed.Lanes[0].Forecast.Outcome)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestSyncForecastOwnershipTracksCanonicalRefresh checks source-driven recalibration.
//
// Example: A later closure recognizes a forecast changed by canonical refresh.
func TestSyncForecastOwnershipTracksCanonicalRefresh(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	closeEvent := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	reopen := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	var ledger ledgerSource
	var forecasts forecastSource
	require.NoError(t, yaml.Unmarshal([]byte(linkedLedger), &ledger))
	require.NoError(t, yaml.Unmarshal([]byte(linkedForecasts), &forecasts))
	work := ledger.Records["work:first"]
	work.Data.State, work.Data.CompletedAt = workStateCompleted, "2026-11-02T15:25:00Z"
	ledger.Records["work:first"] = work
	forecasts.Records["first"]["status"] = "inactive"
	require.NoError(t, os.WriteFile(path, []byte(closeEvent), 0600))
	closed, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	require.NoError(t, err)
	work.Data.State, work.Data.CompletedAt = workStateActive, ""
	ledger.Records["work:first"] = work
	forecasts.Records["first"]["status"] = "active"
	require.NoError(t, os.WriteFile(path, []byte(closeEvent+reopen), 0600))
	reopened, _, err := projectLogStatus(dir, closed, ledger, forecasts)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "latest-status-report.yaml"), reopened, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	changed := strings.Replace(linkedForecasts, "target: '2026-11-02T16:00:00+00:00'", "target: '2026-11-02T16:30:00Z'", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(changed), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	updated, err := os.ReadFile(filepath.Join(dir, "latest-status-report.yaml"))
	require.NoError(t, err)
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(updated, &doc))
	lane := mapValue(doc.Content[0], "lanes").Content[0]
	var value any
	require.NoError(t, mapValue(lane, "forecast").Decode(&value))
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	selector := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	require.JSONEq(t, string(encoded), parsed.StatusSync.Applied[syncTargetKey(selector, "forecast")])
	require.NoError(t, yaml.Unmarshal([]byte(changed), &forecasts))
	work.Data.State, work.Data.CompletedAt = workStateCompleted, "2026-11-02T15:25:00Z"
	ledger.Records["work:first"] = work
	forecasts.Records["first"]["status"] = "inactive"
	require.NoError(t, os.WriteFile(path, []byte(closeEvent+reopen+closeEvent), 0600))
	reclosed, _, err := projectLogStatus(dir, updated, ledger, forecasts)
	require.NoError(t, err)
	reclosedReport, err := parse(reclosed)
	require.NoError(t, err)
	require.Equal(t, statusClosed, reclosedReport.Lanes[0].ImplementationStatus)
	ownerEdited := strings.Replace(string(updated), `"outcome":"First result"`, `"outcome":"owner edit"`, 1)
	require.NotEqual(t, string(updated), ownerEdited)
	projected := strings.Replace(ownerEdited, `"2026-11-02T16:30:00Z"`, `"2026-11-02T16:45:00Z"`, 1)
	require.NotEqual(t, ownerEdited, projected)
	preserved, err := refreshOwnedForecasts([]byte(ownerEdited), []byte(projected))
	require.NoError(t, err)
	preservedReport, err := parse(preserved)
	require.NoError(t, err)
	require.Equal(t, parsed.StatusSync.Applied[syncTargetKey(selector, "forecast")], preservedReport.StatusSync.Applied[syncTargetKey(selector, "forecast")])
	require.Equal(t, "owner edit", preservedReport.Lanes[0].Forecast.Outcome)
}
