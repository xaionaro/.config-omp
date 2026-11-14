package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneRepeatedCancellation checks repeated explicit lifecycle transitions.
//
// Example: Two cancellation and reactivation cycles end at a canonical closure.
func TestLaneRepeatedCancellation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cancel := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"cancelled","fields":{"lifecycle":"cancelled"}}]}}}` + "\n"
	reopen := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"resumed","clear":["lifecycle"],"fields":{"implementation_status":"PAUSED"}}]}}}` + "\n"
	close := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(cancel+reopen+cancel+reopen+close), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out := []byte(linkedReport)
	for i := 0; i < 4; i++ {
		var err error
		out, _, err = projectLogStatus(dir, out, ledger, forecasts)
		require.NoError(t, err)
	}
	r, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, statusClosed, r.Lanes[0].ImplementationStatus)
	require.Empty(t, r.StatusSync.Groups)
}

// TestCancellationContextSurvivesDelayedReplay checks explicit cancellation context.
//
// Example: Preserve the supplied context and next action when canonical work later closes.
func TestCancellationContextSurvivesDelayedReplay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cancel := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"cancelled","fields":{"lifecycle":"cancelled","context":"Paused by owner"}}]}}}` + "\n"
	reopen := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"resumed","clear":["lifecycle"],"fields":{"implementation_status":"PAUSED","next_action":"retry"}}]}}}` + "\n"
	close := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(cancel+reopen+close), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	require.NoError(t, err)
	r, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, statusClosed, r.Lanes[0].ImplementationStatus)
	require.Equal(t, "Paused by owner", r.Lanes[0].Context)
	require.Equal(t, "retry", r.Lanes[0].NextAction)
	require.Empty(t, r.StatusSync.Groups)
}
