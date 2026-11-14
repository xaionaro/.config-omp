package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIndependentLaneOwnerConflict checks disjoint blocked assignments.
//
// Example: Preserve an owner edit while applying a later lane reopening.
func TestIndependentLaneOwnerConflict(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"owner":"projected"}}]}}}` + "\n"
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "active"}}}
	if err := os.WriteFile(path, []byte(first), 0600); err != nil {
		t.Fatal(err)
	}
	initial, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(initial), `"owner": "projected"`, `"owner": "manual"`, 1)
	if edited == string(initial) {
		t.Fatal("edit not applied")
	}
	closeEvent := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	owner := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"owner":"later"}}]}}}` + "\n"
	reopen := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"PAUSED"}}]}}}` + "\n"
	if err := os.WriteFile(path, []byte(first+closeEvent+owner+reopen), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := projectLogStatus(dir, []byte(edited), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusPaused || len(r.StatusSync.Groups) != 1 || r.Lanes[0].Owner != "manual" {
		t.Fatalf("independent owner conflict blocks valid reopening: status=%s owner=%s groups=%d", r.Lanes[0].ImplementationStatus, r.Lanes[0].Owner, len(r.StatusSync.Groups))
	}
}
