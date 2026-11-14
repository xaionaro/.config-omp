package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaneCloseReopenCloseReplay checks repeated lane transitions.
//
// Example: A reopened lane can close again.
func TestLaneCloseReopenCloseReplay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	closeEvent := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	reopen := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	var out = []byte(linkedReport)
	log := ""
	for _, event := range []string{closeEvent, reopen, closeEvent} {
		log += event
		if err := os.WriteFile(path, []byte(log), 0600); err != nil {
			t.Fatal(err)
		}
		var err error
		out, _, err = projectLogStatus(dir, out, ledger, forecasts)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].CompletedAt != "2026-11-02T15:25:00Z" || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("reclose stuck: status=%v completed=%q pending=%d", r.Lanes[0].ImplementationStatus, r.Lanes[0].CompletedAt, len(r.StatusSync.Groups))
	}
}

// TestLaneCompletionClearsExplicitFollowup checks explicit followup removal.
//
// Example: Closing a lane clears a supplied followup marker.
func TestLaneCompletionClearsExplicitFollowup(t *testing.T) {
	t.Parallel()
	original := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    root_task_status: FOLLOWUP\n    implementation_status: IN PROGRESS", 1)
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	event := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","clear":["root_task_status"],"fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}`
	r := explorationProjection(t, original, event, ledger, forecasts)
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].RootTaskStatus != rootTaskNone {
		t.Fatalf("explicit followup clear failed: status=%v followup=%v", r.Lanes[0].ImplementationStatus, r.Lanes[0].RootTaskStatus)
	}
}

// TestLaneFollowupProjectsOnlyExplicitValue checks explicit followup projection.
//
// Example: Closing alone preserves an existing followup marker.
func TestLaneFollowupProjectsOnlyExplicitValue(t *testing.T) {
	t.Parallel()
	event := `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"root_task_status":"FOLLOWUP_PAUSED"}}]}}}`
	r := explorationProjection(t, linkedReport, event, ledgerSource{}, forecastSource{})
	if r.Lanes[0].RootTaskStatus != rootTaskFollowupPaused {
		t.Fatal("explicit followup status not projected")
	}
	original := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    root_task_status: FOLLOWUP\n    implementation_status: IN PROGRESS", 1)
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	closeEvent := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}`
	r = explorationProjection(t, original, closeEvent, ledger, forecasts)
	if r.Lanes[0].RootTaskStatus != rootTaskFollowup {
		t.Fatal("unsupplied followup was inferred away")
	}
}

// TestLaneCompletionTimeCorrectionRequiresCanonicalTime checks isolated time updates.
//
// Example: A different actual completion time stays pending until the source agrees.
func TestLaneCompletionTimeCorrectionRequiresCanonicalTime(t *testing.T) {
	t.Parallel()
	original := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "    completed_at: '2026-11-02T15:25:00Z'\n", 1)
	original = strings.Replace(original, "implementation_status: IN PROGRESS", "implementation_status: CLOSED", 1)
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z"}}}}
	event := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"completed_at":"2026-11-02T15:45:00Z"}}]}}}`
	r := explorationProjection(t, original, event, ledger, forecastSource{})
	if r.Lanes[0].CompletedAt != "2026-11-02T15:25:00Z" || len(r.StatusSync.Groups) != 1 {
		t.Fatalf("mismatched completion published: completed=%q pending=%d", r.Lanes[0].CompletedAt, len(r.StatusSync.Groups))
	}
	ledger.Records["work:first"] = sourceRecord{Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z"}}
	r = explorationProjection(t, original, event, ledger, forecastSource{})
	if r.Lanes[0].CompletedAt != "2026-11-02T15:45:00Z" || len(r.StatusSync.Groups) != 0 {
		t.Fatal("canonical correction rejected")
	}
}
