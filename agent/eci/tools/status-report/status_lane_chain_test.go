package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLaneLifecycleChainComposesExplicitTransitions checks final source-backed closure.
//
// Example: A queued cancellation and reactivation preserve supplied details before closure.
func TestLaneLifecycleChainComposesExplicitTransitions(t *testing.T) {
	t.Parallel()
	base := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	cancel := base
	cancel.Evidence = "cancelled"
	cancel.Fields = map[string]string{"lifecycle": "cancelled"}
	reopen := base
	reopen.Evidence = "reactivated"
	reopen.Fields = map[string]string{"next_action": "retry"}
	reopen.Clear = []string{"lifecycle", "blocker"}
	closeUpdate := base
	closeUpdate.Evidence = "accepted"
	closeUpdate.Fields = map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:25:00Z"}
	makeGroup :=
		// Encode one update at a supplied log position.
		//
		// Example: Put the reactivation on the second log line.
		func(
			line int,
			u syncUpdate,
		) syncPendingGroup {
			s, err := encodeSyncGroup(u)
			if err != nil {
				t.Fatal(err)
			}
			return syncPendingGroup{Line: line, Update: s}
		}
	groups := []syncPendingGroup{makeGroup(1, cancel), makeGroup(2, reopen), makeGroup(3, closeUpdate)}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	got, effects, provenance, ok := planLaneLifecycleChain([]byte(linkedReport), groups, ledger, forecasts, nil)
	if !ok {
		t.Fatal("valid chain rejected")
	}
	r, err := parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].Blocker != "" || r.Lanes[0].NextAction != "retry" || provenance["next_action"].Line != 2 {
		t.Fatal("supplied intermediate changes not preserved")
	}
	var log strings.Builder
	for _, update := range []syncUpdate{cancel, reopen, closeUpdate} {
		raw, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		log.WriteString(`{"event":"lifecycle_change","details":{"status_update":{"updates":[` + string(raw) + `]}}}` + "\n")
	}
	if err := verifyLaneLifecycleChain([]byte(linkedReport), got, groups, effects, provenance, nil, []byte(log.String()), ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := planLaneLifecycleChain([]byte(linkedReport), groups[0:1], ledger, forecasts, nil); ok {
		t.Fatal("pure stale terminal accepted")
	}
	mixed := cancel
	mixed.Fields = map[string]string{"lifecycle": "cancelled", "blocker": "extra"}
	if _, _, _, ok := planLaneLifecycleChain([]byte(linkedReport), []syncPendingGroup{makeGroup(1, mixed), groups[1], groups[2]}, ledger, forecasts, nil); !ok {
		t.Fatal("explicit cancellation fields were dropped")
	}
	contextUpdate := cancel
	contextUpdate.Fields = map[string]string{"lifecycle": "cancelled", "context": "supplied"}
	contextEdited := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    context: owner-edit\n    implementation_status: IN PROGRESS", 1)
	if _, _, _, ok := planLaneLifecycleChain([]byte(contextEdited), []syncPendingGroup{makeGroup(1, contextUpdate), groups[1], groups[2]}, ledger, forecasts, map[string]string{"context": "previous"}); ok {
		t.Fatal("owner-edited cancellation context overwritten")
	}
	cancelledReport := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    lifecycle: cancelled\n    implementation_status: IN PROGRESS", 1)
	cancelledReport = strings.Replace(cancelledReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	if _, _, _, ok := planLaneLifecycleChain([]byte(cancelledReport), groups[1:], ledger, forecasts, nil); !ok {
		t.Fatal("already cancelled lane could not reactivate and close")
	}
	edited := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    next_action: owner edit\n    implementation_status: IN PROGRESS", 1)
	if _, _, _, ok := planLaneLifecycleChain([]byte(edited), groups, ledger, forecasts, map[string]string{"next_action": "previous"}); ok {
		t.Fatal("owner edit overwritten")
	}
}

// TestLaneLifecycleChainRepeatedCancellation preserves every supplied lifecycle step.
//
// Example: Two explicit cancellations and reactivations lead to one canonical closure.
func TestLaneLifecycleChainRepeatedCancellation(t *testing.T) {
	t.Parallel()
	base := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	cancel := base
	cancel.Evidence = "cancelled"
	cancel.Fields = map[string]string{"lifecycle": "cancelled"}
	reopen := base
	reopen.Evidence = "resumed"
	reopen.Clear = []string{"lifecycle"}
	reopen.Fields = map[string]string{"implementation_status": "PAUSED"}
	closeUpdate := base
	closeUpdate.Evidence = "accepted"
	closeUpdate.Fields = map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:45:00Z"}
	var groups []syncPendingGroup
	var log strings.Builder
	for i, update := range []syncUpdate{cancel, reopen, cancel, reopen, closeUpdate} {
		encoded, err := encodeSyncGroup(update)
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, syncPendingGroup{Line: i + 1, Update: encoded})
		raw, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		log.WriteString(`{"event":"lifecycle_change","details":{"status_update":{"updates":[` + string(raw) + `]}}}` + "\n")
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	got, effects, provenance, ok := planLaneLifecycleChain([]byte(linkedReport), groups, ledger, forecasts, nil)
	if !ok {
		t.Fatal("repeated explicit lifecycle rejected")
	}
	if err := verifyLaneLifecycleChain([]byte(linkedReport), got, groups, effects, provenance, nil, []byte(log.String()), ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	r, err := parse(got)
	if err != nil || r.Lanes[0].ImplementationStatus != statusClosed {
		t.Fatalf("closure missing: %v", err)
	}
}

// TestLaneLifecycleChainPreservesPriorClosureMetadata checks delayed completion replay.
//
// Example: A historical closure's context survives a reopen and later final closure.
func TestLaneLifecycleChainPreservesPriorClosureMetadata(t *testing.T) {
	t.Parallel()
	base := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	first := base
	first.Evidence = "accepted first"
	first.Fields = map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:25:00Z", "context": "first result"}
	reopen := base
	reopen.Evidence = "resumed"
	reopen.Fields = map[string]string{"implementation_status": "IN PROGRESS"}
	final := base
	final.Evidence = "accepted final"
	final.Fields = map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:45:00Z"}
	var groups []syncPendingGroup
	var log strings.Builder
	for i, update := range []syncUpdate{first, reopen, final} {
		encoded, err := encodeSyncGroup(update)
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, syncPendingGroup{Line: i + 1, Update: encoded})
		raw, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		log.WriteString(`{"event":"lifecycle_change","details":{"status_update":{"updates":[` + string(raw) + `]}}}` + "\n")
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	got, effects, provenance, ok := planLaneLifecycleChain([]byte(linkedReport), groups, ledger, forecasts, nil)
	if !ok {
		t.Fatal("explicit close-reopen-close chain rejected")
	}
	if err := verifyLaneLifecycleChain([]byte(linkedReport), got, groups, effects, provenance, nil, []byte(log.String()), ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	r, err := parse(got)
	if err != nil || r.Lanes[0].Context != "first result" || r.Lanes[0].CompletedAt != "2026-11-02T15:45:00Z" {
		t.Fatalf("metadata or completion lost: %v", err)
	}
}
