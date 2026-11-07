package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// sourceRef is a canonical document-and-record locator.
//
// Example: Identify one forecast record in forecasts.yaml.
type sourceRef string

// workData holds canonical work facts used for status reconciliation.
//
// Example: workData holds canonical work facts used for status reconciliation.
type workData struct {
	CompletedAt string          `yaml:"completed_at"`
	Owner       string          `yaml:"owner"`
	NextAction  string          `yaml:"next_action"`
	State       workState       `yaml:"state"`
	Roles       []reportingRole `yaml:"reporting_roles"`
	ForecastRef sourceRef       `yaml:"forecast_ref"`
}

// sourceRecord holds the relevant work and outcome fields of a ledger record.
//
// Example: sourceRecord holds the relevant work and outcome fields of a ledger record.
type sourceRecord struct {
	Outcome     string
	OutcomeRefs []string
	Type        string
	Section     string
	Data        workData
}

// UnmarshalYAML reads only fields relevant to the record type.
//
// Example: UnmarshalYAML reads only fields relevant to the record type.
func (r *sourceRecord) UnmarshalYAML(n *yaml.Node) error {
	var wire struct {
		Type    string    `yaml:"type"`
		Section string    `yaml:"section"`
		Data    yaml.Node `yaml:"data"`
		Links   struct {
			Outcomes []string `yaml:"outcomes"`
		} `yaml:"links"`
	}
	if err := n.Decode(&wire); err != nil {
		return err
	}
	r.Type, r.Section = wire.Type, wire.Section
	r.OutcomeRefs = wire.Links.Outcomes
	if wire.Type == "outcome" {
		var data struct {
			Description string `yaml:"description"`
		}
		if err := wire.Data.Decode(&data); err != nil {
			return err
		}
		r.Outcome = data.Description
	}
	if wire.Type == "work" {
		return wire.Data.Decode(&r.Data)
	}
	return nil
}

// ledgerSource is the canonical session ledger envelope.
//
// Example: ledgerSource is the canonical session ledger envelope.
type ledgerSource struct {
	Schema   string                  `yaml:"schema"`
	Sections map[string]any          `yaml:"sections"`
	Records  map[string]sourceRecord `yaml:"records"`
}

// forecastSource is the canonical forecast envelope.
//
// Example: forecastSource is the canonical forecast envelope.
type forecastSource struct {
	Schema  string                    `yaml:"schema"`
	Records map[string]map[string]any `yaml:"records"`
}

// sourceID extracts a semantic record key from a validated locator.
//
// Example: sourceID extracts a semantic record key from a validated locator.
func sourceID(
	ref string,
	file string,
) string {
	return strings.TrimPrefix(ref, file+"#/records/")
}

// validRef checks the expected canonical locator prefix and key.
//
// Example: validRef checks the expected canonical locator prefix and key.
func validRef(
	ref string,
	file string,
) bool {
	return strings.HasPrefix(ref, file+"#/records/") && sourceID(ref, file) != ""
}

// readSource decodes exactly one canonical YAML document.
//
// Example: readSource decodes exactly one canonical YAML document.
func readSource[T any](path string) (T, error) {
	var result T
	b, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return result, fmt.Errorf("multiple YAML documents")
		}
		return result, err
	}
	return result, nil
}

// textField returns a string source field or an empty value.
//
// Example: textField returns a string source field or an empty value.
func textField(
	m map[string]any,
	k string,
) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// sameValue compares strings or equivalent UTC instants.
//
// Example: sameValue compares strings or equivalent UTC instants.
func sameValue(
	a string,
	b string,
) bool {
	if a == b {
		return true
	}
	x, e := parseUTC(a)
	if e != nil {
		return false
	}
	y, e := parseUTC(b)
	return e == nil && x.Equal(y)
}

// projectionEqual recursively compares source projections, including UTC instants.
//
// Example: projectionEqual recursively compares source projections, including UTC instants.
func projectionEqual(
	a any,
	b any,
) bool {
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && sameValue(x, y)
	case map[string]any:
		y, ok := b.(map[string]any)
		return ok && projectionMapsEqual(x, y)
	case []any:
		y, ok := b.([]any)
		return ok && projectionListsEqual(x, y)
	default:
		return reflect.DeepEqual(a, b)
	}
}

