package main

// forecastFieldPolicy maps canonical fields to their report representations.
//
// Example: The canonical original baseline projects to the report baseline.
type forecastFieldPolicy struct {
	Source string
	Report string
	Nested bool
}

// forecastFieldPolicies returns the supported canonical forecast field inventory.
//
// Example: Base-case details belong in source_projection.
func forecastFieldPolicies() []forecastFieldPolicy {
	return []forecastFieldPolicy{
		{Source: "kind"}, {Source: "status"}, {Source: "recalibration"},
		{Source: "outcome", Report: "outcome"}, {Source: "target", Report: "target"},
		{Source: "assessment", Report: "assessment"}, {Source: "original_baseline", Report: "baseline"},
		{Source: "target_basis", Nested: true}, {Source: "baseline_evidence", Nested: true},
		{Source: "base_case", Nested: true}, {Source: "scenarios", Nested: true},
	}
}

// supportedForecastField reports whether a canonical field is understood.
//
// Example: Unknown source fields cannot be silently certified.
func supportedForecastField(key string) bool {
	for _, field := range forecastFieldPolicies() {
		if field.Source == key {
			return true
		}
	}
	return false
}

// forecastReportValue selects a direct typed projection by report field name.
//
// Example: The baseline field reads the projected original baseline.
func forecastReportValue(
	f forecast,
	key string,
) string {
	switch key {
	case "outcome":
		return f.Outcome
	case "target":
		return f.Target
	case "assessment":
		return f.Assessment
	case "baseline":
		return f.Baseline
	default:
		return ""
	}
}

// nestedForecastField identifies fields preserved in source_projection.
//
// Example: A base case is a nested canonical forecast field.
func nestedForecastField(key string) bool {
	for _, field := range forecastFieldPolicies() {
		if field.Source == key {
			return field.Nested
		}
	}
	return false
}

// forecastHistoryFields lists recalibration fields projected directly to a report.
//
// Example: A changed target includes its prior value and reason.
func forecastHistoryFields() []string { return []string{"prior", "reason", "evidence"} }

// forecastHistoryValue selects a projected recalibration field.
//
// Example: The prior field is the previous forecast target.
func forecastHistoryValue(
	f forecast,
	key string,
) string {
	switch key {
	case "prior":
		return f.Prior
	case "reason":
		return f.Reason
	case "evidence":
		return f.Evidence
	default:
		return ""
	}
}
