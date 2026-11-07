package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// parseUTC parses RFC3339 timestamps and requires a zero UTC offset.
//
// Example: Accept Z or +00:00 and reject a nonzero offset.
func parseUTC(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	_, offset := t.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf("timestamp must have zero UTC offset")
	}
	return t.UTC(), nil
}

// validateForecast checks an optional forecast without asserting its truth.
//
// Example: Reject an empty supplied root forecast.
func validateForecast(f forecast) error {
	if !validForecast(f) {
		return fmt.Errorf("forecast requires outcome, target and assessment; use unknown when unsupported")
	}
	if err := validateForecastTime("target", f.Target); err != nil {
		return err
	}
	for _, item := range []struct{ name, value string }{{"baseline", f.Baseline}, {"prior", f.Prior}} {
		if err := validateForecastTime(item.name, item.value); err != nil {
			return err
		}
	}
	for _, item := range f.Scenarios {
		if err := validateScenario(item); err != nil {
			return err
		}
	}
	return nil
}

// validateSections checks typed lane, forecast and timeline relationships.
//
// Example: Reject a timeline interval outside the report window.
func validateSections(r report) error {
	if err := validatePriorities(r); err != nil {
		return err
	}
	if err := validateLanes(r); err != nil {
		return err
	}
	for _, f := range r.RootForecasts {
		if err := validateForecast(f); err != nil {
			return fmt.Errorf("root forecast %s: %w", f.Outcome, err)
		}
	}
	return validateTimeline(r)
}

// validatePriorities checks coverage and each supplied completion estimate.
//
// Example: Every listed priority has its own estimate.
func validatePriorities(r report) error {
	priorities := map[priorityLabel]bool{}
	for _, e := range r.PriorityEstimates {
		if strings.TrimSpace(string(e.Priority)) == "" || priorities[e.Priority] {
			return fmt.Errorf("priority estimates require unique priority")
		}
		priorities[e.Priority] = true
		if err := validateCompletionEstimate(e); err != nil {
			return fmt.Errorf("priority %s estimate: %w", e.Priority, err)
		}
	}
	if r.CompletionEstimate != nil {
		if err := validateCompletionEstimate(*r.CompletionEstimate); err != nil {
			return fmt.Errorf("overall completion estimate: %w", err)
		}
	}
	present := map[priorityLabel]bool{}
	for _, t := range r.Tasks {
		present[t.Priority] = true
	}
	for priority := range priorities {
		if !present[priority] {
			return fmt.Errorf("priority estimate %s has no matching task", priority)
		}
	}
	if r.CompletionEstimate == nil {
		return fmt.Errorf("completion_estimate is required")
	}
	for _, t := range r.Tasks {
		if !priorities[t.Priority] {
			return fmt.Errorf("missing completion estimate for priority %s", t.Priority)
		}
	}
	return nil
}

// validateLanes checks references and the selected acceptance environment.
//
// Example: A closed production lane needs a completion timestamp.
func validateLanes(r report) error {
	byID := map[taskID]task{}
	for _, t := range r.Tasks {
		byID[t.ID] = t
	}
	for _, l := range r.Lanes {
		if err := validateLane(l, byID); err != nil {
			return err
		}
	}
	return nil
}

// validateLane checks one lane against its referenced task and acceptance boundary.
//
// Example: A test-gated lane needs test readiness before completion.
func validateLane(
	l lane,
	byID map[taskID]task,
) error {
	_, ok := byID[l.TaskID]
	if !ok || l.TaskID == "" {
		return fmt.Errorf("lane references unknown task %s", l.TaskID)
	}
	if strings.TrimSpace(l.Outcome) == "" {
		return fmt.Errorf("lane %s requires outcome", l.TaskID)
	}
	if l.ImplementationStatus == statusUnknown || l.TestStatus == statusUnknown || l.ProdStatus == statusUnknown {
		return fmt.Errorf("lane %s requires three readiness statuses", l.TaskID)
	}
	if l.RequiredEnvironment == environmentUnknown {
		return fmt.Errorf("lane %s: required_environment is required", l.TaskID)
	}
	if l.Lifecycle != laneLifecycleNone && l.Lifecycle != laneLifecycleCancelled {
		return fmt.Errorf("lane %s has invalid lifecycle", l.TaskID)
	}
	if l.Lifecycle == laneLifecycleCancelled && (l.CompletedAt != "" || l.Forecast != nil) {
		return fmt.Errorf("lane %s: cancelled lane cannot claim completion or an active forecast", l.TaskID)
	}
	if l.Lifecycle != laneLifecycleCancelled && l.requiredStatus() == statusClosed && l.CompletedAt == "" {
		return fmt.Errorf("lane %s: completed lane requires completed_at", l.TaskID)
	}
	if l.CompletedAt != "" {
		if _, err := parseUTC(l.CompletedAt); err != nil {
			return fmt.Errorf("lane %s: invalid completed_at", l.TaskID)
		}
		if l.requiredStatus() != statusClosed {
			return fmt.Errorf("lane %s: completed_at requires CLOSED required environment", l.TaskID)
		}
		if l.Forecast != nil {
			return fmt.Errorf("lane %s: completion conflicts with active forecast", l.TaskID)
		}
	}
	if l.Forecast != nil {
		if l.requiredStatus() == statusClosed {
			return fmt.Errorf("lane %s: CLOSED required environment cannot retain active forecast", l.TaskID)
		}
		if err := validateForecast(*l.Forecast); err != nil {
			return fmt.Errorf("lane %s forecast: %w", l.TaskID, err)
		}
	}
	return nil
}

