package main

import (
	"fmt"
	"strings"
)

// spreadRow holds the seven displayed cells for one native forecast case.
//
// Example: A base case includes its packages and endpoint.
type spreadRow struct {
	Case     string
	Trigger  string
	Work     string
	Duration string
	Evidence string
	Endpoint string
	Gap      string
}

// renderNativeSpread displays canonical base-case and scenario details when supplied.
//
// Example: A source scenario contributes one row to the spread table.
func renderNativeSpread(
	b *strings.Builder,
	name string,
	projection map[string]any,
) bool {
	base, hasBase := projection["base_case"]
	rawScenarios, hasScenarios := projection["scenarios"]
	scenarios, validScenarios := rawScenarios.([]any)
	if !hasBase && (!hasScenarios || (validScenarios && len(scenarios) == 0)) {
		return false
	}
	fmt.Fprintln(b, "\n| Lane/root and case | Trigger/evidence | Affected work/dependency | Added duration/range and basis | Evidence assessed; assumptions/limits | Endpoint/range | Evidence gap / next fact-finding action |")
	fmt.Fprintln(b, "| --- | --- | --- | --- | --- | --- | --- |")
	writeNativeSpreadRow(b, name, baseSpreadRow(base))
	if hasScenarios && !validScenarios {
		writeNativeSpreadRow(b, name, scenarioSpreadRow(nil))
		return true
	}
	for _, item := range scenarios {
		values, _ := item.(map[string]any)
		writeNativeSpreadRow(b, name, scenarioSpreadRow(values))
	}
	return true
}

// baseSpreadRow projects supplied base fields without inventing missing values.
//
// Example: Each package's work and estimate appear in its respective cell.
func baseSpreadRow(raw any) spreadRow {
	values, _ := raw.(map[string]any)
	works, durations := basePackageCells(values)
	return spreadRow{Case: "base case", Trigger: "unknown", Work: works, Duration: durations, Evidence: spreadPair(values, "evidence", "assumptions"), Endpoint: spreadText(values, "endpoint"), Gap: spreadPair(values, "evidence_gap", "next_action")}
}

// basePackageCells retains all supplied package work, duration and basis values.
//
// Example: Multiple packages are separated within their table cells.
func basePackageCells(values map[string]any) (string, string) {
	packages, ok := values["packages"].([]any)
	if !ok || len(packages) == 0 {
		return "unknown", "unknown"
	}
	var works, durations []string
	for _, item := range packages {
		p, _ := item.(map[string]any)
		works = append(works, spreadText(p, "work"))
		durations = append(durations, spreadPair(p, "duration", "basis"))
	}
	return strings.Join(works, "; "), strings.Join(durations, "; ")
}

// scenarioSpreadRow maps one native downside scenario to a report row.
//
// Example: Preserve the scenario's supplied evidence and assumptions.
func scenarioSpreadRow(values map[string]any) spreadRow {
	return spreadRow{Case: spreadText(values, "name"), Trigger: spreadText(values, "trigger"), Work: spreadText(values, "affected_work"), Duration: spreadPair(values, "added_duration", "basis"), Evidence: spreadPair(values, "evidence", "assumptions"), Endpoint: spreadText(values, "endpoint"), Gap: spreadPair(values, "evidence_gap", "next_action")}
}

// spreadText returns a supplied string or an explicit unknown marker.
//
// Example: A missing endpoint renders as unknown.
func spreadText(
	values map[string]any,
	key string,
) string {
	value := strings.TrimSpace(textField(values, key))
	if value == "" {
		return "unknown"
	}
	return value
}

// spreadPair renders two separately labeled source fields.
//
// Example: Missing duration basis is displayed as unknown.
func spreadPair(
	values map[string]any,
	first string,
	second string,
) string {
	a, b := spreadText(values, first), spreadText(values, second)
	if a == "unknown" && b == "unknown" {
		return "unknown"
	}
	return first + ": " + a + "; " + second + ": " + b
}

// writeNativeSpreadRow renders seven escaped table cells.
//
// Example: Pipe characters in scenario evidence remain inside the cell.
func writeNativeSpreadRow(
	b *strings.Builder,
	name string,
	row spreadRow,
) {
	fmt.Fprintf(b, "| %s — %s | %s | %s | %s | %s | %s | %s |\n", timelineCell(name), timelineCell(row.Case), timelineCell(row.Trigger), timelineCell(row.Work), timelineCell(row.Duration), timelineCell(row.Evidence), timelineCell(row.Endpoint), timelineCell(row.Gap))
}

// provisionalRisk returns only a supplied forecast basis or evidence gap.
//
// Example: Missing risk evidence is explicitly unknown.
func provisionalRisk(projection map[string]any) string {
	if v := strings.TrimSpace(textField(projection, "target_basis")); v != "" && v != "unknown" {
		return v
	}
	if base, ok := projection["base_case"].(map[string]any); ok {
		if gap := strings.TrimSpace(textField(base, "evidence_gap")); gap != "" && gap != "unknown" && gap != "none" {
			return gap
		}
	}
	scenarios, _ := projection["scenarios"].([]any)
	for _, item := range scenarios {
		s, _ := item.(map[string]any)
		gap := strings.TrimSpace(textField(s, "evidence_gap"))
		if gap != "" && gap != "unknown" && gap != "none" {
			return gap
		}
	}
	return "unknown risk"
}
