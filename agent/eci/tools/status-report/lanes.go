package main

import (
	"fmt"
	"strings"
)

// scenario holds a supplied forecast scenario and its evidence.
//
// Example: Record the trigger and endpoint of a downside case.
type scenario struct {
	Case                  string `yaml:"case"`
	Trigger               string `yaml:"trigger"`
	AffectedWork          string `yaml:"affected_work"`
	DurationBasis         string `yaml:"duration_basis"`
	EvidenceAssumptions   string `yaml:"evidence_assumptions"`
	Endpoint              string `yaml:"endpoint"`
	EvidenceGapNextAction string `yaml:"evidence_gap_next_action"`
}

// rootCompletion projects an actual completion for root-only work.
//
// Example: Show a completed root without inventing a lane.
type rootCompletion struct {
	WorkRef     string `yaml:"work_ref"`
	Outcome     string `yaml:"outcome"`
	CompletedAt string `yaml:"completed_at"`
}

// forecast holds an upstream outcome forecast and associated scenarios.
//
// Example: Project a lane deadline and its supporting evidence.
type forecast struct {
	SourceProjection map[string]any `yaml:"source_projection"`
	ForecastRef      string         `yaml:"forecast_ref"`
	Outcome          string         `yaml:"outcome"`
	Target           string         `yaml:"target"`
	Assessment       string         `yaml:"assessment"`
	Baseline         string         `yaml:"baseline"`
	Prior            string         `yaml:"prior"`
	Reason           string         `yaml:"reason"`
	Evidence         string         `yaml:"evidence"`
	Dependencies     string         `yaml:"dependencies"`
	Scenarios        []scenario     `yaml:"scenarios"`
}

// lane preserves the separate readiness states for an outcome.
//
// Example: Keep implementation, test, and production readiness separate.
type lane struct {
	Lifecycle            laneLifecycle       `yaml:"lifecycle"`
	WorkRef              string              `yaml:"work_ref"`
	RequiredEnvironment  requiredEnvironment `yaml:"required_environment"`
	TaskID               taskID              `yaml:"task_id"`
	Outcome              string              `yaml:"outcome"`
	Owner                string              `yaml:"owner"`
	Stage                string              `yaml:"stage"`
	ImplementationStatus workStatus          `yaml:"implementation_status"`
	TestStatus           workStatus          `yaml:"test_status"`
	ProdStatus           workStatus          `yaml:"prod_status"`
	NextMilestone        string              `yaml:"next_milestone"`
	CompletedAt          string              `yaml:"completed_at"`
	Forecasted           bool                `yaml:"forecasted"`
	Blocker              string              `yaml:"blocker"`
	NextAction           string              `yaml:"next_action"`
	Context              string              `yaml:"context"`
	RootTaskStatus       rootTaskStatus      `yaml:"root_task_status"`
	Forecast             *forecast           `yaml:"forecast"`
}

