package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCancelledRootRetiresForecastReplay checks explicit source-backed retirement.
//
// Example: Cancelled work removes its matching inactive forecast, while a changed prior stays pending.
func TestCancelledRootRetiresForecastReplay(t *testing.T) {
	prior := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	original := []byte(valid + "root_forecasts: [" + prior + "]\n")
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCancelled, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	for _, mode := range []string{"valid", "mismatch", "active", "evidence"} {
		update := syncUpdate{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", WorkRef: "project-understanding.yaml#/records/w", Evidence: "owner cancelled", Values: map[string]json.RawMessage{"expected_prior": json.RawMessage(prior)}, Clear: []string{"record"}}
		if mode == "mismatch" {
			update.Values["expected_prior"] = json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"owner edit"}`)
		}
		if mode == "evidence" {
			update.Evidence = ""
		}
		if mode == "active" {
			forecasts.Records["f"]["status"] = "active"
		} else {
			forecasts.Records["f"]["status"] = "inactive"
		}
		raw, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		log := `{"event":"lifecycle_change","details":{"status_update":{"updates":[` + string(raw) + `]}}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600); err != nil {
			t.Fatal(err)
		}
		out, _, err := projectLogStatus(dir, original, ledger, forecasts)
		if err != nil {
			t.Fatal(err)
		}
		report, err := parse(out)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "valid" {
			if len(report.RootForecasts) != 0 || len(report.StatusSync.Groups) != 0 {
				t.Fatal("valid retirement not applied")
			}
			continue
		}
		if len(report.RootForecasts) != 1 {
			t.Fatalf("%s removed forecast", mode)
		}
	}
}

// TestCancelledRootRetirementPreservesComments rejects a deletion that would discard annotations.
//
// Example: A forecast with an inline owner comment remains unchanged.
func TestCancelledRootRetirementPreservesComments(t *testing.T) {
	prior := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	original := []byte(valid + "root_forecasts:\n  - " + prior + " # keep\n")
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCancelled, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	update := syncUpdate{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", WorkRef: "project-understanding.yaml#/records/w", Evidence: "cancelled", Values: map[string]json.RawMessage{"expected_prior": json.RawMessage(prior)}, Clear: []string{"record"}}
	got, _, ok := applyRootRetirementSyncUpdate(original, update, ledger, forecasts, nil)
	if ok || !strings.Contains(string(got), "# keep") {
		t.Fatal("comment lost")
	}
}

// TestCancelledRootRetirementChecksSourceAndPrior exercises removal and its verifier effect.
//
// Example: A different recorded owner value blocks removal.
func TestCancelledRootRetirementChecksSourceAndPrior(t *testing.T) {
	prior := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	original := []byte(valid + "root_forecasts: [" + prior + "]\n")
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCancelled, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	update := syncUpdate{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", WorkRef: "project-understanding.yaml#/records/w", Evidence: "cancelled", Values: map[string]json.RawMessage{"expected_prior": json.RawMessage(prior)}, Clear: []string{"record"}}
	got, effects, ok := applyRootRetirementSyncUpdate(original, update, ledger, forecasts, nil)
	if !ok {
		t.Fatal("valid retirement rejected")
	}
	if err := verifyStatusSyncEffects(original, got, effects); err != nil {
		t.Fatal(err)
	}
	if err := verifyStatusSyncCanonicalEffects(original, got, effects, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := applyRootRetirementSyncUpdate(original, update, ledger, forecasts, map[string]string{"record": `{"outcome":"changed"}`}); ok {
		t.Fatal("owner conflict ignored")
	}
}
