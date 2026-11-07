package main

// validateCanonicalRecalibration checks canonical forecast history consistently.
//
// Example: Reject an unchanged prior that differs from an active target.
func validateCanonicalRecalibration(source map[string]any) []string {
	recal, ok := source["recalibration"].(map[string]any)
	if !ok {
		return []string{"missing recalibration"}
	}
	var issues []string
	state := textField(recal, "status")
	if state != "unchanged" && state != "changed" {
		issues = append(issues, "invalid recalibration status")
	}
	if textField(recal, "evidence") == "" {
		issues = append(issues, "missing recalibration evidence")
	}
	if state == "changed" {
		issues = append(issues, missingChangedHistory(recal)...)
	}
	if state == "unchanged" {
		prior, current := textField(recal, "prior"), textField(recal, "current")
		if prior != "" && current != "" && !sameValue(prior, current) {
			issues = append(issues, "unchanged recalibration prior and current differ")
		}
	}
	target := textField(source, "target")
	if target == "" {
		return issues
	}
	if state == "unchanged" {
		issues = append(issues, conflictingUnchangedHistory(recal, target)...)
	}
	if state == "changed" {
		if v := textField(recal, "current"); v != "" && !sameValue(v, target) {
			issues = append(issues, "recalibration current does not match target")
		}
	}
	return issues
}

// missingChangedHistory identifies required evidence for changed targets.
//
// Example: A changed record needs its previous target.
func missingChangedHistory(recal map[string]any) []string {
	var issues []string
	for _, key := range []string{"prior", "current", "reason"} {
		if textField(recal, key) == "" {
			issues = append(issues, "recalibration missing "+key)
		}
	}
	return issues
}

// conflictingUnchangedHistory compares optional unchanged targets by UTC instant.
//
// Example: Equivalent UTC spellings do not indicate a change.
func conflictingUnchangedHistory(
	recal map[string]any,
	target string,
) []string {
	var issues []string
	for _, key := range []string{"prior", "current"} {
		if v := textField(recal, key); v != "" && !sameValue(v, target) {
			issues = append(issues, "unchanged recalibration "+key+" does not match target")
		}
	}
	return issues
}
