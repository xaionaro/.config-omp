package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// wrapperPrefix marks codex bin wrappers. A mapped wrapper row must name a behavior: a whole-file path match with an
// omp file says nothing about the build, refresh, and install steps the wrapper performs.
const wrapperPrefix = "bin/"

// Failure is one violated coverage rule, rendered as one output line.
type Failure string

// Report is the outcome of one coverage check. Failures lists every violated rule: manifest rows in file order, then
// pinned units that have no row. The state counts give the rows in each state. Open and unrecorded rows also appear
// in Failures, so a run that has either stays red.
type Report struct {
	Mapped      int
	ReasonCoded int
	Open        int
	Unrecorded  int
	Failures    []Failure
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
		report.count(row.State)
	}
	return report, nil
}

// count adds one row to the counter for its state. A row with an unknown state is counted nowhere; its failure
// already names it.
//
// Example: a row in StateOpen increments Open and nothing else.
func (report *Report) count(state State) {
	switch state {
	case StateMapped:
		report.Mapped++
	case StateReasonCoded:
		report.ReasonCoded++
	case StateOpen:
		report.Open++
	case StateUnrecorded:
		report.Unrecorded++
	}
}

// checkCoverage matches manifest rows to pinned units one to one. A pinned unit with no row, a row for a unit that is
// not pinned, and a second row for the same unit each fail.
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

// checkRow dispatches a row to the rules of its state. An unknown state fails, so a typo cannot pass as a disposition.
//
// Example: a row with state "bogus" yields "unknown state \"bogus\"".
func checkRow(
	row Row,
	tree fs.FS,
) ([]Failure, error) {
	switch row.State {
	case StateMapped:
		return checkMappedRow(row, tree)
	case StateReasonCoded:
		return checkReasonRow(row), nil
	case StateOpen:
		return checkOpenRow(row), nil
	case StateUnrecorded:
		return []Failure{rowFailure(row, "no recorded disposition; record a reason code or an omp path")}, nil
	default:
		return []Failure{rowFailure(row, fmt.Sprintf("unknown state %q", row.State))}, nil
	}
}

// checkMappedRow applies the rules of a mapped row: it needs an omp path that exists in the tree, no reason code, and
// a behavior when it belongs to a bin wrapper.
//
// Example: a mapped row for hooks/x.sh with omp path agent/x.md passes only when agent/x.md exists in the tree.
func checkMappedRow(
	row Row,
	tree fs.FS,
) ([]Failure, error) {
	var failures []Failure
	if row.Unit.Behavior == "" && strings.HasPrefix(string(row.Unit.Codex), wrapperPrefix) {
		failures = append(failures, rowFailure(row, "bin/ path match needs a behavior unit; a whole-file mapping is not coverage"))
	}
	if row.Reason != "" {
		failures = append(failures, rowFailure(row, fmt.Sprintf("mapped row also gives reason code %q", row.Reason)))
	}
	if row.Omp == "" {
		return append(failures, rowFailure(row, "mapped row names no omp path")), nil
	}
	missing, err := checkMappedPath(row, tree)
	if err != nil {
		return nil, err
	}
	return append(failures, missing...), nil
}

// checkReasonRow applies the rules of a reason-coded row: the code must be in the recorded vocabulary, and the row
// must not name an omp path.
//
// Example: a reason-coded row with code "bogus" yields "reason code \"bogus\" is not a recorded reason".
func checkReasonRow(row Row) []Failure {
	var failures []Failure
	if row.Omp != "" {
		failures = append(failures, rowFailure(row, fmt.Sprintf("reason-coded row also names omp path %s", row.Omp)))
	}
	switch {
	case row.Reason == "":
		failures = append(failures, rowFailure(row, "reason-coded row gives no reason code"))
	case !row.Reason.recorded():
		failures = append(failures, rowFailure(row, fmt.Sprintf("reason code %q is not a recorded reason", row.Reason)))
	}
	return failures
}

// checkOpenRow always fails: an open row is work another owner has not landed. Its evidence names the owner, and the
// open row keeps the run red until the owner lands the work.
//
// Example: an open row with evidence "owner OrchPort-2: port pending" yields "open until its owner lands: owner
// OrchPort-2: port pending".
func checkOpenRow(row Row) []Failure {
	var failures []Failure
	if row.Omp != "" || row.Reason != "" {
		failures = append(failures, rowFailure(row, "open row must leave omp_path and reason_code as -"))
	}
	if row.Evidence == "" {
		return append(failures, rowFailure(row, "open row names no owner"))
	}
	return append(failures, rowFailure(row, "open until its owner lands: "+row.Evidence))
}

// checkMappedPath reports a mapped row whose omp path is absent from the tree. Any other stat error means the tree
// could not be inspected, so it is returned to the caller.
//
// Example: a row mapped to agent/missing.md yields "mapped path agent/missing.md is not in the tree".
func checkMappedPath(
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
