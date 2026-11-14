package main

import (
	"encoding/json"
	"testing"
)

// TestVerifyStatusSyncEffectsRejectsCollateralChangeAndLostComment checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncEffectsRejectsCollateralChangeAndLostComment(t *testing.T) {
	t.Parallel()
	original := []byte("state: old # keep\nnext_focus: unchanged\n")
	effect := syncEffect{Target: syncTargetReport, ID: "current", Field: "state", Value: "new", Operation: syncEffectSet}
	for _, candidate := range []string{"state: new # keep\nnext_focus: changed\n", "state: new\nnext_focus: unchanged\n"} {
		if verifyStatusSyncEffects(original, []byte(candidate), []syncEffect{effect}) == nil {
			t.Fatalf("accepted collateral change: %s", candidate)
		}
	}
	if err := verifyStatusSyncEffects(original, []byte("state: new # keep\nnext_focus: unchanged\n"), []syncEffect{effect}); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyStatusSyncEffectsChecksIdentitiesAndAdditions checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncEffectsChecksIdentitiesAndAdditions(t *testing.T) {
	t.Parallel()
	original := []byte("tasks: [{id: a, status: NEW}, {id: b, status: NEW}]\nprogress: []\n")
	effects := []syncEffect{
		{Target: syncTargetTask, ID: "a", Field: "status", Value: "CLOSED", Operation: syncEffectSet},
		{Target: syncTargetProgress, ID: "p", Field: "text", Value: "done", Operation: syncEffectSet},
	}
	good := []byte("tasks: [{id: a, status: CLOSED}, {id: b, status: NEW}]\nprogress: [{subject_ref: p, text: done}]\n")
	if err := verifyStatusSyncEffects(original, good, effects); err != nil {
		t.Fatal(err)
	}
	wrong := []byte("tasks: [{id: a, status: NEW}, {id: b, status: CLOSED}]\nprogress: [{subject_ref: p, text: done}]\n")
	if err := verifyStatusSyncEffects(original, wrong, effects); err == nil {
		t.Fatal("accepted wrong target")
	}
	extra := []byte("tasks: [{id: a, status: CLOSED}, {id: b, status: NEW}]\nprogress: [{subject_ref: p, text: done, owner: unexpected}]\n")
	if err := verifyStatusSyncEffects(original, extra, effects); err == nil {
		t.Fatal("accepted extra field")
	}
}

// TestVerifyStatusSyncEffectsRetractionKeepsUnrelatedItem checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncEffectsRetractionKeepsUnrelatedItem(t *testing.T) {
	t.Parallel()
	original := []byte("progress: [{subject_ref: a, text: old}, {subject_ref: b, text: keep}]\n")
	effects := []syncEffect{{Target: syncTargetProgress, ID: "a", Operation: syncEffectRemove}}
	if err := verifyStatusSyncEffects(original, []byte("progress: [{subject_ref: b, text: keep}]\n"), effects); err != nil {
		t.Fatal(err)
	}
	if err := verifyStatusSyncEffects(original, []byte("progress: []\n"), effects); err == nil {
		t.Fatal("accepted unrelated removal")
	}
}

// TestVerifyStatusSyncEffectsChecksFullRecordAndMetadata checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncEffectsChecksFullRecordAndMetadata(t *testing.T) {
	t.Parallel()
	original := []byte("tasks: []\ntop_priorities: [a, b]\n")
	effects := []syncEffect{
		{Target: syncTargetTask, ID: "c", Operation: syncEffectAdd, Value: json.RawMessage(`{"id":"c","effort":2}`)},
		{ID: "a", Operation: syncEffectPriorityRemove},
		{Operation: syncEffectMetadata, Value: syncState{Cursor: 1}},
	}
	good := []byte("tasks: [{id: c, effort: 2}]\ntop_priorities: [b]\nstatus_sync: {cursor: 1, applied: null, latest: null, pending: null}\n")
	if err := verifyStatusSyncEffects(original, good, effects); err != nil {
		t.Fatal(err)
	}
	bad := []byte("tasks: [{id: c, effort: 3}]\ntop_priorities: [b]\nstatus_sync: {cursor: 1, applied: null, latest: null, pending: null}\n")
	if err := verifyStatusSyncEffects(original, bad, effects); err == nil {
		t.Fatal("accepted altered record")
	}
}

