package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestVerifyPendingLaneCorrection checks immutable source and ownership.
//
// Example: A correction must name the actual earlier closure and preserve its fields.
func TestVerifyPendingLaneCorrection(t *testing.T) {
	lines := []string{
		`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"old","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z","context":"result"}}]}}}`,
		`{"event":"correction","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1,"evidence":"corrected","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z","context":"result"}}]}}}`,
	}
	var groups []syncPendingGroup
	var correction syncUpdate
	for i, line := range lines {
		var event syncEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		update := event.Details.StatusUpdate.Updates[0]
		encoded, err := encodeSyncGroup(update)
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, syncPendingGroup{Line: i + 1, Update: encoded})
		correction = update
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	candidate, effects, ok := applySyncGroup([]byte(linkedReport), correction, ledger, forecasts, nil)
	if !ok {
		t.Fatal("invalid fixture")
	}
	for i := range effects {
		effects[i].Evidence = syncGroupEvidence(correction)
	}
	log := []byte(strings.Join(lines, "\n") + "\n")
	if err := verifyPendingLaneCorrection([]byte(linkedReport), candidate, groups[0], groups[1], effects, nil, log, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	lines[0] = strings.Replace(lines[0], "result", "altered", 1)
	if err := verifyPendingLaneCorrection([]byte(linkedReport), candidate, groups[0], groups[1], effects, nil, []byte(strings.Join(lines, "\n")), ledger, forecasts); err == nil {
		t.Fatal("accepted forged old event")
	}
	if err := verifyPendingLaneCorrection([]byte(linkedReport), candidate, groups[0], groups[1], effects, map[string]string{"context": "owner edit"}, log, ledger, forecasts); err == nil {
		t.Fatal("accepted ownership conflict")
	}
}
