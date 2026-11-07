package main

import (
	"encoding/json"
	"testing"
)

// TestLaneRawUpdatesIsolateMalformedSiblings checks raw update identity.
//
// Example: A malformed update still identifies its lane without hiding a valid sibling.
func TestLaneRawUpdatesIsolateMalformedSiblings(t *testing.T) {
	line := []byte(`{"event":"checkpoint","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":42,"fields":{"blocker":"waiting"}},{"target":"task","id":"other","fields":{"owner":"x"}}]}}}`)
	raw, ok := laneRawUpdates(line)
	if !ok || len(raw) != 2 {
		t.Fatal("lost updates")
	}
	lane := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	if !laneRawIdentifies(raw[0], lane) || laneRawIdentifies(raw[1], lane) {
		t.Fatal("wrong raw identity")
	}
	var update syncUpdate
	if json.Unmarshal(raw[0], &update) == nil || json.Unmarshal(raw[1], &update) != nil {
		t.Fatal("unexpected decoding")
	}
}
