package main

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// presentOmpPath is the omp file the rule tests map to. ruleTree holds it and no other file.
const presentOmpPath = "agent/present.md"

// ruleTree is the omp tree the rule tests check mapped paths against.
var ruleTree = fstest.MapFS{presentOmpPath: &fstest.MapFile{}}

// tsvRow joins the six columns of one manifest row into a tab-separated line.
//
// Example: tsvRow("a.md", "-", "mapped", "agent/a.md", "-", "-") is a mapped whole-file row.
func tsvRow(fields ...string) string {
	return strings.Join(fields, "\t")
}

// manifestOf joins rows into a manifest body that ends in a newline, as the file on disk does.
//
// Example: manifestOf(tsvRow("a.md", "-", "mapped", "agent/a.md", "-", "-")) is a one-row manifest.
func manifestOf(rows ...string) string {
	return strings.Join(rows, "\n") + "\n"
}

// checkManifest runs Check over a manifest body against the rule tree and fails the test on an I/O error.
//
// Example: checkManifest(t, manifestOf(row), units) returns the report for that one row.
func checkManifest(
	t *testing.T,
	manifest string,
	units []Unit,
) Report {
	t.Helper()
	report, err := Check(strings.NewReader(manifest), units, ruleTree)
	require.NoError(t, err)
	return report
}

// failureText joins the failure lines of a report so a test can match one rule's message.
//
// Example: failureText(Report{}) is the empty string.
func failureText(report Report) string {
	lines := make([]string, 0, len(report.Failures))
	for _, failure := range report.Failures {
		lines = append(lines, string(failure))
	}
	return strings.Join(lines, "\n")
}

// TestPinnedUnitsMatchCodexCommit pins the unit counts for codex 575bb81: 410 instruction files, plus 24 bin units
// (18 wrapper behaviors and 6 whole wrappers).
//
// Example: a re-pin that changes these counts must update this test in the same commit.
func TestPinnedUnitsMatchCodexCommit(t *testing.T) {
	units, err := pinnedUnits()
	require.NoError(t, err)
	instructionFiles, wrapperUnits := 0, 0
	for _, unit := range units {
		if strings.HasPrefix(string(unit.Codex), wrapperPrefix) {
			wrapperUnits++
			continue
		}
		instructionFiles++
	}
	assert.Equal(t, 410, instructionFiles)
	assert.Equal(t, 24, wrapperUnits)
}

// TestCheckAcceptsCompleteManifest verifies that mapped files, a mapped bin behavior, and reason-coded units all pass,
// and that each state is counted separately.
//
// Example: a manifest that covers each pinned unit exactly once yields no failures.
func TestCheckAcceptsCompleteManifest(t *testing.T) {
	units := []Unit{
		{Codex: "a.md"},
		{Codex: "bin/x.sh", Behavior: "refresh-call"},
		{Codex: "b.toml"},
		{Codex: "bin/y.sh"},
	}
	manifest := manifestOf(
		tsvRow("a.md", "-", "mapped", presentOmpPath, "-", "-"),
		tsvRow("bin/x.sh", "refresh-call", "mapped", presentOmpPath, "-", "-"),
		tsvRow("b.toml", "-", "reason-coded", "-", "config-moved", "agent/AGENTS.md"),
		tsvRow("bin/y.sh", "-", "reason-coded", "-", "obsolete", "CODEX.md:136"),
	)
	report := checkManifest(t, manifest, units)
	assert.Empty(t, report.Failures)
	assert.Equal(t, 2, report.Mapped)
	assert.Equal(t, 2, report.ReasonCoded)
	assert.Equal(t, 0, report.Open)
	assert.Equal(t, 0, report.Unrecorded)
}

// TestCheckKeepsOpenAndUnrecordedRowsRed verifies that open and unrecorded units fail the check, while a reason-coded
// unit adds no failure.
//
// Example: an open row with owner evidence yields one "open until its owner lands" failure.
func TestCheckKeepsOpenAndUnrecordedRowsRed(t *testing.T) {
	units := []Unit{{Codex: "a.md"}, {Codex: "b.md"}, {Codex: "c.md"}}
	manifest := manifestOf(
		tsvRow("a.md", "-", "reason-coded", "-", "config-moved", "agent/AGENTS.md"),
		tsvRow("b.md", "-", "open", "-", "-", "owner OrchPort-2: port pending"),
		tsvRow("c.md", "-", "unrecorded", "-", "-", "no record"),
	)
	report := checkManifest(t, manifest, units)
	assert.Equal(t, 1, report.ReasonCoded)
	assert.Equal(t, 1, report.Open)
	assert.Equal(t, 1, report.Unrecorded)
	require.Len(t, report.Failures, 2)
	assert.Contains(t, failureText(report), "open until its owner lands: owner OrchPort-2: port pending")
	assert.Contains(t, failureText(report), "no recorded disposition")
}

