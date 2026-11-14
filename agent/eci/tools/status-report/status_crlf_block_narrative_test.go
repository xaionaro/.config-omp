package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestCRLFBlockNarrativeAppendPreservesLineEndings checks real refresh output.
//
// Example: A new progress entry retains the owner's comment and CRLF layout.
func TestCRLFBlockNarrativeAppendPreservesLineEndings(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "progress: [Implementation started]\n", "progress:\n  - Implementation started\n# decisions note\n", 1)
	require.NotEqual(t, linkedReport, input)
	input = strings.ReplaceAll(input, "\n", "\r\n")
	// event adds a keyed progress entry without replacing the existing text.
	//
	// Example: Append independently supplied work to a block-style list.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"new progress"}}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(input), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(linkedForecasts), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "Implementation started")
	require.Contains(t, report.Progress, "new progress")
	require.Contains(t, string(result), "# decisions note\r\ndecisions:")
	require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\n")
	require.Equal(t, 1, report.StatusSync.Cursor)
}

// TestCRLFBlockNarrativeRetractionPreservesLineEndings checks an emptied list.
//
// Example: Retracting the last managed entry keeps the report's CRLF layout.
func TestCRLFBlockNarrativeRetractionPreservesLineEndings(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "progress: [Implementation started]\n", "progress:\n  - Implementation started\n# decisions note\n", 1)
	require.NotEqual(t, linkedReport, input)
	input = strings.ReplaceAll(input, "\n", "\r\n")
	// first creates a managed progress entry.
	//
	// Example: Record a claim that may later be retracted.
	const first = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"withdrawn claim"}}]}}}` + "\n"
	// second explicitly retracts that exact claim.
	//
	// Example: Remove the remaining managed item after the owner removes a legacy item.
	const second = `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one","corrects":1,"retract":true}]}}}` + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "high_level_log.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(first), 0600))
	initial, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	edited := strings.Replace(string(initial), "  - Implementation started\r\n", "", 1)
	require.NotEqual(t, string(initial), edited)
	require.NoError(t, os.WriteFile(path, []byte(first+second), 0600))
	result, _, err := projectLogStatus(dir, []byte(edited), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, report.Progress)
	require.Contains(t, string(result), "# decisions note\r\ndecisions:")
	require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\n")
	require.Empty(t, report.StatusSync.Groups)
}

// TestCRLFBlockNarrativeAtEOFWithoutNewline preserves the final line boundary.
//
// Example: Appending to a final list without a newline does not introduce bare CR or LF.
func TestCRLFBlockNarrativeAtEOFWithoutNewline(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "progress: [Implementation started]\n", "", 1)
	require.NotEqual(t, linkedReport, input)
	input = strings.ReplaceAll(input, "\n", "\r\n") + "progress:\r\n  - Implementation started"
	// event appends to the final block list.
	//
	// Example: Preserve the preceding final item when no newline existed at EOF.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"new progress"}}]}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	result, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "Implementation started")
	require.Contains(t, report.Progress, "new progress")
	normalized := strings.ReplaceAll(string(result), "\r\n", "")
	require.NotContains(t, normalized, "\n")
	require.NotContains(t, normalized, "\r")
}

// TestCRLFMultilineNarrativeRetractionKeepsFinalBoundary checks both EOF layouts.
//
// Example: Removing a multiline final item preserves whether its last line was terminated.
func TestCRLFMultilineNarrativeRetractionKeepsFinalBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, input, want string }{
		{"unterminated", "progress:\r\n  - subject_ref: one\r\n    text: done", "progress:\r\n  []"},
		{"terminated", "progress:\r\n  - subject_ref: one\r\n    text: done\r\n", "progress:\r\n  []\r\n"},
		{"following field", "progress:\r\n  - subject_ref: one\r\n    text: done\r\ndecisions: []\r\n", "progress:\r\n  []\r\ndecisions: []\r\n"},
		{"indentless", "progress:\r\n- subject_ref: one\r\n  text: done", "progress:\r\n  []"},
	} {
		t.Run(tc.name,
			// run checks that the emptied list remains valid at each boundary.
			//
			// Example: An unterminated item leaves no stray carriage return.
			func(t *testing.T) {
				out, ok := retractNarrativeSync([]byte(tc.input), syncUpdate{Target: syncTargetProgress, ID: "one"}, "done", true, "", false)
				require.True(t, ok)
				require.Equal(t, tc.want, string(out))
			})
	}
}

