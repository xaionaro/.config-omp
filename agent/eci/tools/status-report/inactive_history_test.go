package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// inactiveHistoryCase describes one targetless canonical history fixture.
//
// Example: An unchanged record may have equivalent prior and current instants.
type inactiveHistoryCase struct {
	name, prior, current, state string
	incomplete                  bool
}

// TestInactiveUnchangedHistory compares prior and current through CLI and refresh.
//
// Example: A targetless inactive forecast cannot claim unequal unchanged instants.
func TestInactiveUnchangedHistory(t *testing.T) {
	for _, tc := range []inactiveHistoryCase{
		{name: "unequal", prior: "2026-11-02T15:00:00Z", current: "2026-11-02T16:00:00Z", state: "unchanged", incomplete: true},
		{name: "equivalent", prior: "2026-11-02T16:00:00+00:00", current: "2026-11-02T16:00:00Z", state: "unchanged"},
		{name: "prior only", prior: "2026-11-02T15:00:00Z", state: "unchanged"},
		{name: "current only", current: "2026-11-02T16:00:00Z", state: "unchanged"},
		{name: "changed", prior: "2026-11-02T15:00:00Z", current: "2026-11-02T16:00:00Z", state: "changed"},
	} {
		t.Run(tc.name,
			// Check one targetless canonical history case.
			//
			// Example: Equivalent UTC spellings stay complete.
			func(t *testing.T) { checkInactiveHistory(t, tc) })
	}
}

// checkInactiveHistory exercises report and refresh using one history fixture.
//
// Example: Unequal unchanged instants remain visibly incomplete.
func checkInactiveHistory(
	t *testing.T,
	tc inactiveHistoryCase,
) {
	t.Helper()
	report := strings.Replace(linkedReport, "implementation_status: IN PROGRESS", "implementation_status: CLOSED\n    completed_at: '2026-11-02T15:00:00Z'", 1)
	report = strings.Replace(report, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish, reporting_roles: [lane]", "state: completed, completed_at: '2026-11-02T15:00:00Z', reporting_roles: [lane]", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	forecasts = strings.Replace(forecasts, "    target: '2026-11-02T16:00:00+00:00'\n", "", 1)
	history := "status: " + tc.state + ", evidence: initial"
	if tc.prior != "" {
		history += ", prior: '" + tc.prior + "'"
	}
	if tc.current != "" {
		history += ", current: '" + tc.current + "'"
	}
	if tc.state == "changed" {
		history += ", reason: revised"
	}
	forecasts = strings.Replace(forecasts, "status: unchanged, evidence: initial", history, 1)
	dir := writeLinkedSources(t, ledger, forecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	for _, command := range []string{"validate", "render"} {
		checkInactiveCLI(t, command, path, tc.incomplete)
	}
	at, err := parseUTC("2026-11-02T15:24:00Z")
	require.NoError(t, err)
	require.NoError(t, refreshStatusAt(dir, "forecast_change", at))
	refreshed, err := os.ReadFile(path)
	require.NoError(t, err)
	if tc.incomplete {
		require.Contains(t, string(refreshed), "unchanged recalibration prior and current differ")
		return
	}
	require.NotContains(t, string(refreshed), "unchanged recalibration prior and current differ")
}

// checkInactiveCLI verifies one validate or render result.
//
// Example: Validation fails while rendering stays useful for conflicting history.
func checkInactiveCLI(
	t *testing.T,
	command string,
	path string,
	incomplete bool,
) {
	t.Helper()
	var out, errs bytes.Buffer
	code := testRun([]string{command, path}, strings.NewReader(""), &out, &errs)
	if command == "render" {
		require.NotContains(t, out.String(), "Forecast deadline: First result")
	}
	if !incomplete {
		require.NotContains(t, out.String(), "INCOMPLETE")
		require.Equal(t, 0, code)
		return
	}
	require.Contains(t, out.String(), "unchanged recalibration prior and current differ")
	if command == "validate" {
		require.Equal(t, 1, code)
	}
}
