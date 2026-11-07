package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDelayedLaneReopen checks a delayed explicit reversal.
//
// Example: Reopen active work without projecting its stale historical closure.
func TestDelayedLaneReopen(t *testing.T) {
	dir := t.TempDir()
	events := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"PAUSED"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(events), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "active"}}}
	result, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusPaused || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("latest reopen not applied: status=%v groups=%d", r.Lanes[0].ImplementationStatus, len(r.StatusSync.Groups))
	}
}

// TestStaleLaneTransitionPreservesUnreplacedFields checks atomic transaction preservation.
//
// Example: An older closure with an unrelated blocker cannot be discarded by reopening.
func TestStaleLaneTransitionPreservesUnreplacedFields(t *testing.T) {
	dir := t.TempDir()
	events := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z","blocker":"hold"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"PAUSED"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(events), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "active"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusInProgress || len(r.StatusSync.Groups) != 2 {
		t.Fatalf("atomic group lost: status=%v groups=%d", r.Lanes[0].ImplementationStatus, len(r.StatusSync.Groups))
	}
}

// TestStaleLaneCompletionReplacedByCanonicalCompletion checks changed completion time.
//
// Example: Only the completion matching the current source is projected.
func TestStaleLaneCompletionReplacedByCanonicalCompletion(t *testing.T) {
	dir := t.TempDir()
	old := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	next := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"corrected","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(old+next), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].CompletedAt != "2026-11-02T15:45:00Z" || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("corrected completion stuck: status=%v completed=%s groups=%d", r.Lanes[0].ImplementationStatus, r.Lanes[0].CompletedAt, len(r.StatusSync.Groups))
	}
}

// TestStaleLaneCancellationReplacedByReactivation checks explicit reversal.
//
// Example: Active work with explicit evidence supersedes an unapplied cancellation.
func TestStaleLaneCancellationReplacedByReactivation(t *testing.T) {
	dir := t.TempDir()
	old := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"cancelled","fields":{"lifecycle":"cancelled"}}]}}}` + "\n"
	next := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"resumed","clear":["lifecycle"],"fields":{"implementation_status":"PAUSED"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(old+next), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "active"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusPaused || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("reactivation stuck: status=%v groups=%d", r.Lanes[0].ImplementationStatus, len(r.StatusSync.Groups))
	}
}
