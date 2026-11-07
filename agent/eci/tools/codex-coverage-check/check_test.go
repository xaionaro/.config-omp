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

// tsvRow joins fields into one tab-separated manifest line.
//
// Example: tsvRow("a.md", "-", "agent/a.md", "-", "-") is a mapped whole-file row.
func tsvRow(fields ...string) string {
	return strings.Join(fields, "\t")
}

// manifestOf joins rows into a manifest body that ends in a newline, as the file on disk does.
//
// Example: manifestOf(tsvRow("a.md", "-", "agent/a.md", "-", "-")) is a one-row manifest.
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

// TestPinnedUnitsMatchCodexCommit pins the unit counts for codex 575bb81: 410 instruction files and 18 bin behaviors.
//
// Example: a change to the pinned list that alters these counts must update this test with the re-pin.
func TestPinnedUnitsMatchCodexCommit(t *testing.T) {
	units, err := pinnedUnits()
	require.NoError(t, err)
	files, behaviors := 0, 0
	for _, unit := range units {
		if unit.Behavior == "" {
			files++
			continue
		}
		behaviors++
	}
	assert.Equal(t, 410, files)
	assert.Equal(t, 18, behaviors)
}

// TestCheckAcceptsCompleteManifest verifies that mapped files, a mapped bin behavior, and a recorded reason all pass.
//
// Example: a manifest that covers each pinned unit exactly once yields no failures.
func TestCheckAcceptsCompleteManifest(t *testing.T) {
	units := []Unit{
		{Codex: "a.md"},
		{Codex: "bin/x.sh", Behavior: "refresh-call"},
		{Codex: "b.toml"},
	}
	manifest := manifestOf(
		tsvRow("a.md", "-", presentOmpPath, "-", "-"),
		tsvRow("bin/x.sh", "refresh-call", presentOmpPath, "-", "-"),
		tsvRow("b.toml", "-", "-", "config-moved", "agent/AGENTS.md"),
	)
	report := checkManifest(t, manifest, units)
	assert.Empty(t, report.Failures)
	assert.Equal(t, 2, report.Mapped)
	assert.Equal(t, 1, report.Recorded)
}

// TestCheckReportsEachViolatedRule gives each rule one violating manifest and expects that rule's message.
//
// Example: a manifest with an unknown reason code reports "reason code \"unrecorded\" is not a recorded reason".
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
				tsvRow("a.md", "-", presentOmpPath, "-", "-"),
				tsvRow("z.md", "-", presentOmpPath, "-", "-"),
			),
			units: wholeFile,
			want:  "manifest line 2: z.md: not in the pinned units",
		},
		{
			name: "duplicate row for one unit",
			manifest: manifestOf(
				tsvRow("a.md", "-", presentOmpPath, "-", "-"),
				tsvRow("a.md", "-", presentOmpPath, "-", "-"),
			),
			units: wholeFile,
			want:  "manifest line 2: a.md: duplicate of line 1",
		},
		{
			name:     "row with the wrong field count",
			manifest: manifestOf(tsvRow("a.md", "-", presentOmpPath)),
			units:    wholeFile,
			want:     "manifest line 1: want 5 tab-separated fields, got 3",
		},
		{
			name:     "absent row without a reason",
			manifest: manifestOf(tsvRow("a.md", "-", "-", "-", "-")),
			units:    wholeFile,
			want:     "manifest line 1: a.md: absent from omp and has no reason code",
		},
		{
			name:     "unrecorded reason",
			manifest: manifestOf(tsvRow("a.md", "-", "-", "unrecorded", "no record")),
			units:    wholeFile,
			want:     `reason code "unrecorded" is not a recorded reason`,
		},
		{
			name:     "row that maps and gives a reason",
			manifest: manifestOf(tsvRow("a.md", "-", presentOmpPath, "config-moved", "-")),
			units:    wholeFile,
			want:     `maps to agent/present.md and also gives reason code "config-moved"`,
		},
		{
			name:     "mapped path missing from the tree",
			manifest: manifestOf(tsvRow("a.md", "-", "agent/missing.md", "-", "-")),
			units:    wholeFile,
			want:     "mapped path agent/missing.md is not in the tree",
		},
		{
			name:     "whole-file row for a bin wrapper",
			manifest: manifestOf(tsvRow("bin/x.sh", "-", presentOmpPath, "-", "-")),
			units:    []Unit{{Codex: "bin/x.sh", Behavior: "refresh-call"}},
			want:     "bin/ wrappers need behavior units",
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
