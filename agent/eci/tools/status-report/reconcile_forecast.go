package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// forecastReconciler holds source and projection checks for one report.
//
// Example: Check the canonical forecast for an active lane.
type forecastReconciler struct {
	dir       string
	timestamp time.Time
	issues    *reconciliationIssues
	source    forecastSource
	used      map[string]bool
	loaded    bool
}

// load reads the canonical forecast document at most once.
//
// Example: Load forecasts for a lane.
func (c *forecastReconciler) load() bool {
	if c.loaded {
		return c.source.Records != nil
	}
	c.loaded = true
	var err error
	c.source, err = readSource[forecastSource](filepath.Join(c.dir, "forecasts.yaml"))
	if err != nil || c.source.Schema != "forecasts/v1" || c.source.Records == nil {
		c.issues.add("forecasts unavailable, malformed, or unsupported")
		c.source.Records = nil
		return false
	}
	return true
}

// check compares one canonical forecast against its report projection.
//
// Example: Check the target and evidence for an active lane.
func (c *forecastReconciler) check(
	ref string,
	f *forecast,
	kind forecastKind,
	open bool,
) {

	if f != nil {
		c.used[f.ForecastRef] = true
	}
	if !validRef(ref, "forecasts.yaml") {
		c.issues.add("invalid or missing forecast reference %q", ref)
		return
	}
	if !c.load() {
		return
	}
	id := sourceID(ref, "forecasts.yaml")
	src, ok := c.source.Records[id]
	if !ok {
		c.issues.add("forecast %s missing", id)
		return
	}
	if textField(src, "kind") != kind.String() {
		c.issues.add("forecast %s kind mismatch", id)
	}
	for _, field := range []string{"kind", "status", "outcome", "assessment", "original_baseline", "baseline_evidence"} {
		if textField(src, field) == "" {
			c.issues.add("forecast %s source missing required %s", id, field)
		}
	}
	for _, issue := range validateCanonicalRecalibration(src) {
		c.issues.add("forecast %s source %s", id, issue)
	}
	for _, issue := range forecastStructure(src) {
		c.issues.add("forecast %s source %s", id, issue)
	}
	assessment := textField(src, "assessment")
	if assessment != "complete" && assessment != "provisional" {
		c.issues.add("forecast %s source has invalid assessment %q", id, assessment)
	}
	if assessment == "provisional" && textField(src, "target_basis") == "" {
		c.issues.add("forecast %s provisional source missing target_basis", id)
	}
	state := textField(src, "status")
	if state != "active" && state != "inactive" {
		c.issues.add("forecast %s source has invalid status %q", id, state)
	}
	if open && textField(src, "target") == "" {
		c.issues.add("forecast %s source missing required target", id)
	}
	if open && state != "active" {
		c.issues.add("forecast %s is not active", id)
	}
	if !open && state == "active" {
		c.issues.add("closed work has active forecast %s", id)
	}
	if !open {
		if f != nil {
			c.issues.add("terminal work has report forecast %s", id)
		}
		return
	}
	if f == nil {
		c.issues.add("forecast %s missing from report", id)
		return
	}
	if f.Target == "unknown" || f.Assessment == "unknown" {
		c.issues.add("forecast %s has unknown target or assessment", id)
	}
	if f.ForecastRef != ref {
		c.issues.add("forecast %s reference mismatch", id)
	}
	for _, field := range forecastFieldPolicies() {
		if field.Report == "" {
			continue
		}
		got := forecastReportValue(*f, field.Report)
		if !sameValue(textField(src, field.Source), got) {
			c.issues.add("forecast %s %s mismatch: source %q; report %q", id, field.Source, textField(src, field.Source), got)
		}
	}
	c.compareRecalibration(id, src, *f)
	if len(f.Scenarios) > 0 {
		c.issues.add("forecast %s report scenarios cannot be verified against canonical scenarios; use source_projection", id)
	}
	for k := range src {
		if !supportedForecastField(k) {
			c.issues.add("forecast %s source field %s is unsupported", id, k)
		}
	}
	for k := range f.SourceProjection {
		if !nestedForecastField(k) {
			c.issues.add("forecast %s unsupported source_projection field %s", id, k)
		}
	}
	for _, field := range forecastFieldPolicies() {
		if !field.Nested {
			continue
		}
		k := field.Source
		source, present := src[k]
		projected, supplied := f.SourceProjection[k]
		if k == "baseline_evidence" && !supplied && textField(src, k) == f.Evidence {
			continue
		}
		if present != supplied || (present && !projectionEqual(source, projected)) {
			c.issues.add("forecast %s %s projection mismatch or missing", id, k)
		}
	}
	if target, err := parseUTC(textField(src, "target")); err == nil {
		at := c.timestamp
		if !target.After(at) {
			c.issues.add("forecast %s target elapsed while work open", id)
		}
	}
}

// compareRecalibration compares projected history with the canonical record.
//
// Example: Detect a prior target added only in the report.
func (c *forecastReconciler) compareRecalibration(
	id string,
	source map[string]any,
	projection forecast,
) {
	recal, ok := source["recalibration"].(map[string]any)
	if !ok {
		return
	}
	if current := textField(recal, "current"); current != "" && !sameValue(current, projection.Target) {
		c.issues.add("forecast %s recalibration current mismatch: source %q; report %q", id, current, projection.Target)
	}
	for _, field := range forecastHistoryFields() {
		if !sameValue(textField(recal, field), forecastHistoryValue(projection, field)) {
			c.issues.add("forecast %s recalibration %s mismatch", id, field)
		}
	}
}

// checkUnclaimed detects active canonical forecasts with no work owner.
//
// Example: Flag a root forecast omitted from the ledger's work records.
func (c *forecastReconciler) checkUnclaimed(works map[string]sourceRecord) {
	if _, err := os.Stat(filepath.Join(c.dir, "forecasts.yaml")); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			c.issues.add("forecast source stat failed: %v", err)
		}
		return
	}
	if !c.load() {
		return
	}
	claimed := map[sourceRef]bool{}
	for _, work := range works {
		if work.Data.ForecastRef != "" {
			claimed[work.Data.ForecastRef] = true
		}
	}
	for id, source := range c.source.Records {
		if textField(source, "status") == "active" && !claimed[sourceRef("forecasts.yaml#/records/"+id)] {
			c.issues.add("active canonical forecast %s is not linked to work", id)
		}
	}
}
