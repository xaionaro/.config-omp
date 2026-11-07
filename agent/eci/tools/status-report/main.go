package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Status report limits bound package, review, and timeline units.
//
// Example: An estimated leaf over thirty minutes needs an indivisibility reason.
const (
	// maxLeafMinutes is the target upper bound for estimated leaf packages.
	//
	// Example: An estimated 45-minute leaf requires a reason.
	maxLeafMinutes = 30
	// maxCriticRounds limits scoped review iterations.
	//
	// Example: A sixth round is rejected.
	maxCriticRounds reviewRounds = 5
	// percentageScale converts a unit ratio to percent.
	//
	// Example: One complete unit of two yields 50 percent.
	percentageScale = 100

	// progressBarWidth is the number of characters in the derived bar.
	//
	// Example: Half of twenty positions are filled for 50 percent.
	progressBarWidth = 20
	// timelineWindow is the preceding period covered by a timeline.
	//
	// Example: An event five hours old is outside the window.
	timelineWindow = 4 * time.Hour
	// timelineInterval is the duration of one timeline row.
	//
	// Example: A row spans thirty minutes.
	timelineInterval = 30 * time.Minute
)

// taskID identifies a task in the report hierarchy.
//
// Example: Task 1.2 belongs to parent 1.
type taskID string

// priorityLabel records the supported priority or an explicit unknown.
//
// Example: An unranked package uses unknown.
type priorityLabel string

// reviewRounds counts the scoped reporter–critic iterations.
//
// Example: One review uses one round.
type reviewRounds int

// estimate describes the remaining work range and its basis.
//
// Example: Record a 20–30 minute elapsed package.
type estimate struct {
	MinMinutes *fractionalMinutes `yaml:"min_minutes"`
	MaxMinutes *fractionalMinutes `yaml:"max_minutes"`
	Kind       effortKind         `yaml:"kind"`
	Basis      string             `yaml:"basis"`
}

// queueReview records the scoped review of one newly estimated queue item.
//
// Example: Record a critic check for a newly queued item.
type queueReview struct {
	TaskID     taskID        `yaml:"task_id"`
	Reporter   string        `yaml:"reporter"`
	Critic     string        `yaml:"critic"`
	Rounds     reviewRounds  `yaml:"rounds"`
	Estimate   string        `yaml:"estimate"`
	Impact     string        `yaml:"impact"`
	Priority   priorityLabel `yaml:"priority"`
	Unresolved string        `yaml:"unresolved"`
}

// task records one node of the package hierarchy.
//
// Example: Represent one leaf package or its parent.
type task struct {
	WorkRefs          []string      `yaml:"work_refs"`
	ID                taskID        `yaml:"id"`
	ParentID          taskID        `yaml:"parent_id"`
	Title             string        `yaml:"title"`
	Owner             string        `yaml:"owner"`
	Context           string        `yaml:"context"`
	Priority          priorityLabel `yaml:"priority"`
	Impact            string        `yaml:"impact"`
	ImpactPoints      *impactPoints `yaml:"impact_points"`
	ImpactBasis       string        `yaml:"impact_basis"`
	Effort            estimate      `yaml:"effort"`
	Status            workStatus    `yaml:"status"`
	Lifecycle         taskLifecycle `yaml:"lifecycle"`
	Evidence          string        `yaml:"evidence"`
	IndivisibleReason string        `yaml:"indivisible_reason"`
}

// report holds one persisted status projection.
//
// Example: Load the current status projection from YAML.
type report struct {
	Incomplete         []string             `yaml:"-"`
	Timestamp          string               `yaml:"timestamp"`
	TimelineAsOf       string               `yaml:"timeline_as_of"`
	StatusSync         *syncState           `yaml:"status_sync"`
	State              string               `yaml:"state"`
	Progress           []string             `yaml:"progress"`
	Decisions          []any                `yaml:"decisions"`
	Blockers           []string             `yaml:"blockers_risks"`
	Verification       []string             `yaml:"verification"`
	NextFocus          string               `yaml:"next_focus"`
	ScopeComplete      *bool                `yaml:"scope_complete"`
	Tasks              []task               `yaml:"tasks"`
	TopPriorities      []taskID             `yaml:"top_priorities"`
	PriorityOrder      []priorityLabel      `yaml:"priority_order"`
	ImpactScale        string               `yaml:"impact_scale"`
	Lanes              []lane               `yaml:"lanes"`
	RootCompletions    []rootCompletion     `yaml:"root_completions"`
	RootForecasts      []forecast           `yaml:"root_forecasts"`
	Roster             map[string]any       `yaml:"roster"`
	Timeline           []timelineEntry      `yaml:"timeline"`
	TimelineState      timelineState        `yaml:"timeline_state"`
	Reviews            []queueReview        `yaml:"reviews"`
	Details            map[string]any       `yaml:"details"`
	PriorityEstimates  []completionEstimate `yaml:"priority_estimates"`
	CompletionEstimate *completionEstimate  `yaml:"completion_estimate"`
}

