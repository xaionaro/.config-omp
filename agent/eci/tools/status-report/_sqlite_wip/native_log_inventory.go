package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// runNativeLogInventory catalogs the exact caller-observed bytes without opening a database.
//
// Example: An unfinished final line is retained by the caller but excluded from the catalog.
func runNativeLogInventory(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 1 || args[0] != "--stdin" {
		if err := writeRefreshDiagnostic(errOut, "usage: status-report log-inventory --stdin\n"); err != nil { return 1 }
		return 2
	}
	data, err := io.ReadAll(in)
	var observation nativeLogObservation
	if err == nil { observation, err = decodeNativeLogObservation(data) }
	var items []baselineOccurrence
	var lines int
	if err == nil { items, lines, err = baselineCatalog(observation.Bytes) }
	if err == nil {
		if items == nil { items = []baselineOccurrence{} }
		err = json.NewEncoder(out).Encode(struct {
			CompleteLines int `json:"complete_lines"`
			Occurrences []baselineOccurrence `json:"occurrences"`
		}{CompleteLines:lines, Occurrences:items})
	}
	if err != nil {
		if writeErr := writeRefreshDiagnostic(errOut, fmt.Sprintln("status-report log-inventory:", err)); writeErr != nil { return 1 }
		return 1
	}
	return 0
}
