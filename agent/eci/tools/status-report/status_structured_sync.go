package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

// validStructuredSyncUpdate accepts only explicitly supported typed fields.
//
// Example: A task effort and impact can be updated in one group.
func validStructuredSyncUpdate(u syncUpdate) bool {
	allowed := map[syncTarget]map[string]bool{
		syncTargetTask:             {"effort": true, "impact": true, "impact_points": true, "impact_basis": true, "priority": true, "work_refs": true, "indivisible_reason": true, "title": true, "parent_id": true},
		syncTargetPriorityEstimate: {"min_minutes": true, "max_minutes": true, "as_of": true, "basis": true, "assumptions": true, "dependencies": true, "critical_path": true},
		syncTargetReport:           {"completion_estimate": true, "top_priorities": true, "roster": true, "priority_order": true, "impact_scale": true},
		syncTargetReview:           {"reporter": true, "critic": true, "rounds": true, "estimate": true, "impact": true, "priority": true, "unresolved": true},
	}
	if strings.TrimSpace(u.ID) == "" || len(u.Values)+len(u.Clear) == 0 || u.Retract || allowed[u.Target] == nil || (u.Target == syncTargetReport && u.ID != "current") {
		return false
	}
	seen := map[string]bool{}
	if len(u.Fields) > 0 {
		probe := u
		probe.Values, probe.Clear = nil, nil
		if !validSyncUpdate(probe) {
			return false
		}
		for field := range u.Fields {
			seen[field] = true
		}
	}
	for field, value := range u.Values {
		if !allowed[u.Target][field] || seen[field] || !json.Valid(value) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		seen[field] = true
	}
	for _, field := range u.Clear {
		clearable := allowed[u.Target][field] || (u.Target == syncTargetTask && (field == "lifecycle" || field == "context" || field == "owner" || field == "evidence"))
		if !clearable || seen[field] {
			return false
		}
		if field == "lifecycle" && (u.Fields["status"] == "" || u.Fields["status"] == "unknown") {
			return false
		}
		seen[field] = true
	}
	return true
}

// structuredSyncNode finds the unique record selected by a typed update.
//
// Example: Select an existing review by its task ID.
func structuredSyncNode(
	root *yaml.Node,
	u syncUpdate,
) *yaml.Node {
	if u.Target == syncTargetReport {
		return root
	}
	section, identity := "", ""
	switch u.Target {
	case syncTargetTask:
		section, identity = "tasks", "id"
	case syncTargetPriorityEstimate:
		section, identity = "priority_estimates", "priority"
	case syncTargetReview:
		section, identity = "reviews", "task_id"
	default:
		return nil
	}
	list := mapValue(root, section)
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil
	}
	var found *yaml.Node
	for _, n := range list.Content {
		if id := mapValue(n, identity); id != nil && id.Value == u.ID {
			if found != nil {
				return nil
			}
			found = n
		}
	}
	return found
}

// structuredSyncValues returns canonical values for conflict detection.
//
// Example: Save a task's effort before applying its next projection.
func structuredSyncValues(
	data []byte,
	u syncUpdate,
) (map[string]string, bool) {
	if !validStructuredSyncUpdate(u) {
		return nil, false
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return nil, false
	}
	node := structuredSyncNode(doc.Content[0], u)
	if node == nil {
		return nil, false
	}
	result := map[string]string{}
	fields := append([]string(nil), u.Clear...)
	for field := range u.Values {
		fields = append(fields, field)
	}
	for field := range u.Fields {
		fields = append(fields, field)
	}
	for _, field := range fields {
		value := mapValue(node, field)
		if value == nil {
			result[field] = syncAbsentValue
			continue
		}
		decoded, err := decodeStructuredSyncValue(value, u, field)
		if err != nil {
			return nil, false
		}
		encoded, err := json.Marshal(decoded)
		if err != nil {
			return nil, false
		}
		_, scalar := u.Fields[field]
		if u.Target == syncTargetTask && (field == "lifecycle" || field == "context" || field == "owner" || field == "evidence") {
			scalar = true
		}
		if scalar {
			if value.Kind != yaml.ScalarNode {
				return nil, false
			}
			result[field] = scalarSyncOwnership(value)
		} else {
			result[field] = string(encoded)
		}
	}
	return result, true
}

