package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// explorationProjection applies one log event to a report for testing.
//
// Example: Project a lane closure against its canonical sources.
func explorationProjection(
	t *testing.T,
	report string,
	log string,
	ledger ledgerSource,
	forecasts forecastSource,
) report {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log+"\n"), 0600); err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	out, _, err := projectLogStatus(dir, []byte(report), ledger, forecasts)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	r, err := parse(out)
	if err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	return r
}

// TestExplorationForecastedLaneCompletion checks completion and forecast retirement.
//
// Example: Completed canonical work closes its forecasted lane.
func TestExplorationForecastedLaneCompletion(t *testing.T) {
	t.Parallel()
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	log := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}`
	r := explorationProjection(t, linkedReport, log, ledger, forecasts)
	if r.Lanes[0].ImplementationStatus != statusClosed || r.Lanes[0].Forecast != nil {
		require.FailNowf(t, "assertion failed", "canonical completion + inactive forecast did not close/retire report lane: status=%v forecast=%v pending=%d", r.Lanes[0].ImplementationStatus, r.Lanes[0].Forecast != nil, len(r.StatusSync.Pending))
	}
}

// TestExplorationForecastedLaneCancellation checks cancellation and forecast retirement.
//
// Example: Cancelled canonical work retires its forecast.
func TestExplorationForecastedLaneCancellation(t *testing.T) {
	t.Parallel()
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCancelled, ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	log := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"owner cancelled","fields":{"lifecycle":"cancelled"}}]}}}`
	r := explorationProjection(t, linkedReport, log, ledger, forecasts)
	if r.Lanes[0].Lifecycle != laneLifecycleCancelled || r.Lanes[0].Forecast != nil {
		require.FailNowf(t, "assertion failed", "canonical cancellation + inactive forecast did not cancel/retire report lane: lifecycle=%v forecast=%v pending=%d", r.Lanes[0].Lifecycle, r.Lanes[0].Forecast != nil, len(r.StatusSync.Pending))
	}
}

// TestExplorationClosedLaneCorrection checks reopening clears completion.
//
// Example: A corrected lane returns to in-progress status.
func TestExplorationClosedLaneCorrection(t *testing.T) {
	t.Parallel()
	report := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "    completed_at: '2026-11-02T15:25:00Z'\n", 1)
	report = strings.Replace(report, "implementation_status: IN PROGRESS", "implementation_status: CLOSED", 1)
	log := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","fields":{"implementation_status":"IN PROGRESS"}}]}}}`
	r := explorationProjection(t, report, log, ledgerSource{}, forecastSource{})
	if r.Lanes[0].ImplementationStatus != statusInProgress || r.Lanes[0].CompletedAt != "" {
		require.FailNowf(t, "assertion failed", "reopening lane could not clear terminal completion: status=%v completed_at=%q pending=%d", r.Lanes[0].ImplementationStatus, r.Lanes[0].CompletedAt, len(r.StatusSync.Pending))
	}
}

// TestExtendedAdditionRequiresIdentityAndCompleteRecords checks addition validation.
//
// Example: A root completion needs a completion time.
func TestExtendedAdditionRequiresIdentityAndCompleteRecords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		target, record string
		want           bool
	}{
		{"lane", `{"task_id":"1","work_ref":"project-understanding.yaml#/records/work:first","outcome":"First result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW"}`, true},
		{"lane", `{"task_id":"1","outcome":"First result"}`, false},
		{"root_completion", `{"work_ref":"project-understanding.yaml#/records/root","outcome":"Root result","completed_at":"2026-11-02T15:00:00Z"}`, true},
		{"root_completion", `{"work_ref":"project-understanding.yaml#/records/root","outcome":"Root result"}`, false},
		{"root_forecast", `{"forecast_ref":"forecasts.yaml#/records/root","outcome":"Root result","target":"2026-11-02T16:00:00Z","assessment":"complete"}`, true},
	} {
		input := []byte(valid)
		got, ok := applySyncAddition(input, syncAddition{Target: parseSyncTarget(tc.target), Record: json.RawMessage(tc.record)})
		if ok != tc.want {
			assert.Failf(t, "assertion failed", "%s: accepted=%v wanted %v: %s", tc.target, ok, tc.want, got)
		}
	}
}

