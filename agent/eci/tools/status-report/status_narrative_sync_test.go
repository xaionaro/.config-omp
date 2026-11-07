package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestNarrativeSyncPreservesOwnerText checks keyed updates and owner-authored entries.
//
// Example: An event updates its own subject while legacy bullets remain intact.
func TestNarrativeSyncPreservesOwnerText(t *testing.T) {
	original := []byte("progress: [legacy, {subject_ref: owner, text: 'owner text'}] # keep\n")
	u := syncUpdate{Target: syncTargetProgress, ID: "work:one", Fields: map[string]string{"text": "Started"}}
	require.True(t, validNarrativeSyncUpdate(u))
	got, ok := applyNarrativeSyncField(original, u, "text", "Started", "", false, false)
	require.True(t, ok)
	require.Contains(t, string(got), "legacy")
	require.Contains(t, string(got), "# keep")
	require.Contains(t, string(got), "Started")
	changed, ok := applyNarrativeSyncField(got, u, "text", "Finished", "Started", true, true)
	require.True(t, ok)
	require.Contains(t, string(changed), "Finished")
	require.NotContains(t, string(changed), "Started")
	u.ID = "owner"
	unchanged, ok := applyNarrativeSyncField(changed, u, "text", "Overwrite", "", false, false)
	require.False(t, ok)
	require.Equal(t, changed, unchanged)
}

// TestNarrativeSyncEvidenceAndDuplicateSubjects checks evidence ownership and ambiguous identities.
//
// Example: Evidence is attached only to a subject owned by the synchronizer.
func TestNarrativeSyncEvidenceAndDuplicateSubjects(t *testing.T) {
	u := syncUpdate{Target: syncTargetVerification, ID: "one", Fields: map[string]string{"evidence": "proof"}}
	source := []byte("verification:\n  - subject_ref: one\n    text: checked\nnext_focus: [next]\n")
	_, ok := applyNarrativeSyncField(source, u, "evidence", "proof", "", false, false)
	require.False(t, ok)
	got, ok := applyNarrativeSyncField(source, u, "evidence", "proof", "", false, true)
	require.True(t, ok)
	require.Contains(t, string(got), `"evidence": "proof"`)
	require.Contains(t, string(got), "next_focus: [next]")
	duplicate := []byte("verification: [{subject_ref: one, text: a}, {subject_ref: one, text: b}]\n")
	_, ok = applyNarrativeSyncField(duplicate, u, "evidence", "proof", "", false, true)
	require.False(t, ok)
}

// TestNarrativeSyncBlockAppend checks that a new item preserves following content.
//
// Example: A block sequence gains a keyed item before the next section.
func TestNarrativeSyncBlockAppend(t *testing.T) {
	source := []byte("progress:\n  - legacy\n# following\nverification: []\n")
	u := syncUpdate{Target: syncTargetProgress, ID: "one", Fields: map[string]string{"text": "new"}}
	got, ok := applyNarrativeSyncField(source, u, "text", "new", "", false, false)
	require.True(t, ok)
	require.Contains(t, string(got), "  - legacy\n  - {subject_ref: \"one\", text: \"new\"}\n# following\nverification: []")
}

// TestNarrativeSyncItemOwnedRejectsEditedText checks managed-item identity.
//
// Example: Owner changes to the text prevent evidence updates.
func TestNarrativeSyncItemOwnedRejectsEditedText(t *testing.T) {
	u := syncUpdate{Target: syncTargetProgress, ID: "one"}
	data := []byte("progress: [{subject_ref: one, text: changed}]\n")
	require.False(t, narrativeSyncItemOwned(data, u, "original", true))
	require.False(t, narrativeSyncItemOwned(data, u, "", false))
	require.True(t, narrativeSyncItemOwned(data, u, "changed", true))
}

// TestNarrativeSyncSameValueIsApplied checks repeated managed values terminate successfully.
//
// Example: An event repeating the current text does not remain pending.
func TestNarrativeSyncSameValueIsApplied(t *testing.T) {
	source := []byte("progress: [{subject_ref: one, text: same}]\n")
	u := syncUpdate{Target: syncTargetProgress, ID: "one", Fields: map[string]string{"text": "same"}}
	got, ok := applyNarrativeSyncField(source, u, "text", "same", "same", true, true)
	require.True(t, ok)
	require.Equal(t, source, got)
}

