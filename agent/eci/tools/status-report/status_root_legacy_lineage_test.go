package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLegacyAppliedRootCorrectionLineage checks every immutable predecessor.
//
// Example: A duplicate or intervening same-work event prevents reconciliation.
func TestLegacyAppliedRootCorrectionLineage(t *testing.T) {
	t.Parallel()
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", Data: workData{Roles: []reportingRole{reportingRoleRoot}}}}}
	firstRecord := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	secondRecord := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}`
	thirdRecord := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:10:00Z"}`
	firstEntry := `{"target":"root_completion","evidence":"accepted","record":` + firstRecord + `}`
	first := `{"event":"lifecycle_change","details":{"status_update":{"additions":[` + firstEntry + `]}}}`
	second := `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":` + firstRecord + `,"record":` + secondRecord + `}]}}}`
	third := `{"event":"correction","details":{"corrects":2,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected again","expected_prior":` + secondRecord + `,"record":` + thirdRecord + `}]}}}`
	cases := []struct {
		name, log string
		want      bool
	}{
		{"valid recursive", first + "\n" + second + "\n" + third, true},
		{"ambiguous predecessor", strings.Replace(first, firstEntry, firstEntry+","+firstEntry, 1) + "\n" + second + "\n" + third, false},
		{"wrong ancestor", first + "\n" + strings.Replace(second, `"corrects":1`, `"corrects":9`, 1) + "\n" + third, false},
		{"intervening work", first + "\n" + strings.Replace(second, `"additions":[`, `"additions":[`+firstEntry+`,`, 1) + "\n" + third, false},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Check the selected chain against its source history.
			//
			// Example: A duplicate predecessor is ambiguous.
			func(t *testing.T) {
				lines := bytes.Split([]byte(tc.log), []byte("\n"))
				var event syncEvent
				require.NoError(t, json.Unmarshal(lines[2], &event))
				old := syncPendingAddition{Line: 3, Target: syncTargetRootCompletion, Record: thirdRecord, ExpectedPrior: secondRecord, PriorLine: 2, PriorRecord: secondRecord}
				require.Equal(t, tc.want, legacyAppliedRootCorrectionLineage(lines, old, event, 0, ledger))
			})
	}
}

// TestLegacyCorrectionDoesNotSkipForecast checks linked forecast history.
//
// Example: A forecast event between a completion and correction prevents clearing pending metadata.
func TestLegacyCorrectionDoesNotSkipForecast(t *testing.T) {
	t.Parallel()
	first := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}`
	next := `{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}`
	log := `{"event":"lifecycle_change","details":{"status_update":{"additions":[{"target":"root_completion","evidence":"done","record":` + first + `}]}}}` + "\n" + `{"event":"decision","details":{"status_update":{"additions":[{"target":"root_forecast","record":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"working"}}]}}}` + "\n" + `{"event":"correction","details":{"corrects":1,"status_update":{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":` + first + `,"record":` + next + `}]}}}`
	lines := bytes.Split([]byte(log), []byte("\n"))
	var event syncEvent
	require.NoError(t, json.Unmarshal(lines[2], &event))
	old := syncPendingAddition{Line: 3, Target: syncTargetRootCompletion, Record: next, ExpectedPrior: first, PriorLine: 1, PriorRecord: first}
	ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", Data: workData{Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}}}}
	require.False(t, legacyAppliedRootCorrectionLineage(lines, old, event, 0, ledger))
	unrelated := bytes.Split([]byte(strings.Replace(log, "forecasts.yaml#/records/f", "forecasts.yaml#/records/other", 1)), []byte("\n"))
	require.True(t, legacyAppliedRootCorrectionLineage(unrelated, old, event, 0, ledger))
	require.False(t, legacyAppliedRootCorrectionLineage(unrelated, old, event, 0, ledgerSource{}))
	invalid := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", Data: workData{Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "bad-ref"}}}}
	require.False(t, legacyAppliedRootCorrectionLineage(unrelated, old, event, 0, invalid))
}
