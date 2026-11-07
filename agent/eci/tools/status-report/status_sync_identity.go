package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// syncUpdateSelectorValid rejects selectors that do not identify the actual target.
//
// Example: A task work reference cannot create a second ownership identity.
func syncUpdateSelectorValid(update syncUpdate) bool {
	if update.Target == syncTargetLane {
		return strings.TrimSpace(update.WorkRef) != "" && update.RequiredEnvironment != syncEnvironmentUnknown
	}
	if update.RequiredEnvironment != syncEnvironmentUnknown {
		return false
	}
	if update.Target == syncTargetRootForecast && validRootRetirementSyncUpdate(update) {
		return strings.TrimSpace(update.WorkRef) != ""
	}
	return update.WorkRef == ""
}

// canonicalSyncOwnershipKey maps historical selector aliases to the actual target identity.
//
// Example: An old task key containing a work reference maps to the task alone.
func canonicalSyncOwnershipKey(key string) string {
	index := strings.LastIndex(key, "/")
	if index < 0 {
		return key
	}
	var selector []string
	if json.Unmarshal([]byte(key[:index]), &selector) != nil || len(selector) != 4 {
		return key
	}
	target := parseSyncTarget(selector[0])
	if target == syncTargetUnknown || target == syncTargetLane {
		return key
	}
	selector[2], selector[3] = "", ""
	encoded, err := json.Marshal(selector)
	if err != nil {
		return key
	}
	return string(encoded) + key[index:]
}

// normalizeSyncOwnership merges old selector aliases without losing conflicting history.
//
// Example: Existing task ownership remains effective after selector validation is tightened.
func normalizeSyncOwnership(state *syncState) error {
	applied := map[string]string{}
	for key, value := range state.Applied {
		canonical := canonicalSyncOwnershipKey(key)
		if prior, ok := applied[canonical]; ok && prior != value {
			return fmt.Errorf("conflicting legacy status ownership for %s", canonical)
		}
		applied[canonical] = value
	}
	latest := map[string]int{}
	ordinals := map[string]int{}
	for key, line := range state.Latest {
		canonical := canonicalSyncOwnershipKey(key)
		ordinal, hasOrdinal := state.AppliedOrdinals[key]
		oldLine, exists := latest[canonical]
		oldOrdinal, hadOrdinal := ordinals[canonical]
		if !exists || line > oldLine || (line == oldLine && (ordinal > oldOrdinal || (ordinal == oldOrdinal && hasOrdinal && !hadOrdinal))) {
			latest[canonical] = line
			if hasOrdinal {
				ordinals[canonical] = ordinal
			} else {
				delete(ordinals, canonical)
			}
		}
	}
	pending := map[string]syncPending{}
	for key, value := range state.Pending {
		canonical := canonicalSyncOwnershipKey(key)
		if prior, ok := pending[canonical]; ok && !reflect.DeepEqual(prior, value) {
			return fmt.Errorf("conflicting legacy pending updates for %s", canonical)
		}
		pending[canonical] = value
	}
	state.Applied, state.Latest, state.AppliedOrdinals, state.Pending = applied, latest, ordinals, pending
	return nil
}

// recordAdditionOwnership binds supplied fields to their new report record.
//
// Example: A later review update cannot overwrite an owner-edited critic.
func recordAdditionOwnership(
	state *syncState,
	update syncUpdate,
	line int,
	values map[string]string,
) {
	for field, value := range values {
		key := syncTargetKey(update, field)
		if latest, exists := state.Latest[key]; exists && latest >= line {
			continue
		}
		state.Applied[key] = value
		if value == syncAbsentValue && syncEarlierPendingField(*state, update, field, line) {
			continue
		}
		state.Latest[key] = line
		state.AppliedOrdinals[key] = 0
	}
}

// pendingAdditionSelector resolves the exact identity of an inserted record.
//
// Example: A lane's task, work reference, and environment remain distinct.
func pendingAdditionSelector(
	key string,
	pending syncPendingAddition,
) (syncUpdate, bool) {
	id := pending.ID
	if id == "" {
		id = strings.TrimPrefix(key, "addition/"+pending.Target.String()+"/")
	}
	update := syncUpdate{Target: pending.Target, ID: id, WorkRef: pending.WorkRef, RequiredEnvironment: pending.RequiredEnvironment}
	if pending.Target == syncTargetLane && (update.WorkRef == "" || update.RequiredEnvironment == syncEnvironmentUnknown) {
		var record struct {
			TaskID              string          `json:"task_id"`
			WorkRef             string          `json:"work_ref"`
			RequiredEnvironment syncEnvironment `json:"required_environment"`
		}
		if json.Unmarshal([]byte(pending.Record), &record) != nil {
			return syncUpdate{}, false
		}
		update.ID, update.WorkRef, update.RequiredEnvironment = record.TaskID, record.WorkRef, record.RequiredEnvironment
	}
	return update, strings.TrimSpace(update.ID) != ""
}

// syncEarlierPendingField preserves an earlier update awaiting an absent field.
//
// Example: An assignment recorded before task creation can still apply.
func syncEarlierPendingField(
	state syncState,
	update syncUpdate,
	field string,
	line int,
) bool {
	key := syncTargetKey(update, field)
	for _, pending := range state.Groups {
		if pending.Line >= line {
			continue
		}
		prior, err := decodeSyncGroup(pending.Update)
		if err != nil {
			continue
		}
		for _, priorField := range syncGroupFields(prior) {
			if syncTargetKey(prior, priorField) == key {
				return true
			}
		}
	}
	if pending, ok := state.Pending[key]; ok && pending.Line < line {
		return true
	}
	return false
}
