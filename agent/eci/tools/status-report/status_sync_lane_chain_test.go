package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaneRepeatedTerminal checks replay against the final canonical state.
//
// Example: A later completion replaces an obsolete close and reopening.
func TestLaneRepeatedTerminal(t *testing.T) {
	dir := t.TempDir()
	events := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"PAUSED"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted again","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(events), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		out, _, err = projectLogStatus(dir, out, ledger, forecasts)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].CompletedAt != "2026-11-02T15:45:00Z" || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("final closure stuck after four refreshes: status=%v completed=%s groups=%d", r.Lanes[0].ImplementationStatus, r.Lanes[0].CompletedAt, len(r.StatusSync.Groups))
	}
}

// TestLaneChainKeepsIntermediateAtomicFields checks that unrelated intent is preserved.
//
// Example: An intermediate blocker is retained when a later closure is selected.
func TestLaneChainKeepsIntermediateAtomicFields(t *testing.T) {
	dir := t.TempDir()
	first := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	middle := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"PAUSED","blocker":"waiting"}}]}}}` + "\n"
	last := strings.Replace(first, "15:25:00Z", "15:45:00Z", 1)
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+middle+last), 0600); err != nil {
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
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].Blocker != "waiting" || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("atomic intermediate lost: status=%v groups=%d", r.Lanes[0].ImplementationStatus, len(r.StatusSync.Groups))
	}
}
