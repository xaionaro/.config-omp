package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeReportOpaqueValues validates native reports without rounding generic values.
//
// Example: A 1e400 decision is preserved while a wrong-shaped list is rejected.
func TestNativeReportOpaqueValues(t *testing.T) {
	t.Parallel()
	input := `{"timestamp":"2026-11-02T15:24:00Z","state":"Working","progress":[],"decisions":[{"amount":1e400,"precise":1.10}],"stuck_work_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}`
	report, err := parseDBReport([]byte(input))
	require.NoError(t, err)
	value := report.Decisions[0].(map[string]any)
	require.Equal(t, json.Number("1e400"), value["amount"])
	require.Equal(t, json.Number("1.10"), value["precise"])
	invalid := strings.Replace(input, `"decisions":[{"amount":1e400,"precise":1.10}]`, `"decisions":{}`, 1)
	_, err = parseDBReport([]byte(invalid))
	require.Error(t, err)
}

// TestNativeReportRejectsCoercedStrings keeps consumed fields strongly typed.
//
// Example: A numeric owner is not a textual owner.
func TestNativeReportRejectsCoercedStrings(t *testing.T) {
	t.Parallel()
	valid := `{"timestamp":"2026-11-02T15:24:00Z","state":"Working","progress":[],"decisions":[],"stuck_work_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[{"id":"1","title":"Work","owner":"Alice","priority":"high","impact":"Useful","effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"},"status":"IN PROGRESS"}],"top_priorities":["1"],"priority_estimates":[{"priority":"high","min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}`
	_, err := parseDBReport([]byte(valid))
	require.NoError(t, err)
	for _, invalid := range []string{
		strings.Replace(valid, `"state":"Working"`, `"state":true`, 1),
		strings.Replace(valid, `"owner":"Alice"`, `"owner":7`, 1),
	} {
		_, err = parseDBReport([]byte(invalid))
		require.Error(t, err)
	}
}
