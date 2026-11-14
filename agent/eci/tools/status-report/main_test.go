package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validCore is a representative report before completion estimates.
//
// Example: Add estimates to exercise validation.
const validCore = `timestamp: "2026-11-02T15:24:00Z"
state: Working
progress: [Implementation started]
decisions: [Use YAML]
blockers_risks: [none]
verification: [pending]
next_focus: Finish
scope_complete: false
tasks:
  - id: "1"
    title: Root
    priority: high
    impact: Useful
    effort: {min_minutes: 60, max_minutes: 60, kind: elapsed, basis: guess}
    status: IN PROGRESS
  - id: "1.1"
    parent_id: "1"
    title: Complete
    priority: high
    impact: Useful
    effort: {min_minutes: 30, max_minutes: 30, kind: elapsed, basis: guess}
    status: CLOSED
    evidence: accepted test
  - id: "1.2"
    parent_id: "1"
    title: Unknown
    priority: unknown
    impact: unknown
    effort: {min_minutes: 10, max_minutes: 30, kind: elapsed, basis: educated guess}
    status: unknown
top_priorities: ["1.1"]
lanes: []
roster: {}
timeline: []
reviews: []
`

// estimates supplies representative priority and overall ranges.
//
// Example: Combine it with validCore for a complete report.
const estimates = `priority_estimates:
  - {priority: high, min_minutes: 20, max_minutes: 40, as_of: "2026-11-02T15:24:00Z", basis: educated guess, assumptions: review available, dependencies: review, critical_path: latest parallel endpoint}
  - {priority: unknown, min_minutes: 20, max_minutes: 40, as_of: "2026-11-02T15:24:00Z", basis: educated guess, assumptions: review available, dependencies: review, critical_path: latest parallel endpoint}
completion_estimate: {min_minutes: 20, max_minutes: 40, as_of: "2026-11-02T15:24:00Z", basis: educated guess, assumptions: review available, dependencies: review, critical_path: latest parallel endpoint}
`

// valid combines the report and its estimates.
//
// Example: Parse it in behavior tests.
const valid = validCore + estimates

// TestRenderProgressAndSeparateLists checks the corresponding status report behavior.
//
// Example: Verify render progress and separate lists through the public behavior.
func TestRenderProgressAndSeparateLists(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(valid))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	out := render(r)
	for _, s := range []string{"Top priorities", "Full task tree", "100%", "partial", "1/1", "1.2", "equal-package"} {
		if !strings.Contains(out, s) {
			assert.Fail(t, fmt.Sprintf("missing %q in %s", s, out))
		}
	}
}

// TestRejectInvalidStructures checks the corresponding status report behavior.
//
// Example: Verify reject invalid structures through the public behavior.
func TestRejectInvalidStructures(t *testing.T) {
	t.Parallel()
	cases := []string{
		strings.Replace(valid, `top_priorities: ["1.1"]`, `top_priorities: ["missing"]`, 1),
		strings.Replace(valid, `parent_id: "1"`, `parent_id: "missing"`, 1),
		strings.Replace(valid, `evidence: accepted test`, `evidence: ""`, 1),
		strings.Replace(valid, `status: CLOSED`, `status: FINISHED`, 1),
		strings.Replace(valid, `scope_complete: false`, `scope_complete: maybe`, 1),
		valid + "state: duplicate\n",
	}
	for _, input := range cases {
		if _, err := parse([]byte(input)); err == nil {
			assert.Fail(t, fmt.Sprintf("accepted invalid input: %s", input))
		}
	}
}

// TestZeroKnownProgress checks the corresponding status report behavior.
//
// Example: Verify zero known progress through the public behavior.
func TestZeroKnownProgress(t *testing.T) {
	t.Parallel()
	input := strings.Replace(valid, "status: CLOSED\n    evidence: accepted test", "status: unknown", 1)
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	out := render(r)
	if !strings.Contains(out, "Progress: unknown") {
		require.FailNow(t, fmt.Sprint(out))
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Progress:") {
			require.NotContains(t, line, "%")
			require.NotContains(t, line, "[")
			require.NotContains(t, line, "#")
		}
	}
}

// TestLaneAndUnknownCoverage checks the corresponding status report behavior.
//
// Example: Verify lane and unknown coverage through the public behavior.
func TestLaneAndUnknownCoverage(t *testing.T) {
	t.Parallel()
	input := strings.Replace(valid, "lanes: []", "lanes:\n  - task_id: 'missing'\n    outcome: Test\n    required_environment: implementation\n    implementation_status: NEW\n    test_status: NEW\n    prod_status: NEW", 1)
	if _, err := parse([]byte(input)); err == nil {
		require.FailNow(t, fmt.Sprint("accepted lane referencing missing task"))
	}
	input = strings.Replace(input, "task_id: 'missing'", "task_id: '1'", 1)
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	out := render(r)
	if !strings.Contains(out, "covered: 1.1") {
		require.FailNow(t, fmt.Sprint(out))
	}
}

// TestLaneForecastRendering checks the corresponding status report behavior.
//
// Example: Verify lane forecast rendering through the public behavior.
func TestLaneForecastRendering(t *testing.T) {
	t.Parallel()
	input := strings.Replace(valid, "lanes: []", `lanes:
  - task_id: '1'
    outcome: Finish work
    required_environment: implementation
    implementation_status: IN PROGRESS
    test_status: NEW
    prod_status: NEW
    next_milestone: Checks pass
    forecast:
      outcome: Finish work
      target: '2026-11-02T17:22:00Z'
      assessment: provisional — review pending; target conditional
      baseline: '2026-11-02T17:22:00Z'
      evidence: Initial estimate
      dependencies: Review
`, 1)
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	out := render(r)
	for _, want := range []string{"Next milestone: Checks pass", "Forecast deadline: Finish work will be finished by 2026-11-02T17:22:00Z.", "Forecast assessment: provisional", "Forecast recalibration: unchanged — baseline", "Dependencies / critical path: Review"} {
		if !strings.Contains(out, want) {
			assert.Fail(t, fmt.Sprintf("missing %q", want))
		}
	}
}

