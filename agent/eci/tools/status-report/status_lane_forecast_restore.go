package main

import (
	"encoding/json"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// laneForecastProjection builds the report forecast from one active canonical record.
//
// Example: Project a reopened lane's current target and baseline.
func laneForecastProjection(
	ref string,
	source map[string]any,
) (json.RawMessage, bool) {
	if !validRef(ref, "forecasts.yaml") || textField(source, "status") != "active" || textField(source, "kind") != "lane" {
		return nil, false
	}
	seed, err := json.Marshal(map[string]any{"forecast_ref": ref})
	if err != nil {
		return nil, false
	}
	data := append([]byte("forecast: "), seed...)
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return nil, false
	}
	node := mapValue(doc.Content[0], "forecast")
	edits, err := projectForecast(data, node, source, &flowDocument{data: data})
	if err != nil {
		return nil, false
	}
	sort.Slice(edits,
		// Apply byte edits from the end so earlier offsets stay valid.
		//
		// Example: Insert a target and replace a baseline without shifting offsets.
		func(
			i int,
			j int,
		) bool {
			return edits[i].Start > edits[j].Start
		})
	for _, edit := range edits {
		data = append(append(append([]byte(nil), data[:edit.Start]...), []byte(edit.Value)...), data[edit.End:]...)
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return nil, false
	}
	node = mapValue(doc.Content[0], "forecast")
	var value map[string]any
	if node == nil || node.Decode(&value) != nil {
		return nil, false
	}
	for _, field := range []string{"outcome", "target", "assessment"} {
		if textField(value, field) == "" {
			return nil, false
		}
	}
	raw, err := json.Marshal(value)
	return raw, err == nil
}

// restoreLaneForecast inserts a canonical projection without rewriting existing fields.
//
// Example: Add a forecast after the same lane is explicitly reopened.
func restoreLaneForecast(
	data []byte,
	update syncUpdate,
	projection json.RawMessage,
) ([]byte, bool) {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return data, false
	}
	list := mapValue(doc.Content[0], "lanes")
	if list == nil || list.Kind != yaml.SequenceNode {
		return data, false
	}
	var lane *yaml.Node
	for _, item := range list.Content {
		id, ref, env := mapValue(item, "task_id"), mapValue(item, "work_ref"), mapValue(item, "required_environment")
		if id != nil && ref != nil && env != nil && id.Value == update.ID && ref.Value == update.WorkRef && env.Value == update.RequiredEnvironment.String() {
			if lane != nil {
				return data, false
			}
			lane = item
		}
	}
	if lane == nil || mapValue(lane, "forecast") != nil {
		return data, false
	}
	var pos int
	var insertion string
	var err error
	if lane.Style&yaml.FlowStyle != 0 {
		pos, err = flowMappingEndCached(&flowDocument{data: data}, lane)
		insertion = ", forecast: " + string(projection)
	} else {
		var indent int
		pos, indent, err = blockMappingEnd(data, lane)
		insertion = strings.Repeat(" ", indent) + "forecast: " + string(projection) + "\n"
	}
	if err != nil {
		return data, false
	}
	return append(append(append([]byte(nil), data[:pos]...), []byte(insertion)...), data[pos:]...), true
}