// TestVerifyStatusSyncEffectsChecksAtomicTimeline checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncEffectsChecksAtomicTimeline(t *testing.T) {
	t.Parallel()
	original := []byte("other: keep\n")
	effects := []syncEffect{{Operation: syncEffectTimeline, Value: syncTimelineSnapshot{AsOf: "2026-11-02T15:24:00Z", State: timelineVerifiedEmpty}}}
	good := []byte("other: keep\ntimeline: []\ntimeline_state: verified_empty\ntimeline_as_of: '2026-11-02T15:24:00Z'\n")
	if err := verifyStatusSyncEffects(original, good, effects); err != nil {
		t.Fatal(err)
	}
	bad := []byte("other: keep\ntimeline: []\ntimeline_state: present\ntimeline_as_of: '2026-11-02T15:24:00Z'\n")
	if err := verifyStatusSyncEffects(original, bad, effects); err == nil {
		t.Fatal("accepted incomplete timeline effect")
	}
}

// TestVerifyStatusSyncCanonicalEffectsRejectsActiveForecast checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncCanonicalEffectsRejectsActiveForecast(t *testing.T) {
	t.Parallel()
	ref := "project-understanding.yaml#/records/work:first"
	original := []byte("lanes: [{task_id: a, work_ref: '" + ref + "', required_environment: implementation, implementation_status: 'IN PROGRESS', forecast: {forecast_ref: 'forecasts.yaml#/records/first'}}]\n")
	candidate := []byte("lanes: [{task_id: a, work_ref: '" + ref + "', required_environment: implementation, implementation_status: CLOSED, completed_at: '2026-11-02T15:25:00Z'}]\n")
	effect := syncEffect{Target: syncTargetLane, ID: "a", WorkRef: ref, RequiredEnvironment: syncEnvironmentImplementation, Evidence: "accepted", Operation: syncEffectForecastRetire}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "active"}}}
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecasts); err == nil {
		t.Fatal("accepted active forecast")
	}
	forecasts.Records["first"]["status"] = "inactive"
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	ledger.Records["work:first"] = sourceRecord{Type: "work", Data: workData{State: workStateActive}}
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecasts); err == nil {
		t.Fatal("accepted active work")
	}
}

// TestVerifyStatusSyncCanonicalAddition checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncCanonicalAddition(t *testing.T) {
	t.Parallel()
	effect := syncEffect{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", Operation: syncEffectAdd, Value: json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"x"}`)}
	if err := verifyStatusSyncCanonicalEffects([]byte("{}"), []byte("{}"), []syncEffect{effect}, ledgerSource{}, forecastSource{}); err == nil {
		t.Fatal("accepted missing canonical forecast")
	}
}

