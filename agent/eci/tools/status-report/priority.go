package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// impactPoints measures impact on the report-wide scale.
//
// Example: Nine points represents a high estimated impact.
type impactPoints float64

// priorityRatio measures impact points per work-minute.
//
// Example: Nine points over thirty minutes gives 0.3.
type priorityRatio float64

// prioritySorter compares scored task IDs.
//
// Example: Higher impact per minute sorts first.
type prioritySorter struct {
	Tasks map[taskID]task
	Scale string
}

// compare orders two scored task IDs while preserving equal-score order.
//
// Example: Equal scores compare as zero.
func (s prioritySorter) compare(
	a taskID,
	b taskID,
) int {
	x, _ := priorityScore(s.Tasks[a], s.Scale)
	y, _ := priorityScore(s.Tasks[b], s.Scale)
	switch {
	case x > y:
		return -1
	case x < y:
		return 1
	default:
		return 0
	}
}

// validatePriorityOrder validates the legacy forecast grouping order.
//
// Example: Unknown labels cannot be assigned a rank.
func validatePriorityOrder(order []priorityLabel) error {
	seen := map[priorityLabel]bool{}
	for _, label := range order {
		trimmed := strings.TrimSpace(string(label))
		if trimmed == "" || strings.EqualFold(trimmed, "unknown") || seen[label] {
			return fmt.Errorf("priority_order requires unique nonblank ranked labels other than unknown")
		}
		seen[label] = true
	}
	return nil
}

// priorityScore computes comparable impact per remaining work minute or explains why it cannot.
//
// Example: Nine points over thirty work minutes yields 0.3.
func priorityScore(
	t task,
	scale string,
) (priorityRatio, string) {
	switch {
	case strings.TrimSpace(scale) == "":
		return 0, "missing impact scale"
	case t.ImpactPoints == nil:
		return 0, "missing impact points"
	case strings.TrimSpace(t.ImpactBasis) == "":
		return 0, "missing impact basis"
	case t.Effort.Kind != effortWork:
		return 0, "effort is not work minutes"
	case t.Effort.MaxMinutes == nil || *t.Effort.MaxMinutes <= 0:
		return 0, "remaining effort must be positive"
	}
	ratio := float64(*t.ImpactPoints) / float64(*t.Effort.MaxMinutes)
	if math.IsInf(ratio, 0) {
		return 0, "calculated priority exceeds numeric range"
	}
	return priorityRatio(ratio), ""
}

// priorityText displays a calculated score or its missing input.
//
// Example: A scored task displays points per work-minute.
func priorityText(
	t task,
	scale string,
) string {
	score, reason := priorityScore(t, scale)
	if reason != "" {
		return "unscored: " + reason
	}
	return fmt.Sprintf("%g points/work-minute (%g points; %s; scale: %s)", score, *t.ImpactPoints, t.ImpactBasis, scale)
}

// orderTaskIDs sorts scored positions while preserving unscored positions and ties.
//
// Example: Low, unscored, high becomes high, unscored, low.
func orderTaskIDs(
	ids []taskID,
	tasks map[taskID]task,
	scale string,
) []taskID {
	result := slices.Clone(ids)
	scored := make([]taskID, 0, len(ids))
	for _, id := range ids {
		if _, reason := priorityScore(tasks[id], scale); reason == "" {
			scored = append(scored, id)
		}
	}
	sorter := prioritySorter{Tasks: tasks, Scale: scale}
	slices.SortStableFunc(scored, sorter.compare)
	next := 0
	for i, id := range result {
		if _, reason := priorityScore(tasks[id], scale); reason == "" {
			result[i] = scored[next]
			next++
		}
	}
	return result
}
