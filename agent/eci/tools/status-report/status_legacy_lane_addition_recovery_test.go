package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyLaneAdditionRecoversSelectorAndOwner checks older saved additions.
//
// Example: Recover the full lane identity and preserve a later manual owner edit.
func TestLegacyLaneAdditionRecoversSelectorAndOwner(t *testing.T) {
	t.Parallel()
	// saved is an older pending lane without explicit selector metadata.
	//
	// Example: Reconstruct identity from its complete original record.
	const saved = `{"cursor":1,"applied":{},"latest":{"addition/lane/1":1},"pending":{},"pending_additions":{"addition/lane/1":{"line":1,"target":"lane","record":"{\"task_id\":\"1\",\"work_ref\":\"project-understanding.yaml#/records/w\",\"outcome\":\"Result\",\"owner\":\"first\",\"required_environment\":\"implementation\",\"implementation_status\":\"NEW\",\"test_status\":\"NEW\",\"prod_status\":\"NEW\",\"forecast\":{\"forecast_ref\":\"forecasts.yaml#/records/f\",\"outcome\":\"Result\",\"target\":\"2026-11-02T16:00:00Z\",\"assessment\":\"complete\"}}"}}}`
	// update is a later assignment for that exact lane.
	//
	// Example: Keep an owner's intervening manual edit instead of replacing it.
	const update = `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/w","required_environment":"implementation","fields":{"owner":"later"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0600))
	initial, err := putSyncState([]byte(valid), saved)
	require.NoError(t, err)
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: workData{State: workStateActive, Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}}, "o": {Type: "outcome", Outcome: "Result"}}}
	forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": "active", "kind": "lane"}}}
	inserted, _, err := projectLogStatus(dir, initial, ledger, forecasts)
	require.NoError(t, err)
	report, err := parse(inserted)
	require.NoError(t, err)
	require.Len(t, report.Lanes, 1)
	require.Equal(t, "first", report.Lanes[0].Owner)
	require.Empty(t, report.StatusSync.PendingAdditions)
	edited := strings.Replace(string(inserted), `"owner":"first"`, `"owner":"manual"`, 1)
	require.NotEqual(t, string(inserted), edited)
	require.NoError(t, os.WriteFile(path, []byte("{}\n"+update), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledger, forecasts)
	require.NoError(t, err)
	report, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, "manual", report.Lanes[0].Owner)
	require.Len(t, report.StatusSync.Groups, 1)
}
