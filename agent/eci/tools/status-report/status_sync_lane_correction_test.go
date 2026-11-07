package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDelayedDocumentedLaneCorrection checks a pending correction locator.
//
// Example: A current completion corrects an unapplied historical completion.
func TestDelayedDocumentedLaneCorrection(t *testing.T) {
	dir := t.TempDir()
	old := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	next := `{"event":"correction","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1,"evidence":"corrected","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(old+next), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].CompletedAt != "2026-11-02T15:45:00Z" || len(r.StatusSync.Groups) != 0 {
		t.Fatalf("documented correction blocked by stale historical closure: status=%s completed_at=%s groups=%d", r.Lanes[0].ImplementationStatus, r.Lanes[0].CompletedAt, len(r.StatusSync.Groups))
	}
}

// TestPendingLaneCorrectionRequiresExactLocator checks invalid correction references.
//
// Example: A correction naming the wrong ordinal cannot publish a completion.
func TestPendingLaneCorrectionRequiresExactLocator(t *testing.T) {
	dir := t.TempDir()
	old := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	next := strings.Replace(old, "15:25:00Z", "15:45:00Z", 1)
	next = strings.Replace(next, `"event":"lifecycle_change"`, `"event":"correction"`, 1)
	next = strings.Replace(next, `"evidence":"accepted"`, `"corrects":1,"corrects_ordinal":1,"evidence":"corrected"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(old+next), 0600); err != nil {
		t.Fatal(err)
	}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lanes[0].ImplementationStatus != statusInProgress || len(r.StatusSync.Groups) == 0 {
		t.Fatalf("wrong locator accepted: status=%s groups=%d", r.Lanes[0].ImplementationStatus, len(r.StatusSync.Groups))
	}
}

// TestPendingLaneCorrectionPreservesContext checks exact whole-transaction correction.
//
// Example: A corrected completion time retains the explicitly supplied context.
func TestPendingLaneCorrectionPreservesContext(t *testing.T) {
	dir := t.TempDir()
	first := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z","context":"verified result"}}]}}}` + "\n"
	last := `{"event":"correction","details":{"corrects":1,"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","corrects":1,"evidence":"corrected actual time","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:45:00Z","context":"verified result"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(first+last), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:45:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"kind": "lane", "status": "inactive"}}}
	out, _, err := projectLogStatus(dir, []byte(linkedReport), ledger, forecasts)
	require.NoError(t, err)
	r, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, statusClosed, r.Lanes[0].ImplementationStatus)
	require.Equal(t, "2026-11-02T15:45:00Z", r.Lanes[0].CompletedAt)
	require.Equal(t, "verified result", r.Lanes[0].Context)
	require.Empty(t, r.StatusSync.Groups)
}