// parse decodes and validates one structured status projection.
//
// Example: Reject a report with a missing task reference.
func parse(data []byte) (report, error) {
	var r report
	normalized, err := normalizeNarratives(data)
	if err != nil {
		return r, fmt.Errorf("invalid YAML/schema: %w", err)
	}
	d := yaml.NewDecoder(bytes.NewReader(normalized))
	d.KnownFields(true)
	if err := d.Decode(&r); err != nil {
		return r, fmt.Errorf("invalid YAML/schema: %w", err)
	}
	if err := validateLifecycleScalars(data); err != nil {
		return r, fmt.Errorf("invalid YAML/schema: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err != nil {
			return r, fmt.Errorf("decode trailing YAML: %w", err)
		}
		return r, fmt.Errorf("expected one YAML document")
	}
	if _, err := parseUTC(r.Timestamp); err != nil {
		return r, fmt.Errorf("timestamp must be RFC3339 UTC: %w", err)
	}
	if strings.TrimSpace(r.State) == "" || strings.TrimSpace(r.NextFocus) == "" || r.ScopeComplete == nil {
		return r, fmt.Errorf("state, next_focus and scope_complete are required")
	}
	if r.Progress == nil || r.Decisions == nil || r.Blockers == nil || r.Verification == nil || r.Tasks == nil || r.TopPriorities == nil {
		return r, fmt.Errorf("progress, decisions, blockers_risks, verification, tasks and top_priorities must be lists")
	}
	if r.TimelineState == timelineVerifiedEmpty && len(r.Timeline) > 0 {
		return r, fmt.Errorf("verified_empty timeline cannot contain entries")
	}
	if r.TimelineState == timelinePresent && len(r.Timeline) == 0 {
		return r, fmt.Errorf("present timeline requires entries")
	}
	byID := map[taskID]task{}
	children := map[taskID]int{}
	for _, t := range r.Tasks {
		if t.ID == "" || t.Title == "" || t.Priority == "" || t.Impact == "" || t.Effort.MinMinutes == nil || t.Effort.MaxMinutes == nil || math.IsNaN(float64(*t.Effort.MinMinutes)) || math.IsNaN(float64(*t.Effort.MaxMinutes)) || math.IsInf(float64(*t.Effort.MinMinutes), 0) || math.IsInf(float64(*t.Effort.MaxMinutes), 0) || *t.Effort.MinMinutes < 0 || *t.Effort.MaxMinutes < *t.Effort.MinMinutes || t.Effort.Kind == effortUnknown || strings.TrimSpace(t.Effort.Basis) == "" {
			return r, fmt.Errorf("task requires id, title, priority, impact and effort")
		}
		if t.ImpactPoints != nil && (math.IsNaN(float64(*t.ImpactPoints)) || math.IsInf(float64(*t.ImpactPoints), 0) || *t.ImpactPoints < 0) {
			return r, fmt.Errorf("task %s: impact_points must be finite and nonnegative", t.ID)
		}
		if _, exists := byID[t.ID]; exists {
			return r, fmt.Errorf("duplicate task id %q", t.ID)
		}
		byID[t.ID] = t

		if t.Lifecycle == taskLifecycleCancelled && (t.Status != statusUnknown || strings.TrimSpace(t.Evidence) == "") {
			return r, fmt.Errorf("task %s: cancelled requires unknown status and cancellation evidence", t.ID)
		}
		if t.Status == statusClosed && strings.TrimSpace(t.Evidence) == "" {
			return r, fmt.Errorf("task %s: CLOSED requires acceptance evidence", t.ID)
		}
	}
	for _, t := range r.Tasks {
		if err := validateTaskAncestry(t, byID); err != nil {
			return r, err
		}
		if t.ParentID != "" {
			children[t.ParentID]++
			if byID[t.ParentID].Lifecycle == taskLifecycleCancelled && t.Lifecycle != taskLifecycleCancelled && t.Status != statusClosed {
				r.Incomplete = append(r.Incomplete, fmt.Sprintf("cancelled task %s has nonterminal child %s", t.ParentID, t.ID))
			}
		}
	}
	for _, t := range r.Tasks {
		if children[t.ID] == 0 && *t.Effort.MaxMinutes > maxLeafMinutes && strings.TrimSpace(t.IndivisibleReason) == "" {
			return r, fmt.Errorf("task %s: leaf longer than 30 minutes needs indivisible_reason", t.ID)
		}
	}
	if err := validatePriorityOrder(r.PriorityOrder); err != nil {
		return r, err
	}
	seenTop := map[taskID]bool{}
	for _, id := range r.TopPriorities {
		if _, ok := byID[id]; !ok {
			return r, fmt.Errorf("unknown top priority task %s", id)
		}
		if seenTop[id] {
			return r, fmt.Errorf("duplicate top priority %s", id)
		}
		seenTop[id] = true
	}
	for _, review := range r.Reviews {
		if _, ok := byID[review.TaskID]; !ok {
			return r, fmt.Errorf("review references unknown task %s", review.TaskID)
		}
		if review.Reporter == "" || review.Critic == "" || review.Rounds < 1 || review.Rounds > maxCriticRounds || review.Estimate == "" || review.Impact == "" || review.Priority == "" || review.Unresolved == "" {
			return r, fmt.Errorf("review for %s requires reporter, critic, 1–5 rounds, estimate, impact, priority and unresolved", review.TaskID)
		}
	}
	for _, c := range r.RootCompletions {
		if c.WorkRef == "" || c.Outcome == "" {
			return r, fmt.Errorf("root completion requires work_ref and outcome")
		}
		if _, err := parseUTC(c.CompletedAt); err != nil {
			return r, fmt.Errorf("root completion invalid completed_at: %w", err)
		}
	}
	if err := validateSections(r); err != nil {
		return r, fmt.Errorf("validate status report: %w", err)
	}
	return r, nil
}

