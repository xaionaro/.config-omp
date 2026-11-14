package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMergedLifecycleScalarChecksResolvedValue exercises merged scalar validation.
//
// Example: A merged null is rejected, while an explicit string override is valid.
func TestMergedLifecycleScalarChecksResolvedValue(t *testing.T) {
	t.Parallel()
	bad := strings.Replace(linkedReport, "    required_environment: implementation", "    <<: {lifecycle: null}\n    required_environment: implementation", 1)
	_, err := parse([]byte(bad))
	require.Error(t, err)
	good := strings.Replace(bad, "    <<: {lifecycle: null}", "    <<: {lifecycle: null}\n    lifecycle: ''", 1)
	_, err = parse([]byte(good))
	require.NoError(t, err)
}

// TestCanonicalFutureCompletionIsVisible checks source time independently of report time.
//
// Example: A future ledger completion remains incomplete even when the report says it was earlier.
func TestCanonicalFutureCompletionIsVisible(t *testing.T) {
	t.Parallel()
	report := strings.Replace(linkedReport, "implementation_status: IN PROGRESS", "implementation_status: CLOSED\n    completed_at: '2026-11-02T15:00:00Z'", 1)
	report = strings.Replace(report, "    forecast: {forecast_ref: 'forecasts.yaml#/records/first', outcome: First result, target: '2026-11-02T16:00:00Z', assessment: complete, baseline: '2026-11-02T16:00:00Z', evidence: initial}\n", "", 1)
	ledger := strings.Replace(linkedLedger, "state: active, next_action: Finish, reporting_roles: [lane]", "state: completed, completed_at: '2026-11-02T16:00:00Z', reporting_roles: [lane]", 1)
	forecasts := strings.Replace(linkedForecasts, "status: active", "status: inactive", 1)
	out := lifecycleResult(t, report, ledger, forecasts)
	require.Contains(t, out, "work work:first actual completed_at is in the future")
}