// renderLaneTable displays readiness and task context without combining statuses.
//
// Example: Show independent readiness columns for a lane.
func renderLaneTable(
	b *strings.Builder,
	lanes []lane,
	tasks map[taskID]task,
	scale string,
) {
	if len(lanes) == 0 {
		return
	}
	fmt.Fprintln(b, "\n| Task ID | Parent ID | Lane | Lane requirement context | Priority group | Calculated priority | Impact | Effort | Stage | Required environment | Owner | Implementation Status | Test Status | Prod Status | Blocker | Next milestone | Forecast deadline / recalibration | Dependencies / critical path | Next proof/action |")
	fmt.Fprintln(b, "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
	ids := make([]taskID, 0, len(lanes))
	byTask := map[taskID][]lane{}
	for _, l := range lanes {
		ids = append(ids, l.TaskID)
		byTask[l.TaskID] = append(byTask[l.TaskID], l)
	}
	for _, id := range orderTaskIDs(ids, tasks, scale) {
		l := byTask[id][0]
		byTask[id] = byTask[id][1:]
		t := tasks[l.TaskID]
		target, dependencies := "", ""
		if l.Forecast != nil {
			target = l.Forecast.Target
			dependencies = l.Forecast.Dependencies
		}
		effort := fmt.Sprintf("%g–%g minutes %s (%s)", *t.Effort.MinMinutes, *t.Effort.MaxMinutes, t.Effort.Kind, t.Effort.Basis)
		context := l.Context
		if strings.TrimSpace(context) == "" || strings.EqualFold(strings.TrimSpace(context), "unknown") {
			context = "lineage unavailable—reconcile"
		}
		vals := []string{string(l.TaskID), string(t.ParentID), l.Outcome, context, string(t.Priority), priorityText(t, scale), t.Impact, effort, l.Stage, l.RequiredEnvironment.String(), l.Owner, l.ImplementationStatus.String(), l.TestStatus.String(), l.ProdStatus.String(), l.Blocker, l.NextMilestone, target, dependencies, l.NextAction}
		fmt.Fprint(b, "|")
		for _, v := range vals {
			fmt.Fprintf(b, " %s |", timelineCell(v))
		}
		fmt.Fprintln(b)
	}
	for _, l := range lanes {
		if l.RootTaskStatus != rootTaskNone {
			fmt.Fprintf(b, "\nRoot-task status [%s / %s]: %s\n", timelineCell(string(l.TaskID)), timelineCell(l.Outcome), l.RootTaskStatus.String())
		}
	}
}

// renderScenarios displays supplied scenario facts in the required table.
//
// Example: Display the base case and a downside case.
func renderScenarios(
	b *strings.Builder,
	name string,
	f forecast,
) {
	nativeShown := renderNativeSpread(b, name, f.SourceProjection)
	if len(f.Scenarios) == 0 {
		return
	}
	if nativeShown {
		fmt.Fprintln(b, "\nLegacy scenarios (unverified):")
	}
	fmt.Fprintln(b, "\n| Lane/root and case | Trigger/evidence | Affected work/dependency | Added duration/range and basis | Evidence assessed; assumptions/limits | Endpoint/range | Evidence gap / next fact-finding action |")
	fmt.Fprintln(b, "| --- | --- | --- | --- | --- | --- | --- |")
	for _, r := range f.Scenarios {
		fmt.Fprintf(b, "| %s — %s | %s | %s | %s | %s | %s | %s |\n", timelineCell(name), timelineCell(r.Case), timelineCell(r.Trigger), timelineCell(r.AffectedWork), timelineCell(r.DurationBasis), timelineCell(r.EvidenceAssumptions), timelineCell(r.Endpoint), timelineCell(r.EvidenceGapNextAction))
	}
}

// renderRootForecast prints the canonical root projection and scenarios.
//
// Example: Print the canonical root completion line.
func renderRootForecast(
	b *strings.Builder,
	f forecast,
) {
	switch {
	case f.Target == "unknown":
		fmt.Fprintf(b, "\nRoot completion forecast: %s — unknown.\n", f.Outcome)
	case f.Target != "":
		fmt.Fprintf(b, "\nRoot completion forecast: %s will be finished by %s.\n", f.Outcome, f.Target)
	}
	renderForecastDetails(b, f)
	renderScenarios(b, f.Outcome, f)
}

// renderForecastDetails prints the assessment and supplied recalibration.
//
// Example: Explain how an initial forecast was established.
func renderForecastDetails(
	b *strings.Builder,
	f forecast,
) {
	if len(f.SourceProjection) > 0 {
		fmt.Fprintln(b, "Forecast source projection:")
		writeValue(b, f.SourceProjection, 1)
	}
	switch f.Assessment {
	case "provisional":
		fmt.Fprintf(b, "Forecast assessment: provisional — %s; target conditional.\n", provisionalRisk(f.SourceProjection))
	case "":
	default:
		fmt.Fprintf(b, "Forecast assessment: %s\n", f.Assessment)
	}
	switch {
	case f.Prior != "":
		renderPriorForecast(b, f)
	case f.Baseline != "":
		renderForecastBaseline(b, f)
	default:
		if f.Reason != "" {
			fmt.Fprintf(b, "Forecast reason: %s\n", f.Reason)
		}
		if f.Evidence != "" {
			fmt.Fprintf(b, "Forecast evidence: %s\n", f.Evidence)
		}
	}
	if f.Dependencies != "" {
		fmt.Fprintf(b, "Dependencies / critical path: %s\n", f.Dependencies)
	}
}

// writeForecastEvidence appends only supplied explanation and evidence.
//
// Example: Preserve the reason and evidence of a changed forecast once.
func writeForecastEvidence(
	b *strings.Builder,
	f forecast,
) {
	if f.Reason != "" {
		fmt.Fprintf(b, "; why moved: %s", f.Reason)
	}
	if f.Evidence != "" {
		fmt.Fprintf(b, "; supporting evidence: %s", f.Evidence)
	}
}

// sameForecastInstant compares supported UTC forecast endpoints.
//
// Example: Z and +00:00 representations of one instant are equal.
func sameForecastInstant(
	a,
	b string,
) bool {
	left, err := parseUTC(a)
	if err != nil {
		return false
	}
	right, err := parseUTC(b)
	if err != nil {
		return false
	}
	return left.Equal(right)
}

// renderForecastBaseline compares endpoints without claiming intermediate history.
//
// Example: Matching instants still leave intermediate recalibration history unknown.
func renderForecastBaseline(
	b *strings.Builder,
	f forecast,
) {
	if sameForecastInstant(f.Baseline, f.Target) {
		fmt.Fprintf(b, "Forecast recalibration: unchanged — baseline %s", f.Baseline)
		if f.Evidence != "" {
			fmt.Fprintf(b, "; supporting evidence: %s", f.Evidence)
		}
		fmt.Fprintln(b, ".")
		fmt.Fprintln(b, "Intermediate recalibration history unknown; endpoints match.")
	} else {
		fmt.Fprintf(b, "Forecast baseline: %s; current target: %s; recalibration history unknown.\n", f.Baseline, f.Target)
		if f.Evidence != "" {
			fmt.Fprintf(b, "Forecast evidence: %s\n", f.Evidence)
		}
	}
	if f.Reason != "" {
		fmt.Fprintf(b, "Forecast reason: %s\n", f.Reason)
	}
}

// renderPriorForecast reports movement only when both instants support it.
//
// Example: Equivalent Z and +00:00 times render as unchanged.
func renderPriorForecast(
	b *strings.Builder,
	f forecast,
) {
	switch {
	case sameForecastInstant(f.Prior, f.Target):
		fmt.Fprintf(b, "Forecast recalibration: unchanged — prior %s; current target %s.\n", f.Prior, f.Target)
		writeForecastContext(b, f)
	case f.Prior == "unknown" || f.Target == "unknown":
		fmt.Fprintf(b, "Forecast recalibration: prior %s; current target %s; movement unknown.\n", f.Prior, f.Target)
		writeForecastContext(b, f)
	default:
		fmt.Fprintf(b, "Forecast recalibration: %s → %s", f.Prior, f.Target)
		writeForecastEvidence(b, f)
		fmt.Fprintln(b, ".")
	}
}

// writeForecastContext preserves context without implying a deadline moved.
//
// Example: An unchanged forecast can still have supplied evidence.
func writeForecastContext(
	b *strings.Builder,
	f forecast,
) {
	if f.Reason != "" {
		fmt.Fprintf(b, "Forecast reason: %s\n", f.Reason)
	}
	if f.Evidence != "" {
		fmt.Fprintf(b, "Forecast evidence: %s\n", f.Evidence)
	}
}
