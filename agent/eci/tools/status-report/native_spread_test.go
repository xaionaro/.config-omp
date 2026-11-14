package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeSpreadRenderingRetainsScenarioFacts checks native table and source details.
//
// Example: A downside scenario displays its basis and endpoint.
func TestNativeSpreadRenderingRetainsScenarioFacts(t *testing.T) {
	t.Parallel()
	f := forecast{Outcome: "Result", Assessment: "provisional", SourceProjection: map[string]any{"target_basis": "review pending", "base_case": map[string]any{"endpoint": "2030-01-01T01:00:00Z", "packages": []any{map[string]any{"work": "Finish", "duration": "20 minutes", "basis": "guess"}}}, "scenarios": []any{map[string]any{"name": "Delay", "trigger": "review", "affected_work": "Finish", "added_duration": "10 minutes", "basis": "guess", "evidence": "pending", "assumptions": "none", "endpoint": "2030-01-01T02:00:00Z", "evidence_gap": "unmeasured", "next_action": "measure", "event_time": "2030-01-01T01:00:00Z"}}}}
	var b strings.Builder
	renderForecastDetails(&b, f)
	renderScenarios(&b, f.Outcome, f)
	out := b.String()
	require.Contains(t, out, "Forecast assessment: provisional — review pending; target conditional.")
	require.Contains(t, out, "| Result — base case |")
	require.Contains(t, out, "| Result — Delay |")
	require.Contains(t, out, "10 minutes")
	require.Contains(t, out, "event_time")
}

// TestProvisionalRiskUsesOnlySuppliedEvidence checks assessment qualifiers.
//
// Example: A supplied scenario gap is used when target basis is absent.
func TestProvisionalRiskUsesOnlySuppliedEvidence(t *testing.T) {
	t.Parallel()
	require.Equal(t, "unknown risk", provisionalRisk(nil))
	require.Equal(t, "unknown risk", provisionalRisk(map[string]any{"target_basis": "unknown "}))
	require.Equal(t, "review timing", provisionalRisk(map[string]any{"scenarios": []any{map[string]any{"evidence_gap": "review timing"}}}))
	var b strings.Builder
	renderForecastDetails(&b, forecast{Assessment: "provisional"})
	require.Contains(t, b.String(), "provisional — unknown risk; target conditional.")
}