// TestVerifyStatusSyncLaneRemoval checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncLaneRemoval(t *testing.T) {
	t.Parallel()
	before := []byte("lanes: [{task_id: a, work_ref: w, required_environment: implementation, completed_at: old}]\n")
	after := []byte("lanes: [{task_id: a, work_ref: w, required_environment: implementation}]\n")
	effect := syncEffect{Target: syncTargetLane, ID: "a", WorkRef: "w", RequiredEnvironment: syncEnvironmentImplementation, Field: "completed_at", Operation: syncEffectRemove}
	if err := verifyStatusSyncEffects(before, after, []syncEffect{effect}); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyStatusSyncCanonicalRootCompletion checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyStatusSyncCanonicalRootCompletion(t *testing.T) {
	t.Parallel()
	work := sourceRecord{Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": work, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "inactive", "kind": "root"}}}
	effect := syncEffect{Target: syncTargetRootCompletion, Operation: syncEffectAdd, Evidence: "accepted", Value: json.RawMessage(`{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`)}
	if err := verifyStatusSyncCanonicalEffects([]byte("{}"), []byte("{}"), []syncEffect{effect}, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	effect.Evidence = ""
	if err := verifyStatusSyncCanonicalEffects([]byte("{}"), []byte("{}"), []syncEffect{effect}, ledger, forecasts); err == nil {
		t.Fatal("accepted missing evidence")
	}
}

// TestVerifyRootRemovalRequiresExplicitPrior checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyRootRemovalRequiresExplicitPrior(t *testing.T) {
	t.Parallel()
	original := []byte("root_forecasts: [{forecast_ref: 'forecasts.yaml#/records/f', outcome: Result}]\n")
	candidate := []byte("root_forecasts: []\n")
	add := syncAddition{Target: syncTargetRootCompletion, RetireForecast: true, ExpectedPrior: json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result"}`), Record: json.RawMessage(`{"work_ref":"project-understanding.yaml#/records/w"}`)}
	effect := syncEffect{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", Operation: syncEffectRemove, Value: add}
	if err := verifyStatusSyncEffects(original, candidate, []syncEffect{effect}); err != nil {
		t.Fatal(err)
	}
	add.ExpectedPrior = json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"wrong"}`)
	effect.Value = add
	if err := verifyStatusSyncEffects(original, candidate, []syncEffect{effect}); err == nil {
		t.Fatal("accepted wrong prior")
	}
}

// TestVerifyCanonicalRootRetirement checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyCanonicalRootRetirement(t *testing.T) {
	t.Parallel()
	prior := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result"}`
	record := json.RawMessage(`{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`)
	original := []byte("root_forecasts: [" + prior + "]\n")
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "inactive", "kind": "root"}}}
	addition := syncAddition{Target: syncTargetRootCompletion, Record: record, Evidence: "accepted", RetireForecast: true, ExpectedPrior: json.RawMessage(prior)}
	removal := syncEffect{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", Operation: syncEffectRemove, Evidence: "accepted", Value: addition}
	add := syncEffect{Target: syncTargetRootCompletion, Operation: syncEffectAdd, Evidence: "accepted", Value: record}
	if err := verifyStatusSyncCanonicalEffects(original, []byte("{}"), []syncEffect{removal, add}, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	addition.RetireForecast = false
	removal.Value = addition
	if err := verifyStatusSyncCanonicalEffects(original, []byte("{}"), []syncEffect{removal, add}, ledger, forecasts); err == nil {
		t.Fatal("accepted missing retirement authorization")
	}
}

// TestVerifyCanonicalRootReopening checks the named verification behavior.
//
// Example: Reject an unauthorized report change.
func TestVerifyCanonicalRootReopening(t *testing.T) {
	t.Parallel()
	prior := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result"}`
	record := json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result"}`)
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "active", "kind": "root"}}}
	addition := syncAddition{Target: syncTargetRootForecast, Record: record, Evidence: "accepted", ReopenWorkRef: "project-understanding.yaml#/records/w", ExpectedPrior: json.RawMessage(prior)}
	removal := syncEffect{Target: syncTargetRootCompletion, ID: addition.ReopenWorkRef, Operation: syncEffectRemove, Evidence: "accepted", Value: addition}
	add := syncEffect{Target: syncTargetRootForecast, Operation: syncEffectAdd, Evidence: "accepted", Value: record}
	original := []byte("root_completions: [" + prior + "]\n")
	if err := verifyStatusSyncCanonicalEffects(original, []byte("{}"), []syncEffect{removal, add}, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	forecasts.Records["f"]["status"] = "inactive"
	if err := verifyStatusSyncCanonicalEffects(original, []byte("{}"), []syncEffect{removal, add}, ledger, forecasts); err == nil {
		t.Fatal("accepted inactive reopened forecast")
	}
}

// TestVerifyPriorityRemovalRequiresExplicitPrior checks estimate retirement authorization.
//
// Example: Reject retirement when the supplied prior estimate differs.
func TestVerifyPriorityRemovalRequiresExplicitPrior(t *testing.T) {
	t.Parallel()
	original := []byte("priority_estimates: [{priority: P1, basis: old}]\n")
	candidate := []byte("priority_estimates: []\n")
	addition := syncAddition{RetirePriority: "P1", ExpectedPrior: json.RawMessage(`{"priority":"P1","basis":"old"}`)}
	effect := syncEffect{Target: syncTargetPriorityEstimate, ID: "P1", Operation: syncEffectRemove, Value: addition}
	if err := verifyStatusSyncEffects(original, candidate, []syncEffect{effect}); err != nil {
		t.Fatal(err)
	}
	addition.ExpectedPrior = json.RawMessage(`{"priority":"P1","basis":"wrong"}`)
	effect.Value = addition
	if err := verifyStatusSyncEffects(original, candidate, []syncEffect{effect}); err == nil {
		t.Fatal("accepted mismatched prior")
	}
	addition.ExpectedPrior = json.RawMessage(`{"priority":"P1","basis":"old"}`)
	addition.RetirePriority = "P2"
	effect.Value = addition
	if err := verifyStatusSyncEffects(original, candidate, []syncEffect{effect}); err == nil {
		t.Fatal("accepted wrong priority")
	}
}

