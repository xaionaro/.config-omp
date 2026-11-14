package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReviewCIndependentAppliedFieldBetweenTransitions checks already-applied provenance.
//
// Example: A verified owner assignment can precede a later explicit reassignment and closure.
func TestReviewCIndependentAppliedFieldBetweenTransitions(t *testing.T) {
	t.Parallel()
	base := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation}
	cancel := base
	cancel.Evidence = "cancelled"
	cancel.Fields = map[string]string{"lifecycle": "cancelled", "context": "Paused by owner"}
	assignment := base
	assignment.Fields = map[string]string{"owner": "beta"}
	reopen := base
	reopen.Evidence = "resumed"
	reopen.Clear = []string{"lifecycle"}
	reopen.Fields = map[string]string{"implementation_status": "PAUSED", "owner": "gamma"}
	closure := base
	closure.Evidence = "accepted"
	closure.Fields = map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:45:00Z"}
	dir := t.TempDir()
	var log []byte
	for _, update := range []syncUpdate{cancel, assignment, reopen, closure} {
		raw, err := json.Marshal(update)
		require.NoError(t, err)
		log = append(log, []byte(`{"event":"lifecycle_change","details":{"status_update":{"updates":[`+string(raw)+`]}}}`+"\n")...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), log, 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out := []byte(linkedReport)
	for i := 0; i < 3; i++ {
		var err error
		out, _, err = projectLogStatus(dir, out, ledger, forecasts)
		require.NoError(t, err)
	}
	r, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, statusClosed, r.Lanes[0].ImplementationStatus)
	require.Equal(t, "gamma", r.Lanes[0].Owner)
	require.Empty(t, r.StatusSync.Groups)
}
