package main

// workReconciler checks work coverage, lifecycle, and forecast applicability.
//
// Example: Check a lane whose source package is complete while acceptance remains open.
type workReconciler struct {
	works           map[string]sourceRecord
	lanes           map[string]lane
	scope           taskWorkIndex
	rootCompletions map[string]rootCompletion
	rootRefs        map[string]bool
	rootForecasts   []forecast
	issues          *reconciliationIssues
	forecasts       *forecastReconciler
}

// check validates one canonical work record against report projections.
//
// Example: Detect active work missing a remaining package.
func (c *workReconciler) check(id string) {
	work := c.works[id]
	l, represented := c.lanes[id]
	c.checkCoverage(id, work, l, represented)
	c.checkRolelessForecast(id, work)
	c.checkCompletion(id, work, l, represented)
	c.checkLane(id, work, l, represented)
	c.checkRoot(id, work, represented)
}

// checkCoverage distinguishes open acceptance from closed package work.
//
// Example: Mark an all-closed package ratio partial when a lane remains open.
func (c *workReconciler) checkCoverage(
	id string,
	work sourceRecord,
	l lane,
	represented bool,
) {
	if !openWork(work.Data.State) {
		return
	}
	if !c.scope.covered[workID(id)] {
		c.issues.add("active work %s missing from task coverage", id)
		return
	}
	if c.scope.remaining[workID(id)] {
		return
	}
	laneOpen := represented && l.requiredStatus() != statusClosed && l.Lifecycle != laneLifecycleCancelled
	if laneOpen && !c.scope.reportOpen {
		c.issues.add("work %s remaining acceptance is represented by an open lane outside scored leaf packages", id)
	}
	if !laneOpen {
		c.issues.add("active work %s has no remaining package or open acceptance lane", id)
	}
}

// checkRolelessForecast rejects active forecasts without a reporting role.
//
// Example: Flag a forecast claimed by direct work with no lane or root role.
func (c *workReconciler) checkRolelessForecast(
	id string,
	work sourceRecord,
) {
	ref := string(work.Data.ForecastRef)
	if ref == "" || hasRole(work.Data.Roles, reportingRoleLane) || hasRole(work.Data.Roles, reportingRoleRoot) {
		return
	}
	if !validRef(ref, "forecasts.yaml") {
		c.issues.add("work %s has invalid forecast reference", id)
		return
	}
	if !c.forecasts.load() {
		return
	}
	source, ok := c.forecasts.source.Records[sourceID(ref, "forecasts.yaml")]
	if !ok || textField(source, "status") != "inactive" {
		c.issues.add("work %s forecast has no applicable reporting role", id)
	}
}

// checkCompletion compares actual completion independently of forecasts.
//
// Example: Flag a lane completion timestamp absent from the ledger.
func (c *workReconciler) checkCompletion(
	id string,
	work sourceRecord,
	l lane,
	represented bool,
) {
	if work.Data.State != workStateCompleted || (!hasRole(work.Data.Roles, reportingRoleLane) && !hasRole(work.Data.Roles, reportingRoleRoot)) {
		return
	}
	if _, err := parseUTC(work.Data.CompletedAt); err != nil {
		c.issues.add("completed work %s missing or invalid completed_at", id)
	}
	if represented && !sameValue(work.Data.CompletedAt, l.CompletedAt) {
		c.issues.add("work %s completed_at mismatch: source %q; report %q", id, work.Data.CompletedAt, l.CompletedAt)
	}
	if !represented && !hasRole(work.Data.Roles, reportingRoleRoot) {
		c.issues.add("completed forecasted work %s completion projection unverified", id)
	}
}

// checkLane validates declared lane readiness and its forecast.
//
// Example: Permit an omitted cancelled lane with an inactive forecast.
func (c *workReconciler) checkLane(
	id string,
	work sourceRecord,
	l lane,
	represented bool,
) {
	if !hasRole(work.Data.Roles, reportingRoleLane) {
		return
	}
	ref := string(work.Data.ForecastRef)
	if !represented {
		c.checkOmittedLane(id, work, ref)
		return
	}
	open := openWork(work.Data.State)
	if (work.Data.State == workStateCompleted || work.Data.State == workStateCancelled) && l.RootTaskStatus != rootTaskNone {
		c.issues.add("terminal work retains followup: %s", id)
	}
	if open && l.requiredStatus() == statusClosed {
		c.issues.add("open lane %s reported closed", id)
	}
	if work.Data.State == workStateCompleted && l.requiredStatus() != statusClosed {
		c.issues.add("closed lane %s reported open", id)
	}
	if work.Data.State == workStateCancelled && l.Lifecycle != laneLifecycleCancelled {
		c.issues.add("cancelled lane %s lifecycle is not marked cancelled", id)
	}
	if work.Data.State != workStateCancelled && l.Lifecycle != laneLifecycleNone {
		c.issues.add("lane %s lifecycle conflicts with source state", id)
	}
	if work.Data.State == workStateCancelled && l.CompletedAt != "" {
		c.issues.add("cancelled lane %s must not claim completion", id)
	}
	if ref != "" || forecastApplicable(work, reportingRoleLane, represented) || l.Forecast != nil {
		c.forecasts.check(ref, l.Forecast, forecastKindLane, open)
	}
}

// checkOmittedLane handles the distinct cancelled and active lifecycle cases.
//
// Example: Require an active lane to appear in the report.
func (c *workReconciler) checkOmittedLane(
	id string,
	work sourceRecord,
	ref string,
) {
	if work.Data.State != workStateCancelled {
		c.issues.add("declared lane %s missing from report", id)
		return
	}
	if ref != "" {
		c.forecasts.check(ref, nil, forecastKindLane, false)
	}
}

// checkRoot validates an unrepresented root's completion or forecast.
//
// Example: Require an active root forecast when no lane represents it.
func (c *workReconciler) checkRoot(
	id string,
	work sourceRecord,
	represented bool,
) {
	if !hasRole(work.Data.Roles, reportingRoleRoot) || represented {
		return
	}
	if work.Data.State == workStateCancelled && hasRole(work.Data.Roles, reportingRoleLane) {
		return
	}
	if _, ok := c.rootCompletions[id]; work.Data.State == workStateCompleted && !ok {
		c.issues.add("completed root %s completion missing from report", id)
	}
	ref := string(work.Data.ForecastRef)
	if forecastApplicable(work, reportingRoleRoot, represented) && !c.rootRefs[ref] {
		c.issues.add("active root %s forecast missing from report", id)
	}
	var found *forecast
	for i := range c.rootForecasts {
		if c.rootForecasts[i].ForecastRef == ref {
			found = &c.rootForecasts[i]
			break
		}
	}
	if ref != "" || openWork(work.Data.State) {
		c.forecasts.check(ref, found, forecastKindRoot, openWork(work.Data.State))
	}
}