// TestPackageAndReviewValidation checks the corresponding status report behavior.
//
// Example: Verify package and review validation through the public behavior.
func TestPackageAndReviewValidation(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		strings.Replace(valid, "max_minutes: 30, kind: elapsed, basis: guess", "max_minutes: 60, kind: elapsed, basis: guess", 1),
		strings.Replace(valid, "min_minutes: 30, max_minutes: 30", "min_minutes: 40, max_minutes: 30", 1),
		strings.Replace(valid, "reviews: []", "reviews:\n  - {task_id: '1.1', reporter: A, critic: B, rounds: 6, estimate: checked, impact: checked, priority: checked, unresolved: none}", 1),
		strings.Replace(valid, "reviews: []", "reviews:\n  - {task_id: missing, reporter: A, critic: B, rounds: 1, estimate: checked, impact: checked, priority: checked, unresolved: none}", 1),
	} {
		if _, err := parse([]byte(input)); err == nil {
			assert.Fail(t, fmt.Sprintf("accepted invalid structure: %s", input))
		}
	}
	oversized := strings.Replace(valid, "max_minutes: 30, kind: elapsed, basis: guess}\n    status: CLOSED", "max_minutes: 60, kind: elapsed, basis: guess}\n    indivisible_reason: Cannot split\n    status: CLOSED", 1)
	r, err := parse([]byte(oversized))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	if !strings.Contains(render(r), "Larger indivisible packages: 1.1: Cannot split") {
		require.FailNow(t, fmt.Sprint(render(r)))
	}
}

// TestEmptyTimelineEvidence checks the corresponding status report behavior.
//
// Example: Verify empty timeline evidence through the public behavior.
func TestEmptyTimelineEvidence(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(valid + "timeline_state: verified_empty\n"))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	if !strings.Contains(render(r), "Timeline: verified no evidenced activity") {
		require.FailNow(t, fmt.Sprint(render(r)))
	}
	if _, err := parse([]byte(valid + "timeline_state: invented\n")); err == nil {
		require.FailNow(t, fmt.Sprint("accepted invented timeline state"))
	}
}

// TestNestedSchemaValidation checks the corresponding status report behavior.
//
// Example: Verify nested schema validation through the public behavior.
func TestNestedSchemaValidation(t *testing.T) {
	t.Parallel()
	for _, replacement := range []string{
		"lanes:\n  - {task_id: '1', outcome: X, required_environment: implementation, implementation_status: NEW, test_status: NEW, prod_status: NEW, forecast: 42}",
		"lanes:\n  - {task_id: '1', outcome: X, required_environment: implementation, implementation_status: NEW, test_status: NEW, prod_status: NEW, forecast: {target: [bad]}}",
		"timeline:\n  - {job: X, parent_chain: X, start: '2026-11-02T13:54:00Z', end: '2026-11-02T14:24:00Z', fast: X, normal: X, lost_field: X}",
	} {
		source := "lanes: []"
		if strings.HasPrefix(replacement, "timeline:") {
			source = "timeline: []"
		}
		if _, err := parse([]byte(strings.Replace(valid, source, replacement, 1))); err == nil {
			assert.Fail(t, fmt.Sprintf("accepted %s", replacement))
		}
	}
}

// TestRejectNonFiniteEffort checks the corresponding status report behavior.
//
// Example: Verify reject non finite effort through the public behavior.
func TestRejectNonFiniteEffort(t *testing.T) {
	t.Parallel()
	for _, value := range []string{".nan", ".inf", "-.inf"} {
		input := strings.Replace(valid, "min_minutes: 30", "min_minutes: "+value, 1)
		if _, err := parse([]byte(input)); err == nil {
			assert.Fail(t, fmt.Sprintf("accepted %s", value))
		}
	}
}

// TestTimelineParentChainConsistency checks the corresponding status report behavior.
//
// Example: Verify timeline parent chain consistency through the public behavior.
func TestTimelineParentChainConsistency(t *testing.T) {
	t.Parallel()
	input := strings.Replace(valid, "timeline: []", "timeline:\n  - {job: X, parent_chain: A, start: '2026-11-02T13:54:00Z', end: '2026-11-02T14:24:00Z', fast: unknown, normal: unknown}\n  - {job: X, parent_chain: B, start: '2026-11-02T14:24:00Z', end: '2026-11-02T14:54:00Z', fast: unknown, normal: unknown}", 1)
	if _, err := parse([]byte(input)); err == nil {
		require.FailNow(t, fmt.Sprint("accepted inconsistent chain"))
	}
}

// TestLaneScenarioAndRootForecastOutput checks the corresponding status report behavior.
//
// Example: Verify lane scenario and root forecast output through the public behavior.
func TestLaneScenarioAndRootForecastOutput(t *testing.T) {
	t.Parallel()
	input := strings.Replace(valid, "lanes: []", `lanes:
  - task_id: '1'
    outcome: Finish work
    required_environment: implementation
    implementation_status: IN PROGRESS
    test_status: NEW
    prod_status: NEW
    root_task_status: FOLLOWUP
    forecast:
      outcome: Finish work
      target: '2026-11-02T17:22:00Z'
      assessment: complete
      scenarios:
        - {case: base, trigger: none, affected_work: checks, duration_basis: none, evidence_assumptions: ready, endpoint: '2026-11-02T17:22:00Z', evidence_gap_next_action: none}
root_forecasts:
  - {outcome: Complete root, target: '2026-11-02T18:00:00Z', assessment: complete}
`, 1)
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	out := render(r)
	for _, want := range []string{"| Task ID |", "| Lane/root and case |", "Root-task status [1 / Finish work]: FOLLOWUP", "Root completion forecast: Complete root will be finished by 2026-11-02T18:00:00Z."} {
		if !strings.Contains(out, want) {
			assert.Fail(t, fmt.Sprintf("missing %q", want))
		}
	}
}

// TestPartialTimelineDisclosure checks the corresponding status report behavior.
//
// Example: Verify partial timeline disclosure through the public behavior.
func TestPartialTimelineDisclosure(t *testing.T) {
	t.Parallel()
	input := strings.Replace(valid, "timeline: []", "timeline_state: unavailable\ntimeline:\n  - {job: X, parent_chain: A, start: '2026-11-02T13:54:00Z', end: '2026-11-02T14:24:00Z', fast: unknown, normal: unknown}", 1)
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	if !strings.Contains(render(r), "History is incomplete or unavailable") {
		require.FailNow(t, fmt.Sprint(render(r)))
	}
}