// TestLaneTransitionRequiresCanonicalIdentity checks transition source validation.
//
// Example: An active forecast prevents closure.
func TestLaneTransitionRequiresCanonicalIdentity(t *testing.T) {
	t.Parallel()
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Evidence: "accepted", Fields: map[string]string{"implementation_status": "CLOSED", "completed_at": "2026-11-02T15:25:00Z"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z", ForecastRef: "forecasts.yaml#/records/first"}}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"first": {"status": "inactive"}}}
	candidate, effects, accepted := applyLaneSyncUpdate([]byte(linkedReport), update, ledger, forecasts, nil)
	if !accepted {
		require.FailNow(t, "assertion failed", "transition effects rejected")
	}
	if err := verifyStatusSyncEffects([]byte(linkedReport), candidate, effects); err != nil {
		require.FailNowf(t, "assertion failed", "effects disagree with transition: %v", err)
	}
	if err := verifyStatusSyncCanonicalEffects([]byte(linkedReport), candidate, effects, ledger, forecasts); err != nil {
		require.FailNowf(t, "assertion failed", "canonical effects rejected: %v", err)
	}
	got, ok := applyLaneSyncUpdateRaw([]byte(linkedReport), update, ledger, forecasts, nil)
	if !ok {
		removed, rok := removeLaneSyncField([]byte(linkedReport), update, "forecast")
		timed, tok := applySyncField(removed, update, "completed_at", update.Fields["completed_at"], "", false)
		closed, cok := applySyncField(timed, update, "implementation_status", "CLOSED", "", false)
		_, err := parse(closed)
		require.FailNowf(t, "assertion failed", "valid transition rejected: remove=%v time=%v close=%v parse=%v candidate=%s", rok, tok, cok, err, closed)
	}
	r, err := parse(got)
	if err != nil || r.Lanes[0].Forecast != nil || r.Lanes[0].ImplementationStatus != statusClosed {
		require.FailNowf(t, "assertion failed", "invalid transition: %v %s", err, got)
	}
	forecasts.Records["first"]["status"] = "active"
	if got, ok = applyLaneSyncUpdateRaw([]byte(linkedReport), update, ledger, forecasts, nil); ok || string(got) != linkedReport {
		require.FailNow(t, "assertion failed", "active forecast allowed closure")
	}
	forecasts.Records["first"]["status"] = "inactive"
	update.Evidence = ""
	if _, ok = applyLaneSyncUpdateRaw([]byte(linkedReport), update, ledger, forecasts, nil); ok {
		require.FailNow(t, "assertion failed", "closure without evidence accepted")
	}
}

// TestLaneTransitionPreservesCommentsAndOwnerCompletion checks ownership preservation.
//
// Example: Reopening preserves an owner comment.
func TestLaneTransitionPreservesCommentsAndOwnerCompletion(t *testing.T) {
	t.Parallel()
	closed := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "    completed_at: '2026-11-02T15:25:00Z' # owner note\n", 1)
	closed = strings.Replace(closed, "implementation_status: IN PROGRESS", "implementation_status: CLOSED", 1)
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Fields: map[string]string{"implementation_status": "IN PROGRESS"}}
	if _, ok := applyLaneSyncUpdateRaw([]byte(closed), update, ledgerSource{}, forecastSource{}, nil); ok {
		require.FailNow(t, "assertion failed", "removed owner comment")
	}
	closed = strings.Replace(closed, " # owner note", "", 1)
	if _, ok := applyLaneSyncUpdateRaw([]byte(closed), update, ledgerSource{}, forecastSource{}, map[string]string{"completed_at": "2026-11-02T15:24:00Z"}); ok {
		require.FailNow(t, "assertion failed", "removed owner-edited completion")
	}
	got, ok := applyLaneSyncUpdateRaw([]byte(closed), update, ledgerSource{}, forecastSource{}, nil)
	if !ok || strings.Contains(string(got), "completed_at:") {
		require.FailNow(t, "assertion failed", "reopen did not clear stale completion")
	}
}

