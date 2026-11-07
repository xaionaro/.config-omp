package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// manifestColumns is the field count of each manifest row: codex path, behavior, state, omp path, reason code, and
// evidence.
const manifestColumns = 6

// CodexPath is a repository-relative path in the pinned codex tree, such as "hooks/validate-bash.sh".
type CodexPath string

// Behavior names one build, refresh, or install step of a codex bin wrapper, such as "refresh-status". The empty
// value marks a whole instruction file or a whole wrapper.
type Behavior string

// OmpPath is a repository-relative path in the omp tree, resolved against the tree root.
type OmpPath string

// State is the disposition of one manifest row. Every row is in exactly one state, and the checker counts each state
// separately so that a reason-coded or open unit is never reported as mapped.
type State string

// Row states. The checker in check.go enforces which columns each state requires.
const (
	// StateMapped marks a row whose omp path must exist in the tree.
	StateMapped State = "mapped"
	// StateExcluded marks a row whose codex unit an omp implementation supersedes. Its omp path names that
	// implementation and must exist, and its reason code records the decision.
	StateExcluded State = "excluded"
	// StateReasonCoded marks a row whose recorded reason code explains why no omp counterpart exists.
	StateReasonCoded State = "reason-coded"
	// StateOpen marks a row whose work another owner has not landed. It always fails the check, so the run stays red
	// until the owner lands the work and the row changes state.
	StateOpen State = "open"
	// StateUnrecorded marks a row with no disposition yet. It always fails the check.
	StateUnrecorded State = "unrecorded"
)

// Unit is one coverage obligation: a whole codex instruction file, a whole codex bin wrapper, or one behavior of a
// codex bin wrapper.
type Unit struct {
	Codex    CodexPath
	Behavior Behavior
}

// Row is one manifest entry for a unit. Which of Omp, Reason, and Evidence the row must set depends on its State.
// Line is the 1-based manifest line, cited in failure messages.
type Row struct {
	Unit     Unit
	Line     int
	State    State
	Omp      OmpPath
	Reason   ReasonCode
	Evidence string
}

// String renders a unit as its codex path for a whole file or wrapper, or as "codex#behavior" for a behavior.
//
// Example: Unit{Codex: "bin/eci-active", Behavior: "refresh-status"}.String() is "bin/eci-active#refresh-status".
func (unit Unit) String() string {
	if unit.Behavior == "" {
		return string(unit.Codex)
	}
	return string(unit.Codex) + "#" + string(unit.Behavior)
}

// parseManifest reads every data row of the manifest, skipping blank and "#" lines. A line with the wrong field count
// yields a Failure instead of a Row, so one bad line does not hide the rest of the report.
//
// Example: a three-field line yields one Failure naming its line number and no Row.
func parseManifest(source io.Reader) ([]Row, []Failure, error) {
	var rows []Row
	var failures []Failure
	scanner := bufio.NewScanner(source)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		text := scanner.Text()
		if isCommentOrBlank(text) {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != manifestColumns {
			failures = append(failures, Failure(fmt.Sprintf(
				"manifest line %d: want %d tab-separated fields, got %d",
				lineNumber, manifestColumns, len(fields),
			)))
			continue
		}
		rows = append(rows, Row{
			Unit:     Unit{Codex: CodexPath(fields[0]), Behavior: Behavior(fieldValue(fields[1]))},
			Line:     lineNumber,
			State:    State(fields[2]),
			Omp:      OmpPath(fieldValue(fields[3])),
			Reason:   ReasonCode(fieldValue(fields[4])),
			Evidence: fieldValue(fields[5]),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read manifest: %w", err)
	}
	return rows, failures, nil
}