// forecastStructure reports missing fields in native forecast detail.
//
// Example: forecastStructure reports missing fields in native forecast detail.
func forecastStructure(src map[string]any) []string {
	var issues []string
	if raw, ok := src["base_case"]; ok {
		issues = append(issues, checkBaseCase(raw)...)
	}
	if raw, ok := src["scenarios"]; ok {
		issues = append(issues, checkForecastScenarios(raw)...)
	}
	issues = append(issues, forecastSpreadIssues(src)...)
	return issues
}

// checkBaseCase validates the base endpoint and package details.
//
// Example: checkBaseCase validates the base endpoint and package details.
func checkBaseCase(raw any) []string {
	base, ok := raw.(map[string]any)
	if !ok {
		return []string{"base_case must be a mapping"}
	}
	var issues []string
	if textField(base, "endpoint") == "" {
		issues = append(issues, "base_case missing endpoint")
	}
	packages, ok := base["packages"].([]any)
	if !ok || len(packages) == 0 {
		issues = append(issues, "base_case missing packages")
	}
	for _, item := range packages {
		issues = append(issues, checkForecastFields(item, "base_case package", []string{"work", "duration", "basis"})...)
	}
	return issues
}

// checkForecastScenarios validates each supplied material downside scenario.
//
// Example: checkForecastScenarios validates each supplied material downside scenario.
func checkForecastScenarios(raw any) []string {
	scenarios, ok := raw.([]any)
	if !ok {
		return []string{"scenarios must be a list"}
	}
	var issues []string
	for _, item := range scenarios {
		issues = append(issues, checkForecastFields(item, "scenario", []string{"name", "trigger", "affected_work", "evidence", "assumptions", "added_duration", "event_time", "endpoint", "basis"})...)
	}
	return issues
}

// checkForecastFields checks required fields in one native forecast mapping.
//
// Example: checkForecastFields checks required fields in one native forecast mapping.
func checkForecastFields(
	raw any,
	name string,
	fields []string,
) []string {
	values, ok := raw.(map[string]any)
	if !ok {
		return []string{"invalid " + name}
	}
	var issues []string
	for _, field := range fields {
		if textField(values, field) == "" {
			issues = append(issues, name+" missing "+field)
		}
	}
	return issues
}

// hasRole checks an explicitly declared reporting responsibility.
//
// Example: hasRole checks an explicitly declared reporting responsibility.
func hasRole(
	roles []reportingRole,
	role reportingRole,
) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// ledgerRecordIssues checks the source fields needed for coverage decisions.
//
// Example: Report a missing owner on active work.
func ledgerRecordIssues(
	id string,
	record sourceRecord,
	sections map[string]any,
) []string {
	var issues []string
	if record.Type == "" || record.Section == "" {
		issues = append(issues, fmt.Sprintf("ledger record %s missing type or section", id))
	}
	if _, ok := sections[record.Section]; record.Type != "" && record.Section != "" && !ok {
		issues = append(issues, fmt.Sprintf("ledger record %s references unknown section %s", id, record.Section))
	}
	if record.Type != "work" {
		return issues
	}
	if record.Data.Owner == "" {
		issues = append(issues, fmt.Sprintf("work %s missing owner", id))
	}
	if openWork(record.Data.State) && record.Data.NextAction == "" {
		issues = append(issues, fmt.Sprintf("work %s missing next action", id))
	}
	switch record.Data.State {
	case workStateActive, workStateBlocked, workStateCompleted, workStateCancelled:
	default:
		issues = append(issues, fmt.Sprintf("work %s has unknown state", id))
	}
	if record.Data.Roles == nil {
		issues = append(issues, fmt.Sprintf("work %s reporting roles unverified", id))
	}
	for _, role := range record.Data.Roles {
		if role != reportingRoleLane && role != reportingRoleRoot {
			issues = append(issues, fmt.Sprintf("work %s has unknown reporting role %v", id, role))
		}
	}
	return issues
}

// reconciliationIssues accumulates advisory source consistency findings.
//
// Example: Collect an unavailable source and a mismatched forecast.
type reconciliationIssues []string