// TestCheckReportsEachViolatedRule gives each rule one violating manifest and expects that rule's message.
//
// Example: a reason-coded row with code "bogus" reports "reason code \"bogus\" is not a recorded reason".
func TestCheckReportsEachViolatedRule(t *testing.T) {
	wholeFile := []Unit{{Codex: "a.md"}}
	cases := []struct {
		name     string
		manifest string
		units    []Unit
		want     string
	}{
		{
			name:     "pinned unit without a row",
			manifest: manifestOf(),
			units:    wholeFile,
			want:     "pinned units: a.md: no manifest row",
		},
		{
			name: "row for an unpinned unit",
			manifest: manifestOf(
				tsvRow("a.md", "-", "mapped", presentOmpPath, "-", "-"),
				tsvRow("z.md", "-", "mapped", presentOmpPath, "-", "-"),
			),
			units: wholeFile,
			want:  "manifest line 2: z.md: not in the pinned units",
		},
		{
			name: "duplicate row for one unit",
			manifest: manifestOf(
				tsvRow("a.md", "-", "mapped", presentOmpPath, "-", "-"),
				tsvRow("a.md", "-", "mapped", presentOmpPath, "-", "-"),
			),
			units: wholeFile,
			want:  "manifest line 2: a.md: duplicate of line 1",
		},
		{
			name:     "row with the wrong field count",
			manifest: manifestOf(tsvRow("a.md", "-", "mapped")),
			units:    wholeFile,
			want:     "manifest line 1: want 6 tab-separated fields, got 3",
		},
		{
			name:     "reason-coded row without a code",
			manifest: manifestOf(tsvRow("a.md", "-", "reason-coded", "-", "-", "x")),
			units:    wholeFile,
			want:     "manifest line 1: a.md: reason-coded row gives no reason code",
		},
		{
			name:     "reason code outside the vocabulary",
			manifest: manifestOf(tsvRow("a.md", "-", "reason-coded", "-", "bogus", "x")),
			units:    wholeFile,
			want:     `reason code "bogus" is not a recorded reason`,
		},
		{
			name:     "reason-coded row that also names an omp path",
			manifest: manifestOf(tsvRow("a.md", "-", "reason-coded", presentOmpPath, "obsolete", "x")),
			units:    wholeFile,
			want:     "reason-coded row also names omp path agent/present.md",
		},
		{
			name:     "mapped path missing from the tree",
			manifest: manifestOf(tsvRow("a.md", "-", "mapped", "agent/missing.md", "-", "-")),
			units:    wholeFile,
			want:     "mapped path agent/missing.md is not in the tree",
		},
		{
			name:     "mapped row that also gives a reason code",
			manifest: manifestOf(tsvRow("a.md", "-", "mapped", presentOmpPath, "obsolete", "-")),
			units:    wholeFile,
			want:     `mapped row also gives reason code "obsolete"`,
		},
		{
			name:     "mapped row without an omp path",
			manifest: manifestOf(tsvRow("a.md", "-", "mapped", "-", "-", "-")),
			units:    wholeFile,
			want:     "mapped row names no omp path",
		},
		{
			name:     "whole-file mapping of a bin wrapper",
			manifest: manifestOf(tsvRow("bin/x.sh", "-", "mapped", presentOmpPath, "-", "-")),
			units:    []Unit{{Codex: "bin/x.sh", Behavior: "refresh-call"}},
			want:     "bin/ path match needs a behavior unit",
		},
		{
			name:     "unknown state",
			manifest: manifestOf(tsvRow("a.md", "-", "bogus", "-", "-", "x")),
			units:    wholeFile,
			want:     `unknown state "bogus"`,
		},
		{
			name:     "open row without an owner",
			manifest: manifestOf(tsvRow("a.md", "-", "open", "-", "-", "")),
			units:    wholeFile,
			want:     "open row names no owner",
		},
		{
			name:     "open row that also gives a reason code",
			manifest: manifestOf(tsvRow("a.md", "-", "open", "-", "obsolete", "owner AUTOBUILD")),
			units:    wholeFile,
			want:     "open row must leave omp_path and reason_code as -",
		},
		{
			name:     "unrecorded row",
			manifest: manifestOf(tsvRow("a.md", "-", "unrecorded", "-", "-", "x")),
			units:    wholeFile,
			want:     "no recorded disposition",
		},
	}
	for _, tc := range cases {
		report := checkManifest(t, tc.manifest, tc.units)
		assert.Contains(t, failureText(report), tc.want, tc.name)
	}
}

// TestRecordedReasonsRejectUnrecorded keeps the unrecorded placeholder and the empty code outside the vocabulary.
//
// Example: ReasonCode("unrecorded").recorded() is false, and every listed code is true.
func TestRecordedReasonsRejectUnrecorded(t *testing.T) {
	assert.False(t, ReasonCode("unrecorded").recorded())
	assert.False(t, ReasonCode("").recorded())
	for _, code := range recordedReasons {
		assert.True(t, code.recorded(), string(code))
	}
}
