package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInvalidCorrectionLocatorsDoNotBlockLaterAssignments checks replay ordering.
//
// Example: A self-referencing or future correction cannot block a later owner.
func TestInvalidCorrectionLocatorsDoNotBlockLaterAssignments(t *testing.T) {
	for _, tc := range []struct {
		name, locator string
	}{
		{"self", `"corrects":1`},
		{"future", `"corrects":2`},
		{"same ordinal", `"corrects":1,"corrects_ordinal":0`},
		{"later ordinal", `"corrects":1,"corrects_ordinal":1`},
	} {
		t.Run(tc.name,
			// Verify an impossible locator is diagnosed without blocking valid work.
			//
			// Example: The log retains both the invalid correction and assignment.
			func(t *testing.T) {
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				log := `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1",` + tc.locator + `,"fields":{"owner":"mistaken"}}]}}}` + "\n" + `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"actual"}}]}}}` + "\n"
				require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				require.NoError(t, refreshTestStatus(dir, "assignment_change"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, "actual", report.Tasks[0].Owner)
				require.Empty(t, report.StatusSync.Groups)
				require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
				require.NoError(t, refreshTestStatus(dir, "assignment_change"))
				retry, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, result, retry)
			})
	}
}

// TestPersistedSelfCorrectionDoesNotBlockRetry checks existing queued state.
//
// Example: A previously saved impossible locator is diagnosed and removed.
func TestPersistedSelfCorrectionDoesNotBlockRetry(t *testing.T) {
	// saved is a previously persisted impossible correction and later assignment.
	//
	// Example: Retry removes the invalid group without discarding the assignment.
	const saved = `status_sync: {"cursor":2,"applied":{},"latest":{},"pending":{},"groups":{"[1,0]":{"line":1,"ordinal":0,"update":"{\"target\":\"task\",\"id\":\"1\",\"corrects\":1,\"fields\":{\"owner\":\"mistaken\"}}"},"[2,0]":{"line":2,"ordinal":0,"update":"{\"target\":\"task\",\"id\":\"1\",\"fields\":{\"owner\":\"actual\"}}"}}}`
	// log preserves the two immutable events referenced by the saved state.
	//
	// Example: The correction cannot precede itself.
	const log = `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"mistaken"}}]}}}` + "\n" + `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"actual"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport+saved+"\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	require.NoError(t, refreshTestStatus(dir, "assignment_change"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "actual", report.Tasks[0].Owner)
	require.Empty(t, report.StatusSync.Groups)
	require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
}

// TestCorrectionCanNameEarlierSameLineOrdinal checks valid intra-event ordering.
//
// Example: A second correction may name the first correction on the same line.
func TestCorrectionCanNameEarlierSameLineOrdinal(t *testing.T) {
	// log contains two ordered corrections in one canonical event.
	//
	// Example: The last correction names ordinal zero of its own line.
	const log = `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"first"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"second"}},{"target":"task","id":"1","corrects":2,"corrects_ordinal":0,"fields":{"owner":"third"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, "third", report.Tasks[0].Owner)
	require.Empty(t, report.StatusSync.Groups)
}

