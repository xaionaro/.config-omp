package main

import (
	"fmt"
	"strings"
	"time"
)

// spreadEndpoint parses one UTC instant or an ordered UTC en-dash range.
//
// Example: A range contributes its upper endpoint to the spread.
func spreadEndpoint(value string) (time.Time, error) {
	left, right, isRange := strings.Cut(value, "–")
	start, err := parseUTC(left)
	if err != nil {
		return time.Time{}, err
	}
	if !isRange {
		return start, nil
	}
	end, err := parseUTC(right)
	if err != nil {
		return time.Time{}, err
	}
	if end.Before(start) {
		return time.Time{}, fmt.Errorf("reversed endpoint range")
	}
	return end, nil
}

// forecastSpreadIssues checks an active target against supported spread endpoints.
//
// Example: A known scenario beyond the target makes the projection incomplete.
func forecastSpreadIssues(source map[string]any) []string {
	if textField(source, "status") != "active" {
		return nil
	}
	baseRaw, hasBase := source["base_case"]
	scenarioRaw, hasScenarios := source["scenarios"]
	if !hasBase && !hasScenarios {
		return nil
	}
	if scenarios, ok := scenarioRaw.([]any); !hasBase && hasScenarios && ok && len(scenarios) == 0 {
		return nil
	}
	target, err := parseUTC(textField(source, "target"))
	if err != nil {
		return []string{"forecast spread consistency unverified: target is not a supported UTC instant"}
	}
	endpoints, comparable := collectSpreadEndpoints(baseRaw, hasBase, scenarioRaw, hasScenarios)
	var issues []string
	var latest time.Time
	for _, value := range endpoints {
		endpoint, err := spreadEndpoint(value)
		if err != nil {
			comparable = false
			continue
		}
		if latest.IsZero() || endpoint.After(latest) {
			latest = endpoint
		}
		if endpoint.After(target) {
			issues = append(issues, "forecast spread endpoint "+value+" exceeds target")
		}
	}
	if !comparable || latest.IsZero() {
		issues = append(issues, "forecast spread consistency unverified: missing or unsupported endpoint")
		return issues
	}
	if latest.Before(target) {
		issues = append(issues, "forecast spread target exceeds latest endpoint")
	}
	return issues
}

// scenarioEndpoints returns one raw endpoint per supplied scenario.
//
// Example: A malformed scenario contributes an unsupported empty endpoint.
func scenarioEndpoints(scenarios []any) []string {
	endpoints := make([]string, 0, len(scenarios))
	for _, item := range scenarios {
		values, _ := item.(map[string]any)
		endpoints = append(endpoints, textField(values, "endpoint"))
	}
	return endpoints
}

// collectSpreadEndpoints gathers raw endpoint strings without inferring missing data.
//
// Example: A scenario-only spread lacks a comparable base case.
func collectSpreadEndpoints(
	baseRaw any,
	hasBase bool,
	scenarioRaw any,
	hasScenarios bool,
) ([]string, bool) {
	var endpoints []string
	base, baseOK := baseRaw.(map[string]any)
	comparable := hasBase && baseOK
	if comparable {
		endpoints = append(endpoints, textField(base, "endpoint"))
	}
	if !hasScenarios {
		return endpoints, comparable
	}
	scenarios, ok := scenarioRaw.([]any)
	if !ok {
		return endpoints, false
	}
	return append(endpoints, scenarioEndpoints(scenarios)...), comparable
}