// TestRepairRegressionInputs checks the corresponding status report behavior.
//
// Example: Verify repair regression inputs through the public behavior.
func TestRepairRegressionInputs(t *testing.T) {
	t.Parallel()
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    required_environment: implementation
    implementation_status: IN PROGRESS
    test_status: NEW
    prod_status: NEW
    completed_at: '2026-11-02T15:00:00Z'
    forecast: {outcome: Work, target: '2026-11-02T16:00:00Z', assessment: complete}
`
	if _, err := parse([]byte(strings.Replace(valid, "lanes: []", lane, 1))); err == nil {
		assert.Fail(t, fmt.Sprint("accepted active lane with completed_at and active forecast"))
	}
	if _, err := parse([]byte(valid + "root_forecasts: [{}]\n")); err == nil {
		assert.Fail(t, fmt.Sprint("accepted empty root forecast"))
	}
	bad := strings.Replace(valid, "timeline: []", "timeline:\n  - {job: '1', parent_chain: '1', start: '2026-11-02T13:00:00Z', end: '2026-11-02T14:00:00Z', fast: unknown, normal: unknown}", 1)
	if _, err := parse([]byte(bad)); err == nil {
		assert.Fail(t, fmt.Sprint("accepted one-hour interval"))
	}
	utc := strings.Replace(valid, "2026-11-02T15:24:00Z", "2026-11-02T15:24:00+00:00", 1)
	if _, err := parse([]byte(utc)); err != nil {
		assert.Fail(t, fmt.Sprintf("rejected zero-offset UTC: %v", err))
	}
}

// TestCompletionEstimates checks the corresponding status report behavior.
//
// Example: Verify completion estimates through the public behavior.
func TestCompletionEstimates(t *testing.T) {
	t.Parallel()
	input := validCore + `priority_estimates:
  - {priority: unknown, min_minutes: 10, max_minutes: 30, as_of: "2026-11-02T15:24:00Z", basis: educated guess, assumptions: review available, dependencies: none, critical_path: unknown}
  - priority: high
    min_minutes: 20
    max_minutes: 40
    as_of: '2026-11-02T15:24:00Z'
    basis: educated guess
    assumptions: review available
    dependencies: review
    critical_path: parallel work, latest endpoint
completion_estimate:
  min_minutes: 20
  max_minutes: 50
  as_of: '2026-11-02T15:24:00Z'
  basis: educated guess
  assumptions: reviewers available
  dependencies: review
  critical_path: latest parallel endpoint
`
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	out := render(r)
	for _, want := range []string{"Priority high completion estimate", "Priority unknown completion estimate", "Overall completion estimate", "latest parallel endpoint"} {
		if !strings.Contains(out, want) {
			assert.Fail(t, fmt.Sprintf("missing %q", want))
		}
	}
}

// TestTimelineUTCAndLifecycle checks the corresponding status report behavior.
//
// Example: Verify timeline utcand lifecycle through the public behavior.
func TestTimelineUTCAndLifecycle(t *testing.T) {
	t.Parallel()
	oldTZ := time.Local
	time.Local = time.FixedZone("local", -5*3600)
	base := strings.Replace(valid, "2026-11-02T15:24:00Z", "2026-11-02T00:15:00+00:00", 1)
	row := "timeline:\n  - {job: '1', parent_chain: '1', start: '2026-11-01T23:15:00Z', end: '2026-11-01T23:45:00+00:00', fast: unknown, normal: unknown}"
	if _, err := parse([]byte(strings.Replace(base, "timeline: []", row, 1))); err != nil {
		assert.Fail(t, fmt.Sprintf("valid midnight UTC: %v", err))
	}
	cross := strings.Replace(row, "2026-11-01T23:15:00Z", "2026-11-01T23:45:00Z", 1)
	cross = strings.Replace(cross, "2026-11-01T23:45:00+00:00", "2026-11-02T00:15:00+00:00", 1)
	_, crossErr := parse([]byte(strings.Replace(base, "timeline: []", cross, 1)))
	require.NoError(t, crossErr)
	for _, replacement := range []string{"2026-11-02T00:01:00Z", "2026-11-01T19:00:00Z", "2026-11-02T00:00:00+01:00"} {
		bad := strings.Replace(row, "2026-11-01T23:45:00+00:00", replacement, 1)
		if _, err := parse([]byte(strings.Replace(base, "timeline: []", bad, 1))); err == nil {
			assert.Fail(t, fmt.Sprintf("accepted invalid end %s", replacement))
		}
	}
	closed := strings.Replace(valid, "status: IN PROGRESS\n  - id:", "status: CLOSED\n    evidence: accepted\n  - id:", 1)
	lane := "lanes:\n  - {task_id: '1', outcome: Work, required_environment: implementation, implementation_status: CLOSED, test_status: IN PROGRESS, prod_status: NEW, forecasted: true}"
	if _, err := parse([]byte(strings.Replace(closed, "lanes: []", lane, 1))); err == nil {
		assert.Fail(t, fmt.Sprint("missing completed_at accepted"))
	}
	lane = strings.Replace(lane, "forecasted: true}", "forecasted: true, completed_at: '2026-11-02T15:00:00+00:00'}", 1)
	if _, err := parse([]byte(strings.Replace(closed, "lanes: []", lane, 1))); err != nil {
		assert.Fail(t, fmt.Sprintf("valid closed lane: %v", err))
	}
	time.Local = oldTZ
}

// TestEstimateCoverageRejectsMissing checks every priority and overall are estimated.
//
// Example: An unknown-priority task still needs a labeled educated estimate.
func TestEstimateCoverageRejectsMissing(t *testing.T) {
	t.Parallel()
	if _, err := parse([]byte(validCore)); err == nil {
		assert.Fail(t, fmt.Sprint("accepted missing estimates"))
	}
	input := strings.Replace(valid, "  - {priority: unknown, min_minutes: 20, max_minutes: 40, as_of: \"2026-11-02T15:24:00Z\", basis: educated guess, assumptions: review available, dependencies: review, critical_path: latest parallel endpoint}\n", "", 1)
	if _, err := parse([]byte(input)); err == nil {
		assert.Fail(t, fmt.Sprint("accepted missing unknown-priority estimate"))
	}
}

// TestClosedLifecycleRejectsContradictoryForecast verifies separate readiness and completion.
//
// Example: A lane closed in its required environment cannot retain an active target.
func TestClosedLifecycleRejectsContradictoryForecast(t *testing.T) {
	t.Parallel()
	closed := strings.Replace(valid, "status: IN PROGRESS\n  - id:", "status: CLOSED\n    evidence: accepted\n  - id:", 1)
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    required_environment: implementation
    implementation_status: CLOSED
    test_status: IN PROGRESS
    prod_status: NEW
    forecasted: true
    completed_at: '2026-11-02T15:00:00Z'
    forecast: {outcome: Work, target: '2026-11-02T16:00:00Z', assessment: complete}
`
	if _, err := parse([]byte(strings.Replace(closed, "lanes: []", lane, 1))); err == nil {
		assert.Fail(t, fmt.Sprint("accepted closed lane with active forecast"))
	}
}