// TestRetractNarrativeSyncPreservesOtherEntries checks exact managed retraction.
//
// Example: A corrected subject disappears while legacy text and comments remain.
func TestRetractNarrativeSyncPreservesOtherEntries(t *testing.T) {
	u := syncUpdate{Target: syncTargetProgress, ID: "one"}
	source := []byte("progress: [legacy, {subject_ref: one, text: done, evidence: proof}, other] # keep\n")
	got, ok := retractNarrativeSync(source, u, "done", true, "proof", true)
	require.True(t, ok)
	require.Contains(t, string(got), "legacy")
	require.Contains(t, string(got), "other")
	require.Contains(t, string(got), "# keep")
	require.NotContains(t, string(got), "subject_ref")
	_, ok = retractNarrativeSync(source, u, "old", true, "proof", true)
	require.False(t, ok)
	_, ok = retractNarrativeSync(source, u, "done", true, "old proof", true)
	require.False(t, ok)
}

// TestRetractNarrativeSyncBlockList checks block entry removal and comment retention.
//
// Example: A following section's comment survives retraction of the only keyed item.
func TestRetractNarrativeSyncBlockList(t *testing.T) {
	u := syncUpdate{Target: syncTargetProgress, ID: "one"}
	source := []byte("progress:\n  - subject_ref: one\n    text: done\n# next section\nverification: []\n")
	got, ok := retractNarrativeSync(source, u, "done", true, "", false)
	require.True(t, ok)
	require.Contains(t, string(got), "# next section\nverification: []")
	require.NotContains(t, string(got), "subject_ref")
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(got, &doc))
	require.Empty(t, mapValue(doc.Content[0], "progress").Content)
}

// TestNarrativeEvidenceProtectsEditedText checks ownership of the whole subject.
//
// Example: An owner text edit blocks a later automatic evidence update.
func TestNarrativeEvidenceProtectsEditedText(t *testing.T) {
	source := []byte("progress: [{subject_ref: one, text: revised, evidence: proof}]\n")
	update := syncUpdate{Target: syncTargetProgress, ID: "one", Fields: map[string]string{"evidence": "new"}}
	got, ok := applyNarrativeSyncField(source, update, "evidence", "new", "proof", true, false)
	require.False(t, ok)
	require.Equal(t, source, got)
}

// TestRetractNarrativePreservesInlineComment checks a comment inside a flow mapping.
//
// Example: A comment added by an owner prevents automatic removal of the entry.
func TestRetractNarrativePreservesInlineComment(t *testing.T) {
	source := []byte("progress: [{subject_ref: one, # owner comment\n text: done}]\n")
	update := syncUpdate{Target: syncTargetProgress, ID: "one"}
	got, ok := retractNarrativeSync(source, update, "done", true, "", false)
	require.False(t, ok)
	require.Equal(t, source, got)
}

// TestNarrativeOwnershipRequiresMembership checks that matching text alone grants no ownership.
//
// Example: A value without an applied metadata entry remains owner-authored.
func TestNarrativeOwnershipRequiresMembership(t *testing.T) {
	source := []byte("progress: [{subject_ref: one, text: same}]\n")
	update := syncUpdate{Target: syncTargetProgress, ID: "one", Fields: map[string]string{"text": "new"}}
	require.False(t, narrativeSyncItemOwned(source, update, "same", false))
	got, ok := applyNarrativeSyncField(source, update, "text", "new", "same", false, false)
	require.False(t, ok)
	require.Equal(t, source, got)
	_, ok = retractNarrativeSync(source, update, "same", false, "", false)
	require.False(t, ok)
}

// TestRetractNarrativeAllowsHashInQuotedValues checks references containing hash characters.
//
// Example: A canonical record reference is not mistaken for a YAML comment.
func TestRetractNarrativeAllowsHashInQuotedValues(t *testing.T) {
	update := syncUpdate{Target: syncTargetProgress, ID: "one"}
	for _, source := range []string{
		"progress: [{subject_ref: one, text: done, evidence: 'ledger.yaml#/records/one'}]\n",
		"progress:\n  - subject_ref: one\n    text: done\n    evidence: 'ledger.yaml#/records/one'\n",
	} {
		got, ok := retractNarrativeSync([]byte(source), update, "done", true, "ledger.yaml#/records/one", true)
		require.True(t, ok, source)
		require.NotContains(t, string(got), "subject_ref")
	}
}

// TestRetractNarrativeBlockPreservesInlineComment checks block entries with owner comments.
//
// Example: An inline comment makes automatic retraction wait for review.
func TestRetractNarrativeBlockPreservesInlineComment(t *testing.T) {
	source := []byte("progress:\n  - subject_ref: one\n    text: done # owner note\n")
	update := syncUpdate{Target: syncTargetProgress, ID: "one"}
	got, ok := retractNarrativeSync(source, update, "done", true, "", false)
	require.False(t, ok)
	require.Equal(t, source, got)
}