// render derives a readable status report from a validated projection.
//
// Example: Produce a human-readable report and derived progress bar.
func render(r report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Status (%s)\n%s\n\nSource freshness: unverified (no freshness marker checked).\n\n", r.Timestamp, r.State)
	if len(r.Incomplete) > 0 {
		writeList(&b, "INCOMPLETE — source reconciliation", r.Incomplete)
	}
	writeList(&b, "Progress updates", r.Progress)
	fmt.Fprintln(&b, "Decisions:")
	for _, v := range r.Decisions {
		writeValue(&b, v, 1)
	}
	b.WriteString("\n")
	writeList(&b, "Blockers / risks", r.Blockers)
	writeList(&b, "Verification", r.Verification)
	fmt.Fprintf(&b, "Next focus: %s\n\n", r.NextFocus)
	byID := map[taskID]task{}
	children := map[taskID][]taskID{}
	for _, t := range r.Tasks {
		byID[t.ID] = t
		children[t.ParentID] = append(children[t.ParentID], t.ID)
	}
	fmt.Fprintln(&b, "Calculated priority: impact points / maximum remaining work minutes; unscored positions are preserved.")
	fmt.Fprintln(&b, "Top priorities:")
	activeTop := make([]taskID, 0, len(r.TopPriorities))
	for _, id := range r.TopPriorities {
		if byID[id].Lifecycle != taskLifecycleCancelled || len(children[id]) > 0 {
			activeTop = append(activeTop, id)
		}
	}
	if len(activeTop) == 0 {
		fmt.Fprintln(&b, "- unknown / none identified")
	}
	for _, id := range orderTaskIDs(activeTop, byID, r.ImpactScale) {
		writeTaskLine(&b, byID[id], "", r.ImpactScale)
	}
	fmt.Fprintln(&b, "\nFull task tree:")
	writeTaskTree(&b, byID, children, r.ImpactScale, "", 0)
	known, done, unknownCount := 0, 0, 0
	var unscored, covered, oversized []string
	for _, t := range r.Tasks {
		if len(children[t.ID]) > 0 {
			continue
		}
		if t.Lifecycle == taskLifecycleCancelled {
			continue
		}
		if *t.Effort.MaxMinutes > maxLeafMinutes {
			oversized = append(oversized, string(t.ID)+": "+t.IndivisibleReason)
		}
		if t.Status == statusUnknown {
			unknownCount++
			unscored = append(unscored, string(t.ID))
			continue
		}
		covered = append(covered, string(t.ID))
		known++
		if t.Status == statusClosed {
			done++
		}
	}
	if known == 0 {
		fmt.Fprintf(&b, "\nProgress: unknown (no scored leaf packages; equal-package estimate from half-hour breakdown; partial coverage; unscored: %s; scope complete: %t)\n", strings.Join(unscored, ", "), (*r.ScopeComplete && len(r.Incomplete) == 0))
	} else {
		pct := done * percentageScale / known
		filled := done * progressBarWidth / known
		partial := unknownCount > 0 || !*r.ScopeComplete || len(r.Incomplete) > 0
		qualifier := ""
		if partial {
			qualifier = "; partial coverage"
		}
		fmt.Fprintf(&b, "\nProgress: [%s%s] %d%% (%d/%d known leaf tasks; equal-package estimate from half-hour breakdown%s; covered: %s; unscored: %s; scope complete: %t)\n", strings.Repeat("#", filled), strings.Repeat("-", progressBarWidth-filled), pct, done, known, qualifier, strings.Join(covered, ", "), strings.Join(unscored, ", "), (*r.ScopeComplete && len(r.Incomplete) == 0))
	}
	if len(oversized) > 0 {
		fmt.Fprintf(&b, "Larger indivisible packages: %s\n", strings.Join(oversized, "; "))
	}
	renderCompletionEstimates(&b, r)
	if len(r.Incomplete) > 0 {
		fmt.Fprintln(&b, "\nForecasts below are unreconciled; see source reconciliation issues above.")
	}
	renderLaneTable(&b, r.Lanes, byID, r.ImpactScale)
	for _, lane := range r.Lanes {
		if len(r.Incomplete) > 0 && lane.Forecast != nil {
			fmt.Fprintf(&b, "\nForecast %s: unreconciled.\n", lane.Forecast.ForecastRef)
		}
		renderForecast(&b, lane)
	}
	for _, c := range r.RootCompletions {
		fmt.Fprintf(&b, "\nRoot outcome: %s\nCompleted: %s; no active forecast deadline.\n", c.Outcome, c.CompletedAt)
	}
	for _, f := range r.RootForecasts {
		if len(r.Incomplete) > 0 {
			fmt.Fprintf(&b, "\nForecast %s: unreconciled.\n", f.ForecastRef)
		}
		renderRootForecast(&b, f)
	}
	if len(r.Roster) == 0 {
		fmt.Fprintln(&b, "\nRoster: unavailable (no roster evidence supplied).")
	} else {
		fmt.Fprintln(&b, "\nRoster:")
		writeValue(&b, r.Roster, 0)
	}
	fmt.Fprintln(&b, "\nDetails:")
	writeValue(&b, r.Details, 0)
	if r.TimelineState == timelineVerifiedEmpty {
		fmt.Fprintln(&b, "\nTimeline: verified no evidenced activity.")
		if r.TimelineAsOf != "" && !sameValue(r.TimelineAsOf, r.Timestamp) {
			fmt.Fprintf(&b, "Historical timeline as of %s; coverage may be incomplete.\n", r.TimelineAsOf)
		}
	} else {
		fmt.Fprintln(&b, "\nTimeline:")
		if r.TimelineAsOf != "" && !sameValue(r.TimelineAsOf, r.Timestamp) {
			fmt.Fprintf(&b, "Historical timeline as of %s; coverage may be incomplete.\n", r.TimelineAsOf)
		}
		if r.TimelineState == timelineUnavailable || (r.TimelineState == timelineUnknown && len(r.Timeline) > 0) {
			fmt.Fprintln(&b, "History is incomplete or unavailable; displayed rows do not establish full coverage.")
		}
		writeTimeline(&b, r.Timeline)
	}
	fmt.Fprintln(&b, "\nQueue item reviews:")
	for _, v := range r.Reviews {
		fmt.Fprintf(&b, "- task %s | reporter: %s | critic: %s | rounds: %d | estimate: %s | impact: %s | priority: %s | unresolved: %s\n", v.TaskID, v.Reporter, v.Critic, v.Rounds, v.Estimate, v.Impact, v.Priority, v.Unresolved)
	}
	return b.String()
}

