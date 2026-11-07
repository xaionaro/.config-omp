package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCalculatedPriority checks score ordering and explicit unknowns through the report.
//
// Example: A higher return moves past a lower one while an unscored slot remains fixed.
func TestCalculatedPriority(t *testing.T) {
	input := strings.Replace(valid, "tasks:\n", "impact_scale: '0-10 user benefit'\ntasks:\n", 1)
	input = strings.Replace(input, "impact: Useful\n    effort: {min_minutes: 30, max_minutes: 30, kind: elapsed, basis: guess}", "impact: Useful\n    impact_points: 9\n    impact_basis: educated guess\n    effort: {min_minutes: 30, max_minutes: 30, kind: work, basis: guess}", 1)
	input = strings.Replace(input, "impact: unknown\n    effort: {min_minutes: 10, max_minutes: 30, kind: elapsed, basis: educated guess}", "impact: unknown\n    impact_points: 3\n    impact_basis: guess\n    effort: {min_minutes: 10, max_minutes: 30, kind: work, basis: educated guess}", 1)
	input = strings.Replace(input, `top_priorities: ["1.1"]`, `top_priorities: ["1.2", "1", "1.1"]`, 1)
	r, err := parse([]byte(input))
	require.NoError(t, err)
	out := render(r)
	top := strings.Split(strings.Split(out, "Top priorities:\n")[1], "\nFull task tree:")[0]
	require.Less(t, strings.Index(top, "1.1 Complete"), strings.Index(top, "1.2 Unknown"))
	require.Contains(t, top, "0.3 points/work-minute")
	require.Contains(t, top, "unscored:")
}

// TestPriorityScoreRejectsInvalidPoints checks invalid score inputs are rejected.
//
// Example: Negative impact cannot be ranked.
func TestPriorityScoreRejectsInvalidPoints(t *testing.T) {
	for _, value := range []string{"-1", ".nan", ".inf"} {
		input := strings.Replace(valid, "impact: Useful", "impact: Useful\n    impact_points: "+value, 1)
		_, err := parse([]byte(input))
		require.Error(t, err, value)
	}
}

// TestPriorityScoreBoundaries checks missing and zero inputs and stable ordering.
//
// Example: Zero impact is scored, while zero effort is unscored.
func TestPriorityScoreBoundaries(t *testing.T) {
	zero, one, two := impactPoints(0), impactPoints(1), impactPoints(2)
	zeroMinutes := fractionalMinutes(0)
	ten := fractionalMinutes(10)
	base := task{ImpactPoints: &one, ImpactBasis: "guess", Effort: estimate{MaxMinutes: &ten, Kind: effortWork}}
	for _, tc := range []struct {
		name          string
		task          task
		scale, reason string
	}{
		{"scale", base, "", "missing impact scale"},
		{"points", task{ImpactBasis: "guess", Effort: base.Effort}, "scale", "missing impact points"},
		{"basis", task{ImpactPoints: &one, Effort: base.Effort}, "scale", "missing impact basis"},
		{"kind", task{ImpactPoints: &one, ImpactBasis: "guess", Effort: estimate{MaxMinutes: &ten, Kind: effortElapsed}}, "scale", "effort is not work minutes"},
		{"zero effort", task{ImpactPoints: &one, ImpactBasis: "guess", Effort: estimate{MaxMinutes: &zeroMinutes, Kind: effortWork}}, "scale", "remaining effort must be positive"},
	} {
		_, reason := priorityScore(tc.task, tc.scale)
		require.Equal(t, tc.reason, reason, tc.name)
	}
	tasks := map[taskID]task{"low": {ImpactPoints: &zero, ImpactBasis: "guess", Effort: base.Effort}, "unknown": {}, "high": {ImpactPoints: &two, ImpactBasis: "guess", Effort: base.Effort}, "tie": {ImpactPoints: &two, ImpactBasis: "guess", Effort: base.Effort}}
	require.Equal(t, []taskID{"high", "unknown", "tie", "low"}, orderTaskIDs([]taskID{"low", "unknown", "high", "tie"}, tasks, "scale"))
	require.Contains(t, priorityText(tasks["low"], "scale"), "0 points/work-minute")
}

// TestLaneCalculatedPriority checks lane output uses the same score as task output.
//
// Example: A lane displays its task's points per work-minute.
func TestLaneCalculatedPriority(t *testing.T) {
	points := impactPoints(6)
	minutes := fractionalMinutes(20)
	tasks := map[taskID]task{"1": {ImpactPoints: &points, ImpactBasis: "guess", Effort: estimate{MinMinutes: &minutes, MaxMinutes: &minutes, Kind: effortWork}}}
	var out strings.Builder
	renderLaneTable(&out, []lane{{TaskID: "1"}}, tasks, "benefit")
	require.Contains(t, out.String(), "0.3 points/work-minute")
	require.NotContains(t, out.String(), "unscored:")
}