// TestLegacyFieldSelfCorrectionIsRejected checks field-wise persisted state.
//
// Example: A saved correction cannot authorize its own owner value.
func TestLegacyFieldSelfCorrectionIsRejected(t *testing.T) {
	// saved is a legacy field pending entry with a self-referencing locator.
	//
	// Example: The invalid correction must never be projected on retry.
	const saved = `status_sync: {"cursor":1,"applied":{},"latest":{"[\"task\",\"1\",\"\",\"\"]/owner":1},"pending":{"[\"task\",\"1\",\"\",\"\"]/owner":{"line":1,"update":{"target":"task","id":"1","corrects":1,"fields":{"owner":"mistaken"}}}}}`
	// log is the exact immutable source for the saved field.
	//
	// Example: Its only update cannot precede itself.
	const log = `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"mistaken"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, []byte(linkedReport+saved+"\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Empty(t, report.Tasks[0].Owner)
	require.Empty(t, report.StatusSync.Pending)
	require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
}

// TestLegacySameLineCorrectionUsesUniqueSourceOrdinal checks old pending compatibility.
//
// Example: A uniquely identified later update can correct ordinal zero.
func TestLegacySameLineCorrectionUsesUniqueSourceOrdinal(t *testing.T) {
	for _, tc := range []struct {
		name, duplicate string
		applies         bool
	}{
		{"unique", "", true},
		{"ambiguous", `,{"target":"task","id":"1","corrects":2,"corrects_ordinal":0,"fields":{"owner":"third"}}`, false},
	} {
		t.Run(tc.name,
			// Replay one field saved without an ordinal.
			//
			// Example: Two indistinguishable occurrences remain pending.
			func(t *testing.T) {
				// saved is the field-wise state produced before ordinal storage.
				//
				// Example: The previous owner was projected on line two.
				const saved = `status_sync: {"cursor":2,"applied":{"[\"task\",\"1\",\"\",\"\"]/owner":"second"},"latest":{"[\"task\",\"1\",\"\",\"\"]/owner":2},"pending":{"[\"task\",\"1\",\"\",\"\"]/owner":{"line":2,"update":{"target":"task","id":"1","corrects":2,"corrects_ordinal":0,"fields":{"owner":"third"}}}}}`
				log := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"first"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"second"}},{"target":"task","id":"1","corrects":2,"corrects_ordinal":0,"fields":{"owner":"third"}}` + tc.duplicate + `]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				input := strings.Replace(linkedReport, "title: First,", `title: First, owner: second,`, 1)
				require.NoError(t, os.WriteFile(path, []byte(input+saved+"\n"), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				require.NoError(t, refreshTestStatus(dir, "correction"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				if tc.applies {
					require.Equal(t, "third", report.Tasks[0].Owner)
					require.Empty(t, report.StatusSync.Pending)
				} else {
					require.Equal(t, "second", report.Tasks[0].Owner)
					require.NotEmpty(t, report.StatusSync.Pending)
				}
			})
	}
}

// TestLegacyCancellationRejectsSelfCorrectionsBeforeAtomicReplay checks grouped fields.
//
// Example: An invalid lifecycle correction cannot cancel a task through legacy pending state.
func TestLegacyCancellationRejectsSelfCorrectionsBeforeAtomicReplay(t *testing.T) {
	// log contains a cancellation whose correction names itself.
	//
	// Example: All three fields must remain unapplied.
	const log = `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"lifecycle":"cancelled","status":"unknown","evidence":"withdrawn"}}]}}}` + "\n"
	state := syncState{Cursor: 1, Applied: map[string]string{}, Latest: map[string]int{}, Pending: map[string]syncPending{}}
	for field, value := range map[string]string{"lifecycle": "cancelled", "status": "unknown", "evidence": "withdrawn"} {
		update := syncUpdate{Target: syncTargetTask, ID: "1", Corrects: 1, Fields: map[string]string{field: value}}
		key := syncTargetKey(update, field)
		state.Latest[key] = 1
		state.Pending[key] = syncPending{Line: 1, Update: update}
	}
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	input, err := putSyncState([]byte(linkedReport), string(encoded))
	require.NoError(t, err)
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	path := filepath.Join(dir, "latest-status-report.yaml")
	require.NoError(t, os.WriteFile(path, input, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	require.NoError(t, refreshTestStatus(dir, "correction"))
	result, err := os.ReadFile(path)
	require.NoError(t, err)
	report, err := parse(result)
	require.NoError(t, err)
	require.Equal(t, statusInProgress, report.Tasks[0].Status)
	require.NotEqual(t, taskLifecycleCancelled, report.Tasks[0].Lifecycle)
	require.Empty(t, report.Tasks[0].Evidence)
	require.Empty(t, report.StatusSync.Pending)
	require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
}

// TestLegacySameLineCorrectionRequiresMatchingPriorField checks provenance.
//
// Example: A different task or field cannot authorize an owner correction.
func TestLegacySameLineCorrectionRequiresMatchingPriorField(t *testing.T) {
	for _, tc := range []struct {
		name, prior string
	}{
		{"other task", `{"target":"task","id":"2","corrects":1,"fields":{"owner":"other"}}`},
		{"other field", `{"target":"task","id":"1","corrects":1,"fields":{"status":"IN PROGRESS"}}`},
	} {
		t.Run(tc.name,
			// Check an unrelated predecessor beside a saved legacy correction.
			//
			// Example: The existing owner remains unchanged.
			func(t *testing.T) {
				// saved is a field-wise correction naming ordinal zero of line two.
				//
				// Example: The saved owner remains "second" when its locator is invalid.
				const saved = `status_sync: {"cursor":2,"applied":{"[\"task\",\"1\",\"\",\"\"]/owner":"second"},"latest":{"[\"task\",\"1\",\"\",\"\"]/owner":2},"pending":{"[\"task\",\"1\",\"\",\"\"]/owner":{"line":2,"update":{"target":"task","id":"1","corrects":2,"corrects_ordinal":0,"fields":{"owner":"third"}}}}}`
				log := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"first"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[` + tc.prior + `,{"target":"task","id":"1","corrects":2,"corrects_ordinal":0,"fields":{"owner":"third"}}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				input := strings.Replace(linkedReport, "title: First,", `title: First, owner: second,`, 1)
				require.NoError(t, os.WriteFile(path, []byte(input+saved+"\n"), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				require.NoError(t, refreshTestStatus(dir, "correction"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, "second", report.Tasks[0].Owner)
				require.Empty(t, report.StatusSync.Pending)
				require.Contains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
			})
	}
}

// TestLegacyEarlierCorrectionRequiresMatchingLatestPrior checks cross-line provenance.
//
// Example: A prior assignment to another task cannot authorize this correction.
func TestLegacyEarlierCorrectionRequiresMatchingLatestPrior(t *testing.T) {
	for _, tc := range []struct {
		name, prior, middle string
		applies, pending    bool
	}{
		{"valid", `{"target":"task","id":"1","fields":{"owner":"second"}}`, `{}`, true, false},
		{"other task", `{"target":"task","id":"2","fields":{"owner":"other"}}`, `{}`, false, false},
		{"other field", `{"target":"task","id":"1","fields":{"status":"IN PROGRESS"}}`, `{}`, false, false},
		{"newer assignment", `{"target":"task","id":"1","fields":{"owner":"first"}}`, `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"second"}}]}}}`, false, false},
		{"missing source", ``, `{}`, false, true},
	} {
		t.Run(tc.name,
			// Check one predecessor and any intervening field update.
			//
			// Example: Missing source remains pending for reconciliation.
			func(t *testing.T) {
				// saved is legacy field state for a line-three correction.
				//
				// Example: Owner "second" remains until a valid correction applies.
				const saved = `status_sync: {"cursor":3,"applied":{"[\"task\",\"1\",\"\",\"\"]/owner":"second"},"latest":{"[\"task\",\"1\",\"\",\"\"]/owner":3},"pending":{"[\"task\",\"1\",\"\",\"\"]/owner":{"line":3,"update":{"target":"task","id":"1","corrects":1,"fields":{"owner":"third"}}}}}`
				prior := `{}`
				if tc.prior != "" {
					prior = `{"event":"assignment_change","details":{"status_update":{"updates":[` + tc.prior + `]}}}`
				}
				log := prior + "\n" + tc.middle + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"third"}}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				path := filepath.Join(dir, "latest-status-report.yaml")
				input := strings.Replace(linkedReport, "title: First,", `title: First, owner: second,`, 1)
				require.NoError(t, os.WriteFile(path, []byte(input+saved+"\n"), 0600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				require.NoError(t, refreshTestStatus(dir, "correction"))
				result, err := os.ReadFile(path)
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				if tc.applies {
					require.Equal(t, "third", report.Tasks[0].Owner)
				} else {
					require.Equal(t, "second", report.Tasks[0].Owner)
				}
				if tc.pending {
					require.NotEmpty(t, report.StatusSync.Pending)
				} else {
					require.Empty(t, report.StatusSync.Pending)
				}
			})
	}
}

// TestLegacyCorrectionIgnoresInvalidInterveningUpdates compares fresh replay.
//
// Example: A self-referencing correction does not supersede the real prior owner.
func TestLegacyCorrectionIgnoresInvalidInterveningUpdates(t *testing.T) {
	for _, tc := range []struct {
		name, event, update string
	}{
		{"self", "correction", `{"target":"task","id":"1","corrects":2,"fields":{"owner":"mistaken"}}`},
		{"future", "correction", `{"target":"task","id":"1","corrects":3,"fields":{"owner":"mistaken"}}`},
		{"wrong event", "decision", `{"target":"task","id":"1","corrects":1,"fields":{"owner":"mistaken"}}`},
		{"invalid value", "decision", `{"target":"task","id":"1","fields":{"owner":"mistaken","status":"impossible"}}`},
	} {
		t.Run(tc.name,
			// Compare a saved field with replay from the full immutable log.
			//
			// Example: Both paths keep the valid later correction.
			func(t *testing.T) {
				// saved retains the previously applied owner and pending correction.
				//
				// Example: A restart must match replay from the beginning.
				const saved = `status_sync: {"cursor":3,"applied":{"[\"task\",\"1\",\"\",\"\"]/owner":"second"},"latest":{"[\"task\",\"1\",\"\",\"\"]/owner":3},"pending":{"[\"task\",\"1\",\"\",\"\"]/owner":{"line":3,"update":{"target":"task","id":"1","corrects":1,"fields":{"owner":"third"}}}}}`
				log := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"second"}}]}}}` + "\n" + `{"event":"` + tc.event + `","details":{"status_update":{"updates":[` + tc.update + `]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"third"}}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				fresh, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				input := strings.Replace(linkedReport, "title: First,", `title: First, owner: second,`, 1) + saved + "\n"
				replayed, _, err := projectLogStatus(dir, []byte(input), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				freshReport, err := parse(fresh)
				require.NoError(t, err)
				replayedReport, err := parse(replayed)
				require.NoError(t, err)
				require.Equal(t, "third", freshReport.Tasks[0].Owner)
				require.Equal(t, freshReport.Tasks[0].Owner, replayedReport.Tasks[0].Owner)
				require.Empty(t, replayedReport.StatusSync.Pending)
			})
	}
}

// TestLegacyCorrectionRespectsInterveningRetraction checks narrative version ordering.
//
// Example: A valid withdrawal cannot be undone by correcting an older claim.
func TestLegacyCorrectionRespectsInterveningRetraction(t *testing.T) {
	for _, tc := range []struct {
		name, event, subject, locator string
		withdraws                     bool
	}{
		{"valid", "correction", "one", `"corrects":1`, true},
		{"self", "correction", "one", `"corrects":2`, false},
		{"other subject", "correction", "other", `"corrects":1`, false},
		{"wrong event", "decision", "one", `"corrects":1`, false},
	} {
		t.Run(tc.name,
			// Compare legacy pending replay with fresh replay of the same history.
			//
			// Example: An invalid retraction cannot supersede the prior claim.
			func(t *testing.T) {
				log := `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first","evidence":"proof"}}]}}}` + "\n" + `{"event":"` + tc.event + `","details":{"status_update":{"updates":[{"target":"progress","id":"` + tc.subject + `",` + tc.locator + `,"retract":true}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one","corrects":1,"fields":{"text":"third"}}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				fresh, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				update := syncUpdate{Target: syncTargetProgress, ID: "one", Corrects: 1, Fields: map[string]string{"text": "third"}}
				key := syncTargetKey(update, "text")
				state := syncState{Cursor: 3, Applied: map[string]string{}, Latest: map[string]int{key: 3}, Pending: map[string]syncPending{key: {Line: 3, Update: update}}}
				input := linkedReport
				if !tc.withdraws {
					state.Applied[key] = "first"
					state.Applied[syncTargetKey(update, "evidence")] = "proof"
					input = strings.Replace(input, "progress: [Implementation started]", `progress: [Implementation started, {subject_ref: one, text: first, evidence: proof}]`, 1)
				}
				encoded, err := json.Marshal(state)
				require.NoError(t, err)
				initial, err := putSyncState([]byte(input), string(encoded))
				require.NoError(t, err)
				replayed, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				freshReport, err := parse(fresh)
				require.NoError(t, err)
				replayedReport, err := parse(replayed)
				require.NoError(t, err)
				require.Equal(t, freshReport.Progress, replayedReport.Progress)
			})
	}
}