// TestCRLFRetractionOfLastEntryKeepsSurvivorBoundary preserves EOF formatting.
//
// Example: Removing the final managed entry keeps the preceding legacy item intact.
func TestCRLFRetractionOfLastEntryKeepsSurvivorBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, suffix, want string }{
		{"unterminated", "", "progress:\r\n  - legacy\r\n"},
		{"terminated", "\r\n", "progress:\r\n  - legacy\r\n"},
		{"following field", "\r\ndecisions: []\r\n", "progress:\r\n  - legacy\r\ndecisions: []\r\n"},
	} {
		t.Run(tc.name,
			// check verifies the surviving list and its original final boundary.
			//
			// Example: An unterminated final entry does not leave a bare CR.
			func(t *testing.T) {
				input := "progress:\r\n  - legacy\r\n  - subject_ref: one\r\n    text: done" + tc.suffix
				out, ok := retractNarrativeSync([]byte(input), syncUpdate{Target: syncTargetProgress, ID: "one"}, "done", true, "", false)
				require.True(t, ok)
				require.Equal(t, tc.want, string(out))
			})
	}
}

// TestRetractionPreservesBlockScalarSurvivor checks unowned narrative values.
//
// Example: A clipped literal retains its final newline after a later item is removed.
func TestRetractionPreservesBlockScalarSurvivor(t *testing.T) {
	t.Parallel()
	for _, style := range []string{"|", ">", "|-", ">-"} {
		t.Run(style,
			// check compares the surviving scalar before and after retraction.
			//
			// Example: A clipped folded scalar retains its newline.
			func(t *testing.T) {
				input := []byte("progress:\r\n  - " + style + "\r\n    legacy scalar\r\n  - subject_ref: one\r\n    text: done")
				var before, after yaml.Node
				require.NoError(t, yaml.Unmarshal(input, &before))
				out, ok := retractNarrativeSync(input, syncUpdate{Target: syncTargetProgress, ID: "one"}, "done", true, "", false)
				require.True(t, ok)
				require.NoError(t, yaml.Unmarshal(out, &after))
				require.Equal(t, mapValue(before.Content[0], "progress").Content[0].Value, mapValue(after.Content[0], "progress").Content[0].Value)
			})
	}
}