// TestPriorityOverflowIsUnscored checks a finite input cannot produce an infinite ranking.
//
// Example: A huge impact divided by a tiny effort is reported as unscored.
func TestPriorityOverflowIsUnscored(t *testing.T) {
	points := impactPoints(1e308)
	minutes := fractionalMinutes(1e-308)
	item := task{ImpactPoints: &points, ImpactBasis: "guess", Effort: estimate{MinMinutes: &minutes, MaxMinutes: &minutes, Kind: effortWork}}
	require.Contains(t, priorityText(item, "benefit"), "unscored:")
	require.NotContains(t, priorityText(item, "benefit"), "+Inf")
}

// TestLanePriorityOrdering checks stable ranking including repeated task references.
//
// Example: High-scoring lanes move past low-scoring lanes around an unscored lane.
func TestLanePriorityOrdering(t *testing.T) {
	low, high := impactPoints(1), impactPoints(3)
	minutes := fractionalMinutes(10)
	effort := estimate{MinMinutes: &minutes, MaxMinutes: &minutes, Kind: effortWork}
	tasks := map[taskID]task{"low": {ImpactPoints: &low, ImpactBasis: "guess", Effort: effort}, "high": {ImpactPoints: &high, ImpactBasis: "guess", Effort: effort}, "unknown": {Effort: effort}}
	lanes := []lane{{TaskID: "low", Outcome: "low lane"}, {TaskID: "unknown", Outcome: "unknown lane"}, {TaskID: "high", Outcome: "first high"}, {TaskID: "high", Outcome: "second high"}}
	var out strings.Builder
	renderLaneTable(&out, lanes, tasks, "benefit")
	text := out.String()
	require.Less(t, strings.Index(text, "first high"), strings.Index(text, "unknown lane"))
	require.Less(t, strings.Index(text, "unknown lane"), strings.Index(text, "second high"))
	require.Less(t, strings.Index(text, "second high"), strings.Index(text, "low lane"))
}

// TestSiblingPriorityOrdering checks the report's tree orders siblings by score.
//
// Example: A later high-scoring child appears above a lower-scoring child.
func TestSiblingPriorityOrdering(t *testing.T) {
	input := strings.Replace(valid, "tasks:\n", "impact_scale: benefit\ntasks:\n", 1)
	input = strings.Replace(input, "impact: Useful\n    effort: {min_minutes: 30, max_minutes: 30, kind: elapsed, basis: guess}", "impact: Useful\n    impact_points: 1\n    impact_basis: guess\n    effort: {min_minutes: 30, max_minutes: 30, kind: work, basis: guess}", 1)
	input = strings.Replace(input, "impact: unknown\n    effort: {min_minutes: 10, max_minutes: 30, kind: elapsed, basis: educated guess}", "impact: unknown\n    impact_points: 9\n    impact_basis: guess\n    effort: {min_minutes: 10, max_minutes: 30, kind: work, basis: educated guess}", 1)
	r, err := parse([]byte(input))
	require.NoError(t, err)
	tree := strings.Split(strings.Split(render(r), "Full task tree:\n")[1], "\nProgress:")[0]
	require.Less(t, strings.Index(tree, "1.2 Unknown"), strings.Index(tree, "1.1 Complete"))
}

// TestLaneRequirementContextFallback preserves readable context and marks missing lineage.
//
// Example: A lane without context asks for reconciliation in the report.
func TestLaneRequirementContextFallback(t *testing.T) {
	minutes := fractionalMinutes(10)
	effort := estimate{MinMinutes: &minutes, MaxMinutes: &minutes, Kind: effortWork}
	tasks := map[taskID]task{"1": {Effort: effort}, "2": {Effort: effort}, "3": {Effort: effort}}
	lanes := []lane{{TaskID: "1", Outcome: "missing"}, {TaskID: "2", Outcome: "unknown", Context: "unknown"}, {TaskID: "3", Outcome: "known", Context: "Checkout requirement"}}
	var out strings.Builder
	renderLaneTable(&out, lanes, tasks, "benefit")
	require.Contains(t, out.String(), "| missing | lineage unavailable—reconcile |")
	require.Contains(t, out.String(), "| unknown | lineage unavailable—reconcile |")
	require.Contains(t, out.String(), "| known | Checkout requirement |")
}
