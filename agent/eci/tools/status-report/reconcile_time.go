package main

import "time"

// futureEstimateIssues checks time anchors even when canonical sources are unavailable.
//
// Example: A future as_of remains incomplete without a session directory.
func futureEstimateIssues(
	r report,
	evaluation time.Time,
) []string {
	reportTime, _ := parseUTC(r.Timestamp)
	estimates := append([]completionEstimate{}, r.PriorityEstimates...)
	if r.CompletionEstimate != nil {
		estimates = append(estimates, *r.CompletionEstimate)
	}
	var issues []string
	for _, e := range estimates {
		base, err := parseUTC(e.AsOf)
		if err == nil && (base.After(reportTime) || base.After(evaluation)) {
			issues = append(issues, "completion estimate as_of "+e.AsOf+" is in the future")
		}
	}
	return issues
}

// futureActualIssues checks displayed actual completions against evaluation time.
//
// Example: A future completion stays incomplete even without source context.
func futureActualIssues(
	r report,
	evaluation time.Time,
) []string {
	var issues []string
	for _, l := range r.Lanes {
		if at, err := parseUTC(l.CompletedAt); err == nil && at.After(evaluation) {
			issues = append(issues, "lane "+string(l.TaskID)+" actual completed_at is in the future")
		}
	}
	for _, c := range r.RootCompletions {
		if at, err := parseUTC(c.CompletedAt); err == nil && at.After(evaluation) {
			issues = append(issues, "root "+c.WorkRef+" actual completed_at is in the future")
		}
	}
	return issues
}

// canonicalFutureActualIssue checks a completed lane or root's source timestamp.
//
// Example: A ledger cannot claim an actual completion after evaluation time.
func canonicalFutureActualIssue(
	id string,
	work sourceRecord,
	evaluation time.Time,
) string {
	if work.Data.State != workStateCompleted || (!hasRole(work.Data.Roles, reportingRoleLane) && !hasRole(work.Data.Roles, reportingRoleRoot)) {
		return ""
	}
	at, err := parseUTC(work.Data.CompletedAt)
	if err == nil && at.After(evaluation) {
		return "work " + id + " actual completed_at is in the future"
	}
	return ""
}