// decodeStructuredSyncValue preserves lexical task labels for ownership checks.
//
// Example: Numeric 1.10 is not conflated with 1.1 in stored ownership.
func decodeStructuredSyncValue(
	value *yaml.Node,
	update syncUpdate,
	field string,
) (any, error) {
	if update.Target == syncTargetTask && (field == "priority" || field == "parent_id") {
		var text string
		err := value.Decode(&text)
		return text, err
	}
	if update.Target == syncTargetReport && (field == "top_priorities" || field == "priority_order") {
		var labels []string
		err := value.Decode(&labels)
		return labels, err
	}
	var decoded any
	err := value.Decode(&decoded)
	return decoded, err
}

// applyStructuredSyncUpdate applies a validated group without rewriting unrelated bytes.
//
// Example: Reject an estimate if an owner changed its prior value.
func applyStructuredSyncUpdate(
	data []byte,
	u syncUpdate,
	previous map[string]string,
) ([]byte, bool) {
	current, ok := structuredSyncValues(data, u)
	if !ok {
		return data, false
	}
	for field, value := range previous {
		if current[field] != value {
			return data, false
		}
	}
	candidate := append([]byte(nil), data...)
	for field, value := range u.Fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			return data, false
		}
		var applied bool
		candidate, applied = editStructuredField(candidate, u, field, string(encoded))
		if !applied {
			return data, false
		}
	}
	fields := make([]string, 0, len(u.Values))
	for field := range u.Values {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		var applied bool
		candidate, applied = editStructuredField(candidate, u, field, string(u.Values[field]))
		if !applied {
			return data, false
		}
	}
	for _, field := range u.Clear {
		var applied bool
		candidate, applied = clearStructuredField(candidate, u, field)
		if !applied {
			return data, false
		}
	}
	if _, err := parse(candidate); err != nil {
		return data, false
	}
	return candidate, true
}

// editStructuredField replaces one value using its source span.
//
// Example: Replace an effort mapping while retaining neighboring comments.
func editStructuredField(
	data []byte,
	u syncUpdate,
	field string,
	value string,
) ([]byte, bool) {
	var doc yaml.Node
	if yaml.Unmarshal(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), &doc) != nil || len(doc.Content) != 1 {
		return data, false
	}
	parent := structuredSyncNode(doc.Content[0], u)
	if parent == nil {
		return data, false
	}
	node := mapValue(parent, field)
	if node == nil {
		var start int
		var insertion string
		if parent.Style&yaml.FlowStyle != 0 {
			end, err := flowMappingEndCached(&flowDocument{data: data}, parent)
			if err != nil {
				return data, false
			}
			start = end
			insertion = ", " + field + ": " + value
		} else {
			end, indent, err := blockMappingEnd(data, parent)
			if err != nil {
				return data, false
			}
			start = end
			insertion = strings.Repeat(" ", indent) + field + ": " + value + "\n"
		}
		return append(append(append([]byte(nil), data[:start]...), []byte(insertion)...), data[start:]...), true
	}
	start, err := yamlByteOffset(data, node.Line, node.Column)
	if err != nil {
		return data, false
	}
	end := 0
	suffix := ""
	switch node.Kind {
	case yaml.ScalarNode:
		edit, err := scalarReplacement(&flowDocument{data: data}, node, "")
		if err != nil {
			return data, false
		}
		start, end = int(edit.Start), int(edit.End)
	case yaml.MappingNode, yaml.SequenceNode:
		if node.Style&yaml.FlowStyle != 0 {
			start, err = yamlAnchoredCollectionStart(data, node)
			if err != nil {
				return data, false
			}
			opening := token.MappingStartType
			if node.Kind == yaml.SequenceNode {
				opening = token.SequenceStartType
			}
			last, err := flowEnd(data, start, opening)
			if err != nil {
				return data, false
			}
			end = last + 1
		} else {
			var err error
			if node.Kind == yaml.SequenceNode {
				end, err = structuredBlockSequenceEnd(data, parent, field)
				for i := 0; i+1 < len(parent.Content); i += 2 {
					if parent.Content[i].Value == field && node.Column <= parent.Content[i].Column {
						value = "  " + value
					}
				}
			} else {
				end, _, err = blockMappingEnd(data, node)
			}
			if err != nil {
				return data, false
			}
			if end < start || end > len(data) {
				return data, false
			}
			suffix = "\n"
			if bytes.Contains(data, []byte("\r\n")) {
				suffix = "\r\n"
			}
		}
	default:
		return data, false
	}
	if end < start || end > len(data) || laneRemovalHasComment(data, start, end) {
		return data, false
	}
	return append(append(append([]byte(nil), data[:start]...), []byte(value+suffix)...), data[end:]...), true
}