// writeValue renders extensible nested report details.
//
// Example: Show nested extension details without dropping fields.
func writeValue(
	b *strings.Builder,
	v any,
	depth int,
) {
	pad := strings.Repeat("  ", depth)
	switch x := v.(type) {
	case map[string]any:
		writeDetailMap(b, x, depth)
	case []any:
		writeDetailList(b, x, depth)
	case []map[string]any:
		for _, item := range x {
			fmt.Fprintf(b, "%s-\n", pad)
			writeValue(b, item, depth+1)
		}
	default:
		fmt.Fprintf(b, "%s%v\n", pad, v)
	}
}

// main routes validation and rendering from a file or stdin.
//
// Example: Run status-report validate with a YAML file.
func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run executes one command and returns its CLI exit status.
//
// Example: Render a report read from stdin.
func run(
	args []string,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
) int {
	if len(args) > 0 && args[0] == "refresh" {
		return runRefresh(args[1:], errOut)
	}
	return runAt(args, in, out, errOut, time.Now().UTC())
}

// runAt executes a command using one captured evaluation time.
//
// Example: Validate a report at a deterministic instant.
func runAt(
	args []string,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
	evaluation time.Time,
) int {
	if len(args) < 1 || (args[0] != "validate" && args[0] != "render") {
		fmt.Fprintln(errOut, "usage: status-report validate|render [--session-dir DIR] [FILE]")
		return 2
	}
	command := args[0]
	var file, sessionDir string
	for i := 1; i < len(args); i++ {
		if args[i] == "--session-dir" && i+1 < len(args) && sessionDir == "" {
			i++
			sessionDir = args[i]
			continue
		}
		if file != "" || strings.HasPrefix(args[i], "--") {
			fmt.Fprintln(errOut, "invalid arguments")
			return 2
		}
		file = args[i]
	}
	if sessionDir == "" && file != "" && file != "-" && filepath.Base(file) == "latest-status-report.yaml" {
		sessionDir = filepath.Dir(file)
	}
	var data []byte
	var err error
	if file != "" && file != "-" {
		data, err = os.ReadFile(file)
	} else {
		data, err = io.ReadAll(in)
	}
	if err != nil {
		fmt.Fprintln(errOut, "status-report:", err)
		return 1
	}
	r, err := parse(data)
	if err != nil {
		fmt.Fprintln(errOut, "status-report:", err)
		return 1
	}
	r.Incomplete = append(r.Incomplete, reconcileAt(r, sessionDir, evaluation)...)
	payload := render(r)
	if command == "validate" {
		payload = "Schema valid; source reconciliation complete; source freshness unverified (does not verify factual truth or reviewer independence).\n"
		if len(r.Incomplete) > 0 {
			payload = "INCOMPLETE — source reconciliation:\n- " + strings.Join(r.Incomplete, "\n- ") + "\n"
		}
	}
	written, err := out.Write([]byte(payload))
	if err == nil && written != len(payload) {
		err = io.ErrShortWrite
	}
	if err != nil {
		fmt.Fprintln(errOut, "status-report: writing report:", err)
		return 1
	}
	if command == "validate" && len(r.Incomplete) > 0 {
		return 1
	}
	return 0
}

