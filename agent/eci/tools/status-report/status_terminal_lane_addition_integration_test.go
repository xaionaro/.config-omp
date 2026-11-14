package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTerminalLaneAdditionRetriesAfterSourceCompletion checks source-backed replay.
//
// Example: An early completion stays pending until both work and forecast close.
func TestTerminalLaneAdditionRetriesAfterSourceCompletion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// event is a complete lane accepted by the producer.
	//
	// Example: Record a completed implementation lane.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"lane","evidence":"accepted","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","required_environment":"implementation","implementation_status":"CLOSED","test_status":"NEW","prod_status":"NEW","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "active", "kind": "lane"}}}
	pending, _, err := projectLogStatus(dir, []byte(valid), ledger, forecasts)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Empty(t, report.Lanes)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	ledger.Records["w"] = sourceRecord{Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}}
	stillPending, _, err := projectLogStatus(dir, pending, ledger, forecasts)
	require.NoError(t, err)
	report, err = parse(stillPending)
	require.NoError(t, err)
	require.Empty(t, report.Lanes)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	forecasts.Records["f"]["status"] = "inactive"
	completed, _, err := projectLogStatus(dir, stillPending, ledger, forecasts)
	require.NoError(t, err)
	report, err = parse(completed)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 1)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Equal(t, "2026-11-02T15:00:00Z", report.Lanes[0].CompletedAt)
	require.Empty(t, report.StatusSync.PendingAdditions)
	retried, _, err := projectLogStatus(dir, completed, ledger, forecasts)
	require.NoError(t, err)
	require.Equal(t, completed, retried)
}

// TestTerminalLaneAdditionRefreshCLIRecoversPendingEvent exercises persisted retries.
//
// Example: A later source completion lets a saved log event add its lane.
func TestTerminalLaneAdditionRefreshCLIRecoversPendingEvent(t *testing.T) {
	t.Parallel()
	// event requests a source-backed completed lane.
	//
	// Example: The source initially remains active when the event arrives.
	const event = `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"lane","evidence":"accepted","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/work:third","outcome":"Third result","required_environment":"implementation","implementation_status":"CLOSED","test_status":"NEW","prod_status":"NEW","completed_at":"2026-11-02T15:00:00Z"}}]}}}` + "\n"
	// active supplies an in-progress source with an active forecast.
	//
	// Example: Publish canonical completion only after the producer's event.
	const active = `  outcome:third:
    section: progress
    type: outcome
    data: {description: Third result}
  work:third:
    section: progress
    type: work
    links: {outcomes: ['outcome:third']}
    data: {owner: three, state: active, next_action: Finish, reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/third'}
    evidence: []
`
	// forecast supplies the third source forecast.
	//
	// Example: Retire this forecast when canonical work completes.
	const forecast = `  third:
    kind: lane
    outcome: Third result
    status: active
    assessment: complete
    target: '2026-11-02T16:00:00Z'
    original_baseline: '2026-11-02T16:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(valid), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger+active), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts+forecast), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "lifecycle_change"}, strings.NewReader(""), io.Discard, &stderr), stderr.String())
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(before)
	require.NoError(t, err)
	require.Empty(t, report.Lanes)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	completed := strings.Replace(active, "state: active, next_action: Finish", "state: completed, completed_at: '2026-11-02T15:00:00Z', next_action: Finish", 1)
	inactive := strings.Replace(forecast, "status: active", "status: inactive", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger+completed), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts+inactive), 0600))
	stderr.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "lifecycle_change"}, strings.NewReader(""), io.Discard, &stderr), stderr.String())
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(after)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 1)
	require.Equal(t, statusClosed, report.Lanes[0].ImplementationStatus)
	require.Empty(t, report.StatusSync.PendingAdditions)
	require.Contains(t, string(after), "project-understanding.yaml#/records/work:third")
}