// TestLaneRequiredEnvironmentSeparatesPackageProgress checks environment-based closure.
//
// Example: A closed source package still has an active test lane forecast.
func TestLaneRequiredEnvironmentSeparatesPackageProgress(t *testing.T) {
	t.Parallel()
	closed := strings.Replace(valid, "status: IN PROGRESS\n  - id:", "status: CLOSED\n    evidence: accepted\n  - id:", 1)
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    required_environment: test
    implementation_status: CLOSED
    test_status: IN PROGRESS
    prod_status: NEW
    forecasted: true
    forecast: {outcome: Work, target: '2026-11-02T16:00:00Z', assessment: complete}
`
	input := strings.Replace(closed, "lanes: []", lane, 1)
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprintf("active test lane rejected: %v", err))
	}
	if !strings.Contains(render(r), "Forecast deadline: Work will be finished") {
		require.FailNow(t, fmt.Sprint(render(r)))
	}
	if !strings.Contains(render(r), "| Required environment |") || !strings.Contains(render(r), "| test |") {
		assert.Fail(t, fmt.Sprint("required environment missing from lane table"))
	}
	bad := strings.Replace(lane, "forecasted: true", "completed_at: '2026-11-02T15:00:00Z'", 1)
	bad = strings.Replace(bad, "    forecast: {outcome: Work, target: '2026-11-02T16:00:00Z', assessment: complete}\n", "", 1)
	if _, err := parse([]byte(strings.Replace(closed, "lanes: []", bad, 1))); err == nil {
		assert.Fail(t, fmt.Sprint("accepted completion before required test readiness"))
	}
	source := strings.Replace(bad, "required_environment: test", "required_environment: implementation", 1)
	if _, err := parse([]byte(strings.Replace(valid, "lanes: []", source, 1))); err != nil {
		assert.Fail(t, fmt.Sprintf("source-only lane rejected despite implementation CLOSED: %v", err))
	}
	prod := strings.Replace(source, "required_environment: implementation", "required_environment: prod", 1)
	if _, err := parse([]byte(strings.Replace(valid, "lanes: []", prod, 1))); err == nil {
		assert.Fail(t, fmt.Sprint("accepted completion with prod NEW"))
	}
	prod = strings.Replace(prod, "prod_status: NEW", "prod_status: CLOSED", 1)
	if _, err := parse([]byte(strings.Replace(valid, "lanes: []", prod, 1))); err != nil {
		assert.Fail(t, fmt.Sprintf("prod CLOSED rejected: %v", err))
	}
}

// TestScenarioFieldsAreExplicit checks complete scenario projections.
//
// Example: Unsupported fields are supplied as unknown rather than omitted.
func TestScenarioFieldsAreExplicit(t *testing.T) {
	t.Parallel()
	prefix := "root_forecasts:\n  - outcome: Work\n    target: '2026-11-02T16:00:00Z'\n    assessment: complete\n    scenarios:\n"
	for _, row := range []string{"      - {}\n", "      - {case: base}\n"} {
		if _, err := parse([]byte(valid + prefix + row)); err == nil {
			assert.Fail(t, fmt.Sprint("accepted incomplete scenario"))
		}
	}
	complete := "      - {case: base, trigger: unknown, affected_work: unknown, duration_basis: unknown, evidence_assumptions: unknown, endpoint: unknown, evidence_gap_next_action: unknown}\n"
	if _, err := parse([]byte(valid + prefix + complete)); err != nil {
		assert.Fail(t, fmt.Sprintf("explicit unknown scenario rejected: %v", err))
	}
	r, err := parse([]byte(valid + prefix + complete))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	if !strings.Contains(render(r), "| Work — base | unknown | unknown |") {
		assert.Fail(t, fmt.Sprint("explicit unknown scenario not rendered"))
	}
}

// TestStrictStatusRepair reproduces invalid enum kinds and closed-lane timestamps.
//
// Example: A mapping cannot masquerade as an unknown task status.
func TestStrictStatusRepair(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"{}", "[]", "!custom unknown"} {
		input := strings.Replace(valid, "status: unknown", "status: "+bad, 1)
		if _, err := parse([]byte(input)); err == nil {
			assert.Fail(t, fmt.Sprintf("accepted status %s", bad))
		}
	}
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    required_environment: test
    implementation_status: CLOSED
    test_status: CLOSED
    prod_status: NEW
`
	if _, err := parse([]byte(strings.Replace(valid, "lanes: []", lane, 1))); err == nil {
		assert.Fail(t, fmt.Sprint("closed lane accepted without completed_at"))
	}
	if _, err := parse([]byte(strings.Replace(valid, "lanes: []", strings.Replace(lane, "    outcome:", "    id: duplicate\n    outcome:", 1), 1))); err == nil {
		assert.Fail(t, fmt.Sprint("accepted lane id alias"))
	}
}

// TestCompletionEstimateAsOf requires duration endpoints to be derived.
//
// Example: A 30-minute range beginning at midnight ends at 00:30 UTC.
func TestCompletionEstimateAsOf(t *testing.T) {
	t.Parallel()
	input := valid
	r, err := parse([]byte(input))
	if err != nil {
		require.FailNow(t, fmt.Sprint(err))
	}
	out := render(r)
	if !strings.Contains(out, "2026-11-02T15:44:00Z–2026-11-02T16:04:00Z") {
		require.FailNow(t, fmt.Sprint(out))
	}
	if _, err := parse([]byte(strings.Replace(valid, "as_of:", "earliest:", 1))); err == nil {
		assert.Fail(t, fmt.Sprint("accepted independently mutable endpoints"))
	}
}

