package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSavedChainRejectsMalformedCanonicalMeaning exercises affected records without publishing partial changes.
//
// Example: a missing exact source cannot be hidden by otherwise valid outcome links.
func TestSavedChainRejectsMalformedCanonicalMeaning(t *testing.T) {
	cases := []struct {
		Name  string
		Old   string
		New   string
		Error string
	}{
		{Name: "syntax", Old: snapshotFixture, New: "[", Error: "yaml:"},
		{Name: "empty", Old: snapshotFixture, New: "", Error: "EOF"},
		{Name: "extra-document", Old: snapshotFixture, New: snapshotFixture + "\n---\nother: document\n", Error: "one ledger document"},
		{Name: "scalar-root", Old: snapshotFixture, New: "plain scalar", Error: "YAML mapping"},
		{Name: "missing-records", Old: "records:", New: "other_records:", Error: "records:"},
		{Name: "nonstring-record-key", Old: "  unrelated:", New: "  7:", Error: "string keys"},
		{Name: "duplicate-record", Old: "  unrelated:", New: "  requirement:metrics:", Error: "duplicate mapping key"},
		{Name: "duplicate-field", Old: "      source_text:", New: "      acceptance: []\n      source_text:", Error: "duplicate mapping key"},
		{Name: "missing-outcome", Old: "  outcome:metrics:", New: "  outcome:other:", Error: "outcome outcome:metrics"},
		{Name: "wrong-outcome-type", Old: "type: outcome", New: "type: scope", Error: "not an outcome"},
		{Name: "outcome-links-scalar", Old: "    links:\n      requirements:", New: "    links: scalar\n    ignored:\n      requirements:", Error: "YAML mapping"},
		{Name: "missing-requirements", Old: "      requirements:", New: "      other_requirements:", Error: "outcome requirements"},
		{Name: "empty-scopes", Old: `scope: ["scope:metrics", "scope:preserved"]`, New: `scope: []`, Error: "outcome scopes"},
		{Name: "overlapping-link-types", Old: `scope: ["scope:metrics", "scope:preserved"]`, New: `scope: ["requirement:metrics"]`, Error: "both scope and requirement"},
		{Name: "wrong-linked-type", Old: "    type: requirement", New: "    type: scope", Error: "wrong type"},
		{Name: "evidence-scalar", Old: `evidence: ["original source"]`, New: `evidence: invalid`, Error: "evidence:"},
		{Name: "missing-data", Old: "    data:\n      source_text:", New: "    other_data:\n      source_text:", Error: "has no data"},
		{Name: "record-data-scalar", Old: "    data:\n      source_text:", New: "    data: invalid\n    ignored:\n      source_text:", Error: "data:"},
		{Name: "missing-source", Old: "      source_text:", New: "      other_source:", Error: "no exact source text"},
		{Name: "nonstring-source", Old: `source_text: "User: old compactness"`, New: `source_text: 7`, Error: "no exact source text"},
		{Name: "empty-source", Old: `source_text: "User: old compactness"`, New: `source_text: " "`, Error: "no exact source text"},
		{Name: "acceptance-nonstring", Old: `acceptance: ["Compact expanded content; collapse is additional"]`, New: `acceptance: [false]`, Error: "acceptance:"},
		{Name: "scope-include-scalar", Old: `include: ["expanded panel"]`, New: `include: invalid`, Error: "include:"},
		{Name: "scope-exclude-nonstring", Old: `exclude: ["production deployment"]`, New: `exclude: [7]`, Error: "exclude:"},
		{Name: "missing-description", Old: "description:", New: "other_description:", Error: "no description"},
		{Name: "supporting-links-scalar", Old: `    evidence: ["original source"]`, New: "    links: invalid\n    evidence: [\"original source\"]", Error: "links:"},
		{Name: "supporting-link-nonstring", Old: `    evidence: ["original source"]`, New: "    links: {related: [7]}\n    evidence: [\"original source\"]", Error: "link related:"},
		{Name: "alias-outside-projection", Old: `domain_detail: "must survive projection"`, New: `domain_detail: *outside`, Error: "unsupported projection"},
	}
	for _, entry := range cases {
		t.Run(entry.Name,
			// Reject the malformed binding for its specific reason before any publication is possible.
			//
			// Example: unsupported aliases must fail instead of dropping external meaning from output.
			func(t *testing.T) {
				contents := strings.Replace(snapshotFixture, entry.Old, entry.New, 1)
				if entry.Name == "alias-outside-projection" {
					contents = "outside_value: &outside meaning\n" + contents
				}
				chain, err := savedChain([]byte(contents), Request{Requirements: []RecordID{"requirement:metrics"}, Outcome: "outcome:metrics"})
				require.ErrorContains(t, err, entry.Error)
				require.Empty(t, chain)
			})
	}
}

