package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// wrapperPrefix marks codex bin wrappers. A wrapper is covered only by its behavior units: a path match with an omp
// file says nothing about the build, refresh, and install steps the wrapper performs.
const wrapperPrefix = "bin/"

// Failure is one violated coverage rule, rendered as one output line.
type Failure string

// Report is the outcome of one coverage check. Failures lists every violated rule: manifest rows in file order, then
// pinned units that have no row. Mapped and Recorded count the rows that name an omp path or a reason code.
type Report struct {
	Mapped   int
	Recorded int
	Failures []Failure
}

// Check verifies manifest rows against the pinned units and the omp tree. It returns one Failure per violated rule.
// It returns an error only when the manifest cannot be read or the tree cannot be inspected.
//
// Example: Check(manifest, units, os.DirFS(ompRoot)) reports no failures when every unit has exactly one valid row.
func Check(
	manifest io.Reader,
	units []Unit,
	tree fs.FS,
) (Report, error) {
	rows, failures, err := parseManifest(manifest)
	if err != nil {
		return Report{}, err
	}
	report := Report{Failures: append(failures, checkCoverage(rows, units)...)}
	for _, row := range rows {
		rowFailures, rowErr := checkRow(row, tree)
		if rowErr != nil {
			return Report{}, rowErr
		}
		report.Failures = append(report.Failures, rowFailures...)
		switch {
		case row.Omp != "":
			report.Mapped++
		case row.Reason != "":
			report.Recorded++
		}
	}
	return report, nil
}

// checkCoverage matches manifest rows to pinned units one to one. A pinned unit with no row, a row for a unit that
// is not pinned, and a second row for the same unit each fail.
//
// Example: a pinned unit with no row yields "pinned units: <unit>: no manifest row".
func checkCoverage(
	rows []Row,
	units []Unit,
) []Failure {
	pinned := make(map[Unit]bool, len(units))
	for _, unit := range units {
		pinned[unit] = true
	}
	firstLine := make(map[Unit]int, len(rows))
	var failures []Failure
	for _, row := range rows {
		if !pinned[row.Unit] {
			failures = append(failures, rowFailure(row, "not in the pinned units"))
		}
		if first, seen := firstLine[row.Unit]; seen {
			failures = append(failures, rowFailure(row, fmt.Sprintf("duplicate of line %d", first)))
			continue
		}
		firstLine[row.Unit] = row.Line
	}
	for _, unit := range units {
		if _, ok := firstLine[unit]; !ok {
			failures = append(failures, Failure(fmt.Sprintf("pinned units: %s: no manifest row", unit)))
		}
	}
	return failures
}

// checkRow applies the rules one row must satisfy on its own. A bin wrapper row needs a behavior. Any other row maps
// to an omp path that exists in the tree or carries a recorded reason code, never both and never neither.
//
// Example: a row mapped to agent/x.md passes only when agent/x.md exists in the tree.
func checkRow(
	row Row,
	tree fs.FS,
) ([]Failure, error) {
	if row.Unit.Behavior == "" && strings.HasPrefix(string(row.Unit.Codex), wrapperPrefix) {
		return []Failure{rowFailure(row, "bin/ wrappers need behavior units; a whole-file row is a path match, not coverage")}, nil
	}
	switch {
	case row.Omp == "" && row.Reason == "":
		return []Failure{rowFailure(row, "absent from omp and has no reason code")}, nil
	case row.Omp != "" && row.Reason != "":
		return []Failure{rowFailure(row, fmt.Sprintf("maps to %s and also gives reason code %q", row.Omp, row.Reason))}, nil
	case row.Omp != "":
		return checkMapped(row, tree)
	case row.Reason.recorded():
		return nil, nil
	default:
		return []Failure{rowFailure(row, fmt.Sprintf("reason code %q is not a recorded reason", row.Reason))}, nil
	}
}

// checkMapped verifies that the omp path of a mapped row exists in the tree. A missing path is a rule violation; any
// other stat error means the tree could not be inspected, so it is returned to the caller.
//
// Example: a row mapped to agent/missing.md yields "mapped path agent/missing.md is not in the tree".
func checkMapped(
	row Row,
	tree fs.FS,
) ([]Failure, error) {
	_, err := fs.Stat(tree, string(row.Omp))
	switch {
	case err == nil:
		return nil, nil
	case errors.Is(err, fs.ErrNotExist):
		return []Failure{rowFailure(row, fmt.Sprintf("mapped path %s is not in the tree", row.Omp))}, nil
	default:
		return nil, fmt.Errorf("stat %s: %w", row.Omp, err)
	}
}

// rowFailure renders one violated rule for a manifest row, naming the row's line and unit.
//
// Example: rowFailure(row on line 3 for hooks/x.sh, "is not mapped") renders "manifest line 3: hooks/x.sh: is not mapped".
func rowFailure(
	row Row,
	problem string,
) Failure {
	return Failure(fmt.Sprintf("manifest line %d: %s: %s", row.Line, row.Unit, problem))
}
