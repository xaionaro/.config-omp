package main

import (
	"encoding/json"
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
	return false
}
