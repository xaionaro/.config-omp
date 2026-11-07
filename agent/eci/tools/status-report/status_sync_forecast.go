package main

import (
	"encoding/json"
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// refreshOwnedForecasts advances ownership after a verified canonical projection.
//
// Example: A source target change does not look like an owner edit on later closure.
func refreshOwnedForecasts(
	before []byte,
	projected []byte,
) ([]byte, error) {
	var original, updated yaml.Node
	if err := yaml.Unmarshal(before, &original); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(projected, &updated); err != nil {
		return nil, err
	}
	metadata := mapValue(original.Content[0], "status_sync")
	if metadata == nil {
		return projected, nil
	}
	var state syncState
	if err := metadata.Decode(&state); err != nil {
		return nil, err
	}
	oldLanes, newLanes := mapValue(original.Content[0], "lanes"), mapValue(updated.Content[0], "lanes")
	if oldLanes == nil || newLanes == nil || oldLanes.Kind != yaml.SequenceNode || newLanes.Kind != yaml.SequenceNode {
		return projected, nil
	}
	changed := false
	for _, oldLane := range oldLanes.Content {
		id, ref, env := mapValue(oldLane, "task_id"), mapValue(oldLane, "work_ref"), mapValue(oldLane, "required_environment")
		oldForecast := mapValue(oldLane, "forecast")
		if id == nil || ref == nil || env == nil || oldForecast == nil {
			continue
		}
		update := syncUpdate{Target: syncTargetLane, ID: id.Value, WorkRef: ref.Value, RequiredEnvironment: parseSyncEnvironment(env.Value)}
		if update.RequiredEnvironment == syncEnvironmentUnknown {
			continue
		}
		key := syncTargetKey(update, "forecast")
		previous, owned := state.Applied[key]
		if !owned || previous == syncAbsentValue {
			continue
		}
		oldValue, ok := syncForecastValue(oldForecast)
		if !ok {
			continue
		}
		var prior any
		if json.Unmarshal([]byte(previous), &prior) != nil || !reflect.DeepEqual(oldValue, prior) {
			continue
		}
		var newForecast *yaml.Node
		found := false
		for _, lane := range newLanes.Content {
			laneID, laneRef, laneEnv := mapValue(lane, "task_id"), mapValue(lane, "work_ref"), mapValue(lane, "required_environment")
			if laneID != nil && laneRef != nil && laneEnv != nil && laneID.Value == id.Value && laneRef.Value == ref.Value && laneEnv.Value == env.Value {
				if found {
					return nil, fmt.Errorf("ambiguous lane during forecast ownership refresh")
				}
				found = true
				newForecast = mapValue(lane, "forecast")
			}
		}
		if newForecast == nil {
			continue
		}
		newValue, ok := syncForecastValue(newForecast)
		if !ok {
			continue
		}
		if reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		encoded, err := json.Marshal(newValue)
		if err != nil {
			return nil, err
		}
		state.Applied[key] = string(encoded)
		changed = true
	}
	if !changed {
		return projected, nil
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	result, err := putSyncState(projected, string(encoded))
	if err != nil {
		return nil, err
	}
	if err := verifyStatusSyncEffects(projected, result, []syncEffect{{Operation: syncEffectMetadata, Value: state}}); err != nil {
		return nil, fmt.Errorf("unsafe forecast ownership refresh: %w", err)
	}
	return result, nil
}

// syncForecastValue decodes a forecast into JSON-compatible semantic values.
//
// Example: Compare complete forecast values before advancing ownership.
func syncForecastValue(node *yaml.Node) (any, bool) {
	var value any
	if node.Decode(&value) != nil {
		return nil, false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var normalized any
	if json.Unmarshal(encoded, &normalized) != nil {
		return nil, false
	}
	return normalized, true
}
