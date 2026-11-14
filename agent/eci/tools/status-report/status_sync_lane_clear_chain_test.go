package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaneClearBlockerChain checks explicit clears in a transition chain.
//
// Example: Clear a blocker while retaining a supplied context and final closure.
func TestLaneClearBlockerChain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	middle := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"PAUSED","context":"resumed"},"clear":["blocker"]}]}}}` + "\n"
	last := strings.Replace(first, "15:25:00Z", "15:45:00Z", 1)
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+middle+last), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out := []byte(strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    blocker: waiting\n    implementation_status: IN PROGRESS", 1))
	for i := 0; i < 4; i++ {
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
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].Context != "resumed" || r.Lanes[0].Blocker != "" || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("chain stuck status=%s pending=%d", r.Lanes[0].ImplementationStatus, len(r.StatusSync.Groups))
	}
}