// TestEnumNodeKinds enforces the documented wire types for every enum family.
//
// Example: An empty mapping cannot select a lane environment or effort kind.
func TestEnumNodeKinds(t *testing.T) {
	t.Parallel()
	cases := []struct{ old, new string }{
		{"status: unknown", "status: {}"}, {"status: unknown", "status: []"}, {"status: unknown", "status: !custom unknown"},
		{"kind: elapsed", "kind: {}"}, {"kind: elapsed", "kind: !custom elapsed"},
		{"timeline: []", "timeline_state: []\ntimeline: []"},
	}
	for _, tc := range cases {
		_, err := parse([]byte(strings.Replace(valid, tc.old, tc.new, 1)))
		require.Error(t, err, tc.new)
	}
	_, err := parse([]byte(strings.Replace(valid, "status: unknown", "status: null", 1)))
	require.NoError(t, err)
}

// TestEstimateDurationBoundaries checks UTC arithmetic and overflow rejection.
//
// Example: Half a minute from midnight produces a 30-second endpoint.
func TestEstimateDurationBoundaries(t *testing.T) {
	t.Parallel()
	e := completionEstimate{AsOf: "2026-11-01T23:59:45+00:00", MinMinutes: floatPointer(0.5), MaxMinutes: floatPointer(1.5)}
	first, last, err := completionEndpoints(e)
	require.NoError(t, err)
	require.Equal(t, "2026-11-02T00:00:15Z", first.Format(time.RFC3339Nano))
	require.Equal(t, "2026-11-02T00:01:15Z", last.Format(time.RFC3339Nano))
	e.AsOf = "2026-11-01T23:59:45-05:00"
	_, _, err = completionEndpoints(e)
	require.Error(t, err)
	e.AsOf = "9999-12-31T23:59:45Z"
	_, _, err = completionEndpoints(e)
	require.Error(t, err)
	e.AsOf = "2026-11-01T23:59:45Z"
	e.MaxMinutes = floatPointer(1e100)
	_, _, err = completionEndpoints(e)
	require.Error(t, err)
}

// floatPointer supplies an optional numeric field for focused test cases.
//
// Example: Build a fractional minute range.
func floatPointer(value float64) *fractionalMinutes {
	result := fractionalMinutes(value)
	return &result
}

// TestHierarchicalTaskIDsRejectMismatchedParents checks identity lineage.
//
// Example: Task 1.2 cannot claim a missing or unrelated parent.
func TestHierarchicalTaskIDsRejectMismatchedParents(t *testing.T) {
	t.Parallel()
	for _, replacement := range []string{"parent_id: ''", "parent_id: '1.2'", "parent_id: 'other'"} {
		input := strings.Replace(valid, `parent_id: "1"`, replacement, 1)
		_, err := parse([]byte(input))
		require.Error(t, err, replacement)
	}
}

// TestCLICommands exercises the supported command paths and useful errors.
//
// Example: Validation and rendering work on stdin and fail on malformed input.
func TestCLICommands(t *testing.T) {
	t.Parallel()
	var out, errOut strings.Builder
	require.Equal(t, 1, testRun([]string{"validate"}, strings.NewReader(valid), &out, &errOut))
	require.Contains(t, out.String(), "INCOMPLETE")
	out.Reset()
	require.Equal(t, 0, testRun([]string{"render", "-"}, strings.NewReader(valid), &out, &errOut))
	require.Contains(t, out.String(), "Full task tree")
	require.Equal(t, 2, testRun([]string{"invalid"}, strings.NewReader(valid), &out, &errOut))
	require.Equal(t, 1, testRun([]string{"validate"}, strings.NewReader("[invalid"), &out, &errOut))
	require.Equal(t, 1, testRun([]string{"validate", "/nonexistent/status.yaml"}, strings.NewReader(""), &out, &errOut))
}

// TestStatusAndEstimateVariants covers supported report states and time units.
//
// Example: Paused and blocked packages remain incomplete in the progress bar.
func TestStatusAndEstimateVariants(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"NEW", "IN PROGRESS", "PAUSED", "BLOCKED", "CLOSED", "unknown"} {
		input := strings.Replace(valid, "status: unknown", "status: "+state, 1)
		if state == "CLOSED" {
			input = strings.Replace(input, "status: CLOSED\ntop_priorities:", "status: CLOSED\n    evidence: accepted\ntop_priorities:", 1)
		}
		r, err := parse([]byte(input))
		require.NoError(t, err, state)
		require.Contains(t, render(r), "status: "+state)
	}
	work := strings.Replace(valid, "kind: elapsed", "kind: work", 1)
	r, err := parse([]byte(work))
	require.NoError(t, err)
	require.Contains(t, render(r), "minutes work")
	for _, state := range []string{"unavailable", "present"} {
		input := strings.Replace(valid, "timeline: []", "timeline_state: "+state+"\ntimeline:\n  - {job: '1', parent_chain: '1', start: '2026-11-02T13:54:00Z', end: '2026-11-02T14:24:00Z', fast: unknown, normal: unknown}", 1)
		_, err = parse([]byte(input))
		require.NoError(t, err, state)
	}
}

// TestEstimateValidationRejectsInvalidFields checks actual estimate boundaries.
//
// Example: Missing basis or non-finite durations cannot validate.
func TestEstimateValidationRejectsInvalidFields(t *testing.T) {
	t.Parallel()
	cases := []struct{ old, new string }{
		{"min_minutes: 20", "min_minutes: -1"}, {"max_minutes: 40", "max_minutes: .nan"},
		{"basis: educated guess", "basis: ''"}, {"assumptions: review available", "assumptions: ''"},
		{"dependencies: review", "dependencies: ''"}, {"critical_path: latest parallel endpoint", "critical_path: ''"},
		{"as_of: \"2026-11-02T15:24:00Z\"", "as_of: 'invalid'"},
	}
	for _, tc := range cases {
		_, err := parse([]byte(strings.Replace(valid, tc.old, tc.new, 1)))
		require.Error(t, err, tc.new)
	}
}

// TestRichDetailsAndForecastRendering preserves supplied content.
//
// Example: Recalibration and nested evidence survive the rendered report.
func TestRichDetailsAndForecastRendering(t *testing.T) {
	t.Parallel()
	report := valid + `details:
  evidence:
    - item: first
      notes: [one, two]
root_forecasts:
  - outcome: Root
    target: unknown
    assessment: provisional
    prior: '2026-11-02T15:00:00Z'
    reason: review
    evidence: changed
    dependencies: owner
`
	r, err := parse([]byte(report))
	require.NoError(t, err)
	out := render(r)
	for _, v := range []string{"Root completion forecast: Root — unknown", "Forecast recalibration:", "first", "one", "two"} {
		require.Contains(t, out, v)
	}
}

