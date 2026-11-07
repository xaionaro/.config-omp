package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReviewCancellationChain checks a cancellation followed by final completion.
//
// Example: The final lane completion is projected after replay.
func TestReviewCancellationChain(t *testing.T) {
	dir := t.TempDir()
	events := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"cancelled","fields":{"lifecycle":"cancelled"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"resumed","clear":["lifecycle"],"fields":{"implementation_status":"PAUSED"}}]}}}` + "\n" + `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(events), 0600); err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out := []byte(linkedReport)
	for i := 0; i < 4; i++ {
		var err error
		out, _, err = projectLogStatus(dir, out, ledger, forecasts)
		if err != nil {
			require.FailNow(t, "assertion failed", err)
		}
	}
	r, err := parse(out)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	if r.Lanes[0].ImplementationStatus != statusClosed || len(r.StatusSync.Groups) != 0 {
		require.FailNowf(t, "assertion failed", "final completion blocked: status=%s groups=%d", r.Lanes[0].ImplementationStatus, len(r.StatusSync.Groups))
	}
}
