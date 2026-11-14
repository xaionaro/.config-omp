package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSourceBackedAdditionsReplay verifies accepted and pending source identities.
//
// Example: A mismatched outcome remains pending.
func TestSourceBackedAdditionsReplay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, entry string
		work        sourceRecord
		forecast    map[string]any
		root        bool
	}{
		{name: "lane", entry: `{"target":"lane","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW","forecast":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}}`, work: sourceRecord{Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}}, forecast: map[string]any{"status": "active", "kind": "lane"}},
		{name: "root completion", entry: `{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}}`, work: sourceRecord{Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, forecast: map[string]any{"status": "inactive", "kind": "root"}, root: true},
		{name: "root forecast", entry: `{"target":"root_forecast","record":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}`, work: sourceRecord{Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, forecast: map[string]any{"status": "active", "kind": "root"}, root: true},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Check one addition against matching and mismatching source outcomes.
			//
			// Example: Reject a root forecast with a different outcome.
			func(t *testing.T) {
				dir := t.TempDir()
				log := `{"event":"requirement_change","details":{"status_update":{"additions":[` + tc.entry + `]}}}` + "\n"
				if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600); err != nil {
					t.Fatal(err)
				}
				ledger := ledgerSource{Records: map[string]sourceRecord{"w": tc.work, "o": {Type: "outcome", Outcome: "Result"}}}
				forecasts := forecastSource{Records: map[string]map[string]any{"f": tc.forecast}}
				for _, mismatch := range []bool{false, true} {
					if mismatch {
						ledger.Records["o"] = sourceRecord{Type: "outcome", Outcome: "Different"}
					}
					out, _, err := projectLogStatus(dir, []byte(valid), ledger, forecasts)
					if err != nil {
						t.Fatal(err)
					}
					report, err := parse(out)
					if err != nil {
						t.Fatal(err)
					}
					count := len(report.Lanes)
					if tc.root {
						count = len(report.RootCompletions)
						if tc.name == "root forecast" {
							count = len(report.RootForecasts)
						}
					}
					pending := len(report.StatusSync.PendingAdditions) + len(report.StatusSync.PendingRootAdditions)
					switch {
					case mismatch && (count != 0 || pending != 1):
						t.Fatalf("mismatched source published: count=%d pending=%d", count, pending)
					case !mismatch && (count != 1 || pending != 0):
						t.Fatalf("matching source not published: count=%d pending=%d", count, pending)
					}
				}
			})
	}
}

// TestLaneAdditionUsesCompleteSelector checks lane identity and duplicate rejection.
//
// Example: A distinct work reference permits another lane.
func TestLaneAdditionUsesCompleteSelector(t *testing.T) {
	t.Parallel()
	original := []byte(linkedReport)
	addition := syncAddition{Target: syncTargetLane, Record: json.RawMessage(`{"task_id":"1","work_ref":"project-understanding.yaml#/records/work:third","outcome":"Third","required_environment":"test","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW"}`)}
	got, ok := applySyncAddition(original, addition)
	if !ok {
		t.Fatal("distinct lane selector rejected")
	}
	report, err := parse(got)
	if err != nil || len(report.Lanes) != 3 {
		t.Fatalf("lane not inserted: %v", err)
	}
	if _, ok := applySyncAddition(got, addition); ok {
		t.Fatal("duplicate lane selector accepted")
	}
}
