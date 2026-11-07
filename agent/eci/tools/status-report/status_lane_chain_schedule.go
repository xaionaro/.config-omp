package main

import "encoding/json"

// applyPendingLaneLifecycleChain composes a verified sequence without publishing historical states.
//
// Example: Apply an explicit reactivation and current completion after a stale cancellation.
func applyPendingLaneLifecycleChain(
	data []byte,
	state *syncState,
	firstKey string,
	keys []string,
	logBytes []byte,
	ledger ledgerSource,
	forecasts forecastSource,
) ([]byte, []syncEffect, bool) {
	first, ok := state.Groups[firstKey]
	if !ok {
		return data, nil, false
	}
	firstUpdate, err := decodeSyncGroup(first.Update)
	if err != nil || firstUpdate.Target != syncTargetLane {
		return data, nil, false
	}
	selected := []syncPendingGroup{first}
	selectedKeys := []string{firstKey}
	var skipped []syncUpdate
	reached := false
	for _, key := range keys {
		group, exists := state.Groups[key]
		if !exists || group.Line < first.Line || (group.Line == first.Line && group.Ordinal <= first.Ordinal) {
			continue
		}
		next, err := decodeSyncGroup(group.Update)
		if err != nil || syncTargetKey(next, "") != syncTargetKey(firstUpdate, "") {
			continue
		}
		dependencies := syncGroupDependencies(next)
		if !syncDependenciesOverlap(syncGroupDependencies(firstUpdate), dependencies) {
			skipped = append(skipped, next)
			continue
		}
		selected = append(selected, group)
		selectedKeys = append(selectedKeys, key)
		if next.Fields[next.RequiredEnvironment.String()+"_status"] == "CLOSED" {
			reached = true
			break
		}
	}
	if !reached {
		return data, nil, false
	}
	for _, group := range selected {
		update, err := decodeSyncGroup(group.Update)
		if err != nil {
			return data, nil, false
		}
		for _, other := range skipped {
			if syncDependenciesOverlap(syncGroupDependencies(update), syncGroupDependencies(other)) {
				return data, nil, false
			}
		}
	}
	previous := map[string]string{}
	for _, group := range selected {
		update, _ := decodeSyncGroup(group.Update)
		for field, value := range syncGroupPriorValues(*state, update) {
			previous[field] = value
		}
	}
	candidate, effects, provenance, ok := planLaneLifecycleChain(data, selected, ledger, forecasts, previous)
	if !ok {
		return data, nil, false
	}
	if verifyLaneLifecycleChain(data, candidate, selected, effects, provenance, previous, logBytes, ledger, forecasts, state) != nil {
		return data, nil, false
	}
	valuesByField := map[string]string{}
	for field, group := range provenance {
		update, err := decodeSyncGroup(group.Update)
		if err != nil {
			return data, nil, false
		}
		values, ok := syncGroupAppliedValues(candidate, update)
		if !ok {
			return data, nil, false
		}
		value, exists := values[field]
		if !exists {
			if field == "forecast" || field == "completed_at" || field == "lifecycle" {
				value = syncAbsentValue
			} else {
				return data, nil, false
			}
		}
		for _, effect := range effects {
			if effect.Field == field && effect.Operation == syncEffectLaneForecastRestore {
				raw, ok := effect.Value.(json.RawMessage)
				if !ok {
					return data, nil, false
				}
				value = string(raw)
			}
		}
		valuesByField[field] = value
	}
	for field, group := range provenance {
		target := syncTargetKey(firstUpdate, field)
		state.Applied[target] = valuesByField[field]
		state.Latest[target] = group.Line
		state.AppliedOrdinals[target] = group.Ordinal
	}
	for _, key := range selectedKeys {
		delete(state.Groups, key)
	}
	return candidate, effects, true
}
