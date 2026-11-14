package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// legacyWithdrawalSourceLog supplies the immutable claim and its retraction.
//
// Example: Verify the pending retraction names the earlier narrative subject.
const legacyWithdrawalSourceLog = `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"work:one","fields":{"text":"withdrawn claim","evidence":"obsolete proof"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"work:one","corrects":1,"retract":true}]}}}` + "\n"

// TestLegacyWithdrawalCancelsUnpublishedNarrative prevents stale replay after correction.
//
// Example: Retract a pending claim before it appears while keeping unrelated progress.
func TestLegacyWithdrawalCancelsUnpublishedNarrative(t *testing.T) {
	t.Parallel()
	// saved records a never-published claim and its later retraction.
	//
	// Example: Resume pending fields after a restart without resurrecting withdrawn text.
	const saved = `{"cursor":2,"applied":{},"latest":{"[\"progress\",\"work:one\",\"\",\"\"]/text":1,"[\"progress\",\"work:one\",\"\",\"\"]/evidence":1,"[\"progress\",\"work:one\",\"\",\"\"]/retract":2},"pending":{"[\"progress\",\"work:one\",\"\",\"\"]/text":{"line":1,"update":{"target":"progress","id":"work:one","fields":{"text":"withdrawn claim"}}},"[\"progress\",\"work:one\",\"\",\"\"]/evidence":{"line":1,"update":{"target":"progress","id":"work:one","fields":{"evidence":"obsolete proof"}}},"[\"progress\",\"work:one\",\"\",\"\"]/retract":{"line":2,"update":{"target":"progress","id":"work:one","corrects":1,"retract":true,"fields":null}}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(legacyWithdrawalSourceLog), 0600))
	initial, err := putSyncState([]byte(linkedReport), saved)
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "Implementation started")
	require.NotContains(t, report.Progress, "withdrawn claim")
	require.NotContains(t, string(result), "obsolete proof")
	require.Empty(t, report.StatusSync.Pending)
	retry, _, err := projectLogStatus(dir, result, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	require.Equal(t, result, retry)
}

// TestLegacyWithdrawalKeepsOwnerAuthoredSubject checks a conflicting existing entry.
//
// Example: Cancelling an unpublished claim does not remove owner-authored text.
func TestLegacyWithdrawalKeepsOwnerAuthoredSubject(t *testing.T) {
	t.Parallel()
	// saved contains an unpublished claim and its subsequent withdrawal.
	//
	// Example: Preserve an independently authored entry with the same subject.
	const saved = `{"cursor":2,"applied":{},"latest":{"[\"progress\",\"work:one\",\"\",\"\"]/text":1,"[\"progress\",\"work:one\",\"\",\"\"]/retract":2},"pending":{"[\"progress\",\"work:one\",\"\",\"\"]/text":{"line":1,"update":{"target":"progress","id":"work:one","fields":{"text":"withdrawn claim"}}},"[\"progress\",\"work:one\",\"\",\"\"]/retract":{"line":2,"update":{"target":"progress","id":"work:one","corrects":1,"retract":true,"fields":null}}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(legacyWithdrawalSourceLog), 0600))
	input := strings.Replace(linkedReport, "progress: [Implementation started]", "progress: [Implementation started, {subject_ref: 'work:one', text: 'owner text'}]", 1)
	require.NotEqual(t, linkedReport, input)
	initial, err := putSyncState([]byte(input), saved)
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Contains(t, report.Progress, "owner text")
	require.NotContains(t, report.Progress, "withdrawn claim")
	require.Empty(t, report.StatusSync.Pending)
}

// TestLegacyWithdrawalWaitsForSourceProvenance preserves unresolved legacy state.
//
// Example: Missing source events cannot authorize or publish a withdrawn claim.
func TestLegacyWithdrawalWaitsForSourceProvenance(t *testing.T) {
	t.Parallel()
	// saved retains a claim and retraction when their original events are unavailable.
	//
	// Example: Neither pending field is applied without its immutable predecessor.
	const saved = `{"cursor":2,"applied":{},"latest":{"[\"progress\",\"work:one\",\"\",\"\"]/text":1,"[\"progress\",\"work:one\",\"\",\"\"]/retract":2},"pending":{"[\"progress\",\"work:one\",\"\",\"\"]/text":{"line":1,"update":{"target":"progress","id":"work:one","fields":{"text":"withdrawn claim"}}},"[\"progress\",\"work:one\",\"\",\"\"]/retract":{"line":2,"update":{"target":"progress","id":"work:one","corrects":1,"retract":true,"fields":null}}}}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n{}\n"), 0600))
	initial, err := putSyncState([]byte(linkedReport), saved)
	require.NoError(t, err)
	result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.NotContains(t, report.Progress, "withdrawn claim")
	require.Len(t, report.StatusSync.Pending, 2)
}
