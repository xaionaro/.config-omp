package main

import (
	"regexp"
	"strings"
)

// patchHeader recognizes omp's anchored hashline edit header.
//
// Example: [file.go#ABCD] selects file.go.
var patchHeader = regexp.MustCompile(`^\[(.+)#[A-Za-z0-9]{4}\]\s*$`)

// writeTargets extracts concrete mutation paths from host tool inputs.
//
// Example: an edit rename contributes its destination path.
func writeTargets(req Request) []string {
	switch req.Tool {
	case "write":
		return []string{asString(req.Input["path"])}
	case "edit":
		return editPaths(req)
	}
	return nil
}

// editPaths extracts paths from an edit call when host inspection is unavailable.
//
// Example: a rename destination is returned alongside the source.
func editPaths(req Request) []string {
	if req.EditTargets != nil {
		return req.EditTargets
	}
	paths := append([]string{asString(req.Input["path"])}, stringsFrom(req.Input["paths"])...)
	patch := asString(req.Input["input"])
	if patch == "" {
		patch = asString(req.Input["_input"])
	}
	for _, line := range strings.Split(patch, "\n") {
		if path := patchLinePath(line); path != "" {
			paths = append(paths, path)
		}
	}
	for _, edit := range mapsFrom(req.Input["edits"]) {
		if rename := asString(edit["rename"]); rename != "" {
			paths = append(paths, rename)
		}
	}
	return paths
}

// patchLinePath returns a path from a recognized edit header or move.
//
// Example: a hashline header identifies its file.
func patchLinePath(line string) string {
	if match := patchHeader.FindStringSubmatch(line); len(match) == 2 {
		return match[1]
	}
	for _, prefix := range []string{"MV ", "*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: ", "*** Edit File: "} {
		if strings.HasPrefix(line, prefix) {
			return strings.Trim(strings.TrimSuffix(strings.TrimPrefix(line, prefix), " all"), `"'`)
		}
	}
	return ""
}