// TestAppendAfterUnterminatedBlockScalarPreservesValue checks EOF replay.
//
// Example: A clipped literal without a final newline remains unchanged when appending.
func TestAppendAfterUnterminatedBlockScalarPreservesValue(t *testing.T) {
	t.Parallel()
	for _, style := range []string{"|", ">", "|+", ">+", "|-", ">-", "|2 # owner note", ">+2 # owner note", "&owner |", "!!str |", "&owner !!str |", "!!str &owner |", "!<tag:yaml.org,2002:str> |", "!!str &item |2+"} {
		t.Run(style,
			// check compares the existing value across the public projection.
			//
			// Example: An unterminated literal retains its value.
			func(t *testing.T) {
				input := strings.Replace(linkedReport, "progress: [Implementation started]\n", "", 1)
				input = strings.ReplaceAll(input, "\n", "\r\n") + "progress:\r\n  - " + style + "\r\n    legacy scalar"
				// event adds a separate managed progress item.
				//
				// Example: Replay a newly recorded work update.
				const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"new progress"}}]}}}` + "\n"
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				result, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, []string{"legacy scalar", "new progress"}, report.Progress)
				if strings.Contains(style, "# owner note") {
					require.Contains(t, string(result), "# owner note\r\n")
				}
				require.NotContains(t, strings.ReplaceAll(string(result), "\r\n", ""), "\r")
			})
	}
}

// TestAppendAfterScalarWithSeparatePropertiesPreservesOwnerData checks EOF replay.
//
// Example: An anchor on the preceding line remains attached to the existing scalar.
func TestAppendAfterScalarWithSeparatePropertiesPreservesOwnerData(t *testing.T) {
	t.Parallel()
	for _, property := range []string{"&owner", "!!str", "&owner !!str"} {
		t.Run(property,
			// check verifies the value, properties, and comments after appending.
			//
			// Example: A tagged literal retains its owner note and value.
			func(t *testing.T) {
				input := []byte("progress:\r\n  - " + property + " # property note\r\n    | # scalar note\r\n      legacy scalar")
				var before, after yaml.Node
				require.NoError(t, yaml.Unmarshal(input, &before))
				out, ok := applyNarrativeSyncField(input, syncUpdate{Target: syncTargetProgress, ID: "one"}, "text", "new progress", "", false, false)
				require.True(t, ok)
				require.NoError(t, yaml.Unmarshal(out, &after))
				original := mapValue(before.Content[0], "progress").Content[0]
				updated := mapValue(after.Content[0], "progress").Content[0]
				require.Equal(t, original.Value, updated.Value)
				require.Equal(t, original.Anchor, updated.Anchor)
				require.Equal(t, original.Tag, updated.Tag)
				require.Contains(t, string(out), "# property note\r\n")
				require.Contains(t, string(out), "# scalar note\r\n")
			})
	}
}

// TestAppendAfterKeptScalarBlankLinesPreservesValue checks EOF replay.
//
// Example: Keep-chomping retains blank body lines before a new progress item.
func TestAppendAfterKeptScalarBlankLinesPreservesValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ style, tail string }{
		{"|+", "\r\n    \r\n    "}, {">+", "\r\n    \r\n    "},
		{"|+", "\r\n    legacy scalar\r\n\r\n"}, {">+", "\r\n    legacy scalar\r\n\r\n"},
		{"|+", "\r\n    legacy scalar\r\n    "}, {">+", "\r\n    legacy scalar\r\n    "},
	} {
		t.Run(tc.style+tc.tail,
			// check compares the scalar value and retains its header comment.
			//
			// Example: A trailing empty line remains part of a kept scalar.
			func(t *testing.T) {
				input := []byte("progress:\r\n  - " + tc.style + " # owner note" + tc.tail)
				var before, after yaml.Node
				require.NoError(t, yaml.Unmarshal(input, &before))
				out, ok := applyNarrativeSyncField(input, syncUpdate{Target: syncTargetProgress, ID: "one"}, "text", "new progress", "", false, false)
				require.True(t, ok)
				require.NoError(t, yaml.Unmarshal(out, &after))
				require.Equal(t, mapValue(before.Content[0], "progress").Content[0].Value, mapValue(after.Content[0], "progress").Content[0].Value)
				require.Contains(t, string(out), "# owner note\r\n")
			})
	}
}

// TestAppendAfterKeptScalarBeforeFollowingField preserves the boundary.
//
// Example: A decisions comment remains beside its field after a progress append.
func TestAppendAfterKeptScalarBeforeFollowingField(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"decisions: [keep]\r\n", "# decisions note\r\ndecisions: [keep]\r\n", "# trailing note\r\n"} {
		t.Run(suffix,
			// check compares the existing scalar and the following owner text.
			//
			// Example: A kept blank line precedes the new item.
			func(t *testing.T) {
				input := []byte("progress:\r\n  - |+ # scalar note\r\n    legacy scalar\r\n\r\n" + suffix)
				var before, after yaml.Node
				require.NoError(t, yaml.Unmarshal(input, &before))
				out, ok := applyNarrativeSyncField(input, syncUpdate{Target: syncTargetProgress, ID: "one"}, "text", "new progress", "", false, false)
				require.True(t, ok)
				require.NoError(t, yaml.Unmarshal(out, &after))
				require.Equal(t, mapValue(before.Content[0], "progress").Content[0].Value, mapValue(after.Content[0], "progress").Content[0].Value)
				require.True(t, strings.HasSuffix(string(out), suffix))
			})
	}
}

// TestReplayEOFScalarCommentsAndIndent preserves valid source comments.
//
// Example: An unterminated scalar header comment survives a progress append.
func TestReplayEOFScalarCommentsAndIndent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, scalar string }{
		{"explicit indent", "|2\r\n    legacy scalar\r\n    "},
		{"header comment", "|+ # owner note"},
	} {
		t.Run(tc.name,
			// check replays an independent progress event over valid YAML.
			//
			// Example: The comment stream remains intact at an unterminated EOF.
			func(t *testing.T) {
				input := strings.Replace(linkedReport, "progress: [Implementation started]\n", "", 1)
				input = strings.ReplaceAll(input, "\n", "\r\n") + "progress:\r\n  - " + tc.scalar
				// event adds progress without modifying the existing scalar.
				//
				// Example: A worker records another update.
				const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"new progress"}}]}}}` + "\n"
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
				out, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				before, err := parse([]byte(input))
				require.NoError(t, err)
				after, err := parse(out)
				require.NoError(t, err)
				require.Equal(t, append(before.Progress, "new progress"), after.Progress)
				if strings.Contains(tc.scalar, "# owner note") {
					require.Contains(t, string(out), "# owner note\r\n")
				}
			})
	}
}

// TestReplayEmptyKeptScalarWithComment preserves empty scalar and comment.
//
// Example: A new progress item follows an empty owner-authored block scalar.
func TestReplayEmptyKeptScalarWithComment(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "progress: [Implementation started]\n", "", 1)
	input = strings.ReplaceAll(input, "\n", "\r\n") + "progress:\r\n  - |+ # owner note\r\n"
	// event records a separate progress item.
	//
	// Example: Replay a worker update without altering an empty legacy item.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"new progress"}}]}}}` + "\n"
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	out, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	before, err := parse([]byte(input))
	require.NoError(t, err)
	after, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, append(before.Progress, "new progress"), after.Progress)
	require.Contains(t, string(out), "|+ # owner note\r\n")
}

