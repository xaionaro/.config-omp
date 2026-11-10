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
func writeTargets(req Request) ([]string, error) {
	switch req.Tool {
	case "write":
		return []string{asString(req.Input["path"])}, nil
	case "edit":
		if req.EditTargets != nil {
			return req.EditTargets, nil
		}
		paths := append([]string{asString(req.Input["path"])}, stringsFrom(req.Input["paths"])...)
		patch := asString(req.Input["input"])
		if patch == "" {
			patch = asString(req.Input["_input"])
		}
		for _, line := range strings.Split(patch, "\n") {
			if match := patchHeader.FindStringSubmatch(line); len(match) == 2 {
				paths = append(paths, match[1])
				continue
			}
			for _, prefix := range []string{"MV ", "*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: ", "*** Edit File: "} {
				if strings.HasPrefix(line, prefix) {
					paths = append(paths, strings.Trim(strings.TrimSuffix(strings.TrimPrefix(line, prefix), " all"), `"'`))
					break
				}
			}
		}
		for _, edit := range mapsFrom(req.Input["edits"]) {
			if rename := asString(edit["rename"]); rename != "" {
				paths = append(paths, rename)
			}
		}
		return paths, nil
	}
	return nil, nil
}
