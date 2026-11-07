package main

import (
	"encoding/json"
	"reflect"
)

// applyPendingLaneCorrection applies a source-supported correction to an unapplied closure.
//
// Example: Correct an obsolete completion time without inventing the earlier source state.
func applyPendingLaneCorrection(
	data []byte,
	state *syncState,
	old syncUpdate,
	oldGroup syncPendingGroup,
	keys []string,
	logBytes []byte,
	ledger ledgerSource,
	forecasts forecastSource,
) ([]byte, []syncEffect, string, bool) {
	if old.Target != syncTargetLane || old.Corrects != 0 || old.Retract || len(old.Values) != 0 || len(old.Clear) != 0 {
		return data, nil, "", false
	}
	status := old.RequiredEnvironment.String() + "_status"
	if old.Fields[status] != "CLOSED" || old.Fields["completed_at"] == "" {
		return data, nil, "", false
	}
	work, ok := ledger.Records[sourceID(old.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || work.Data.State != workStateCompleted || sameValue(work.Data.CompletedAt, old.Fields["completed_at"]) {
		return data, nil, "", false
	}
	for _, key := range keys {
		group, exists := state.Groups[key]
		if !exists || group.Line < oldGroup.Line || (group.Line == oldGroup.Line && group.Ordinal <= oldGroup.Ordinal) {
			continue
		}
		next, err := decodeSyncGroup(group.Update)
		if err != nil || syncTargetKey(next, "") != syncTargetKey(old, "") {
			continue
		}
		if !syncDependenciesOverlap(syncGroupDependencies(old), syncGroupDependencies(next)) {
			continue
		}
		if next.Corrects != oldGroup.Line || next.Retract || !reflect.DeepEqual(syncGroupFields(old), syncGroupFields(next)) {
			return data, nil, "", false
		}
		if next.CorrectsOrdinal == nil && oldGroup.Ordinal != 0 {
			return data, nil, "", false
		}
		if next.CorrectsOrdinal != nil && *next.CorrectsOrdinal != oldGroup.Ordinal {
			return data, nil, "", false
		}
		for _, field := range syncGroupFields(old) {
			if state.Latest[syncTargetKey(old, field)] >= oldGroup.Line {
				return data, nil, "", false
			}
		}
		candidate, effects, ok := applySyncGroup(data, next, ledger, forecasts, syncGroupPriorValues(*state, next))
		if !ok {
			return data, nil, "", false
		}
		for i := range effects {
			effects[i].Evidence = syncGroupEvidence(next)
		}
		if verifyStatusSyncCanonicalEffects(data, candidate, effects, ledger, forecasts) != nil {
			return data, nil, "", false
		}
		if verifyPendingLaneCorrection(data, candidate, oldGroup, group, effects, syncGroupPriorValues(*state, next), logBytes, ledger, forecasts) != nil {
			return data, nil, "", false
		}
		values, ok := syncGroupAppliedValues(candidate, next)
		if !ok {
			return data, nil, "", false
		}
		for _, change := range effects {
			if change.Operation == syncEffectLaneForecastRestore {
				if _, ok := change.Value.(json.RawMessage); !ok {
					return data, nil, "", false
				}
			}
		}
		for _, field := range syncGroupFields(next) {
			target := syncTargetKey(next, field)
			state.Latest[target] = group.Line
			state.AppliedOrdinals[target] = group.Ordinal
			state.Applied[target] = values[field]
		}
		for _, change := range effects {
			switch change.Operation {
			case syncEffectCompletedAtClear, syncEffectForecastRetire:
				field := "completed_at"
				if change.Operation == syncEffectForecastRetire {
					field = "forecast"
				}
				target := syncTargetKey(next, field)
				state.Applied[target] = syncAbsentValue
				state.Latest[target] = group.Line
				state.AppliedOrdinals[target] = group.Ordinal
			case syncEffectLaneForecastRestore:
				raw, ok := change.Value.(json.RawMessage)
				if !ok {
					return data, nil, "", false
				}
				target := syncTargetKey(next, "forecast")
				state.Applied[target] = string(raw)
				state.Latest[target] = group.Line
				state.AppliedOrdinals[target] = group.Ordinal
			}
		}
		return candidate, effects, key, true
	}
	return data, nil, "", false
}