// TestVerifyPriorityRetirementTransaction checks its paired changes and original task.
//
// Example: Reject retirement when the selected task had another priority.
func TestVerifyPriorityRetirementTransaction(t *testing.T) {
	t.Parallel()
	original := []byte("tasks: [{id: a, priority: P1}]\npriority_estimates: [{priority: P1, basis: old}]\n")
	addition := syncAddition{Target: syncTargetPriorityEstimate, RetirePriority: "P1", ExpectedPrior: json.RawMessage(`{"priority":"P1","basis":"old"}`), Record: json.RawMessage(`{"priority":"P2","basis":"new"}`)}
	effects := []syncEffect{{Target: syncTargetPriorityEstimate, ID: "P1", Operation: syncEffectRemove, Value: addition}, {Target: syncTargetPriorityEstimate, ID: "P2", Operation: syncEffectAdd, Value: addition.Record}, {Target: syncTargetTask, ID: "a", Field: "priority", Operation: syncEffectSet, Value: json.RawMessage(`"P2"`)}}
	if err := verifyPriorityRetirementEffects(original, effects); err != nil {
		t.Fatal(err)
	}
	if err := verifyPriorityRetirementEffects(original, effects[:2]); err == nil {
		t.Fatal("accepted missing task update")
	}
	effects[2].ID = "other"
	if err := verifyPriorityRetirementEffects(original, effects); err == nil {
		t.Fatal("accepted wrong task")
	}
	effects[2].ID = "a"
	effects[1].Value = json.RawMessage(`{"priority":"P2","basis":"wrong"}`)
	if err := verifyPriorityRetirementEffects(original, effects); err == nil {
		t.Fatal("accepted altered estimate")
	}
}

// TestVerifyLaneForecastRestore checks source and projection when reopening a lane.
//
// Example: Reject a restored forecast with a changed target.
func TestVerifyLaneForecastRestore(t *testing.T) {
	t.Parallel()
	ref := "project-understanding.yaml#/records/w"
	original := []byte("lanes: [{task_id: a, work_ref: '" + ref + "', required_environment: implementation, implementation_status: CLOSED}]\n")
	candidate := []byte("lanes: [{task_id: a, work_ref: '" + ref + "', required_environment: implementation, implementation_status: 'IN PROGRESS', forecast: {forecast_ref: 'forecasts.yaml#/records/f', outcome: Result, target: '2026-11-02T16:00:00Z', assessment: progressing}}]\n")
	raw := json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"progressing"}`)
	effect := syncEffect{Target: syncTargetLane, ID: "a", WorkRef: ref, RequiredEnvironment: syncEnvironmentImplementation, Operation: syncEffectLaneForecastRestore, Value: raw}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", Data: workData{State: workStateActive, ForecastRef: "forecasts.yaml#/records/f"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "lane", "status": "active", "outcome": "Result", "target": "2026-11-02T16:00:00Z", "assessment": "progressing"}}}
	effects := []syncEffect{effect, {Target: syncTargetLane, ID: "a", WorkRef: ref, RequiredEnvironment: syncEnvironmentImplementation, Field: "implementation_status", Value: "IN PROGRESS", Operation: syncEffectSet}}
	if err := verifyStatusSyncCanonicalEffects(original, candidate, effects, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	forecasts.Records["f"]["target"] = "2026-11-02T17:00:00Z"
	if err := verifyStatusSyncCanonicalEffects(original, candidate, effects, ledger, forecasts); err == nil {
		t.Fatal("accepted stale forecast")
	}
}

// TestVerifyCancelledRootForecastRetirement checks explicit source-backed removal.
//
// Example: Reject removal while canonical root work remains active.
func TestVerifyCancelledRootForecastRetirement(t *testing.T) {
	t.Parallel()
	prior := json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result"}`)
	original := []byte("root_forecasts: [" + string(prior) + "]\n")
	candidate := []byte("root_forecasts: []\n")
	ref := "project-understanding.yaml#/records/w"
	update := syncUpdate{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", WorkRef: ref, Evidence: "cancelled", Values: map[string]json.RawMessage{"expected_prior": prior}, Clear: []string{"record"}}
	effect := syncEffect{Target: update.Target, ID: update.ID, WorkRef: ref, Evidence: update.Evidence, Value: update, Operation: syncEffectCancelledRootForecastRetire}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", Data: workData{State: workStateCancelled, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	if err := verifyStatusSyncEffects(original, candidate, []syncEffect{effect}); err != nil {
		t.Fatal(err)
	}
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	forecasts.Records["f"]["status"] = "active"
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecasts); err == nil {
		t.Fatal("accepted active forecast")
	}
	forecasts.Records["f"]["status"] = "inactive"
	update.Values["expected_prior"] = json.RawMessage(`{"forecast_ref":"forecasts.yaml#/records/f","outcome":"wrong"}`)
	effect.Value = update
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecasts); err == nil {
		t.Fatal("accepted altered prior")
	}
	update.Values["expected_prior"] = prior
	update.Clear = nil
	effect.Value = update
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecasts); err == nil {
		t.Fatal("accepted missing removal authorization")
	}
}

