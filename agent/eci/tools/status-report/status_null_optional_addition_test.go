package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestNullOptionalSectionsAcceptExplicitAdditions checks source-backed replay.
//
// Example: Null reviews, lanes and root sections accept their first record.
func TestNullOptionalSectionsAcceptExplicitAdditions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, section, event string
		work                 workData
		forecastStatus       string
	}{
		{"review", "reviews", `{"target":"review","record":{"task_id":"1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}}`, workData{}, ""},
		{"lane", "lanes", `{"target":"lane","record":{"task_id":"1","work_ref":"project-understanding.yaml#/records/w","outcome":"Result","required_environment":"implementation","implementation_status":"IN PROGRESS","test_status":"NEW","prod_status":"NEW","forecast":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}}`, workData{State: workStateActive, Roles: []reportingRole{reportingRoleLane}, ForecastRef: "forecasts.yaml#/records/f"}, "active"},
		{"root completion", "root_completions", `{"target":"root_completion","evidence":"accepted","record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"}}`, workData{State: workStateCompleted, CompletedAt: "2026-11-02T15:00:00Z", Roles: []reportingRole{reportingRoleRoot}}, ""},
		{"root forecast", "root_forecasts", `{"target":"root_forecast","record":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}}`, workData{State: workStateActive, Roles: []reportingRole{reportingRoleRoot}, ForecastRef: "forecasts.yaml#/records/f"}, "active"},
	} {
		t.Run(tc.name,
			// Replay with actual canonical work and an explicit null optional section.
			//
			// Example: The addition is persisted and a retry is idempotent.
			func(t *testing.T) {
				input := valid
				if strings.Contains(input, tc.section+": []") {
					input = strings.Replace(input, tc.section+": []", tc.section+": null # owner note", 1)
				} else {
					input += tc.section + ": null # owner note\n"
				}
				if tc.section == "root_completions" {
					input += "root_forecasts: null # other note\n"
				}
				if tc.section == "root_forecasts" {
					input += "root_completions: null # other note\n"
				}
				dir := t.TempDir()
				event := `{"event":"decision","details":{"status_update":{"additions":[` + tc.event + `]}}}` + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				ledger := ledgerSource{Records: map[string]sourceRecord{"w": {Type: "work", OutcomeRefs: []string{"o"}, Data: tc.work}, "o": {Type: "outcome", Outcome: "Result"}}}
				kind := "root"
				if tc.section == "lanes" {
					kind = "lane"
				}
				forecasts := forecastSource{Records: map[string]map[string]any{"f": {"status": tc.forecastStatus, "kind": kind}}}
				result, _, err := projectLogStatus(dir, []byte(input), ledger, forecasts)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Contains(t, string(result), "# owner note")
				if strings.HasPrefix(tc.section, "root_") {
					require.Contains(t, string(result), "# other note")
				}
				switch tc.section {
				case "reviews":
					require.Len(t, report.Reviews, 1)
				case "lanes":
					require.Len(t, report.Lanes, 1)
				case "root_completions":
					require.Len(t, report.RootCompletions, 1)
				case "root_forecasts":
					require.Len(t, report.RootForecasts, 1)
				}
				retry, _, err := projectLogStatus(dir, result, ledger, forecasts)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}

// TestNullReviewCLIReplaysAndPreservesComments checks the public refresh path.
//
// Example: A flow-root null review list accepts an explicitly supplied review.
func TestNullReviewCLIReplaysAndPreservesComments(t *testing.T) {
	t.Parallel()
	var mapping map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(linkedReport), &mapping))
	mapping["reviews"] = nil
	input, err := json.Marshal(mapping)
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, input, 0600))
	event := `{"event":"review_result","details":{"status_update":{"additions":[{"target":"review","record":{"task_id":"1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "review_result"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Len(t, report.Reviews, 1)
	require.Empty(t, report.StatusSync.PendingAdditions)
}

// TestNullReviewStylesPreserveOwnerComments checks supported YAML null forms.
//
// Example: Tagged, implicit and CRLF nulls initialize without losing a note.
func TestNullReviewStylesPreserveOwnerComments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, suffix string }{
		{"implicit", "reviews: # owner note\n"},
		{"tilde", "reviews: ~ # owner note\n"},
		{"tagged empty", "reviews: !!null '' # owner note\n"},
		{"escaped", "reviews: !!null \"n\\u0075ll\" # owner note\n"},
		{"anchor split", "reviews: !!null # tag note\n  &owner null # owner note\n"},
		{"CRLF", "reviews: null # owner note\r\n"},
	} {
		t.Run(tc.name,
			// Check an actual first review and an identical retry.
			//
			// Example: Preserve the report's original line endings.
			func(t *testing.T) {
				input := linkedReport
				if tc.name == "CRLF" {
					input = strings.ReplaceAll(input, "\n", "\r\n")
				}
				input += tc.suffix
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				require.NoError(t, os.WriteFile(path, []byte(input), 0600))
				event := `{"event":"review_result","details":{"status_update":{"additions":[{"target":"review","record":{"task_id":"1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				var out, errOut bytes.Buffer
				require.Equal(t, 0, run([]string{"refresh", dir, "review_result"}, strings.NewReader(""), &out, &errOut), errOut.String())
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Len(t, report.Reviews, 1)
				require.Contains(t, string(result), "# owner note")
				if tc.name == "anchor split" {
					require.Contains(t, string(result), "# tag note")
				}
				if tc.name == "CRLF" {
					require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\n")
				}
				out.Reset()
				errOut.Reset()
				require.Equal(t, 0, run([]string{"refresh", dir, "review_result"}, strings.NewReader(""), &out, &errOut), errOut.String())
				retry, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}

// TestNullReviewSharedAliasPreservesOtherSection checks shared owner data.
//
// Example: A root-completions alias must not turn into a review list.
func TestNullReviewSharedAliasPreservesOtherSection(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "decisions: [Use YAML]", "reviews: &owner null\nroot_completions: *owner\ndecisions: [Use YAML]", 1)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(input), 0600))
	event := `{"event":"review_result","details":{"status_update":{"additions":[{"target":"review","record":{"task_id":"1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}}]}}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	var out, errOut bytes.Buffer
	code := run([]string{"refresh", dir, "review_result"}, strings.NewReader(""), &out, &errOut)
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(result), "reviews: &owner null\nroot_completions: *owner")
	report, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, report.Reviews)
	require.Empty(t, report.RootCompletions)
	if code == 0 {
		require.NotNil(t, report.StatusSync)
		require.NotEmpty(t, report.StatusSync.PendingAdditions)
	}
}

// TestAnchoredNullReviewAcceptsLaterAddition checks continued replay.
//
// Example: An initialized anchored list accepts a later distinct review.
func TestAnchoredNullReviewAcceptsLaterAddition(t *testing.T) {
	t.Parallel()
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport+"reviews: &owner null # owner note\n"), 0600))
	logPath := filepath.Join(dir, "high_level_log.jsonl")
	first := `{"event":"review_result","details":{"status_update":{"additions":[{"target":"review","record":{"task_id":"1","reporter":"A","critic":"B","rounds":1,"estimate":"checked","impact":"checked","priority":"checked","unresolved":"none"}}]}}}` + "\n"
	second := strings.Replace(first, `"task_id":"1"`, `"task_id":"2"`, 1)
	require.NoError(t, os.WriteFile(logPath, []byte(first), 0600))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"refresh", dir, "review_result"}, strings.NewReader(""), &out, &errOut), errOut.String())
	require.NoError(t, os.WriteFile(logPath, []byte(first+second), 0600))
	out.Reset()
	errOut.Reset()
	require.Equal(t, 0, run([]string{"refresh", dir, "review_result"}, strings.NewReader(""), &out, &errOut), errOut.String())
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Len(t, report.Reviews, 2)
	require.Contains(t, string(result), "# owner note")
	require.Empty(t, report.StatusSync.PendingAdditions)
}
