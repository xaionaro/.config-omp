package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBlockPriorityUpdatesPreserveFollowingSections checks block-list projection.
//
// Example: Both indentless and indented lists accept an explicit priority update or cancellation.
func TestBlockPriorityUpdatesPreserveFollowingSections(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, items, event string }{
		{"indentless update", "- '1'\n- '2'", `{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","values":{"top_priorities":["2"]}}]}}}`},
		{"indented update", "  - '1'\n  - '2'", `{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","values":{"top_priorities":["2"]}}]}}}`},
		{"indentless cancellation", "- '1'\n- '2'", `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"lifecycle":"cancelled","status":"unknown","evidence":"cancelled by owner"}}]}}}`},
		{"indented cancellation", "  - '1'\n  - '2'", `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"lifecycle":"cancelled","status":"unknown","evidence":"cancelled by owner"}}]}}}`},
	} {
		t.Run(tc.name,
			// Replay a complete explicit update and retain the next report section.
			//
			// Example: Preserve a neighboring priority estimate and owner comment.
			func(t *testing.T) {
				input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities:\n"+tc.items, 1)
				require.NotEqual(t, linkedReport, input)
				input = strings.Replace(input, "priority_estimates:", "priority_estimates: # keep next section\n", 1)
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(tc.event+"\n"), 0600))
				out, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(out)
				require.NoError(t, err)
				require.Equal(t, []taskID{"2"}, report.TopPriorities)
				require.Empty(t, report.StatusSync.Groups)
				if strings.Contains(tc.name, "cancellation") {
					require.Equal(t, taskLifecycleCancelled, report.Tasks[0].Lifecycle)
					require.Equal(t, statusUnknown, report.Tasks[0].Status)
					require.Equal(t, "cancelled by owner", report.Tasks[0].Evidence)
				}
				require.Contains(t, string(out), "priority_estimates: # keep next section")
				require.Contains(t, string(out), "completion_estimate:")
				require.Len(t, report.PriorityEstimates, 1)
			})
	}
}

// TestNestedBlockSequencePreservesSiblingFields checks task-scoped list edits.
//
// Example: Replacing work references leaves following status and sibling tasks intact.
func TestNestedBlockSequencePreservesSiblingFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, items string
		last        bool
	}{
		{"indentless before sibling", "    - 'project-understanding.yaml#/records/old'", false},
		{"indented before sibling", "      - 'project-understanding.yaml#/records/old'", false},
		{"indentless last field", "    - 'project-understanding.yaml#/records/old'", true},
		{"indented last field", "      - 'project-understanding.yaml#/records/old'", true},
	} {
		t.Run(tc.name,
			// Check a list boundary within an existing task mapping.
			//
			// Example: Stop before the next task when the list is the last field.
			func(t *testing.T) {
				status := "    status: IN PROGRESS\n"
				list := "    work_refs:\n" + tc.items + "\n"
				replacement := list + status
				if tc.last {
					replacement = status + list
				}
				input := strings.Replace(valid, status, replacement, 1)
				require.NotEqual(t, valid, input)
				event := `{"event":"requirement_change","details":{"status_update":{"updates":[{"target":"task","id":"1","values":{"work_refs":["project-understanding.yaml#/records/new"]}}]}}}`
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event+"\n"), 0600))
				out, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(out)
				require.NoError(t, err)
				require.Equal(t, []string{"project-understanding.yaml#/records/new"}, report.Tasks[0].WorkRefs)
				require.Equal(t, statusInProgress, report.Tasks[0].Status)
				require.Equal(t, "Complete", report.Tasks[1].Title)
				require.Empty(t, report.StatusSync.Groups)
			})
	}
}

// TestCommentInsideBlockPriorityListStaysPending checks comment safety.
//
// Example: Do not replace a list containing an owner's note.
func TestCommentInsideBlockPriorityListStaysPending(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, event string }{
		{"typed update", `{"event":"decision","details":{"status_update":{"updates":[{"target":"report","id":"current","values":{"top_priorities":["2"]}}]}}}`},
		{"cancellation", `{"event":"lifecycle_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"lifecycle":"cancelled","status":"unknown","evidence":"cancelled by owner"}}]}}}`},
	} {
		t.Run(tc.name,
			// Refuse to consume a comment in the replaced span.
			//
			// Example: Keep the original list and its owner note.
			func(t *testing.T) {
				input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities:\n- '1' # owner note\n- '2'", 1)
				require.NotEqual(t, linkedReport, input)
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(tc.event+"\n"), 0600))
				out, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(out)
				require.NoError(t, err)
				require.Equal(t, []taskID{"1", "2"}, report.TopPriorities)
				require.NotEmpty(t, report.StatusSync.Groups)
				require.Contains(t, string(out), "# owner note")
			})
	}
}

