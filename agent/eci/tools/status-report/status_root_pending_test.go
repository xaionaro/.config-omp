package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestRootPendingRetainsOrderedOccurrences verifies distinct locators retain repeated roots.
//
// Example: Two same-line transitions remain independently addressable.
func TestRootPendingRetainsOrderedOccurrences(t *testing.T) {
	t.Parallel()
	pending := map[string]syncPendingRootAddition{}
	a := syncAddition{Target: syncTargetRootCompletion, Record: json.RawMessage(`{"work_ref":"project-understanding.yaml#/records/w"}`)}
	if !queueRootAddition(pending, 1, 0, a) || !queueRootAddition(pending, 1, 1, a) || len(pending) != 2 {
		t.Fatal("distinct occurrences not retained")
	}
	if queueRootAddition(pending, 1, 1, a) {
		t.Fatal("duplicate locator accepted")
	}
}

// TestRootPendingYAMLRoundTrip restores structured JSON fields from persisted state.
//
// Example: A pending transition survives report decoding.
func TestRootPendingYAMLRoundTrip(t *testing.T) {
	t.Parallel()
	original := syncPendingRootAddition{Locator: rootLifecycleLocator{Line: 1, Ordinal: 0, Target: syncTargetRootCompletion}, Addition: syncAddition{Target: syncTargetRootCompletion, Record: json.RawMessage(`{"work_ref":"w","outcome":"x"}`), ExpectedPrior: json.RawMessage(`{"forecast_ref":"f"}`), Evidence: "accepted"}}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded syncPendingRootAddition
	if err := yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	require.JSONEq(t, string(original.Addition.Record), string(decoded.Addition.Record))
	require.JSONEq(t, string(original.Addition.ExpectedPrior), string(decoded.Addition.ExpectedPrior))
	if decoded.Locator != original.Locator {
		t.Fatalf("round trip changed entry: %+v", decoded)
	}
}
