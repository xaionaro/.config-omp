package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStatusSyncClosesOnlyExplicitTask checks task identity isolation.
//
// Example: Accepting one task leaves its sibling open.
func TestStatusSyncClosesOnlyExplicitTask(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "latest-status-report.yaml"), []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	log := `{"schema":"eci-high-level-log/v1","event":"lifecycle_change","timestamp":"2026-11-02T15:25:00Z","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"accepted"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	data, err := os.ReadFile(filepath.Join(dir, "latest-status-report.yaml"))
	require.NoError(t, err)
	report, err := parse(data)
	require.NoError(t, err)
	require.Equal(t, statusClosed, report.Tasks[0].Status)
	require.Equal(t, "accepted", report.Tasks[0].Evidence)
	require.Equal(t, statusInProgress, report.Tasks[1].Status)
}

// TestStatusSyncCorrectionDoesNotOverwriteNewerValue checks correction ordering.
//
// Example: A correction naming an old event cannot replace a newer value.
func TestStatusSyncCorrectionDoesNotOverwriteNewerValue(t *testing.T) {
	dir := t.TempDir()
	log := strings.Join([]string{
		`{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"work","fields":{"text":"first"}}]}}}`,
		`{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"work","fields":{"text":"newer"}}]}}}`,
		`{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work","corrects":1,"fields":{"text":"stale correction"}}]}}}`,
	}, "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	updated, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Contains(t, string(updated), "newer")
	require.NotContains(t, string(updated), "stale correction")
	require.Contains(t, string(updated), "invalid status_update requires manual review")
	retry, _, err := projectLogStatus(dir, updated, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, updated, retry)
}

// TestStatusSyncLaneCompletionIsAtomicAndSourceBacked checks lane completion.
//
// Example: Status and completion time change together after source completion.
func TestStatusSyncLaneCompletionIsAtomicAndSourceBacked(t *testing.T) {
	dir := t.TempDir()
	report := strings.Replace(linkedReport, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	log := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:first","required_environment":"implementation","evidence":"accepted","fields":{"implementation_status":"CLOSED","completed_at":"2026-11-02T15:25:00Z"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	ledger := ledgerSource{Records: map[string]sourceRecord{"work:first": {Type: "work", Data: workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:25:00Z"}}}}
	updated, _, err := projectLogStatus(dir, []byte(report), ledger, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, statusClosed, parsed.Lanes[0].ImplementationStatus)
	require.Equal(t, "2026-11-02T15:25:00Z", parsed.Lanes[0].CompletedAt)
	require.Equal(t, statusInProgress, parsed.Lanes[1].ImplementationStatus)
	ledger.Records["work:first"] = sourceRecord{Type: "work", Data: workData{State: workStateActive}}
	unchanged, _, err := projectLogStatus(dir, []byte(report), ledger, forecastSource{})
	require.NoError(t, err)
	parsed, err = parse(unchanged)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, parsed.Lanes[0].ImplementationStatus)
	require.Empty(t, parsed.Lanes[0].CompletedAt)
}

// TestStatusSyncAddsTaskWithItsPriorityEstimate checks grouped insertion.
//
// Example: A task introduces a priority together with its estimate.
func TestStatusSyncAddsTaskWithItsPriorityEstimate(t *testing.T) {
	dir := t.TempDir()
	log := `{"event":"requirement_change","details":{"status_update":{"additions":[{"target":"task","record":{"id":"3","title":"New","priority":"low","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"NEW"}},{"target":"priority_estimate","record":{"priority":"low","min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	updated, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Len(t, parsed.Tasks, 3)
	require.Equal(t, taskID("3"), parsed.Tasks[2].ID)
	require.Len(t, parsed.PriorityEstimates, 2)
	retry, _, err := projectLogStatus(dir, updated, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, updated, retry)
}

// TestStatusSyncCancelsOnlyNamedTask checks independent cancellation.
//
// Example: A cancelled task leaves its sibling open and unmodified.
func TestStatusSyncCancelsOnlyNamedTask(t *testing.T) {
	dir := t.TempDir()
	log := `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"lifecycle":"cancelled","status":"unknown","evidence":"cancelled by owner"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	updated, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, taskLifecycleCancelled, parsed.Tasks[0].Lifecycle)
	require.Equal(t, statusUnknown, parsed.Tasks[0].Status)
	require.Equal(t, statusInProgress, parsed.Tasks[1].Status)
	require.NotContains(t, parsed.TopPriorities, taskID("1"))
}

// TestStatusSyncRetractionDropsOnlyItsManagedEntry checks precise retraction.
//
// Example: Retracting one progress subject leaves legacy prose untouched.
func TestStatusSyncRetractionDropsOnlyItsManagedEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"work","fields":{"text":"managed"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	updated, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	correction := `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work","corrects":1,"retract":true}]}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(first+correction), 0600))
	updated, _, err = projectLogStatus(dir, updated, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	parsed, err := parse(updated)
	require.NoError(t, err)
	require.Equal(t, []string{"Implementation started"}, parsed.Progress)
}

// TestStatusSyncIgnoresClosureWithoutAcceptance checks closure evidence.
//
// Example: A task stays open when acceptance evidence is absent.
func TestStatusSyncIgnoresClosureWithoutAcceptance(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "latest-status-report.yaml"), []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	log := `{"details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	require.NoError(t, refreshTestStatus(dir, "lifecycle_change"))
	data, err := os.ReadFile(filepath.Join(dir, "latest-status-report.yaml"))
	require.NoError(t, err)
	report, err := parse(data)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Tasks[0].Status)
}