// renderForecast projects the supplied forecast; it does not estimate deadlines.
//
// Example: Print the canonical lane deadline from its projection.
func renderForecast(
	b *strings.Builder,
	l lane,
) {
	if l.Lifecycle == laneLifecycleCancelled {
		fmt.Fprintf(b, "\nCancelled: %s; no active forecast deadline.\n", l.Outcome)
		return
	}
	if l.NextMilestone != "" {
		fmt.Fprintf(b, "\nNext milestone: %s\n", l.NextMilestone)
	}
	if l.CompletedAt != "" {
		fmt.Fprintf(b, "Completed: %s; no active forecast deadline.\n", l.CompletedAt)
		return
	}
	if l.Forecast == nil {
		return
	}
	f := *l.Forecast
	switch {
	case f.Target == "unknown":
		fmt.Fprintf(b, "Forecast deadline: %s — unknown.\n", f.Outcome)
	case f.Target != "":
		fmt.Fprintf(b, "Forecast deadline: %s will be finished by %s.\n", f.Outcome, f.Target)
	}
	renderForecastDetails(b, f)
	renderScenarios(b, f.Outcome, f)
}

// writeList displays the supplied status items.
//
// Example: Print the report verification items.
func writeList(
	b *strings.Builder,
	title string,
	values []string,
) {
	fmt.Fprintf(b, "%s:\n", title)
	for _, v := range values {
		fmt.Fprintf(b, "- %s\n", v)
	}
	b.WriteByte('\n')
}

