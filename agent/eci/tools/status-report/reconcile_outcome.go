package main

import (
	"reflect"
	"sort"
)

// validateWorkOutcomes checks canonical work outcome links by record identity.
//
// Example: Reject a dangling link even if its text resembles an outcome ID.
func validateWorkOutcomes(
	id string,
	work sourceRecord,
	records map[string]sourceRecord,
) []string {
	if len(work.OutcomeRefs) == 0 {
		return []string{"work " + id + " missing outcome links"}
	}
	var issues []string
	seen := map[string]bool{}
	for _, ref := range work.OutcomeRefs {
		record, ok := records[ref]
		if !ok || record.Type != "outcome" {
			issues = append(issues, "work "+id+" has invalid outcome link "+ref)
		}
		if seen[ref] {
			issues = append(issues, "work "+id+" has duplicate outcome link "+ref)
		}
		seen[ref] = true
	}
	return issues
}

// outcomeSet returns the complete canonical identity set in stable order.
//
// Example: Two differently ordered references identify the same set.
func outcomeSet(refs []string) []string {
	out := append([]string{}, refs...)
	sort.Strings(out)
	return out
}

// checkSharedForecastOwners rejects forecast claims for unrelated outcome sets.
//
// Example: Two roots may share a forecast only for the same full outcome set.
func checkSharedForecastOwners(works map[string]sourceRecord) []string {
	owners := map[sourceRef][]string{}
	var issues []string
	for id, work := range works {
		if work.Data.ForecastRef == "" || (!openWork(work.Data.State) && !hasRole(work.Data.Roles, reportingRoleRoot)) {
			continue
		}
		prior, ok := owners[work.Data.ForecastRef]
		current := outcomeSet(work.OutcomeRefs)
		if ok && (!reflect.DeepEqual(prior, current) || len(current) == 0) {
			issues = append(issues, "forecast "+string(work.Data.ForecastRef)+" shared by unrelated work "+id)
		}
		if !ok {
			owners[work.Data.ForecastRef] = current
		}
	}
	return issues
}

// outcomeCorresponds checks every linked outcome against each displayed claim.
//
// Example: A full canonical outcome description supports an identical claim.
func outcomeCorresponds(
	work sourceRecord,
	records map[string]sourceRecord,
	claims ...string,
) bool {
	if len(work.OutcomeRefs) == 0 || len(claims) == 0 {
		return false
	}
	for _, id := range work.OutcomeRefs {
		record, ok := records[id]
		if !ok || record.Type != "outcome" || record.Outcome == "" || !matchesClaims(record.Outcome, claims) {
			return false
		}
	}
	return true
}

// matchesClaims verifies that each claim is backed by the same description.
//
// Example: A distinct unverified paraphrase remains incomplete.
func matchesClaims(description string, claims []string) bool {
	for _, claim := range claims {
		if claim != description {
			return false
		}
	}
	return true
}

// rootOutcomeIssues checks every displayed active root forecast claim.
//
// Example: An unrelated root label is semantically unverified.
func rootOutcomeIssues(
	id string,
	work sourceRecord,
	records map[string]sourceRecord,
	forecasts []forecast,
) []string {
	if !openWork(work.Data.State) || !hasRole(work.Data.Roles, reportingRoleRoot) {
		return nil
	}
	var issues []string
	for _, f := range forecasts {
		if f.ForecastRef == string(work.Data.ForecastRef) && !outcomeCorresponds(work, records, f.Outcome) {
			issues = append(issues, "root "+id+" forecast semantic equivalence unverified")
		}
	}
	return issues
}