// TestEmptyKeptScalarCommentScanPreservesOrderedComments checks scanner fallback.
//
// Example: A changed comment after an empty scalar remains detectable.
func TestEmptyKeptScalarCommentScanPreservesOrderedComments(t *testing.T) {
	t.Parallel()
	input := []byte("# first\nprogress:\n  - &owner |+ # scalar\n  - {subject_ref: one, text: '# text'} # item\n# last\n")
	comments, err := yamlComments(input)
	require.NoError(t, err)
	require.Equal(t, []string{" first", " scalar", " item", " last"}, comments)
	changed := strings.Replace(string(input), "# item", "# changed", 1)
	other, err := yamlComments([]byte(changed))
	require.NoError(t, err)
	require.NotEqual(t, comments, other)
}

// TestRefreshEmptyKeptScalarWithReconciliation checks the full refresh path.
//
// Example: An incomplete ledger can add a diagnostic while preserving an empty scalar.
func TestRefreshEmptyKeptScalarWithReconciliation(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "progress: [Implementation started]\n", "", 1)
	input = strings.ReplaceAll(input, "\n", "\r\n") + "progress:\r\n  - |+ # owner note\r\n"
	// event records a separate progress item.
	//
	// Example: The report can replay progress and reconcile its source.
	const event = `{"event":"worker_progress","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"new progress"}}]}}}` + "\n"
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(reportPath, []byte(input), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte("schema: project-understanding/v1\nrecords: {}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600))
	require.NoError(t, refreshTestStatus(dir, "worker_progress"))
	out, err := os.ReadFile(reportPath)
	require.NoError(t, err)
	report, err := parse(out)
	require.NoError(t, err)
	require.Equal(t, []string{"", "new progress"}, report.Progress)
	require.Contains(t, string(out), "|+ # owner note\r\n")
	require.NotEmpty(t, report.Blockers)
}

// TestReplayBesideEmptyKeptScalarPreservesExistingItems checks shared scanning.
//
// Example: Appending and retracting a managed item preserves neighboring scalars.
func TestReplayBesideEmptyKeptScalarPreservesExistingItems(t *testing.T) {
	t.Parallel()
	input := []byte("progress:\r\n  - |+ # empty owner\r\n  - | # last owner\r\n    legacy scalar")
	var before, appended, retracted yaml.Node
	require.NoError(t, yaml.Unmarshal(input, &before))
	update := syncUpdate{Target: syncTargetProgress, ID: "one"}
	out, ok := applyNarrativeSyncField(input, update, "text", "new progress", "", false, false)
	require.True(t, ok)
	require.NoError(t, yaml.Unmarshal(out, &appended))
	oldItems := mapValue(before.Content[0], "progress").Content
	newItems := mapValue(appended.Content[0], "progress").Content
	require.Equal(t, oldItems[0].Value, newItems[0].Value)
	require.Equal(t, oldItems[1].Value, newItems[1].Value)
	out, ok = retractNarrativeSync(out, update, "new progress", true, "", false)
	require.True(t, ok)
	require.NoError(t, yaml.Unmarshal(out, &retracted))
	require.Equal(t, oldItems[1].Value, mapValue(retracted.Content[0], "progress").Content[1].Value)
	require.Contains(t, string(out), "# empty owner")
	require.Contains(t, string(out), "# last owner")
}

// TestRefreshForecastProjectionBesideEmptyKeptScalar checks comment range scanning.
//
// Example: A forecast basis can update while an empty progress item retains its comment.
func TestRefreshForecastProjectionBesideEmptyKeptScalar(t *testing.T) {
	t.Parallel()
	input := strings.Replace(linkedReport, "evidence: initial}", "evidence: initial, source_projection: {baseline_evidence: initial, target_basis: old}}", 1)
	input = strings.Replace(input, "progress: [Implementation started]\n", "progress:\n  - |+ # empty owner item\n  - Implementation started\n", 1)
	forecasts := strings.Replace(linkedForecasts, "    baseline_evidence: initial\n", "    baseline_evidence: initial\n    target_basis: new basis\n", 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(input), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project-understanding.yaml"), []byte(linkedLedger), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forecasts.yaml"), []byte(forecasts), 0600))
	require.NoError(t, refreshTestStatus(dir, "forecast_change"))
	out, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(out), "new basis")
	require.Contains(t, string(out), "# empty owner item")
}
