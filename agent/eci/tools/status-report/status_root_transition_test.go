package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootTransitionPreservesComments(t *testing.T) {
	prior := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	original := []byte(valid + "root_forecasts:\n  - " + prior + " # owner comment\n")
	if _, ok := removeRootSyncRecord(original, syncTargetRootForecast, "forecasts.yaml#/records/f", json.RawMessage(prior)); ok {
		t.Fatal("removed owner comment")
	}
}

func TestRootRemovalHandlesBlockAndFlowLists(t *testing.T) {
	prior := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	other := `{"forecast_ref":"forecasts.yaml#/records/g","outcome":"Other","target":"2026-11-02T17:00:00Z","assessment":"complete"}`
	for _, section := range []string{"root_forecasts:\n  - " + prior + "\n", "root_forecasts:\n  - " + prior + "\n  - " + other + "\n", "root_forecasts: [" + prior + ", " + other + "]\n", "root_forecasts: [" + other + ", " + prior + "]\n"} {
		got, ok := removeRootSyncRecord([]byte(valid+section), syncTargetRootForecast, "forecasts.yaml#/records/f", json.RawMessage(prior))
		if !ok {
			t.Fatalf("removal failed: %s", section)
		}
		r, err := parse(got)
		if err != nil {
			t.Fatalf("invalid removal %s: %v", got, err)
		}
		for _, f := range r.RootForecasts {
			if f.ForecastRef == "forecasts.yaml#/records/f" {
				t.Fatal("target still present")
			}
		}
	}
}

// TestRootTransitionsReplay checks explicit source-backed root transitions.
//
// Example: Reopening requires matching prior completion and evidence.
func TestRootTransitionsReplay(t *testing.T) {
	forecast := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`
	completion := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	cases := []struct {
		name, section string
		addition      syncAddition
		work          workData
		state         string
	}{
		{"complete", "root_forecasts", syncAddition{Target: syncTargetRootCompletion, Evidence: "accepted", RetireForecast: true, ExpectedPrior: json.RawMessage(forecast), Record: json.RawMessage(completion)}, workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}, "inactive"},
		{"reopen", "root_completions", syncAddition{Target: syncTargetRootForecast, Evidence: "reopened", ReopenWorkRef: "project-understanding.yaml#/records/w", ExpectedPrior: json.RawMessage(completion), Record: json.RawMessage(forecast)}, workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}, "active"},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Check one direction of root lifecycle replay.
			//
			// Example: A mismatched prior remains pending.
			func(t *testing.T) {
				prior := forecast
				if tc.name == "reopen" {
					prior = completion
				}
				original := []byte(valid + tc.section + ": [" + prior + "]\n")
				ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: tc.work}, "o": {Type: "outcome", Outcome: "Result"}}}
				forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": tc.state}}}
				for _, mode := range []string{"valid", "mismatch", "missing", "no evidence"} {
					addition := tc.addition
					switch mode {
					case "mismatch":
						addition.ExpectedPrior = json.RawMessage(`{"outcome":"wrong"}`)
					case "missing":
						addition.ExpectedPrior = nil
					case "no evidence":
						addition.Evidence = ""
					}
					raw, err := json.Marshal(addition)
					if err != nil {
						t.Fatal(err)
					}
					dir := t.TempDir()
					log := `{"event":"lifecycle_change","details":{"status_update":{"additions":[` + string(raw) + `]}}}` + "\n"
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
					if mode != "valid" {
						if len(report.StatusSync.PendingRootAdditions) != 1 {
							t.Fatal("mismatched prior did not remain pending")
						}
						continue
					}
					if len(report.StatusSync.PendingRootAdditions) != 0 {
						t.Fatal("valid transition pending")
					}
					if tc.name == "complete" && (len(report.RootCompletions) != 1 || len(report.RootForecasts) != 0) {
						t.Fatal("completion transition not applied")
					}
					if tc.name == "reopen" && (len(report.RootCompletions) != 0 || len(report.RootForecasts) != 1) {
						t.Fatal("reopen transition not applied")
					}
				}
			})
	}
}

func TestPriorityEstimateRemovalRequiresExactPrior(t *testing.T) {
	prior := `{"priority":"high","min_minutes":20,"max_minutes":40,"as_of":"2026-11-02T15:24:00Z","basis":"educated guess","assumptions":"review available","dependencies":"review","critical_path":"latest parallel endpoint"}`
	got, ok := removeRootSyncRecord([]byte(valid), syncTargetPriorityEstimate, "high", json.RawMessage(prior))
	if !ok || strings.Contains(string(got), "priority: high, min_minutes") {
		t.Fatal("matching estimate not removed")
	}
	if _, ok := removeRootSyncRecord([]byte(valid), syncTargetPriorityEstimate, "high", json.RawMessage(`{"priority":"high"}`)); ok {
		t.Fatal("incomplete prior accepted")
	}
	commented := strings.Replace(valid, "  - {priority: high, min_minutes", "  # owner note\n  - {priority: high, min_minutes", 1)
	if got, ok := removeRootSyncRecord([]byte(commented), syncTargetPriorityEstimate, "high", json.RawMessage(prior)); !ok || !strings.Contains(string(got), "# owner note") {
		t.Fatal("owner comment not preserved")
	}
}

// TestRootCompletionCorrectionReplay requires an exact prior and canonical replacement.
//
// Example: A corrected completion time replaces only the previously supplied record.
func TestRootCompletionCorrectionReplay(t *testing.T) {
	old := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	newer := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}`
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","record":` + old + `}]}}}` + "\n"
	if err := os.WriteFile(path, []byte(first), 0600); err != nil {
		t.Fatal(err)
	}
	projected, _, err := projectLogStatus(dir, []byte(valid), ledger, forecastSource{})
	if err != nil {
		t.Fatal(err)
	}
	work := ledger.Records["w"]
	work.Data.CompletedAt = "2026-11-02T15:05:00Z"
	ledger.Records["w"] = work
	second := `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":` + old + `,"record":` + newer + `}]}}}` + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0600); err != nil {
		t.Fatal(err)
	}
	corrected, _, err := projectLogStatus(dir, projected, ledger, forecastSource{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(corrected)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.RootCompletions) != 1 || r.RootCompletions[0].CompletedAt != "2026-11-02T15:05:00Z" || len(r.StatusSync.PendingAdditions) != 0 {
		t.Fatalf("correction not applied: %+v pending=%d", r.RootCompletions, len(r.StatusSync.PendingAdditions))
	}
}
