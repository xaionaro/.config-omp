package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNumericPriorityEstimateSelectorsRemainLexical checks public refresh.
//
// Example: Priorities 1.10 and 1.1 select distinct estimates.
func TestNumericPriorityEstimateSelectorsRemainLexical(t *testing.T) {
	report := strings.Replace(linkedReport, "title: First, priority: high", "title: First, priority: 1.10", 1)
	report = strings.Replace(report, "title: Second, priority: high", "title: Second, priority: 1.1", 1)
	report = strings.Replace(report, "priority: high, min_minutes: 20", "priority: 1.10, min_minutes: 20", 1)
	report = strings.Replace(report, "completion_estimate:", "  - {priority: 1.1, min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest}\ncompletion_estimate:", 1)
	_, err := parse([]byte(report))
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	event := `{"event":"forecast_change","details":{"status_update":{"updates":[{"target":"priority_estimate","id":"1.10","values":{"basis":"updated basis"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "forecast_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("1.10"), parsed.PriorityEstimates[0].Priority)
	require.Equal(t, "updated basis", parsed.PriorityEstimates[0].Basis)
	require.Equal(t, priorityLabel("1.1"), parsed.PriorityEstimates[1].Priority)
	require.Equal(t, "guess", parsed.PriorityEstimates[1].Basis)
	require.Contains(t, string(result), "priority: 1.10")
	require.Contains(t, string(result), "priority: 1.1")
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "forecast_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
	retry, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, result, retry)
	edited := strings.Replace(string(retry), `basis: "updated basis"`, `basis: "owner edit"`, 1)
	require.NotEqual(t, string(retry), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	second := `{"event":"forecast_change","details":{"status_update":{"updates":[{"target":"priority_estimate","id":"1.10","values":{"basis":"later basis"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event+second), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "forecast_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
	conflicted, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(conflicted)
	require.NoError(t, err)
	require.Equal(t, "owner edit", parsed.PriorityEstimates[0].Basis)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestExplicitPriorityValueMayChangeRepresentation checks authorized edits.
//
// Example: An explicitly supplied string label can replace numeric YAML syntax.
func TestExplicitPriorityValueMayChangeRepresentation(t *testing.T) {
	before := []byte("tasks: [{id: one, priority: 1}]\n")
	after := []byte("tasks: [{id: one, priority: '1'}]\n")
	effects := []syncEffect{{Target: syncTargetTask, ID: "one", Field: "priority", Value: json.RawMessage(`"1"`), Operation: syncEffectSet}}
	require.NoError(t, verifyStatusSyncEffects(before, after, effects))
}

// TestVerificationProtectsNumericPriorityLabels checks untouched source syntax.
//
// Example: An estimate update cannot rewrite another numeric label as text.
func TestVerificationProtectsNumericPriorityLabels(t *testing.T) {
	before := []byte("tasks: [{id: one, priority: 1}]\npriority_estimates: [{priority: 1, basis: old}, {priority: 2, basis: keep}]\n")
	effects := []syncEffect{{Target: syncTargetPriorityEstimate, ID: "1", Field: "basis", Value: "new", Operation: syncEffectSet}}
	good := []byte("tasks: [{id: one, priority: 1}]\npriority_estimates: [{priority: 1, basis: new}, {priority: 2, basis: keep}]\n")
	changedEstimate := []byte("tasks: [{id: one, priority: 1}]\npriority_estimates: [{priority: 1, basis: new}, {priority: '2', basis: keep}]\n")
	changedTask := []byte("tasks: [{id: one, priority: '1'}]\npriority_estimates: [{priority: 1, basis: new}, {priority: 2, basis: keep}]\n")
	require.NoError(t, verifyStatusSyncEffects(before, good, effects))
	require.Error(t, verifyStatusSyncEffects(before, changedEstimate, effects))
	require.Error(t, verifyStatusSyncEffects(before, changedTask, effects))
}

// TestVerificationProtectsNumericPriorityOrder checks unrelated scalar styles.
//
// Example: A task update cannot rewrite a numeric priority order entry as text.
func TestVerificationProtectsNumericPriorityOrder(t *testing.T) {
	before := []byte("tasks: [{id: one, owner: old}]\npriority_order: [1.10, 1.1]\n")
	after := []byte("tasks: [{id: one, owner: new}]\npriority_order: ['1.10', 1.1]\n")
	effects := []syncEffect{{Target: syncTargetTask, ID: "one", Field: "owner", Value: "new", Operation: syncEffectSet}}
	require.Error(t, verifyStatusSyncEffects(before, after, effects))
}

// TestNumericPriorityMergeRetiresExactEstimate checks paired replay.
//
// Example: A numeric destination or source label remains lexical through retirement.
func TestNumericPriorityMergeRetiresExactEstimate(t *testing.T) {
	for _, tc := range []struct{ name, source, destination string }{
		{"numeric destination", "high", "1.10"},
		{"numeric source", "1.10", "low"},
	} {
		t.Run(tc.name,
			// Move the last source task while retaining the existing destination estimate.
			//
			// Example: The complete supplied prior authorizes exactly one retirement.
			func(t *testing.T) {
				report := strings.Replace(linkedReport, "title: First, priority: high", "title: First, priority: "+tc.source, 1)
				report = strings.Replace(report, "title: Second, priority: high", "title: Second, priority: "+tc.destination, 1)
				report = strings.Replace(report, "priority: high, min_minutes: 20", "priority: "+tc.source+", min_minutes: 20", 1)
				report = strings.Replace(report, "completion_estimate:", "  - {priority: "+tc.destination+", min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest} # destination owner\ncompletion_estimate:", 1)
				_, err := parse([]byte(report))
				require.NoError(t, err)
				estimate := `{"priority":"` + tc.destination + `","min_minutes":20,"max_minutes":40,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"latest"}`
				prior := strings.Replace(estimate, `"priority":"`+tc.destination+`"`, `"priority":"`+tc.source+`"`, 1)
				event := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":"` + tc.destination + `"}}],"additions":[{"target":"priority_estimate","retire_priority":"` + tc.source + `","expected_prior":` + prior + `,"record":` + estimate + `}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, priorityLabel(tc.destination), parsed.Tasks[0].Priority)
				require.Len(t, parsed.PriorityEstimates, 1)
				require.Equal(t, priorityLabel(tc.destination), parsed.PriorityEstimates[0].Priority)
				require.Contains(t, string(result), "# destination owner")
				require.Empty(t, parsed.StatusSync.Groups)
				require.Empty(t, parsed.StatusSync.PendingAdditions)
			})
	}
}

// TestStringDomainPriorityLabelsKeepYAMLLexemes checks non-string YAML scalars.
//
// Example: Boolean-looking and exponent labels remain their explicit ID strings.
func TestStringDomainPriorityLabelsKeepYAMLLexemes(t *testing.T) {
	for _, label := range []string{"1", "01", "0x10", "1e2", "true"} {
		t.Run(label,
			// Replay a selector against a valid scalar label without numeric coercion.
			//
			// Example: The selector 01 is distinct from 1.
			func(t *testing.T) {
				report := strings.ReplaceAll(linkedReport, "priority: high", "priority: "+label)
				_, err := parse([]byte(report))
				require.NoError(t, err)
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(report), 0600))
				event := `{"event":"forecast_change","details":{"status_update":{"updates":[{"target":"priority_estimate","id":"` + label + `","values":{"basis":"changed"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "forecast_change"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, priorityLabel(label), parsed.PriorityEstimates[0].Priority)
				require.Equal(t, "changed", parsed.PriorityEstimates[0].Basis)
				require.Contains(t, string(result), "priority: "+label)
			})
	}
}

// TestNumericPriorityValueKeepsLexicalOwnership checks numeric JSON updates.
//
// Example: A later owner edit from 1.1 to 1.10 cannot be mistaken for the same label.
func TestNumericPriorityValueKeepsLexicalOwnership(t *testing.T) {
	report := strings.Replace(linkedReport, "title: First, priority: high", "title: First, priority: 1.10", 1)
	report = strings.Replace(report, "title: Second, priority: high", "title: Second, priority: 1.1", 1)
	report = strings.Replace(report, "top_priorities:", "  - {id: '3', title: Third, priority: 1.10, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS}\ntop_priorities:", 1)
	report = strings.Replace(report, "priority: high, min_minutes: 20", "priority: 1.10, min_minutes: 20", 1)
	report = strings.Replace(report, "completion_estimate:", "  - {priority: 1.1, min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest}\ncompletion_estimate:", 1)
	_, err := parse([]byte(report))
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	first := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":1.1}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("1.1"), parsed.Tasks[0].Priority)
	edited := strings.Replace(string(result), "title: First, priority: 1.1,", "title: First, priority: 1.10,", 1)
	require.NotEqual(t, string(result), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first+first), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err = os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, priorityLabel("1.10"), parsed.Tasks[0].Priority)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestNumericPriorityOrderOwnershipKeepsLexicalValues checks report-level labels.
//
// Example: A later 1.100 owner edit cannot be confused with projected 1.10.
func TestNumericPriorityOrderOwnershipKeepsLexicalValues(t *testing.T) {
	report := linkedReport + "priority_order: [1.1, 1.10]\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	first := `{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","values":{"priority_order":[1.10,1.1]}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, []priorityLabel{"1.10", "1.1"}, parsed.PriorityOrder)
	edited := strings.Replace(string(result), "priority_order: [1.10,1.1]", "priority_order: [1.100,1.1]", 1)
	require.NotEqual(t, string(result), edited)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0600))
	require.NoError(t, os.WriteFile(logPath, []byte(first+first), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err = os.ReadFile(path)
	require.NoError(t, err)
	parsed, err = parse(result)
	require.NoError(t, err)
	require.Equal(t, []priorityLabel{"1.100", "1.1"}, parsed.PriorityOrder)
	require.NotEmpty(t, parsed.StatusSync.Groups)
}

// TestNumericTopPriorityValuesKeepLexicalIDs checks explicit list updates.
//
// Example: Numeric event values 1.10 and 1.1 remain distinct task IDs.
func TestNumericTopPriorityValuesKeepLexicalIDs(t *testing.T) {
	report := strings.Replace(linkedReport, "id: '1', title: First", "id: 1, title: First", 1)
	report = strings.Replace(report, "id: '2', title: Second", "id: 1.1, parent_id: 1, title: Second", 1)
	report = strings.Replace(report, "top_priorities: ['1', '2']", "  - {id: 1.10, parent_id: 1, title: Third, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS}\ntop_priorities: [1, 1.1, 1.10]", 1)
	report = strings.Replace(report, "task_id: '1'", "task_id: 1", 1)
	report = strings.Replace(report, "task_id: '2'", "task_id: 1.1", 1)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	event := `{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","values":{"top_priorities":[1.10,1.1]}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, []taskID{"1.10", "1.1"}, parsed.TopPriorities)
}

// TestNumericPriorityValuePairedRetirementIsAtomic checks the public refresh path.
//
// Example: JSON 1.10 matches a supplied estimate label "1.10" without coercion.
func TestNumericPriorityValuePairedRetirementIsAtomic(t *testing.T) {
	// prior is the exact source estimate explicitly authorized for retirement.
	//
	// Example: The last high-priority task moves into the 1.10 group.
	const prior = `{"priority":"high","min_minutes":20,"max_minutes":40,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"latest"}`
	estimate := strings.Replace(prior, `"priority":"high"`, `"priority":"1.10"`, 1)
	report := strings.Replace(linkedReport, "title: Second, priority: high", "title: Second, priority: 1.10", 1)
	report = strings.Replace(report, "completion_estimate:", "  - {priority: 1.10, min_minutes: 20, max_minutes: 40, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: latest} # owner estimate\ncompletion_estimate:", 1)
	event := `{"event":"decision","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"priority":1.10}}],"additions":[{"target":"priority_estimate","retire_priority":"high","expected_prior":` + prior + `,"record":` + estimate + `}]}}}` + "\n"
	for _, tc := range []struct {
		name, input string
		succeeds    bool
	}{
		{"matching", report, true},
		{"owner conflict", strings.Replace(report, "critical_path: latest} # owner estimate", "critical_path: owner-edit} # owner estimate", 1), false},
		{"remaining source", strings.Replace(report, "top_priorities:", "  - {id: '3', title: Other, priority: high, impact: Useful, effort: {min_minutes: 10, max_minutes: 20, kind: elapsed, basis: guess}, status: IN PROGRESS}\ntop_priorities:", 1), false},
	} {
		t.Run(tc.name,
			// Replay and retry one complete paired priority transaction.
			//
			// Example: A conflicting estimate leaves the task and both estimates unchanged.
			func(t *testing.T) {
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(tc.input), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				parsed, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, string(result), "# owner estimate")
				if tc.succeeds {
					require.Equal(t, priorityLabel("1.10"), parsed.Tasks[0].Priority)
					require.Len(t, parsed.PriorityEstimates, 1)
					require.Empty(t, parsed.StatusSync.Groups)
					require.Empty(t, parsed.StatusSync.PendingAdditions)
				} else {
					require.Equal(t, priorityLabel("high"), parsed.Tasks[0].Priority)
					require.Len(t, parsed.PriorityEstimates, 2)
					require.NotEmpty(t, parsed.StatusSync.Groups)
				}
				out.Reset()
				errOut.Reset()
				require.Equal(t, 0, run([]string{"refresh", dir, "decision"}, strings.NewReader(""), &out, &errOut), errOut.String())
				retry, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}

// TestPriorityRetirementRejectsDifferentNumericLexeme checks independent verification.
//
// Example: An event value 1.1 cannot authorize an estimate labelled 1.10.
func TestPriorityRetirementRejectsDifferentNumericLexeme(t *testing.T) {
	original := []byte("tasks: [{id: '1', priority: high}]\npriority_estimates: [{priority: 1.10}]\n")
	addition := syncAddition{Target: syncTargetPriorityEstimate, RetirePriority: "high", Record: json.RawMessage(`{"priority":"1.10"}`)}
	effects := []syncEffect{{Target: syncTargetPriorityEstimate, ID: "high", Value: addition, Operation: syncEffectRemove}, {Target: syncTargetTask, ID: "1", Field: "priority", Value: json.RawMessage(`1.1`), Operation: syncEffectSet}}
	require.Error(t, verifyPriorityRetirementEffects(original, effects))
	effects[1].Value = json.RawMessage(`1.10`)
	require.NoError(t, verifyPriorityRetirementEffects(original, effects))
}
