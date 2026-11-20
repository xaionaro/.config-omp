package main

import (
	_ "embed"
	"fmt"
	"strings"
)

// pinnedColumns is the field count of each pinned-units line: source path and behavior.
const pinnedColumns = 2

// pinnedUnitsSource lists every unit the manifest must cover, pinned to pre-migration source 575bb81. It is embedded so
// the check never reads the source repository. It lives in its own file so that deleting a manifest row cannot also
// delete the requirement that the row exist.
//
// Example: each data line is one unit: a source path, then a behavior or a dash.
//
//go:embed pinned-units-575bb81.tsv
var pinnedUnitsSource string

// pinnedUnits parses pinnedUnitsSource into units in file order, skipping blank and "#" lines. A malformed line is an
// error because the pinned list belongs to the checker, not to user input.
//
// Example: pinnedUnits() returns the 434 units of the pre-migration source 575bb81 pin.
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
		units = append(units, Unit{Source: SourcePath(fields[0]), Behavior: Behavior(fieldValue(fields[1]))})
	}
	return units, nil
}
