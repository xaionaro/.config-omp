package main

import (
	"encoding/json"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// syncAbsentValue marks a field previously removed by projection.
//
// Example: Reject an owner-added blocker after a projected clear.
const syncAbsentValue = "\x00absent"

// validLaneSyncUpdate accepts scalar fields and explicit optional-field removals.
//
// Example: Accept a lane update that clears a blocker.
func validLaneSyncUpdate(update syncUpdate) bool {
	if update.Target != syncTargetLane || len(update.Values) != 0 || update.Retract || (len(update.Fields) == 0 && len(update.Clear) == 0) {
		return false
	}
	probe := update
	if len(probe.Fields) == 0 {
		probe.Fields = map[string]string{"context": "probe"}
	}
	if !validSyncUpdate(probe) {
		return false
	}
	seen := map[string]bool{}
	for field := range update.Fields {
		seen[field] = true
	}
	for _, field := range update.Clear {
		if seen[field] || (field != "lifecycle" && field != "blocker" && field != "next_action" && field != "completed_at" && field != "root_task_status") {
			return false
		}
		seen[field] = true
	}
	return true
}

// applyLaneSyncUpdateRaw builds one validated lane transition.
//
// Example: Retire an inactive forecast while closing its canonical work.
func applyLaneSyncUpdateRaw(
	data []byte,
	update syncUpdate,
	ledger ledgerSource,
	forecasts forecastSource,
	previous map[string]string,
) ([]byte, bool) {
	if !validLaneSyncUpdate(update) {
		return data, false
	}
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
	if lane == nil {
		return data, false
	}
	for _, field := range update.Clear {
		if field == "lifecycle" {
			if !validRef(update.WorkRef, "project-understanding.yaml") || strings.TrimSpace(update.Evidence) == "" {
				return data, false
			}
			work, ok := ledger.Records[sourceID(update.WorkRef, "project-understanding.yaml")]
			if !ok || work.Type != "work" || !openWork(work.Data.State) {
				return data, false
			}
		}
		existing := mapValue(lane, field)
		if prior, ok := previous[field]; ok {
			if scalarSyncOwnership(existing) != prior {
				return data, false
			}
		}
	}
	statusField := update.RequiredEnvironment.String() + "_status"
	if completed, supplied := update.Fields["completed_at"]; supplied {
		if !validRef(update.WorkRef, "project-understanding.yaml") {
			return data, false
		}
		work, ok := ledger.Records[sourceID(update.WorkRef, "project-understanding.yaml")]
		if !ok || work.Type != "work" || work.Data.State != workStateCompleted || work.Data.CompletedAt == "" || !sameValue(completed, work.Data.CompletedAt) {
			return data, false
		}
	}
	terminal := update.Fields[statusField] == "CLOSED" || update.Fields["lifecycle"] == "cancelled"
	candidate := data
	if terminal {
		if !validRef(update.WorkRef, "project-understanding.yaml") || strings.TrimSpace(update.Evidence) == "" {
			return data, false
		}
		work, ok := ledger.Records[sourceID(update.WorkRef, "project-understanding.yaml")]
		if !ok || work.Type != "work" {
			return data, false
		}
		if update.Fields["lifecycle"] == "cancelled" {
			if work.Data.State != workStateCancelled {
				return data, false
			}
		}
		if update.Fields["lifecycle"] != "cancelled" && (work.Data.State != workStateCompleted || work.Data.CompletedAt == "" || !sameValue(update.Fields["completed_at"], work.Data.CompletedAt)) {
			return data, false
		}
		if forecast := mapValue(lane, "forecast"); forecast != nil {
			if prior, exists := previous["forecast"]; exists && !laneForecastMatchesPrior(forecast, prior) {
				return data, false
			}
			ref := mapValue(forecast, "forecast_ref")
			if ref == nil || ref.Value != string(work.Data.ForecastRef) || !validRef(ref.Value, "forecasts.yaml") || textField(forecasts.Records[sourceID(ref.Value, "forecasts.yaml")], "status") != "inactive" {
				return data, false
			}
			candidate, ok = removeLaneSyncField(candidate, update, "forecast")
			if !ok {
				return data, false
			}
		}
		if mapValue(lane, "forecast") == nil && work.Data.ForecastRef != "" && (!validRef(string(work.Data.ForecastRef), "forecasts.yaml") || textField(forecasts.Records[sourceID(string(work.Data.ForecastRef), "forecasts.yaml")], "status") != "inactive") {
			return data, false
		}
	}
	if status, ok := update.Fields[statusField]; ok && status != "CLOSED" && mapValue(lane, "completed_at") != nil {
		if prior, exists := previous["completed_at"]; exists && scalarSyncOwnership(mapValue(lane, "completed_at")) != prior {
			return data, false
		}
		var removed bool
		candidate, removed = removeLaneSyncField(candidate, update, "completed_at")
		if !removed {
			return data, false
		}
	}
	for _, field := range update.Clear {
		var removed bool
		candidate, removed = removeLaneSyncField(candidate, update, field)
		if !removed {
			return data, false
		}
	}
	reopening := false
	if status, ok := update.Fields[statusField]; ok && status != "CLOSED" {
		reopening = true
	}
	for _, field := range update.Clear {
		if field == "lifecycle" {
			reopening = true
		}
	}
	if reopening && mapValue(lane, "forecast") == nil && validRef(update.WorkRef, "project-understanding.yaml") {
		work, ok := ledger.Records[sourceID(update.WorkRef, "project-understanding.yaml")]
		if ok && work.Type == "work" && openWork(work.Data.State) && work.Data.ForecastRef != "" {
			if prior, exists := previous["forecast"]; exists && prior != syncAbsentValue {
				return data, false
			}
			ref := string(work.Data.ForecastRef)
			if !validRef(ref, "forecasts.yaml") {
				return data, false
			}
			projection, valid := laneForecastProjection(ref, forecasts.Records[sourceID(ref, "forecasts.yaml")])
			if !valid {
				return data, false
			}
			candidate, valid = restoreLaneForecast(candidate, update, projection)
			if !valid {
				return data, false
			}
		}
	}
	for field, value := range update.Fields {
		var ok bool
		prior, exists := previous[field]
		if prior == syncAbsentValue {
			if mapValue(lane, field) != nil {
				return data, false
			}
		}
		candidate, ok = applySyncField(candidate, update, field, value, prior, exists)
		if !ok {
			return data, false
		}
	}
	if _, err := parse(candidate); err != nil {
		return data, false
	}
	return candidate, true
}

// laneForecastMatchesPrior compares the complete current forecast with its projected value.
//
// Example: Detect an owner edit before retiring an inactive forecast.
func laneForecastMatchesPrior(
	node *yaml.Node,
	prior string,
) bool {
	if prior == syncAbsentValue {
		return false
	}
	var current any
	if node.Decode(&current) != nil {
		return false
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return false
	}
	var actual, expected any
	if json.Unmarshal(encoded, &actual) != nil || json.Unmarshal([]byte(prior), &expected) != nil {
		return false
	}
	return reflect.DeepEqual(actual, expected)
}

// removeLaneSyncField removes a supplied field only when its bytes are unambiguous.
//
// Example: Remove a comment-free completion time when reopening a lane.
func removeLaneSyncField(
	data []byte,
	update syncUpdate,
	field string,
) ([]byte, bool) {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return data, false
	}
	list := mapValue(doc.Content[0], "lanes")
	if list == nil {
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
	if lane == nil || lane.Kind != yaml.MappingNode {
		return data, false
	}
	for i := 0; i+1 < len(lane.Content); i += 2 {
		key := lane.Content[i]
		if key.Value != field {
			continue
		}
		start, err := yamlByteOffset(data, key.Line, key.Column)
		if err != nil {
			return data, false
		}
		var end int
		if lane.Style&yaml.FlowStyle != 0 {
			if i+2 < len(lane.Content) {
				end, err = yamlByteOffset(data, lane.Content[i+2].Line, lane.Content[i+2].Column)
			} else {
				end, err = flowMappingEndCached(&flowDocument{data: data}, lane)
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
			if i+2 < len(lane.Content) {
				end, err = yamlByteOffset(data, lane.Content[i+2].Line, 1)
			} else {
				end, _, err = blockMappingEnd(data, lane)
			}
		}
		if err != nil || start < 0 || end < start || end > len(data) || laneRemovalHasComment(data, start, end) {
			return data, false
		}
		out := append(append([]byte(nil), data[:start]...), data[end:]...)
		return out, true
	}
	return data, true
}

// laneRemovalHasComment protects comments while allowing hash characters in references.
//
// Example: Preserve an inline owner comment during forecast retirement.
func laneRemovalHasComment(
	data []byte,
	start int,
	end int,
) bool {
	return yamlRangeHasComment(data, start, end)
}

// findSyncLane locates the lane selected by an update.
//
// Example: Match task, work reference, and required environment.
func findSyncLane(
	doc *yaml.Node,
	update syncUpdate,
) *yaml.Node {
	list := mapValue(doc.Content[0], "lanes")
	for _, item := range list.Content {
		id, ref, env := mapValue(item, "task_id"), mapValue(item, "work_ref"), mapValue(item, "required_environment")
		if id != nil && ref != nil && env != nil && id.Value == update.ID && ref.Value == update.WorkRef && env.Value == update.RequiredEnvironment.String() {
			return item
		}
	}
	return nil
}

// applyLaneSyncUpdate records the authorized changes accompanying a lane transition.
//
// Example: Record the forecast removal and status update for verification.
func applyLaneSyncUpdate(
	data []byte,
	update syncUpdate,
	ledger ledgerSource,
	forecasts forecastSource,
	previous map[string]string,
) ([]byte, []syncEffect, bool) {
	candidate, ok := applyLaneSyncUpdateRaw(data, update, ledger, forecasts, previous)
	if !ok {
		return data, nil, false
	}
	var before, after yaml.Node
	if yaml.Unmarshal(data, &before) != nil || yaml.Unmarshal(candidate, &after) != nil {
		return data, nil, false
	}
	oldLane, newLane := findSyncLane(&before, update), findSyncLane(&after, update)
	if oldLane == nil || newLane == nil {
		return data, nil, false
	}
	base := syncEffect{Target: syncTargetLane, ID: update.ID, WorkRef: update.WorkRef, RequiredEnvironment: update.RequiredEnvironment, Evidence: update.Evidence}
	var effects []syncEffect
	for _, field := range []string{"forecast", "completed_at"} {
		if mapValue(oldLane, field) != nil && mapValue(newLane, field) == nil {
			effect := base
			effect.Field = field
			effect.Operation = syncEffectCompletedAtClear
			if field == "forecast" {
				effect.Operation = syncEffectForecastRetire
			}
			effects = append(effects, effect)
		}
	}
	if mapValue(oldLane, "forecast") == nil && mapValue(newLane, "forecast") != nil {
		var value any
		if mapValue(newLane, "forecast").Decode(&value) != nil {
			return data, nil, false
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return data, nil, false
		}
		effect := base
		effect.Field, effect.Value, effect.Operation = "forecast", json.RawMessage(raw), syncEffectLaneForecastRestore
		effects = append(effects, effect)
	}
	for _, field := range update.Clear {
		if field == "completed_at" && mapValue(oldLane, field) != nil && mapValue(newLane, field) == nil {
			continue
		}
		effect := base
		effect.Field = field
		effect.Operation = syncEffectClear
		effects = append(effects, effect)
	}
	for field, value := range update.Fields {
		effect := base
		effect.Field = field
		effect.Value = value
		effect.Operation = syncEffectSet
		effects = append(effects, effect)
	}
	return candidate, effects, true
}