// TestLegacyRetractionUsesLatestNarrativeEvidence checks subject-level chronology.
//
// Example: A retraction names later evidence rather than the older text update.
func TestLegacyRetractionUsesLatestNarrativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, locator string
		withdraws     bool
	}{
		{"latest evidence", `"corrects":2`, true},
		{"stale text", `"corrects":1`, false},
		{"self reference", `"corrects":3`, false},
	} {
		t.Run(tc.name,
			// Compare fresh and saved-field replay after separate evidence.
			//
			// Example: A stale retraction does not prevent a valid correction.
			func(t *testing.T) {
				log := `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first"}}]}}}` + "\n" + `{"event":"verification_result","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"evidence":"proof"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one",` + tc.locator + `,"retract":true}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one","corrects":1,"fields":{"text":"third"}}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				fresh, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				update := syncUpdate{Target: syncTargetProgress, ID: "one", Corrects: 1, Fields: map[string]string{"text": "third"}}
				key := syncTargetKey(update, "text")
				state := syncState{Cursor: 4, Applied: map[string]string{}, Latest: map[string]int{key: 4}, Pending: map[string]syncPending{key: {Line: 4, Update: update}}}
				input := linkedReport
				if !tc.withdraws {
					state.Applied[key] = "first"
					state.Applied[syncTargetKey(update, "evidence")] = "proof"
					input = strings.Replace(input, "progress: [Implementation started]", `progress: [Implementation started, {subject_ref: one, text: first, evidence: proof}]`, 1)
				}
				encoded, err := json.Marshal(state)
				require.NoError(t, err)
				initial, err := putSyncState([]byte(input), string(encoded))
				require.NoError(t, err)
				replayed, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				freshReport, err := parse(fresh)
				require.NoError(t, err)
				replayedReport, err := parse(replayed)
				require.NoError(t, err)
				require.Equal(t, freshReport.Progress, replayedReport.Progress)
			})
	}
}

// TestLegacyTaskRetractionCannotAuthorizeCorrection checks predecessor validity.
//
// Example: A task evidence correction cannot name an invalid task retraction.
func TestLegacyTaskRetractionCannotAuthorizeCorrection(t *testing.T) {
	// log names a task retraction, which is not an allowed narrative operation.
	//
	// Example: Fresh and legacy replay both retain the original evidence.
	const log = `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"evidence":"first proof"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"retract":true}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":2,"fields":{"evidence":"new proof"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	fresh, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	update := syncUpdate{Target: syncTargetTask, ID: "1", Corrects: 2, Fields: map[string]string{"evidence": "new proof"}}
	key := syncTargetKey(update, "evidence")
	state := syncState{Cursor: 3, Applied: map[string]string{key: "first proof"}, Latest: map[string]int{key: 3}, Pending: map[string]syncPending{key: {Line: 3, Update: update}}}
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	input := strings.Replace(linkedReport, "title: First,", "title: First, evidence: first proof,", 1)
	initial, err := putSyncState([]byte(input), string(encoded))
	require.NoError(t, err)
	replayed, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	freshReport, err := parse(fresh)
	require.NoError(t, err)
	replayedReport, err := parse(replayed)
	require.NoError(t, err)
	require.Equal(t, "first proof", freshReport.Tasks[0].Evidence)
	require.Equal(t, freshReport.Tasks[0].Evidence, replayedReport.Tasks[0].Evidence)
}

// TestLegacyTextCorrectionAfterEvidenceCorrectionMatchesFreshReplay checks independent fields.
//
// Example: A valid evidence correction does not hold an older text correction.
func TestLegacyTextCorrectionAfterEvidenceCorrectionMatchesFreshReplay(t *testing.T) {
	for _, tc := range []struct {
		name, withdrawal string
		withdraws        bool
	}{
		{"independent evidence", "", false},
		{"later retraction", `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one","corrects":2,"retract":true}]}}}` + "\n", true},
	} {
		t.Run(tc.name,
			// Compare fresh and saved replay around a corrected evidence version.
			//
			// Example: A later retraction names the corrected evidence.
			func(t *testing.T) {
				prefix := `{"event":"decision","details":{"status_update":{"updates":[{"target":"progress","id":"one","fields":{"text":"first","evidence":"proof"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one","corrects":1,"fields":{"evidence":"new proof"}}]}}}` + "\n" + tc.withdrawal
				log := prefix + `{"event":"correction","details":{"status_update":{"updates":[{"target":"progress","id":"one","corrects":1,"fields":{"text":"third"}}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				fresh, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				update := syncUpdate{Target: syncTargetProgress, ID: "one", Corrects: 1, Fields: map[string]string{"text": "third"}}
				key := syncTargetKey(update, "text")
				line := 3
				if tc.withdraws {
					line = 4
				}
				state := syncState{Cursor: line, Applied: map[string]string{}, Latest: map[string]int{key: line}, Pending: map[string]syncPending{key: {Line: line, Update: update}}}
				input := linkedReport
				if !tc.withdraws {
					state.Applied[key] = "first"
					state.Applied[syncTargetKey(update, "evidence")] = "new proof"
					input = strings.Replace(input, "progress: [Implementation started]", `progress: [Implementation started, {subject_ref: one, text: first, evidence: new proof}]`, 1)
				}
				encoded, err := json.Marshal(state)
				require.NoError(t, err)
				initial, err := putSyncState([]byte(input), string(encoded))
				require.NoError(t, err)
				replayed, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				freshReport, err := parse(fresh)
				require.NoError(t, err)
				replayedReport, err := parse(replayed)
				require.NoError(t, err)
				require.Equal(t, freshReport.Progress, replayedReport.Progress)
			})
	}
}

// TestLegacyInvalidBaseCannotAuthorizeCorrection checks complete source shape.
//
// Example: A valid owner alongside an invalid status cannot authorize a correction.
func TestLegacyInvalidBaseCannotAuthorizeCorrection(t *testing.T) {
	// log contains one invalid mixed assignment and a later correction.
	//
	// Example: Both fresh and legacy replay preserve the absent owner.
	const log = `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"mistaken","status":"impossible"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"third"}}]}}}` + "\n"
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
	fresh, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	update := syncUpdate{Target: syncTargetTask, ID: "1", Corrects: 1, Fields: map[string]string{"owner": "third"}}
	key := syncTargetKey(update, "owner")
	state := syncState{Cursor: 2, Applied: map[string]string{}, Latest: map[string]int{key: 2}, Pending: map[string]syncPending{key: {Line: 2, Update: update}}}
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	initial, err := putSyncState([]byte(linkedReport), string(encoded))
	require.NoError(t, err)
	replayed, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
	require.NoError(t, err)
	freshReport, err := parse(fresh)
	require.NoError(t, err)
	replayedReport, err := parse(replayed)
	require.NoError(t, err)
	require.Empty(t, freshReport.Tasks[0].Owner)
	require.Equal(t, freshReport.Tasks[0].Owner, replayedReport.Tasks[0].Owner)
}

// TestLegacyCorrectionRequiresValidCurrentTransaction checks atomic input validation.
//
// Example: An invalid status prevents an otherwise valid owner correction.
func TestLegacyCorrectionRequiresValidCurrentTransaction(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		applies      bool
	}{
		{"invalid sibling field", `"owner":"third","status":"impossible"`, false},
		{"valid correction", `"owner":"third"`, true},
	} {
		t.Run(tc.name,
			// Compare fresh replay with legacy field-wise recovery.
			//
			// Example: An invalid atomic update cannot project its owner alone.
			func(t *testing.T) {
				log := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"first"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{` + tc.fields + `}}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				fresh, _, err := projectLogStatus(dir, []byte(linkedReport), ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				update := syncUpdate{Target: syncTargetTask, ID: "1", Corrects: 1, Fields: map[string]string{"owner": "third"}}
				key := syncTargetKey(update, "owner")
				state := syncState{Cursor: 2, Applied: map[string]string{key: "first"}, Latest: map[string]int{key: 2}, Pending: map[string]syncPending{key: {Line: 2, Update: update}}}
				encoded, err := json.Marshal(state)
				require.NoError(t, err)
				input := strings.Replace(linkedReport, "title: First,", "title: First, owner: first,", 1)
				initial, err := putSyncState([]byte(input), string(encoded))
				require.NoError(t, err)
				replayed, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				freshReport, err := parse(fresh)
				require.NoError(t, err)
				replayedReport, err := parse(replayed)
				require.NoError(t, err)
				require.Equal(t, freshReport.Tasks[0].Owner, replayedReport.Tasks[0].Owner)
				if tc.applies {
					require.Equal(t, "third", replayedReport.Tasks[0].Owner)
				} else {
					require.Equal(t, "first", replayedReport.Tasks[0].Owner)
					require.Contains(t, replayedReport.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
				}
			})
	}
}

// TestLegacyCorrectionHoldsStructuredCompanions checks atomic legacy recovery.
//
// Example: An owner fragment cannot apply without its accompanying effort change.
func TestLegacyCorrectionHoldsStructuredCompanions(t *testing.T) {
	for _, tc := range []struct {
		name, companion string
	}{
		{"invalid effort", `"values":{"effort":{"min_minutes":10,"max_minutes":5,"kind":"elapsed","basis":"guess"}}`},
		{"valid effort", `"values":{"effort":{"min_minutes":10,"max_minutes":20,"kind":"elapsed","basis":"guess"}}`},
		{"clear context", `"clear":["context"]`},
	} {
		t.Run(tc.name,
			// Preserve the whole unresolved transaction and its existing owner.
			//
			// Example: Both valid and invalid effort values remain reviewable.
			func(t *testing.T) {
				log := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"first"}}]}}}` + "\n" + `{"event":"correction","details":{"status_update":{"updates":[{"target":"task","id":"1","corrects":1,"fields":{"owner":"third"},` + tc.companion + `}]}}}` + "\n"
				dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(log), 0600))
				update := syncUpdate{Target: syncTargetTask, ID: "1", Corrects: 1, Fields: map[string]string{"owner": "third"}}
				key := syncTargetKey(update, "owner")
				state := syncState{Cursor: 2, Applied: map[string]string{key: "first"}, Latest: map[string]int{key: 2}, Pending: map[string]syncPending{key: {Line: 2, Update: update}}}
				encoded, err := json.Marshal(state)
				require.NoError(t, err)
				input := strings.Replace(linkedReport, "title: First,", "title: First, owner: first,", 1)
				initial, err := putSyncState([]byte(input), string(encoded))
				require.NoError(t, err)
				result, _, err := projectLogStatus(dir, initial, ledgerSource{}, forecastSource{})
				require.NoError(t, err)
				report, err := parse(result)
				require.NoError(t, err)
				require.Equal(t, "first", report.Tasks[0].Owner)
				require.NotEmpty(t, report.StatusSync.Pending)
				require.Contains(t, report.Blockers, "Status refresh incomplete: status updates remain pending manual reconciliation")
				require.NotContains(t, report.Blockers, "Status refresh incomplete: invalid status_update requires manual review")
			})
	}
}