// TestSavedChainPreservesSupportingRecordsAndEmptyLists exercises valid domain values and repeated links.
//
// Example: scalar context data, empty evidence/exclusions and cyclic references retain their meaning.
func TestSavedChainPreservesSupportingRecordsAndEmptyLists(t *testing.T) {
	contents := strings.Replace(snapshotFixture, `    evidence: ["original source"]`, "    links: {context: [\"context:extra\"], repeat: [\"outcome:metrics\"]}\n    evidence: []", 1)
	contents = strings.Replace(contents, `exclude: ["production deployment"]`, `exclude: []`, 1)
	contents += "  context:extra:\n    type: context\n    data: |\n      retained context meaning\n    evidence: []\n"
	chain, err := savedChain([]byte(contents), Request{Requirements: []RecordID{"requirement:metrics"}, Outcome: "outcome:metrics"})
	require.NoError(t, err)
	require.Contains(t, chain, "retained context meaning")
	require.Contains(t, chain, "evidence: []")
	require.Contains(t, chain, "exclude: []")
	require.Contains(t, chain, "must survive projection")
	require.Equal(t, 1, strings.Count("\n"+chain, "\noutcome:metrics:"))
}

// TestRequestValidationRejectsUnsupportedOrIncompleteInputs covers malformed requests before any ledger write.
//
// Example: trailing JSON and missing logging identity cannot create a prepared assignment.
func TestRequestValidationRejectsUnsupportedOrIncompleteInputs(t *testing.T) {
	f := newCLIFixture(t)
	original, err := os.ReadFile(f.Request)
	require.NoError(t, err)
	cases := []string{"{", "{}", "null", string(original) + " {}", strings.Replace(string(original), `"reason":"material correction"`, `"reason":" "`, 1), strings.Replace(string(original), `"role":"Supervisor"`, `"role":""`, 1), strings.Replace(string(original), `"requirements":["requirement:metrics"]`, `"requirements":[]`, 1)}
	for _, contents := range cases {
		require.NoError(t, os.WriteFile(f.Request, []byte(contents), 0600))
		_, err := readRequest(f.Request)
		require.Error(t, err)
	}
	_, err = readRequest(filepath.Join(f.Root, "missing-request.json"))
	require.Error(t, err)
}

// TestPreparationRejectsMissingAndIndirectSnapshots prevents publication through an accidental wrong target.
//
// Example: a symlink to a foreign snapshot cannot be used as the session's direct snapshot.
func TestPreparationRejectsMissingAndIndirectSnapshots(t *testing.T) {
	f := newCLIFixture(t)
	request, err := readRequest(f.Request)
	require.NoError(t, err)
	_, err = prepare(filepath.Join(f.Root, "missing.yaml"), request)
	require.Error(t, err)
	link := filepath.Join(f.Root, "indirect.yaml")
	require.NoError(t, os.Symlink(f.Ledger, link))
	_, err = prepare(link, request)
	require.ErrorContains(t, err, "regular direct file")
}
