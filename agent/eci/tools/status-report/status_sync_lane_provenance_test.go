package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLaneChainProvenanceRejectsForgedLogPosition checks event locators.
//
// Example: Reject an ordinal outside the logged updates.
func TestLaneChainProvenanceRejectsForgedLogPosition(t *testing.T) {
	u := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Evidence: "done", Fields: map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:45:00Z"}}
	encoded, err := encodeSyncGroup(u)
	if err != nil {
		t.Fatal(err)
	}
	g := syncPendingGroup{Line: 1, Ordinal: 0, Update: encoded}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	log := []byte(`{"event":"lifecycle_change","details":{"status_update":{"updates":[` + string(raw) + `]}}}` + "\n")
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	if err := verifyLaneLifecycleChain([]byte(linkedReport), []byte(linkedReport), []syncPendingGroup{g}, nil, nil, nil, log, ledger, forecasts); err == nil {
		t.Fatal("accepted missing effects")
	}
	g.Ordinal = 1
	if err := verifyLaneLifecycleChain([]byte(linkedReport), []byte(linkedReport), []syncPendingGroup{g}, nil, nil, nil, log, ledger, forecasts); err == nil {
		t.Fatal("accepted forged ordinal")
	}
}

// TestLaneChainProvenanceChecksLogAndOwner checks source and ownership.
//
// Example: Reject a changed log payload or owner conflict.
func TestLaneChainProvenanceChecksLogAndOwner(t *testing.T) {
	raw := []string{
		`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"cancelled","fields":{"lifecycle":"cancelled"}}]}}}`,
		`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"reopened","fields":{"blocker":"waiting"},"clear":["lifecycle"]}]}}}`,
		`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}`,
	}
	var groups []syncPendingGroup
	for i, line := range raw {
		var event syncEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeSyncGroup(event.Details.StatusUpdate.Updates[0])
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, syncPendingGroup{Line: i + 1, Update: encoded})
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	candidate, effects, provenance, ok := planLaneLifecycleChain([]byte(linkedReport), groups, ledger, forecasts, nil)
	if !ok {
		t.Fatal("planner rejected fixture")
	}
	log := []byte(strings.Join(raw, "\n") + "\n")
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, log, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, map[string]string{"blocker": "someone else's value"}, log, ledger, forecasts); err == nil {
		t.Fatal("accepted owner conflict")
	}
	raw[1] = strings.Replace(raw[1], "waiting", "forged", 1)
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, []byte(strings.Join(raw, "\n")), ledger, forecasts); err == nil {
		t.Fatal("accepted mismatched log payload")
	}
}

// TestLaneChainProvenanceRepeatedTransitions verifies every event in a longer chain.
//
// Example: A second cancellation and reactivation still retain the final supplied blocker.
func TestLaneChainProvenanceRepeatedTransitions(t *testing.T) {
	cancel := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"cancelled","fields":{"lifecycle":"cancelled","context":"retained"}}]}}}`
	reopen := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"reopened","fields":{"blocker":"waiting"},"clear":["lifecycle"]}]}}}`
	close := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}`
	lines := []string{cancel, reopen, cancel, reopen, close}
	var groups []syncPendingGroup
	for i, line := range lines {
		var event syncEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeSyncGroup(event.Details.StatusUpdate.Updates[0])
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, syncPendingGroup{Line: i + 1, Update: encoded})
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	candidate, effects, provenance, ok := planLaneLifecycleChain([]byte(linkedReport), groups, ledger, forecasts, nil)
	if !ok {
		t.Fatal("planner rejected repeated transitions")
	}
	log := []byte(strings.Join(lines, "\n") + "\n")
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, log, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	omitted := strings.Replace(lines[2], `}]}}}`, `},{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"blocker":"omitted"}}]}}}`, 1)
	withOmitted := append([]string(nil), lines...)
	withOmitted[2] = omitted
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, []byte(strings.Join(withOmitted, "\n")), ledger, forecasts); err == nil {
		t.Fatal("accepted omitted dependent update")
	}
	withOmitted[2] = strings.Replace(omitted, `"fields":{"blocker":"omitted"}`, `"evidence":42,"fields":{"blocker":"omitted"}`, 1)
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, []byte(strings.Join(withOmitted, "\n")), ledger, forecasts); err == nil {
		t.Fatal("accepted malformed omitted lane update")
	}
	withOmitted[2] = strings.Replace(withOmitted[2], `"evidence":42`, `"id":"other","evidence":42`, 1)
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, []byte(strings.Join(withOmitted, "\n")), ledger, forecasts); err != nil {
		t.Fatalf("rejected unrelated malformed sibling: %v", err)
	}
	withOmitted[2] = strings.Replace(omitted, `"blocker":"omitted"`, `"owner":"other"`, 1)
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, []byte(strings.Join(withOmitted, "\n")), ledger, forecasts); err != nil {
		t.Fatalf("rejected disjoint update: %v", err)
	}
	lines[2] = strings.Replace(lines[2], "cancelled", "altered", 1)
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, []byte(strings.Join(lines, "\n")), ledger, forecasts); err == nil {
		t.Fatal("accepted altered intermediate cancellation")
	}
}

// TestLaneChainProvenanceReclosed verifies retained fields from an earlier closure.
//
// Example: A reopening preserves context supplied by the preceding closure.
func TestLaneChainProvenanceReclosed(t *testing.T) {
	lines := []string{
		`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"first accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z","context":"retained"}}]}}}`,
		`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"reopened","fields":{"implementation_status":"PAUSED","blocker":"waiting"}}]}}}`,
		`{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}`,
	}
	var groups []syncPendingGroup
	for i, line := range lines {
		var event syncEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		encoded, err := encodeSyncGroup(event.Details.StatusUpdate.Updates[0])
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, syncPendingGroup{Line: i + 1, Update: encoded})
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	candidate, effects, provenance, ok := planLaneLifecycleChain([]byte(linkedReport), groups, ledger, forecasts, nil)
	if !ok {
		t.Fatal("planner rejected reclosure")
	}
	log := []byte(strings.Join(lines, "\n") + "\n")
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, log, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	lines[0] = strings.Replace(lines[0], "retained", "altered", 1)
	if err := verifyLaneLifecycleChain([]byte(linkedReport), candidate, groups, effects, provenance, nil, []byte(strings.Join(lines, "\n")), ledger, forecasts); err == nil {
		t.Fatal("accepted altered earlier closure")
	}
}