// TestLaneExplicitClearIsAtomicAndPreservesOwnership checks explicit field clears.
//
// Example: An owner edit prevents a conflicting clear.
func TestLaneExplicitClearIsAtomicAndPreservesOwnership(t *testing.T) {
	t.Parallel()
	original := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    blocker: waiting\n    next_action: retry\n    implementation_status: IN PROGRESS", 1)
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Clear: []string{"blocker", "next_action"}}
	got, effects, ok := applyLaneSyncUpdate([]byte(original), update, ledgerSource{}, forecastSource{}, map[string]string{"blocker": "waiting"})
	if !ok || strings.Contains(string(got), "blocker: waiting") || strings.Contains(string(got), "next_action: retry") {
		require.FailNow(t, "assertion failed", "clear failed")
	}
	if err := verifyStatusSyncEffects([]byte(original), got, effects); err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	if _, _, ok := applyLaneSyncUpdate([]byte(original), update, ledgerSource{}, forecastSource{}, map[string]string{"blocker": "old"}); ok {
		require.FailNow(t, "assertion failed", "owner edit overwritten")
	}
	update.Clear = []string{"forecast"}
	if _, _, ok := applyLaneSyncUpdate([]byte(original), update, ledgerSource{}, forecastSource{}, nil); ok {
		require.FailNow(t, "assertion failed", "unverified forecast clear accepted")
	}
}

// TestLaneClearCancellationRequiresActiveCanonicalWork checks cancellation reversal.
//
// Example: Cancelled canonical work cannot reopen.
func TestLaneClearCancellationRequiresActiveCanonicalWork(t *testing.T) {
	t.Parallel()
	original := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    lifecycle: cancelled\n    implementation_status: IN PROGRESS", 1)
	original = strings.Replace(original, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Evidence: "reopened", Clear: []string{"lifecycle"}}
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateActive}}}}
	got, effects, ok := applyLaneSyncUpdate([]byte(original), update, ledger, forecastSource{}, nil)
	if !ok || strings.Contains(string(got), "lifecycle: cancelled") {
		require.FailNow(t, "assertion failed", "active work did not reopen")
	}
	if err := verifyStatusSyncEffects([]byte(original), got, effects); err != nil {
		require.FailNow(t, "assertion failed", err)
	}
	ledger.Records["work:first"] = sourceRecord{Type: "work", Data: workData{State: workStateCancelled}}
	if _, _, ok := applyLaneSyncUpdate([]byte(original), update, ledger, forecastSource{}, nil); ok {
		require.FailNow(t, "assertion failed", "cancelled canonical work reopened")
	}
}

// TestLanePreviouslyClearedFieldProtectsOwner checks projected absence ownership.
//
// Example: A later owner addition is preserved.
func TestLanePreviouslyClearedFieldProtectsOwner(t *testing.T) {
	t.Parallel()
	update := syncUpdate{Target: syncTargetLane, ID: "1", WorkRef: "project-understanding.yaml#/records/work:first", RequiredEnvironment: syncEnvironmentImplementation, Fields: map[string]string{"blocker": "new"}}
	if _, _, ok := applyLaneSyncUpdate([]byte(linkedReport), update, ledgerSource{}, forecastSource{}, map[string]string{"blocker": syncAbsentValue}); !ok {
		require.FailNow(t, "assertion failed", "projected absence blocked update")
	}
	edited := strings.Replace(linkedReport, "    implementation_status: IN PROGRESS", "    blocker: owner\n    implementation_status: IN PROGRESS", 1)
	if _, _, ok := applyLaneSyncUpdate([]byte(edited), update, ledgerSource{}, forecastSource{}, map[string]string{"blocker": syncAbsentValue}); ok {
		require.FailNow(t, "assertion failed", "owner addition overwritten")
	}
}