// structuredBlockSequenceEnd locates the next field or containing mapping boundary.
//
// Example: An indentless sequence ends before the next top-level field.
func structuredBlockSequenceEnd(
	data []byte,
	parent *yaml.Node,
	field string,
) (int, error) {
	if next := nextMappingFieldLine(parent, field); next > 0 {
		return yamlByteOffset(data, sequenceFollowingCommentLine(data, parent, field, next), 1)
	}
	end, _, err := blockMappingEnd(data, parent)
	return end, err
}

// sequenceFollowingCommentLine keeps the next key's head comment outside the edit.
//
// Example: A note directly before a sibling key remains with that sibling.
func sequenceFollowingCommentLine(
	data []byte,
	parent *yaml.Node,
	field string,
	next int,
) int {
	for i := 0; i+3 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == field {
			return sequenceHeadCommentLine(data, parent.Content[i+2], next)
		}
	}
	return next
}

// sequenceHeadCommentLine locates the first outdented line of a key's head comment.
//
// Example: A sibling comment is excluded from the preceding list replacement.
func sequenceHeadCommentLine(
	data []byte,
	key *yaml.Node,
	next int,
) int {
	if key.HeadComment == "" {
		return next
	}
	lines := strings.Split(string(data), "\n")
	line := next - 1
	for line > 0 {
		text := strings.TrimSuffix(lines[line-1], "\r")
		trimmed := strings.TrimSpace(text)
		if !strings.HasPrefix(trimmed, "#") || len(text)-len(strings.TrimLeft(text, " ")) >= key.Column {
			break
		}
		line--
	}
	return line + 1
}

// clearStructuredField removes a comment-free mapping entry.
//
// Example: Clear an optional task impact basis without changing other entries.
func clearStructuredField(
	data []byte,
	u syncUpdate,
	field string,
) ([]byte, bool) {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return data, false
	}
	parent := structuredSyncNode(doc.Content[0], u)
	if parent == nil || parent.Kind != yaml.MappingNode {
		return data, false
	}
	for i := 0; i < len(parent.Content); i += 2 {
		key := parent.Content[i]
		if key.Value != field {
			continue
		}
		start, err := yamlByteOffset(data, key.Line, key.Column)
		if err != nil {
			return data, false
		}
		var end int
		if parent.Style&yaml.FlowStyle != 0 {
			if i+2 < len(parent.Content) {
				end, err = yamlByteOffset(data, parent.Content[i+2].Line, parent.Content[i+2].Column)
			} else {
				end, err = flowMappingEndCached(&flowDocument{data: data}, parent)
				if err == nil {
					for start > 0 && (data[start-1] == ' ' || data[start-1] == '\t') {
						start--
					}
					if start == 0 || data[start-1] != ',' {
						return data, false
					}
					start--
				}
			}
		} else {
			start -= key.Column - 1
			if i+2 < len(parent.Content) {
				end, err = yamlByteOffset(data, parent.Content[i+2].Line, 1)
			} else {
				end, _, err = blockMappingEnd(data, parent)
			}
		}
		if err != nil || start < 0 || end < start || end > len(data) || laneRemovalHasComment(data, start, end) {
			return data, false
		}
		return append(append([]byte(nil), data[:start]...), data[end:]...), true
	}
	return data, true
}
