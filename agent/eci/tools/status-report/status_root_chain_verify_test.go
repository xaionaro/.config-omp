package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestVerifyRootLifecycleChainRejectsBrokenLink checks source event lineage.
//
// Example: Reject a completion whose expected forecast differs from its predecessor.
func TestVerifyRootLifecycleChainRejectsBrokenLink(t *testing.T) {
	f := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"working"}`
	c := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	log := []byte(`{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"accepted","retire_forecast":true,"expected_prior":` + f + `,"record":` + c + `}]}}}` + "\n")
	original := []byte("root_forecasts: [" + f + "]\nroot_completions: []\n")
	candidate := []byte("root_forecasts: []\nroot_completions: [" + c + "]\n")
	addition := syncAddition{Target: syncTargetRootCompletion, Evidence: "accepted", RetireForecast: true, ExpectedPrior: json.RawMessage(f), Record: json.RawMessage(c)}
	effects := []syncEffect{{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", Operation: syncEffectRemove, Value: addition, Evidence: "accepted"}, {Target: syncTargetRootCompletion, ID: "project-understanding.yaml#/records/w", Operation: syncEffectAdd, Value: json.RawMessage(c), Evidence: "accepted"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "inactive"}}}
	locators := []rootLifecycleLocator{{Line: 1, Ordinal: 0, Target: syncTargetRootCompletion}}
	if err := verifyRootLifecycleChain(original, candidate, log, locators, effects, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	locators[0].Ordinal = 1
	if err := verifyRootLifecycleChain(original, candidate, log, locators, effects, ledger, forecasts); err == nil {
		t.Fatal("accepted nonexistent locator")
	}
}

// TestVerifyRootLifecycleChainReopen checks net replacement and source identity.
//
// Example: Complete and reopen without publishing the obsolete completion.
func TestVerifyRootLifecycleChainReopen(t *testing.T) {
	f1 := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"old"}`
	f2 := `{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T17:00:00Z","assessment":"new"}`
	c := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	log := []byte(`{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"done","retire_forecast":true,"expected_prior":` + f1 + `,"record":` + c + `}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_forecast","evidence":"reopened","reopen_work_ref":"project-understanding.yaml#/records/w","expected_prior":` + c + `,"record":` + f2 + `}]}}}` + "\n")
	original := []byte("root_forecasts: [" + f1 + "]\nroot_completions: []\n")
	candidate := []byte("root_forecasts: [" + f2 + "]\nroot_completions: []\n")
	plan := syncAddition{Target: syncTargetRootForecast, ExpectedPrior: json.RawMessage(f1), Record: json.RawMessage(f2)}
	effects := []syncEffect{{Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", Operation: syncEffectRootForecastReplace, Value: plan}, {Target: syncTargetRootForecast, ID: "forecasts.yaml#/records/f", Operation: syncEffectAdd, Value: json.RawMessage(f2)}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"kind": "root", "status": "active"}}}
	locators := []rootLifecycleLocator{{Line: 1, Ordinal: 0, Target: syncTargetRootCompletion}, {Line: 2, Ordinal: 0, Target: syncTargetRootForecast}}
	if err := verifyRootLifecycleChain(original, candidate, log, locators, effects, ledger, forecasts); err != nil {
		t.Fatal(err)
	}
	bad := bytes.Replace(log, []byte(`"expected_prior":`+c), []byte(`"expected_prior":{}`), 1)
	if err := verifyRootLifecycleChain(original, candidate, bad, locators, effects, ledger, forecasts); err == nil {
		t.Fatal("accepted broken predecessor")
	}
	effects = append(effects, syncEffect{Target: syncTargetReport, ID: "current", Field: "state", Value: "bad", Operation: syncEffectSet})
	if err := verifyRootLifecycleChain(original, candidate, log, locators, effects, ledger, forecasts); err == nil {
		t.Fatal("accepted unrelated effect")
	}
}

// TestVerifyRootLifecycleChainUnpublishedCorrection checks an unpublished predecessor.
//
// Example: Apply a corrected completion without inventing its prior report record.
func TestVerifyRootLifecycleChainUnpublishedCorrection(t *testing.T) {
	old := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	current := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}`
	log := []byte(`{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"done","record":` + old + `}]}}}` + "\n" + `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":` + old + `,"record":` + current + `}]}}}` + "\n")
	original := []byte("root_completions: []\n")
	candidate := []byte("root_completions: [" + current + "]\n")
	effects := []syncEffect{{Target: syncTargetRootCompletion, ID: "project-understanding.yaml#/records/w", Operation: syncEffectAdd, Value: json.RawMessage(current), Evidence: "corrected"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:05:00Z", Roles: []reportingRole{reportingRoleRoot}}}, "o": {Type: "outcome", Outcome: "Result"}}}
	locators := []rootLifecycleLocator{{Line: 1, Ordinal: 0, Target: syncTargetRootCompletion}, {Line: 2, Ordinal: 0, Target: syncTargetRootCompletion}}
	if err := verifyRootLifecycleChain(original, candidate, log, locators, effects, ledger, forecastSource{}); err != nil {
		t.Fatal(err)
	}
	bad := bytes.Replace(log, []byte(`"corrects":1`), []byte(`"corrects":9`), 1)
	if err := verifyRootLifecycleChain(original, candidate, bad, locators, effects, ledger, forecastSource{}); err == nil {
		t.Fatal("accepted wrong correction locator")
	}
}

// TestRootChainIgnoresUnrelatedMalformedRecords checks identity-aware isolation.
//
// Example: A malformed sibling does not hide an explicitly identified conflict.
func TestRootChainIgnoresUnrelatedMalformedRecords(t *testing.T) {
	locators := []rootLifecycleLocator{{Line: 1, Ordinal: 0, Target: syncTargetRootCompletion}, {Line: 1, Ordinal: 2, Target: syncTargetRootForecast}}
	lines := [][]byte{[]byte(`{"details":{"status_update":{"additions":[{"target":"root_completion","record":{"work_ref":"w"}},{"target":"root_completion","record":42},{"target":"root_forecast","record":{"forecast_ref":"f"}}]}}}`)}
	if err := verifyRootChainNoSkippedEvents(lines, locators, "w", "f"); err != nil {
		t.Fatal(err)
	}
	lines[0] = []byte(`{"details":{"status_update":{"additions":[{"target":"root_completion","record":{"work_ref":"w"}},{"target":"root_completion","record":{"work_ref":"w","forecast_ref":42}},{"target":"root_forecast","record":{"forecast_ref":"f"}}]}}}`)
	if err := verifyRootChainNoSkippedEvents(lines, locators, "w", "f"); err == nil {
		t.Fatal("accepted skipped same-identity record")
	}
}

// TestRootChainFindsIdentityInMalformedItem checks raw provenance fields.
//
// Example: A bad evidence type cannot hide a matching work reference.
func TestRootChainFindsIdentityInMalformedItem(t *testing.T) {
	locators := []rootLifecycleLocator{{Line: 1, Ordinal: 0, Target: syncTargetRootCompletion}, {Line: 1, Ordinal: 2, Target: syncTargetRootForecast}}
	lines := [][]byte{[]byte(`{"details":{"status_update":{"additions":[{"target":"root_completion","record":{"work_ref":"w"}},{"target":"root_completion","evidence":42,"record":{"work_ref":"w"}},{"target":"root_forecast","record":{"forecast_ref":"f"}}]}}}`)}
	if err := verifyRootChainNoSkippedEvents(lines, locators, "w", "f"); err == nil {
		t.Fatal("accepted skipped malformed same-root item")
	}
}

// TestRootChainIsolatesMalformedCollections checks independent collection decoding.
//
// Example: An invalid sibling collection cannot hide an identifiable root update.
func TestRootChainIsolatesMalformedCollections(t *testing.T) {
	locators := []rootLifecycleLocator{{Line: 1, Ordinal: 0, Target: syncTargetRootCompletion}}
	lines := [][]byte{[]byte(`{"details":{"status_update":{"updates":7,"additions":[{"target":"root_completion","record":{"work_ref":"w"}}]}}}`)}
	if err := verifyRootChainNoSkippedEvents(lines, locators, "w", "f"); err != nil {
		t.Fatal(err)
	}
	lines[0] = []byte(`{"details":{"status_update":{"updates":[{"target":"root_forecast","work_ref":"w","evidence":7}],"additions":[{"target":"root_completion","record":{"work_ref":"w"}}]}}}`)
	if err := verifyRootChainNoSkippedEvents(lines, locators, "w", "f"); err == nil {
		t.Fatal("accepted identifiable malformed update")
	}
	lines[0] = []byte(`{"details":{"status_update":{"updates":{"target":"root_forecast","work_ref":"w"},"additions":[{"target":"root_completion","record":{"work_ref":"w"}}]}}}`)
	if err := verifyRootChainNoSkippedEvents(lines, locators, "w", "f"); err == nil {
		t.Fatal("accepted identifiable malformed update collection")
	}
}
