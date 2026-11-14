package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLaneAdditionWaitsForMatchingCanonicalOutcome checks source-backed retry.
//
// Example: A lane is added only after the linked outcome agrees with its event.
func TestLaneAdditionWaitsForMatchingCanonicalOutcome(t *testing.T) {
	t.Parallel()
	// source links active lane work to an initially inconsistent outcome.
	//
	// Example: Correcting the canonical description permits the pending addition.
	const source = `  outcome:third: {section: progress, type: outcome, data: {description: Wrong result}}
  work:third:
    section: progress
    type: work
    links: {outcomes: ['outcome:third']}
    data: {owner: three, state: active, next_action: Finish, reporting_roles: [lane], forecast_ref: 'forecasts.yaml#/records/third'}
`
	// forecast is the matching canonical active deadline for that work.
	//
	// Example: A valid forecast alone does not authorize a mismatched outcome.
	const forecast = `  third:
    kind: lane
    outcome: Third result
    status: active
    assessment: complete
    target: '2026-11-02T18:00:00Z'
    original_baseline: '2026-11-02T18:00:00Z'
    baseline_evidence: initial
    recalibration: {status: unchanged, evidence: initial}
`
	// event requests the lane using the intended outcome and forecast.
	//
	// Example: The source mismatch must not create a partial report lane.
	const event = `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"lane","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/work:third","outcome":"Third result","required_environment":"implementation","implementation_status":"NEW","test_status":"NEW","prod_status":"NEW","forecast":{"forecast_ref":"forecasts.yaml#/records/third","outcome":"Third result","target":"2026-11-02T18:00:00Z","assessment":"complete"}}}]}}}` + "\n"
	ledger := linkedLedger + source
	dir := writeLinkedSources(t, ledger, linkedForecasts+forecast)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	pending, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(pending)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 2)
	require.Len(t, report.StatusSync.PendingAdditions, 1)
	fixed := strings.Replace(ledger, "description: Wrong result", "description: Third result", 1)
	require.NotEqual(t, ledger, fixed)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(fixed), 0600))
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 3)
	require.Equal(t, "Third result", report.Lanes[2].Outcome)
	require.NotNil(t, report.Lanes[2].Forecast)
	require.Empty(t, report.StatusSync.PendingAdditions)
	require.NoError(t, refreshTestStatus(dir, "requirement_change"))
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
}
