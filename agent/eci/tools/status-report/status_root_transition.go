package main

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

// removeRootSyncRecord removes an exact root or priority record without deleting comments.
//
// Example: Retire a matching priority estimate while retaining neighboring comments.
func removeRootSyncRecord(
	data []byte,
	target syncTarget,
	id string,
	expected json.RawMessage,
) ([]byte, bool) {
	section, identity := "root_forecasts", "forecast_ref"
	switch target {
	case syncTargetRootCompletion:
		section, identity = "root_completions", "work_ref"
	case syncTargetPriorityEstimate:
		section, identity = "priority_estimates", "priority"
	case syncTargetRootForecast:
	default:
		return data, false
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return data, false
	}
	root := doc.Content[0]
	list := mapValue(root, section)
	if list == nil || list.Kind != yaml.SequenceNode {
		return data, false
	}
	var expectedValue any
	if json.Unmarshal(expected, &expectedValue) != nil {
		return data, false
	}
	index := -1
	for i, item := range list.Content {
		key := mapValue(item, identity)
		if key != nil && key.Value == id {
			if index >= 0 {
				return data, false
			}
			index = i
		}
	}
	if index < 0 {
		return data, false
	}
	var actual any
	if list.Content[index].Decode(&actual) != nil {
		return data, false
	}
	encoded, err := json.Marshal(actual)
	if err != nil {
		return data, false
	}
	var normalized any
	if json.Unmarshal(encoded, &normalized) != nil {
		return data, false
	}
	if target == syncTargetPriorityEstimate {
		value, ok := normalized.(map[string]any)
		if !ok {
			return data, false
		}
		priority, ok := priorityNodeID(mapValue(list.Content[index], identity))
		if !ok {
			return data, false
		}
		value[identity] = priority
	}
	if !equalSyncPriorRecord(target, normalized, expectedValue) {
		return data, false
	}
	item := list.Content[index]
	start, err := yamlByteOffset(data, item.Line, item.Column)
	if err != nil {
		return data, false
	}
	end := 0
	replacement := ""
	if list.Style&yaml.FlowStyle != 0 {
		if index+1 < len(list.Content) {
			end, err = yamlByteOffset(data, list.Content[index+1].Line, list.Content[index+1].Column)
		} else {
			listStart, e := yamlAnchoredCollectionStart(data, list)
			if e != nil {
				return data, false
			}
			end, err = flowEnd(data, listStart, token.SequenceStartType)
			if index > 0 {
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
		lineStart := strings.LastIndex(string(data[:start]), "\n") + 1
		if !strings.HasPrefix(strings.TrimSpace(string(data[lineStart:start])), "-") {
			return data, false
		}
		start = lineStart
		switch {
		case index+1 < len(list.Content):
			end, err = yamlByteOffset(data, list.Content[index+1].Line, 1)
		case nextMappingFieldLine(root, section) > 0:
			end, err = yamlByteOffset(data, nextMappingFieldLine(root, section), 1)
		default:
			end = len(data)
		}
		if len(list.Content) == 1 {
			replacement = "  []\n"
		}
	}
	if err != nil || start < 0 || end < start || end > len(data) || laneRemovalHasComment(data, start, end) {
		return data, false
	}
	return append(append(append([]byte(nil), data[:start]...), []byte(replacement)...), data[end:]...), true
}

// equalSyncPriorRecord compares an expected prior without discarding owner fields.
//
// Example: Priority estimates accept equivalent UTC timestamp spellings.
func equalSyncPriorRecord(
	target syncTarget,
	actual any,
	expected any,
) bool {
	if target == syncTargetPriorityEstimate {
		a, aOK := actual.(map[string]any)
		b, bOK := expected.(map[string]any)
		return aOK && bOK && equalPriorityEstimateRecord(a, b)
	}
	return reflect.DeepEqual(actual, expected)
}
