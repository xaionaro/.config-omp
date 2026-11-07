package main

import (
	_ "embed"
	"fmt"
	"strings"
)

// pinnedColumns is the field count of each pinned-units line: codex path and behavior.
const pinnedColumns = 2

// pinnedUnitsSource lists every unit the manifest must cover, pinned to codex commit 575bb81. It is embedded so the
// check never reads ~/.codex. It lives in its own file so that deleting a manifest row cannot also delete the
// requirement that the row exist.
//
// Example: the first data line is the whole-file unit for CODEX.md.
//
//go:embed codex-575bb81-units.tsv
var pinnedUnitsSource string

// pinnedUnits parses pinnedUnitsSource into units in file order, skipping blank and "#" lines. A malformed line is an
// error because the pinned list belongs to the checker, not to user input.
//
// Example: pinnedUnits() returns the 428 units of the codex 575bb81 pin.
func pinnedUnits() ([]Unit, error) {
	var units []Unit
	for index, text := range strings.Split(pinnedUnitsSource, "\n") {
		if isCommentOrBlank(text) {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != pinnedColumns {
			return nil, fmt.Errorf("pinned units line %d: want %d tab-separated fields, got %d",
				index+1, pinnedColumns, len(fields))
		}
		units = append(units, Unit{Codex: CodexPath(fields[0]), Behavior: Behavior(fieldValue(fields[1]))})
	}
	return units, nil
}
