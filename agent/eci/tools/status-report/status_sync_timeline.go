package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

// syncTimelineSnapshot carries a complete source-supplied timeline and its anchor.
//
// Example: Replace one four-hour snapshot without rebucketing its rows.
type syncTimelineSnapshot struct {
	AsOf    string          `json:"as_of"`
	State   timelineState   `json:"state"`
	Entries []timelineEntry `json:"entries"`
}

// applySyncTimeline replaces an entire validated timeline snapshot.
//
// Example: Publish a complete source-supplied snapshot atomically.
func applySyncTimeline(
	data []byte,
	snapshot syncTimelineSnapshot,
) ([]byte, bool) {
	_, err := parseUTC(snapshot.AsOf)
	if err != nil {
		return data, false
	}
	switch snapshot.State {
	case timelinePresent:
		if len(snapshot.Entries) == 0 {
			return data, false
		}
	case timelineVerifiedEmpty:
		if len(snapshot.Entries) != 0 {
			return data, false
		}
	default:
		return data, false
	}
	probe := report{Timestamp: snapshot.AsOf, TimelineAsOf: snapshot.AsOf, Timeline: snapshot.Entries}
	if validateTimeline(probe) != nil {
		return data, false
	}
	counts := map[string]int{}
	for _, entry := range snapshot.Entries {
		if strings.TrimSpace(entry.Fast) == "" || strings.TrimSpace(entry.Normal) == "" {
			return data, false
		}
		counts[entry.Job]++
	}
	for _, count := range counts {
		if count != int(timelineWindow/timelineInterval) {
			return data, false
		}
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return data, false
	}
	root := doc.Content[0]
	if root.Style&yaml.FlowStyle != 0 {
		return applyFlowRootTimeline(data, snapshot)
	}
	var sequence yaml.Node
	entries := snapshot.Entries
	if entries == nil {
		entries = []timelineEntry{}
	}
	if sequence.Encode(entries) != nil {
		return data, false
	}
	sequence.Style = yaml.FlowStyle
	for _, entry := range sequence.Content {
		entry.Style = yaml.FlowStyle
	}
	encoded, err := yaml.Marshal(&sequence)
	if err != nil {
		return data, false
	}
	values := map[string]string{"timeline_as_of": fmt.Sprintf("%q", snapshot.AsOf), "timeline_state": snapshot.State.String(), "timeline": strings.TrimSpace(string(encoded))}
	lines := strings.SplitAfter(string(data), "\n")
	offsets := make([]int, len(lines)+1)
	for i, line := range lines {
		offsets[i+1] = offsets[i] + len(line)
	}
	var edits []scalarEdit
	var missing []string
	for _, key := range []string{"timeline_as_of", "timeline_state", "timeline"} {
		node := mapValue(root, key)
		replacement := key + ": " + values[key] + "\n"
		if node == nil {
			missing = append(missing, replacement)
			continue
		}
		keyLine := 0
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				keyLine = root.Content[i].Line
				break
			}
		}
		if keyLine < 1 || keyLine > len(lines) {
			return data, false
		}
		endLine := nextMappingFieldLine(root, key)
		if endLine == 0 {
			endLine = len(lines) + 1
		}
		if endLine-1 > len(lines) || endLine <= keyLine {
			return data, false
		}
		start, end := offsets[keyLine-1], offsets[endLine-1]
		edits = append(edits, scalarEdit{Start: reportByteOffset(start), End: reportByteOffset(end), Value: replacement})
	}
	sort.Slice(edits,
		// Sort later offsets first so edits cannot shift later positions.
		//
		// Example: Replace a trailing timeline field before an earlier field.
		func(
			i int,
			j int,
		) bool {
			return edits[i].Start > edits[j].Start
		})
	out := append([]byte(nil), data...)
	for _, edit := range edits {
		out = append(append(append([]byte(nil), out[:edit.Start]...), edit.Value...), out[edit.End:]...)
	}
	if len(missing) > 0 {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, strings.Join(missing, "")...)
	}
	if _, err := parse(out); err != nil {
		return data, false
	}
	beforeComments, err := yamlComments(data)
	if err != nil {
		return data, false
	}
	afterComments, err := yamlComments(out)
	if err != nil || !slices.Equal(beforeComments, afterComments) {
		return data, false
	}
	return out, true
}

// applyFlowRootTimeline updates timeline fields inside a flow report mapping.
//
// Example: Replace a JSON report's timeline without rewriting unrelated fields.
func applyFlowRootTimeline(
	data []byte,
	snapshot syncTimelineSnapshot,
) ([]byte, bool) {
	entries := snapshot.Entries
	if entries == nil {
		entries = []timelineEntry{}
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return data, false
	}
	values := map[string]string{"timeline_as_of": fmt.Sprintf("%q", snapshot.AsOf), "timeline_state": fmt.Sprintf("%q", snapshot.State.String()), "timeline": string(encoded)}
	out := append([]byte(nil), data...)
	for _, key := range []string{"timeline_as_of", "timeline_state", "timeline"} {
		var doc yaml.Node
		if yaml.Unmarshal(out, &doc) != nil || len(doc.Content) != 1 {
			return data, false
		}
		node := mapValue(doc.Content[0], key)
		if node == nil {
			out, err = appendReportField(out, key, values[key])
			if err != nil {
				return data, false
			}
			continue
		}
		var start, end int
		switch node.Kind {
		case yaml.ScalarNode:
			start, end, err = (&flowDocument{data: out}).scalarSpan(node)
		case yaml.SequenceNode:
			start, err = yamlByteOffset(out, node.Line, node.Column)
			if err == nil {
				end, err = flowEnd(out, start, token.SequenceStartType)
				end++
			}
		default:
			return data, false
		}
		if err != nil || yamlRangeHasComment(out, start, end) {
			return data, false
		}
		out = append(append(append([]byte(nil), out[:start]...), []byte(values[key])...), out[end:]...)
	}
	if _, err := parse(out); err != nil {
		return data, false
	}
	return out, true
}
