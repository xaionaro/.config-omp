package main

import "strings"

// emptyField is the placeholder for an empty column in the manifest and the pinned units. A visible marker keeps every
// line the same width under tab splitting, so an empty column cannot shift its neighbors.
const emptyField = "-"

// isCommentOrBlank reports whether a manifest or pinned-units line carries no record. Lines starting with "#" are
// comments.
//
// Example: isCommentOrBlank("# header") is true, and isCommentOrBlank("src-01\t-") is false.
func isCommentOrBlank(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// fieldValue converts a column's placeholder to the empty value and returns any other column unchanged.
//
// Example: fieldValue("-") is "", and fieldValue("agent/x.md") is "agent/x.md".
func fieldValue(field string) string {
	if field == emptyField {
		return ""
	}
	return field
}