// TestInvalidEnumsRejected prevents bypassing scalar validation.
//
// Example: A custom enum tag cannot enter a task record.
func TestInvalidEnumsRejected(t *testing.T) {
	t.Parallel()
	var err error
	for _, replacement := range []string{"kind: null", "kind: bogus", "status: !x CLOSED"} {
		old := "kind: elapsed"
		if strings.HasPrefix(replacement, "status") {
			old = "status: unknown"
		}
		_, err = parse([]byte(strings.Replace(valid, old, replacement, 1)))
		require.Error(t, err)
	}
}

// TestLaneStatusVariantsPreserveReadiness exercises independent lane fields.
//
// Example: A production-gated lane can pause follow-up while implementation is complete.
func TestLaneStatusVariantsPreserveReadiness(t *testing.T) {
	t.Parallel()
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    required_environment: prod
    implementation_status: CLOSED
    test_status: PAUSED
    prod_status: BLOCKED
    root_task_status: FOLLOWUP_PAUSED
    forecast: {outcome: Work, target: unknown, assessment: provisional}
`
	r, err := parse([]byte(strings.Replace(valid, "lanes: []", lane, 1)))
	require.NoError(t, err)
	out := render(r)
	for _, v := range []string{"| prod |", "| PAUSED |", "| BLOCKED |", "FOLLOWUP_PAUSED", "Forecast deadline: Work — unknown"} {
		require.Contains(t, out, v)
	}
	for _, field := range []string{"required_environment: []", "implementation_status: {}", "root_task_status: !bad FOLLOWUP"} {
		old := "required_environment: prod"
		if strings.HasPrefix(field, "implementation") {
			old = "implementation_status: CLOSED"
		}
		if strings.HasPrefix(field, "root_task") {
			old = "root_task_status: FOLLOWUP_PAUSED"
		}
		_, err = parse([]byte(strings.Replace(strings.Replace(valid, "lanes: []", lane, 1), old, field, 1)))
		require.Error(t, err, field)
	}
}

// TestCLIFileInput verifies the file path used by the installed launcher.
//
// Example: A YAML file with spaces in its name validates successfully.
func TestCLIFileInput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "report file.yaml")
	require.NoError(t, os.WriteFile(path, []byte(valid), 0o600))
	var out, errOut strings.Builder
	require.Equal(t, 1, testRun([]string{"validate", path}, strings.NewReader(""), &out, &errOut))
	require.Contains(t, out.String(), "INCOMPLETE")
}

// TestUTCOffsetAtDaylightSavingBoundary rejects non-UTC offsets.
//
// Example: An RFC3339 daylight-saving offset is not a UTC timestamp.
func TestUTCOffsetAtDaylightSavingBoundary(t *testing.T) {
	t.Parallel()
	_, err := parseUTC("2026-03-08T03:30:00-04:00")
	require.Error(t, err)
	utc, err := parseUTC("2026-03-08T07:30:00+00:00")
	require.NoError(t, err)
	require.Equal(t, "2026-03-08T07:30:00Z", utc.Format(time.RFC3339))
}

// TestInvalidLaneEnumLabels rejects unsupported status and environment labels.
//
// Example: A lane cannot use an unrecognized production acceptance boundary.
func TestInvalidLaneEnumLabels(t *testing.T) {
	t.Parallel()
	lane := "lanes:\n  - {task_id: '1', outcome: Work, required_environment: prod, implementation_status: NEW, test_status: NEW, prod_status: NEW, root_task_status: FOLLOWUP}"
	for _, change := range []struct{ old, new string }{{"required_environment: prod", "required_environment: staging"}, {"root_task_status: FOLLOWUP", "root_task_status: INVALID"}, {"implementation_status: NEW", "implementation_status: INVALID"}} {
		input := strings.Replace(strings.Replace(valid, "lanes: []", lane, 1), change.old, change.new, 1)
		_, err := parse([]byte(input))
		require.Error(t, err)
	}
}

// TestInvalidCompletionRenderFailsClosed covers malformed in-memory estimates.
//
// Example: Invalid data cannot produce a fabricated endpoint.
func TestInvalidCompletionRenderFailsClosed(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	writeCompletionEstimate(&out, "Overall", completionEstimate{})
	require.Contains(t, out.String(), "unknown (invalid estimate:")
}

// TestResolvedEnumValuesTreatDirectAndMergedYAMLConsistently checks enum ownership.
//
// Example: A valid status inherited through a YAML merge behaves like a direct value.
func TestResolvedEnumValuesTreatDirectAndMergedYAMLConsistently(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"NEW", "null"} {
		direct := strings.Replace(valid, "status: IN PROGRESS", "status: "+value, 1)
		_, err := parse([]byte(direct))
		require.NoError(t, err, value)
		merged := strings.Replace(valid, "tasks:\n", "details: {defaults: &defaults {status: "+value+"}}\ntasks:\n", 1)
		merged = strings.Replace(merged, "status: IN PROGRESS", "<<: *defaults", 1)
		_, err = parse([]byte(merged))
		require.NoError(t, err, value)
	}
	for _, value := range []string{"{}", "[]", "!bad NEW"} {
		direct := strings.Replace(valid, "status: IN PROGRESS", "status: "+value, 1)
		_, err := parse([]byte(direct))
		require.Error(t, err, value)
		merged := strings.Replace(valid, "tasks:\n", "details: {defaults: &defaults {status: "+value+"}}\ntasks:\n", 1)
		merged = strings.Replace(merged, "status: IN PROGRESS", "<<: *defaults", 1)
		_, err = parse([]byte(merged))
		require.Error(t, err, value)
	}
	optional := valid + "timeline_state: null\n"
	_, err := parse([]byte(optional))
	require.NoError(t, err)
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    required_environment: test
    implementation_status: NEW
    test_status: IN PROGRESS
    prod_status: NEW
    root_task_status: null
`
	_, err = parse([]byte(strings.Replace(valid, "lanes: []", lane, 1)))
	require.NoError(t, err)
	_, err = parse([]byte(strings.Replace(strings.Replace(valid, "lanes: []", lane, 1), "test_status: IN PROGRESS", "test_status: null", 1)))
	require.Error(t, err)
}