// add appends one source consistency finding.
//
// Example: Record an unresolved work reference.
func (i *reconciliationIssues) add(
	format string,
	args ...any,
) {
	*i = append(*i, fmt.Sprintf(format, args...))
}

// resolveWorkRef resolves one canonical locator against the work inventory.
//
// Example: Resolve a task's work reference.
func resolveWorkRef(
	ref string,
	works map[string]sourceRecord,
	issues *reconciliationIssues,
) string {
	if !validRef(ref, "project-understanding.yaml") {
		issues.add("invalid work reference %q", ref)
		return ""
	}
	id := sourceID(ref, "project-understanding.yaml")
	if _, ok := works[id]; !ok {
		issues.add("unknown work reference %q", ref)
		return ""
	}
	return id
}

// reconcile checks a report against canonical session sources without mutation.
//
// Example: reconcile checks a report against canonical session sources without mutation.
func reconcileAt(
	r report,
	dir string,
	evaluation time.Time,
) []string {
	var issues reconciliationIssues
	reportTime, _ := parseUTC(r.Timestamp)
	if reportTime.After(evaluation) {
		issues.add("report timestamp %s is in the future", r.Timestamp)
	}
	issues = append(issues, futureEstimateIssues(r, evaluation)...)
	issues = append(issues, futureActualIssues(r, evaluation)...)
	if !*r.ScopeComplete {
		issues.add("report declares scope incomplete")
	}
	if dir == "" {
		return append(issues, "session context unavailable; coverage and forecasts unverified")
	}
	ledger, ledgerErr := readSource[ledgerSource](filepath.Join(dir, "project-understanding.yaml"))
	if ledgerErr != nil || ledger.Schema != "project-understanding/v1" || ledger.Sections == nil || ledger.Records == nil {
		if _, err := os.Stat(filepath.Join(dir, "forecasts.yaml")); err != nil {
			issues.add("forecast source unavailable; applicability unverified")
		}
		return append(issues, "ledger unavailable, malformed, or unsupported; coverage and forecasts unverified")
	}
	works := map[string]sourceRecord{}
	for id, rec := range ledger.Records {
		issues = append(issues, ledgerRecordIssues(id, rec, ledger.Sections)...)
		if rec.Type != "work" {
			continue
		}
		works[id] = rec
		if openWork(rec.Data.State) {
			issues = append(issues, validateWorkOutcomes(id, rec, ledger.Records)...)
		}
		if issue := canonicalFutureActualIssue(id, rec, evaluation); issue != "" {
			issues = append(issues, issue)
		}
	}
	issues = append(issues, checkSharedForecastOwners(works)...)
	scope := buildTaskWorkIndex(r.Tasks, works)
	issues = append(issues, unlinkedScoredLeaves(r.Tasks, scope)...)
	lanes := map[string]lane{}
	rootRefs := map[string]bool{}
	for _, t := range r.Tasks {
		for _, ref := range t.WorkRefs {
			resolveWorkRef(ref, works, &issues)
		}
	}
	rootCompletions := map[string]rootCompletion{}
	for _, c := range r.RootCompletions {
		id := resolveWorkRef(c.WorkRef, works, &issues)
		if id == "" {
			continue
		}
		if _, ok := rootCompletions[id]; ok {
			issues.add("duplicate root completion %s", id)
		}
		rootCompletions[id] = c
		w := works[id]
		if !openWork(w.Data.State) {
			issues = append(issues, validateWorkOutcomes(id, w, ledger.Records)...)
		}
		if !hasRole(w.Data.Roles, reportingRoleRoot) || w.Data.State != workStateCompleted {
			issues.add("root completion %s does not reference completed root work", id)
		}
		if !outcomeCorresponds(w, ledger.Records, c.Outcome) {
			issues.add("root completion %s outcome unverified against linked outcomes", id)
		}
		if !sameValue(w.Data.CompletedAt, c.CompletedAt) {
			issues.add("root completion %s completed_at mismatch: source %q; report %q", id, w.Data.CompletedAt, c.CompletedAt)
		}
	}
	for _, l := range r.Lanes {
		id := resolveWorkRef(l.WorkRef, works, &issues)
		if id == "" {
			continue
		}
		if !scope.containsWork(l.TaskID, workID(id)) {
			issues.add("lane %s work %s is outside task scope", l.TaskID, id)
		}
		if !hasRole(works[id].Data.Roles, reportingRoleLane) {
			issues.add("work %s is not declared a lane", id)
		}
		if _, ok := lanes[id]; ok {
			issues.add("duplicate lane for %s", id)
		}
		lanes[id] = l
	}
	forecastCounts := map[string]int{}
	for _, f := range r.RootForecasts {
		rootRefs[f.ForecastRef] = true
		forecastCounts[f.ForecastRef]++
	}
	for _, l := range r.Lanes {
		if l.Forecast != nil {
			forecastCounts[l.Forecast.ForecastRef]++
		}
	}
	for ref, count := range forecastCounts {
		if count > 1 {
			issues.add("duplicate report forecast reference %q", ref)
		}
	}
	forecastChecks := forecastReconciler{dir: dir, timestamp: evaluation, issues: &issues, used: map[string]bool{}}
	ids := make([]string, 0, len(works))
	for id := range works {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	workChecks := workReconciler{works: works, lanes: lanes, scope: scope, rootCompletions: rootCompletions, rootRefs: rootRefs, rootForecasts: r.RootForecasts, issues: &issues, forecasts: &forecastChecks}
	for _, id := range ids {
		workChecks.check(id)
	}
	forecastChecks.checkUnclaimed(works)
	for id, l := range lanes {
		work := works[id]
		if !openWork(work.Data.State) {
			issues = append(issues, validateWorkOutcomes(id, work, ledger.Records)...)
		}
		claims := []string{l.Outcome}
		if l.Forecast != nil {
			claims = append(claims, l.Forecast.Outcome)
		}
		if !outcomeCorresponds(work, ledger.Records, claims...) {
			issues.add("lane %s outcome semantic equivalence unverified", id)
		}
	}
	for id, work := range works {
		issues = append(issues, rootOutcomeIssues(id, work, ledger.Records, r.RootForecasts)...)
	}
	for _, f := range r.RootForecasts {
		if !forecastChecks.used[f.ForecastRef] {
			issues.add("root forecast %q is not linked to applicable work", f.ForecastRef)
		}
	}
	for _, l := range r.Lanes {
		if l.Forecast != nil && !forecastChecks.used[l.Forecast.ForecastRef] {
			issues.add("lane forecast %q is not linked to applicable work", l.Forecast.ForecastRef)
		}
	}
	at, _ := parseUTC(r.Timestamp)
	estimates := append([]completionEstimate{}, r.PriorityEstimates...)
	if r.CompletionEstimate != nil {
		estimates = append(estimates, *r.CompletionEstimate)
	}
	anyOpen := scope.reportOpen
	for _, w := range works {
		if w.Data.State == workStateActive || w.Data.State == workStateBlocked {
			anyOpen = true
		}
	}
	for _, e := range estimates {
		relevantOpen := anyOpen
		if e.Priority != "" {
			relevantOpen = scope.priorityOpen(e.Priority, lanes)
		}
		base, err := parseUTC(e.AsOf)
		if err != nil {
			continue
		}
		if !base.After(at) && !base.After(evaluation) && relevantOpen && e.MaxMinutes != nil && !base.Add(time.Duration(float64(*e.MaxMinutes)*float64(time.Minute))).After(evaluation) {
			issues.add("completion estimate as_of %s endpoint elapsed while work open", e.AsOf)
		}
	}
	return issues
}

// projectionMapsEqual compares corresponding nested source fields.
//
// Example: A missing map entry makes projections unequal.
func projectionMapsEqual(
	left map[string]any,
	right map[string]any,
) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || !projectionEqual(value, other) {
			return false
		}
	}
	return true
}

// projectionListsEqual compares ordered nested source fields.
//
// Example: A scenario with a changed endpoint makes projections unequal.
func projectionListsEqual(
	left []any,
	right []any,
) bool {
	if len(left) != len(right) {
		return false
	}
	for i, value := range left {
		if !projectionEqual(value, right[i]) {
			return false
		}
	}
	return true
}
