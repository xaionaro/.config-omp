package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestForecastSpreadUnitConsistency checks the canonical target against supplied spread endpoints.
//
// Example: A known scenario later than the target remains an inconsistency.
func TestForecastSpreadUnitConsistency(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, target, base, endpoint string
		wantIssue                    bool
	}{
		{"valid", "2030-01-01T02:00:00Z", "2030-01-01T01:00:00Z", "2030-01-01T02:00:00+00:00", false},
		{"target earlier", "2030-01-01T01:30:00Z", "2030-01-01T01:00:00Z", "2030-01-01T02:00:00Z", true},
		{"target later", "2030-01-01T03:00:00Z", "2030-01-01T01:00:00Z", "2030-01-01T02:00:00Z", true},
		{"range", "2030-01-01T02:00:00Z", "2030-01-01T01:00:00Z", "2030-01-01T01:30:00Z–2030-01-01T02:00:00Z", false},
		{"reversed", "2030-01-01T02:00:00Z", "2030-01-01T01:00:00Z", "2030-01-01T02:00:00Z–2030-01-01T01:30:00Z", true},
		{"unknown", "2030-01-01T02:00:00Z", "2030-01-01T01:00:00Z", "unknown", true},
		{"qualifier", "2030-01-01T02:00:00Z", "2030-01-01T01:00:00Z", "conditional 2030-01-01T02:00:00Z", true},
		{"missing base", "2030-01-01T02:00:00Z", "", "2030-01-01T02:00:00Z", true},
	}
	for _, tc := range cases {
		t.Run(tc.name,
			// Verify a source spread without changing supplied values.
			//
			// Example: A supported range uses its upper endpoint.
			func(t *testing.T) {
				src := map[string]any{"status": "active", "target": tc.target, "scenarios": []any{map[string]any{"name": "delay", "trigger": "risk", "affected_work": "work", "evidence": "basis", "assumptions": "none", "added_duration": "10 minutes", "event_time": "2030-01-01T00:00:00Z", "endpoint": tc.endpoint, "basis": "guess"}}}
				if tc.base != "" {
					src["base_case"] = map[string]any{"endpoint": tc.base, "packages": []any{map[string]any{"work": "work", "duration": "10 minutes", "basis": "guess"}}}
				}
				issues := strings.Join(forecastStructure(src), "\n")
				if tc.wantIssue {
					require.Contains(t, issues, "forecast spread")
					return
				}
				require.Empty(t, issues)
			})
	}
}

// TestForecastSpreadUnknownDoesNotMaskKnownViolation checks partial spreads.
//
// Example: An unknown scenario cannot hide another endpoint past the target.
func TestForecastSpreadUnknownDoesNotMaskKnownViolation(t *testing.T) {
	t.Parallel()
	src := map[string]any{"status": "active", "target": "2030-01-01T01:00:00Z", "base_case": map[string]any{"endpoint": "2030-01-01T00:30:00Z"}, "scenarios": []any{map[string]any{"endpoint": "unknown"}, map[string]any{"endpoint": "2030-01-01T02:00:00Z"}}}
	issues := strings.Join(forecastSpreadIssues(src), "\n")
	require.Contains(t, issues, "exceeds target")
	require.Contains(t, issues, "unverified")
	require.Empty(t, forecastSpreadIssues(map[string]any{"status": "active", "target": "2030-01-01T01:00:00Z"}))
	require.Empty(t, forecastSpreadIssues(map[string]any{"status": "active", "target": "2030-01-01T01:00:00Z", "scenarios": []any{}}))
	scenariosOnly := map[string]any{"status": "active", "target": "2030-01-01T01:00:00Z", "original_baseline": "2030-01-01T01:00:00Z", "scenarios": []any{map[string]any{"endpoint": "2030-01-01T01:00:00Z"}}}
	require.Contains(t, strings.Join(forecastSpreadIssues(scenariosOnly), "\n"), "unverified")
	src["status"] = "inactive"
	delete(src, "target")
	require.Empty(t, forecastSpreadIssues(src))
}