// TestMergedEnumFieldsKeepTheirSemantics checks merged lane and effort values.
//
// Example: Missing test readiness still fails when inherited as null.
func TestMergedEnumFieldsKeepTheirSemantics(t *testing.T) {
	t.Parallel()
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    required_environment: test
    implementation_status: NEW
    <<: *lane_defaults
    prod_status: NEW
`
	for _, tc := range []struct {
		value string
		valid bool
	}{{"IN PROGRESS", true}, {"null", false}, {"{}", false}, {"!custom NEW", false}} {
		input := strings.Replace(valid, "tasks:\n", "details: {defaults: &lane_defaults {test_status: "+tc.value+"}}\ntasks:\n", 1)
		input = strings.Replace(input, "lanes: []", lane, 1)
		_, err := parse([]byte(input))
		if tc.valid {
			require.NoError(t, err, tc.value)
		} else {
			require.Error(t, err, tc.value)
		}
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{{"elapsed", true}, {"null", false}, {"[]", false}, {"!custom elapsed", false}} {
		input := strings.Replace(valid, "tasks:\n", "details: {defaults: &effort_defaults {kind: "+tc.value+"}}\ntasks:\n", 1)
		input = strings.Replace(input, "kind: elapsed, basis:", "<<: *effort_defaults, basis:", 1)
		_, err := parse([]byte(input))
		if tc.valid {
			require.NoError(t, err, tc.value)
		} else {
			require.Error(t, err, tc.value)
		}
	}
}

// TestOptionalMergedStatusesAndRequiredNull verifies nullable and required fields.
//
// Example: Null follow-up state is allowed, but null lane environment is not.
func TestOptionalMergedStatusesAndRequiredNull(t *testing.T) {
	t.Parallel()
	lane := `lanes:
  - task_id: '1'
    outcome: Work
    implementation_status: NEW
    test_status: IN PROGRESS
    prod_status: NEW
    <<: *defaults
`
	for _, tc := range []struct {
		fields string
		valid  bool
	}{
		{"required_environment: test, root_task_status: null", true},
		{"required_environment: null, root_task_status: null", false},
		{"required_environment: test, root_task_status: {}", false},
		{"required_environment: test, root_task_status: !bad FOLLOWUP", false},
	} {
		input := strings.Replace(valid, "tasks:\n", "details: {defaults: &defaults {"+tc.fields+"}}\ntasks:\n", 1)
		input = strings.Replace(input, "lanes: []", lane, 1)
		_, err := parse([]byte(input))
		if tc.valid {
			require.NoError(t, err, tc.fields)
		} else {
			require.Error(t, err, tc.fields)
		}
	}
}

// testWriter models failed and short writes to the CLI output stream.
//
// Example: A broken pipe must cause a nonzero exit status.
type testWriter struct {
	short bool
	calls int
}

// Write records one attempted output and simulates a failure or short write.
//
// Example: A short write returns fewer bytes without a reported error.
func (w *testWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.short {
		return len(p) - 1, nil
	}
	return 0, io.ErrClosedPipe
}

// TestCLIReportsWriteFailures rejects broken or short output writes.
//
// Example: Both validate and render report an output failure.
func TestCLIReportsWriteFailures(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"validate", "render"} {
		for _, short := range []bool{false, true} {
			writer := &testWriter{short: short}
			var errOut strings.Builder
			code := testRun([]string{cmd}, strings.NewReader(valid), writer, &errOut)
			require.NotZero(t, code)
			require.Equal(t, 1, writer.calls)
			if short {
				require.Contains(t, errOut.String(), io.ErrShortWrite.Error())
			} else {
				require.Contains(t, errOut.String(), io.ErrClosedPipe.Error())
			}
		}
	}
}

// TestForecastEvidenceAndAsOfRemainVisible checks supplied forecast facts.
//
// Example: Evidence without a baseline still appears once.
func TestForecastEvidenceAndAsOfRemainVisible(t *testing.T) {
	t.Parallel()
	for _, metadata := range []string{"", "    prior: '2026-11-02T14:00:00Z'\n", "    baseline: '2026-11-02T14:00:00Z'\n"} {
		root := "root_forecasts:\n  - outcome: Work\n    target: '2026-11-02T16:00:00Z'\n    assessment: provisional\n    reason: unique-reason\n    evidence: unique-evidence\n" + metadata
		r, err := parse([]byte(valid + root))
		require.NoError(t, err)
		out := render(r)
		require.Equal(t, 1, strings.Count(out, "unique-reason"))
		require.Equal(t, 1, strings.Count(out, "unique-evidence"))
		require.Contains(t, out, "as of 2026-11-02T15:24:00Z")
	}
}

// TestTimelineGridAndDuplicateJobs checks report-relative bins.
//
// Example: Two jobs can report work in the same half-hour interval.
func TestTimelineGridAndDuplicateJobs(t *testing.T) {
	t.Parallel()
	row := "  - {job: '%s', parent_chain: '1', start: '2026-11-02T14:24:00Z', end: '2026-11-02T14:54:00Z', fast: unknown, normal: unknown}\n"
	rows := fmt.Sprintf(row, "A") + fmt.Sprintf(row, "B")
	_, err := parse([]byte(strings.Replace(valid, "timeline: []", "timeline:\n"+rows, 1)))
	require.NoError(t, err)
	duplicate := fmt.Sprintf(row, "A") + fmt.Sprintf(row, "A")
	_, err = parse([]byte(strings.Replace(valid, "timeline: []", "timeline:\n"+duplicate, 1)))
	require.Error(t, err)
	offgrid := strings.ReplaceAll(fmt.Sprintf(row, "A"), ":24:00Z", ":25:00Z")
	offgrid = strings.ReplaceAll(offgrid, ":54:00Z", ":55:00Z")
	_, err = parse([]byte(strings.Replace(valid, "timeline: []", "timeline:\n"+offgrid, 1)))
	require.Error(t, err)
}

// TestTimelineRowsRenderChronologically checks ordering of supplied entries.
//
// Example: A later reported event cannot precede an earlier event in the table.
func TestTimelineRowsRenderChronologically(t *testing.T) {
	t.Parallel()
	rows := `timeline:
  - {job: A, parent_chain: '1', start: '2026-11-02T14:54:00Z', end: '2026-11-02T15:24:00Z', fast: later-marker, normal: unknown}
  - {job: A, parent_chain: '1', start: '2026-11-02T13:54:00Z', end: '2026-11-02T14:24:00Z', fast: earlier-marker, normal: unknown}
`
	r, err := parse([]byte(strings.Replace(valid, "timeline: []", rows, 1)))
	require.NoError(t, err)
	out := render(r)
	require.Less(t, strings.Index(out, "earlier-marker"), strings.Index(out, "later-marker"))
	require.Contains(t, out, "earlier-marker")
	require.Contains(t, out, "later-marker")
}

// TestTimelineWindowBoundaryAndEquivalentUTCOffsets validates bin endpoints.
//
// Example: The first and last half-hours fit the report's four-hour window.
func TestTimelineWindowBoundaryAndEquivalentUTCOffsets(t *testing.T) {
	t.Parallel()
	rows := `timeline:
  - {job: A, parent_chain: '1', start: '2026-11-02T11:24:00Z', end: '2026-11-02T11:54:00Z', fast: first, normal: unknown}
  - {job: A, parent_chain: '1', start: '2026-11-02T14:54:00+00:00', end: '2026-11-02T15:24:00+00:00', fast: last, normal: unknown}
`
	input := strings.Replace(valid, "timeline: []", rows, 1)
	r, err := parse([]byte(input))
	require.NoError(t, err)
	out := render(r)
	require.Less(t, strings.Index(out, "first"), strings.Index(out, "last"))
	duplicate := rows + "  - {job: A, parent_chain: '1', start: '2026-11-02T14:54:00Z', end: '2026-11-02T15:24:00Z', fast: duplicate, normal: unknown}\n"
	_, err = parse([]byte(strings.Replace(valid, "timeline: []", duplicate, 1)))
	require.Error(t, err)
}

// TestLegacyPriorityOrderCompatibility checks that legacy ordering remains accepted.
//
// Example: A legacy high label does not supply missing score inputs.
func TestLegacyPriorityOrderCompatibility(t *testing.T) {
	t.Parallel()
	r, err := parse([]byte(valid + "priority_order: [high]\n"))
	require.NoError(t, err)
	require.Contains(t, render(r), "unscored:")
}

// TestForecastBaselineTruth verifies baseline comparisons and explicit uncertainty.
//
// Example: Differing UTC offsets for the same instant remain unchanged.
func TestForecastBaselineTruth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ baseline, target, want string }{
		{"2026-11-02T16:00:00+00:00", "2026-11-02T16:00:00Z", "Forecast recalibration: unchanged — baseline"},
		{"2026-11-02T15:00:00Z", "2026-11-02T16:00:00Z", "Forecast baseline: 2026-11-02T15:00:00Z; current target: 2026-11-02T16:00:00Z; recalibration history unknown"},
		{"unknown", "2026-11-02T16:00:00Z", "Forecast baseline: unknown; current target: 2026-11-02T16:00:00Z; recalibration history unknown"},
	} {
		doc := valid + "root_forecasts:\n  - {outcome: Work, target: '" + tc.target + "', baseline: '" + tc.baseline + "', assessment: complete}\n"
		r, err := parse([]byte(doc))
		require.NoError(t, err)
		require.Contains(t, render(r), tc.want)
		if strings.Contains(tc.want, "unchanged") {
			require.Contains(t, render(r), "Intermediate recalibration history unknown")
		}
	}
	doc := valid + "root_forecasts:\n  - {outcome: Work, target: '2026-11-02T16:00:00Z', baseline: nonsense, assessment: complete}\n"
	_, err := parse([]byte(doc))
	require.Error(t, err)
}

// TestLegacyPriorityOrderValidation rejects invalid legacy ordering labels.
//
// Example: Duplicate and unknown labels are rejected.
func TestLegacyPriorityOrderValidation(t *testing.T) {
	t.Parallel()
	for _, order := range []string{"[high, high]", "[high, unknown]", "[high, '']", "[high, UNKNOWN]"} {
		_, err := parse([]byte(valid + "priority_order: " + order + "\n"))
		require.Error(t, err, order)
	}
}

// TestForecastPriorAndBaselineValidation checks metadata without invented history.
//
// Example: A missing prior never proves intermediate history was unchanged.
func TestForecastPriorAndBaselineValidation(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"prior", "baseline"} {
		doc := valid + "root_forecasts:\n  - {outcome: Work, target: '2026-11-02T16:00:00Z', " + field + ": not-a-time, assessment: complete}\n"
		_, err := parse([]byte(doc))
		require.Error(t, err, field)
	}
	doc := valid + "root_forecasts:\n  - {outcome: Work, target: '2026-11-02T16:00:00Z', baseline: '2026-11-02T15:00:00Z', reason: pending, evidence: record, assessment: provisional}\n"
	r, err := parse([]byte(doc))
	require.NoError(t, err)
	out := render(r)
	require.Contains(t, out, "Forecast reason: pending")
	require.NotContains(t, out, "why moved: pending")
	require.NotContains(t, out, "unchanged — baseline 2026-11-02T15:00:00Z")
}

// TestPriorForecastComparisonDoesNotInventMovement checks prior/current semantics.
//
// Example: Equivalent UTC representations do not imply a changed deadline.
func TestPriorForecastComparisonDoesNotInventMovement(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		prior, target, want string
		moved               bool
	}{
		{"2026-11-02T16:00:00Z", "2026-11-02T16:00:00Z", "unchanged", false},
		{"2026-11-02T16:00:00+00:00", "2026-11-02T16:00:00Z", "unchanged", false},
		{"2026-11-02T15:00:00Z", "2026-11-02T16:00:00Z", "why moved: reason-marker", true},
		{"unknown", "2026-11-02T16:00:00Z", "movement unknown", false},
		{"2026-11-02T15:00:00Z", "unknown", "movement unknown", false},
	} {
		doc := valid + "root_forecasts:\n  - {outcome: Work, target: '" + tc.target + "', prior: '" + tc.prior + "', reason: reason-marker, evidence: evidence-marker, assessment: provisional}\n"
		r, err := parse([]byte(doc))
		require.NoError(t, err)
		out := render(r)
		require.Contains(t, out, tc.want)
		require.Equal(t, 1, strings.Count(out, "reason-marker"))
		require.Equal(t, 1, strings.Count(out, "evidence-marker"))
		if !tc.moved {
			require.NotContains(t, out, "why moved:")
		}
	}
}