// TestVerifyLaneCompletionTimeCorrectionChecksCanonicalWork checks standalone timestamp updates.
//
// Example: Reject a correction that differs from canonical completion.
func TestVerifyLaneCompletionTimeCorrectionChecksCanonicalWork(t *testing.T) {
	t.Parallel()
	ref := "project-understanding.yaml#/records/w"
	original := []byte("lanes: [{task_id: a, work_ref: '" + ref + "', required_environment: implementation, implementation_status: CLOSED, completed_at: '2026-11-02T15:00:00Z'}]\n")
	candidate := []byte("lanes: [{task_id: a, work_ref: '" + ref + "', required_environment: implementation, implementation_status: CLOSED, completed_at: '2026-11-02T15:05:00Z'}]\n")
	effect := syncEffect{Target: syncTargetLane, ID: "a", WorkRef: ref, RequiredEnvironment: syncEnvironmentImplementation, Field: "completed_at", Value: "2026-11-02T15:05:00Z", Operation: syncEffectSet}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z"}}}}
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecastSource{}); err != nil {
		t.Fatal(err)
	}
	work := ledger.Records["w"]
	work.Data.CompletedAt = "2026-11-02T15:06:00Z"
	ledger.Records["w"] = work
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecastSource{}); err == nil {
		t.Fatal("accepted noncanonical timestamp")
	}
	work.Data.CompletedAt = "2026-11-02T15:05:00Z"
	work.Data.State = workStateActive
	ledger.Records["w"] = work
	if err := verifyStatusSyncCanonicalEffects(original, candidate, []syncEffect{effect}, ledger, forecastSource{}); err == nil {
		t.Fatal("accepted active canonical work")
	}
}

// TestVerifyRootCompletionCorrection checks exact prior and canonical replacement.
//
// Example: Reject a correction with an owner-edited prior record.
func TestVerifyRootCompletionCorrection(t *testing.T) {
	t.Parallel()
	ref := "project-understanding.yaml#/records/w"
	prior := json.RawMessage(`{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`)
	record := json.RawMessage(`{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}`)
	original := []byte("root_completions: [" + string(prior) + "]\n")
	candidate := []byte("root_completions: [" + string(record) + "]\n")
	addition := syncAddition{Target: syncTargetRootCompletion, Record: record, ExpectedPrior: prior, Evidence: "corrected"}
	effects := []syncEffect{{Target: syncTargetRootCompletion, ID: ref, Evidence: addition.Evidence, Value: addition, Operation: syncEffectRootCompletionCorrection}, {Target: syncTargetRootCompletion, ID: ref, Evidence: addition.Evidence, Value: record, Operation: syncEffectAdd}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
	if err := verifyStatusSyncEffects(original, candidate, effects); err != nil {
		t.Fatal(err)
	}
	if err := verifyStatusSyncCanonicalEffects(original, candidate, effects, ledger, forecastSource{}); err != nil {
		t.Fatal(err)
	}
	addition.ExpectedPrior = json.RawMessage(`{"work_ref":"project-understanding.yaml#/records/w","outcome":"wrong"}`)
	effects[0].Value = addition
	if err := verifyStatusSyncCanonicalEffects(original, candidate, effects, ledger, forecastSource{}); err == nil {
		t.Fatal("accepted wrong prior")
	}
	addition.ExpectedPrior = prior
	effects[0].Value = addition
	work := ledger.Records["w"]
	work.Data.CompletedAt = "2026-11-02T15:06:00Z"
	ledger.Records["w"] = work
	if err := verifyStatusSyncCanonicalEffects(original, candidate, effects, ledger, forecastSource{}); err == nil {
		t.Fatal("accepted noncanonical replacement time")
	}
}
