package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestLaneReopenRestoresCanonicalForecast verifies replay restores an active forecast.
//
// Example: Closing and reopening a lane restores its canonical target.
func TestLaneReopenRestoresCanonicalForecast(t *testing.T) {
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
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(reopened, &doc))
	parsed, err := parse(reopened)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, parsed.Lanes[0].ImplementationStatus)
	require.Empty(t, parsed.Lanes[0].CompletedAt)
	edits, err := collectForecastEdits(reopened, doc.Content[0], ledger, forecasts, &flowDocument{data: reopened}, reportLaneRefs(parsed))
	require.NoError(t, err)
	t.Logf("ready status=%v forecast=%v forecast-edits=%d groups=%d", parsed.Lanes[0].ImplementationStatus, parsed.Lanes[0].Forecast != nil, len(edits), len(parsed.StatusSync.Groups))
	if parsed.Lanes[0].Forecast == nil {
		t.Fatalf("reopened active lane has no canonical forecast; automatic refresh cannot restore it (edits=%d)", len(edits))
	}
}

// TestLaneRestoreRejectsInvalidSourceAndPreservesOwnerForecast tests both conflict boundaries.
//
// Example: An inactive source or an owner-removed forecast leaves reopening pending.
func TestLaneRestoreRejectsInvalidSourceAndPreservesOwnerForecast(t *testing.T) {
	t.Parallel()
	closed := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "    completed_at: '2026-11-02T15:25:00Z'\n", 1)
	closed = strings.Replace(closed, "implementation_status: IN PROGRESS", "implementation_status: CLOSED", 1)
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Fields: map[string]string{"implementation_status": "IN PROGRESS"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive", "kind": "lane"}}}
	if _, _, ok := applyLaneSyncUpdate([]byte(closed), update, ledger, forecasts, nil); ok {
		t.Fatal("inactive forecast accepted")
	}
	forecasts.Records["first"]["status"] = "active"
	if _, _, ok := applyLaneSyncUpdate([]byte(closed), update, ledger, forecasts, map[string]string{"forecast": "owner removed"}); ok {
		t.Fatal("owner removal overwritten")
	}
}

// TestLaneTerminalPreservesOwnerForecastEdit verifies retained projection ownership.
//
// Example: A changed forecast stays visible and the closure remains pending.
func TestLaneTerminalPreservesOwnerForecastEdit(t *testing.T) {
	t.Parallel()
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Evidence: "accepted", Fields: map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:25:00Z"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	prior := `{"forecast_ref":"forecasts.yaml#/records/first","outcome":"First result","target":"2026-11-02T16:00:00Z","assessment":"complete","baseline":"2026-11-02T16:00:00Z","evidence":"initial"}`
	if _, _, ok := applyLaneSyncUpdate([]byte(linkedReport), update, ledger, forecasts, map[string]string{"forecast": prior}); !ok {
		t.Fatal("unchanged forecast was not retired")
	}
	edited := strings.Replace(linkedReport, "outcome: First result, target:", "outcome: owner edit, target:", 1)
	if _, _, ok := applyLaneSyncUpdate([]byte(edited), update, ledger, forecasts, map[string]string{"forecast": prior}); ok {
		t.Fatal("owner-edited forecast was removed")
	}
	if _, _, ok := applyLaneSyncUpdate([]byte(linkedReport), update, ledger, forecasts, map[string]string{"forecast": syncAbsentValue}); ok {
		t.Fatal("owner-added forecast was removed")
	}
}

// TestLaneEmptyPriorValuePreservesOwnerEdit checks an empty projected baseline.
//
// Example: An owner name added after an empty baseline is not overwritten.
func TestLaneEmptyPriorValuePreservesOwnerEdit(t *testing.T) {
	t.Parallel()
	edited := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    owner: owner-edit\n    implementation_status: IN PROGRESS", 1)
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Fields: map[string]string{"owner": "projected"}}
	if _, _, ok := applyLaneSyncUpdate([]byte(edited), update, ledgerSource{}, forecastSource{}, map[string]string{"owner": ""}); ok {
		t.Fatal("owner edit overwritten after empty baseline")
	}
	original := strings.Replace(edited, "owner: owner-edit", `owner: ""`, 1)
	if _, _, ok := applyLaneSyncUpdate([]byte(original), update, ledgerSource{}, forecastSource{}, map[string]string{"owner": ""}); !ok {
		t.Fatal("matching empty baseline rejected")
	}
}

// TestLaneClearDistinguishesNullFromLiteralText protects projected ownership.
//
// Example: A quoted null added after a null baseline stays untouched.
func TestLaneClearDistinguishesNullFromLiteralText(t *testing.T) {
	t.Parallel()
	original := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    blocker: null\n    implementation_status: IN PROGRESS", 1)
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Clear: []string{"blocker"}}
	if _, _, ok := applyLaneSyncUpdate([]byte(original), update, ledgerSource{}, forecastSource{}, map[string]string{"blocker": syncNullValue}); !ok {
		t.Fatal("matching null baseline rejected")
	}
	edited := strings.Replace(original, "blocker: null", `blocker: "null"`, 1)
	if _, _, ok := applyLaneSyncUpdate([]byte(edited), update, ledgerSource{}, forecastSource{}, map[string]string{"blocker": syncNullValue}); ok {
		t.Fatal("literal null overwrote null baseline")
	}
}