// TestBlockPriorityEditsPreserveCRLF checks line-ending preservation.
//
// Example: Replacing an indentless list does not introduce lone line feeds.
func TestBlockPriorityEditsPreserveCRLF(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities:\n- '1'\n- '2'", 1)
	require.NotEqual(t, linkedReport, input)
	input = strings.ReplaceAll(input, "\n", "\r\n")
	update := syncUpdate{Target: syncTargetReport, ID: "current"}
	updated, ok := editStructuredField([]byte(input), update, "top_priorities", `["2"]`)
	require.True(t, ok)
	require.NotContains(t, strings.ReplaceAll(string(updated), "\r\n", ""), "\n")
	cancelled, ok := removeCancelledPriority([]byte(input), taskID("1"))
	require.True(t, ok)
	require.NotContains(t, strings.ReplaceAll(string(cancelled), "\r\n", ""), "\n")
}

// TestIndentedDocumentWithIndentlessPriorities checks nonzero document indentation.
//
// Example: A list aligned with a two-space-indented top-level key remains valid.
func TestIndentedDocumentWithIndentlessPriorities(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities:\n- '1'\n- '2'", 1)
	require.NotEqual(t, linkedReport, input)
	input = "  " + strings.ReplaceAll(input, "\n", "\n  ")
	_, err := parse([]byte(input))
	require.NoError(t, err)
	updated, ok := editStructuredField([]byte(input), syncUpdate{Target: syncTargetReport, ID: "current"}, "top_priorities", `["2"]`)
	require.True(t, ok)
	_, err = parse(updated)
	require.NoError(t, err)
	cancelled, ok := removeCancelledPriority([]byte(input), taskID("1"))
	require.True(t, ok)
	_, err = parse(cancelled)
	require.NoError(t, err)
}

// TestSequencePreservesFollowingHeadComment checks sibling comment ownership.
//
// Example: Updating a list retains the next section's outdented note.
func TestSequencePreservesFollowingHeadComment(t *testing.T) {
	t.Parallel()
	for _, items := range []string{"- '1'\n- '2'", "  - '1'\n  - '2'"} {
		input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities:\n"+items, 1)
		input = strings.Replace(input, "priority_estimates:", "# following owner note\npriority_estimates:", 1)
		out, ok := editStructuredField([]byte(input), syncUpdate{Target: syncTargetReport, ID: "current"}, "top_priorities", `["2"]`)
		require.True(t, ok)
		require.Contains(t, string(out), "# following owner note\npriority_estimates:")
		out, ok = removeCancelledPriority([]byte(input), taskID("1"))
		require.True(t, ok)
		require.Contains(t, string(out), "# following owner note\npriority_estimates:")
	}
}

// TestSequenceInternalCommentRemainsPending checks standalone item notes.
//
// Example: A comment between sequence items prevents replacement.
func TestSequenceInternalCommentRemainsPending(t *testing.T) {
	t.Parallel()
	for _, items := range []string{"- '1'\n# item note\n- '2'", "  - '1'\n  # item note\n  - '2'"} {
		input := strings.Replace(linkedReport, "top_priorities: ['1', '2']", "top_priorities:\n"+items, 1)
		out, ok := editStructuredField([]byte(input), syncUpdate{Target: syncTargetReport, ID: "current"}, "top_priorities", `["2"]`)
		require.False(t, ok)
		require.Equal(t, input, string(out))
		out, ok = removeCancelledPriority([]byte(input), taskID("1"))
		require.False(t, ok)
		require.Equal(t, input, string(out))
	}
}

// TestFinalSequencePreservesCRLF checks a final list without a trailing newline.
//
// Example: Both replacement paths preserve CRLF line endings at end of file.
func TestFinalSequencePreservesCRLF(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "top_priorities: ['1', '2']\n", "", 1) + "top_priorities:\n- '1'"
	input = strings.ReplaceAll(input, "\n", "\r\n")
	out, ok := editStructuredField([]byte(input), syncUpdate{Target: syncTargetReport, ID: "current"}, "top_priorities", `["2"]`)
	require.True(t, ok)
	require.NotContains(t, strings.ReplaceAll(string(out), "\r\n", ""), "\n")
	out, ok = removeCancelledPriority([]byte(input), taskID("1"))
	require.True(t, ok)
	require.NotContains(t, strings.ReplaceAll(string(out), "\r\n", ""), "\n")
}

// TestNestedSequenceCommentWithCRLF checks sibling ownership under CRLF.
//
// Example: A nested status note survives a work-reference update.
func TestNestedSequenceCommentWithCRLF(t *testing.T) {
	t.Parallel()
	for _, items := range []string{"    - 'project-understanding.yaml#/records/old'", "      - 'project-understanding.yaml#/records/old'"} {
		input := strings.Replace(valid, "    status: IN PROGRESS\n", "    work_refs:\n"+items+"\n    # status note\n    status: IN PROGRESS\n", 1)
		input = strings.ReplaceAll(input, "\n", "\r\n")
		out, ok := editStructuredField([]byte(input), syncUpdate{Target: syncTargetTask, ID: "1"}, "work_refs", `["project-understanding.yaml#/records/new"]`)
		require.True(t, ok)
		require.Contains(t, string(out), "    # status note\r\n    status: IN PROGRESS")
		_, err := parse(out)
		require.NoError(t, err)
	}
}