// validateTimeline checks report-relative intervals and duplicate job slots.
//
// Example: A job cannot report the same half-hour twice.
func validateTimeline(r report) error {
	now, err := parseUTC(r.Timestamp)
	if err != nil {
		return err
	}
	if r.TimelineAsOf != "" {
		anchor, err := parseUTC(r.TimelineAsOf)
		if err != nil || anchor.After(now) {
			return fmt.Errorf("timeline_as_of must be UTC and no later than timestamp")
		}
		now = anchor
	}
	chains := map[string]string{}
	seenSlots := map[string]map[time.Time]bool{}
	for _, row := range r.Timeline {
		if strings.TrimSpace(row.Job) == "" || strings.TrimSpace(row.ParentChain) == "" {
			return fmt.Errorf("timeline requires job and parent_chain")
		}
		if previous, ok := chains[row.Job]; ok && previous != row.ParentChain {
			return fmt.Errorf("timeline job %s has inconsistent parent_chain", row.Job)
		}
		chains[row.Job] = row.ParentChain
		start, err := parseUTC(row.Start)
		if err != nil {
			return fmt.Errorf("timeline invalid start: %w", err)
		}
		end, err := parseUTC(row.End)
		if err != nil {
			return fmt.Errorf("timeline invalid end: %w", err)
		}
		if end.Sub(start) != timelineInterval || start.Before(now.Add(-timelineWindow)) || end.After(now) {
			return fmt.Errorf("timeline interval must be 30 minutes within the preceding four hours")
		}
		if start.Sub(now.Add(-timelineWindow))%timelineInterval != 0 {
			return fmt.Errorf("timeline job %s: interval is not aligned to report bins", row.Job)
		}
		if seenSlots[row.Job] == nil {
			seenSlots[row.Job] = map[time.Time]bool{}
		}
		if seenSlots[row.Job][start] {
			return fmt.Errorf("timeline job %s: duplicate interval", row.Job)
		}
		seenSlots[row.Job][start] = true
	}
	return nil
}

// validateCompletionEstimate checks a supplied duration range and UTC endpoints.
//
// Example: Reject a reversed duration range.
func validateCompletionEstimate(e completionEstimate) error {
	if e.MinMinutes == nil || e.MaxMinutes == nil || math.IsNaN(float64(*e.MinMinutes)) || math.IsNaN(float64(*e.MaxMinutes)) || math.IsInf(float64(*e.MinMinutes), 0) || math.IsInf(float64(*e.MaxMinutes), 0) || *e.MinMinutes < 0 || *e.MaxMinutes < *e.MinMinutes {
		return fmt.Errorf("completion estimate requires ordered finite minute range")
	}
	if _, _, err := completionEndpoints(e); err != nil {
		return err
	}
	if strings.TrimSpace(e.Basis) == "" || strings.TrimSpace(e.Assumptions) == "" || strings.TrimSpace(e.Dependencies) == "" || strings.TrimSpace(e.CriticalPath) == "" {
		return fmt.Errorf("completion estimate requires basis, assumptions, dependencies and critical_path")
	}
	return nil
}

// completionEndpoints derives UTC endpoints from the estimate's as-of time.
//
// Example: Thirty minutes after midnight renders as 00:30 UTC.
func completionEndpoints(e completionEstimate) (time.Time, time.Time, error) {
	base, err := parseUTC(e.AsOf)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid estimate as_of: %w", err)
	}
	if e.MinMinutes == nil || e.MaxMinutes == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("missing minute range")
	}
	minNanos, maxNanos := float64(*e.MinMinutes)*float64(time.Minute), float64(*e.MaxMinutes)*float64(time.Minute)
	if math.IsNaN(minNanos) || math.IsNaN(maxNanos) || math.IsInf(minNanos, 0) || math.IsInf(maxNanos, 0) || minNanos < 0 || maxNanos < minNanos || maxNanos >= float64(math.MaxInt64) {
		return time.Time{}, time.Time{}, fmt.Errorf("estimate duration outside supported range")
	}
	earliest := base.Add(time.Duration(math.Round(minNanos)))
	latest := base.Add(time.Duration(math.Round(maxNanos)))
	if earliest.Year() > 9999 || latest.Year() > 9999 {
		return time.Time{}, time.Time{}, fmt.Errorf("estimate endpoint outside RFC3339 range")
	}
	return earliest, latest, nil
}

// validateScenario requires explicit information in every scenario field.
//
// Example: Use unknown when a scenario endpoint is not yet supported.
func validateScenario(item scenario) error {
	for _, value := range []string{item.Case, item.Trigger, item.AffectedWork, item.DurationBasis, item.EvidenceAssumptions, item.Endpoint, item.EvidenceGapNextAction} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("scenario %q requires all fields; use unknown where unsupported", item.Case)
		}
	}
	return nil
}

// validateForecastTime checks an optional or explicitly unknown UTC timestamp.
//
// Example: An unsupported prior can be recorded as unknown.
func validateForecastTime(
	name string,
	value string,
) error {
	if value == "" || value == "unknown" {
		return nil
	}
	if _, err := parseUTC(value); err != nil {
		return fmt.Errorf("invalid forecast %s: %w", name, err)
	}
	return nil
}