// writeTaskLine displays a task once in a requested list.
//
// Example: Print one task in the priority list.
func writeTaskLine(
	b *strings.Builder,
	t task,
	indent string,
	scale string,
) {
	fmt.Fprintf(b, "%s- %s %s | priority group: %s | impact: %s | effort: %g–%g minutes %s (%s) | status: %s", indent, t.ID, t.Title, t.Priority, t.Impact, *t.Effort.MinMinutes, *t.Effort.MaxMinutes, t.Effort.Kind, t.Effort.Basis, t.Status.String())
	if t.Lifecycle == taskLifecycleCancelled {
		fmt.Fprint(b, " | lifecycle: cancelled")
	}
	fmt.Fprintf(b, " | calculated priority: %s", priorityText(t, scale))
	if t.Owner != "" {
		fmt.Fprintf(b, " | owner: %s", t.Owner)
	} else {
		fmt.Fprint(b, " | owner: unowned")
	}
	if t.Context != "" {
		fmt.Fprintf(b, " | context: %s", t.Context)
	}
	if t.Evidence != "" {
		fmt.Fprintf(b, " | evidence: %s", t.Evidence)
	}
	if t.IndivisibleReason != "" {
		fmt.Fprintf(b, " | indivisible: %s", t.IndivisibleReason)
	}
	b.WriteByte('\n')
}

// writeTaskTree renders parent and child tasks in input order.
//
// Example: Print child packages beneath their parent.
func writeTaskTree(
	b *strings.Builder,
	byID map[taskID]task,
	children map[taskID][]taskID,
	scale string,
	parent taskID,
	depth int,
) {
	for _, id := range orderTaskIDs(children[parent], byID, scale) {
		writeTaskLine(b, byID[id], strings.Repeat("  ", depth), scale)
		writeTaskTree(b, byID, children, scale, id, depth+1)
	}
}

// validateTaskAncestry enforces the strictly shorter parent-prefix invariant.
//
// Example: Task 1.2 must refer to existing parent 1.
func validateTaskAncestry(
	t task,
	byID map[taskID]task,
) error {
	at := strings.LastIndex(string(t.ID), ".")
	if at >= 0 && (at == 0 || at == len(t.ID)-1 || strings.Contains(string(t.ID), "..")) {
		return fmt.Errorf("task %s: malformed hierarchical ID", t.ID)
	}
	if at >= 0 && t.ParentID != t.ID[:at] {
		return fmt.Errorf("task %s: parent_id must match hierarchical ancestry", t.ID)
	}
	if at < 0 && t.ParentID != "" {
		return fmt.Errorf("task %s: root ID cannot name a parent", t.ID)
	}
	if t.ParentID != "" {
		if _, ok := byID[t.ParentID]; !ok {
			return fmt.Errorf("task %s: missing parent %s", t.ID, t.ParentID)
		}
	}
	return nil
}

// writeDetailMap renders extensible mappings in deterministic key order.
//
// Example: Evidence keys appear in a stable order.
func writeDetailMap(
	b *strings.Builder,
	value map[string]any,
	depth int,
) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeDetailField(b, key, value[key], depth)
	}
}

// writeDetailField renders one scalar or nested mapping entry.
//
// Example: A nested evidence map is rendered beneath its key.
func writeDetailField(
	b *strings.Builder,
	key string,
	value any,
	depth int,
) {
	pad := strings.Repeat("  ", depth)
	switch value.(type) {
	case map[string]any, []any:
		fmt.Fprintf(b, "%s%s:\n", pad, key)
		writeValue(b, value, depth+1)
	default:
		fmt.Fprintf(b, "%s%s: %v\n", pad, key, value)
	}
}

// writeDetailList renders extensible sequence entries in input order.
//
// Example: Multiple evidence notes remain separate list items.
func writeDetailList(
	b *strings.Builder,
	values []any,
	depth int,
) {
	for _, value := range values {
		writeDetailItem(b, value, depth)
	}
}

// writeDetailItem renders one scalar or nested sequence item.
//
// Example: A nested decision list is rendered beneath its bullet.
func writeDetailItem(
	b *strings.Builder,
	value any,
	depth int,
) {
	pad := strings.Repeat("  ", depth)
	switch value.(type) {
	case map[string]any, []any:
		fmt.Fprintf(b, "%s-\n", pad)
		writeValue(b, value, depth+1)
	default:
		fmt.Fprintf(b, "%s- %v\n", pad, value)
	}
}
