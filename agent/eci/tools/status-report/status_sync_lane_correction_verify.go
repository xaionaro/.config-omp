package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// verifyPendingLaneCorrection validates a pending closure correction against its immutable log.
//
// Example: Reject a correction that omits an original context field.
func verifyPendingLaneCorrection(
	original []byte,
	candidate []byte,
	oldGroup syncPendingGroup,
	newGroup syncPendingGroup,
	effects []syncEffect,
	previous map[string]string,
	log []byte,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	lines := bytes.Split(log, []byte("\n"))
	if oldGroup.Line < 1 || newGroup.Line > len(lines) || oldGroup.Ordinal < 0 || newGroup.Ordinal < 0 || newGroup.Line < oldGroup.Line || (newGroup.Line == oldGroup.Line && newGroup.Ordinal <= oldGroup.Ordinal) {
		return fmt.Errorf("invalid correction positions")
	}
	var oldEvent, newEvent struct {
		Event string `json:"event"`
	}
	oldUpdates, oldArray := laneRawUpdates(lines[oldGroup.Line-1])
	newUpdates, newArray := laneRawUpdates(lines[newGroup.Line-1])
	if json.Unmarshal(lines[oldGroup.Line-1], &oldEvent) != nil || json.Unmarshal(lines[newGroup.Line-1], &newEvent) != nil || oldEvent.Event == "correction" || newEvent.Event != "correction" || !oldArray || !newArray || oldGroup.Ordinal >= len(oldUpdates) || newGroup.Ordinal >= len(newUpdates) {
		return fmt.Errorf("invalid correction log")
	}
	old, err := decodeSyncGroup(oldGroup.Update)
	if err != nil {
		return err
	}
	next, err := decodeSyncGroup(newGroup.Update)
	if err != nil {
		return err
	}
	var oldLogged, newLogged syncUpdate
	if json.Unmarshal(oldUpdates[oldGroup.Ordinal], &oldLogged) != nil || json.Unmarshal(newUpdates[newGroup.Ordinal], &newLogged) != nil || !reflect.DeepEqual(old, oldLogged) || !reflect.DeepEqual(next, newLogged) || old.Target != syncTargetLane || !validLaneSyncUpdate(old) || !validLaneSyncUpdate(next) || old.Corrects != 0 || next.Corrects != oldGroup.Line || next.Retract || syncTargetKey(old, "") != syncTargetKey(next, "") || !reflect.DeepEqual(syncGroupFields(old), syncGroupFields(next)) {
		return fmt.Errorf("correction differs from log or original fields")
	}
	if (next.CorrectsOrdinal == nil && oldGroup.Ordinal != 0) || (next.CorrectsOrdinal != nil && *next.CorrectsOrdinal != oldGroup.Ordinal) {
		return fmt.Errorf("correction ordinal differs")
	}
	status := old.RequiredEnvironment.String() + "_status"
	if old.Fields[status] != "CLOSED" || next.Fields[status] != "CLOSED" || old.Fields["completed_at"] == "" || next.Fields["completed_at"] == "" {
		return fmt.Errorf("correction is not a completion")
	}
	work, ok := ledger.Records[sourceID(old.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || work.Data.State != workStateCompleted || sameValue(work.Data.CompletedAt, old.Fields["completed_at"]) {
		return fmt.Errorf("old completion is not stale")
	}
	for line := oldGroup.Line; line <= newGroup.Line; line++ {
		updates, array := laneRawUpdates(lines[line-1])
		for ordinal, raw := range updates {
			if (line == oldGroup.Line && ordinal <= oldGroup.Ordinal) || (line == newGroup.Line && ordinal >= newGroup.Ordinal) {
				continue
			}
			if !laneRawIdentifies(raw, old) {
				continue
			}
			var update syncUpdate
			if !array || json.Unmarshal(raw, &update) != nil {
				return fmt.Errorf("malformed intervening lane update")
			}
			if update.Target == syncTargetLane && syncTargetKey(update, "") == syncTargetKey(old, "") && syncDependenciesOverlap(syncGroupDependencies(old), syncGroupDependencies(update)) {
				return fmt.Errorf("intervening dependent update")
			}
		}
	}
	var doc yaml.Node
	if yaml.Unmarshal(original, &doc) != nil || len(doc.Content) != 1 {
		return fmt.Errorf("invalid report")
	}
	lane := findSyncLane(&doc, next)
	if lane == nil {
		return fmt.Errorf("lane missing")
	}
	for _, field := range syncGroupFields(next) {
		if prior, exists := previous[field]; exists && scalarSyncOwnership(mapValue(lane, field)) != prior {
			return fmt.Errorf("lane field %s ownership changed", field)
		}
	}
	seen := map[string]bool{}
	for _, effect := range effects {
		if effect.Target != syncTargetLane || effect.ID != next.ID || effect.WorkRef != next.WorkRef || effect.RequiredEnvironment != next.RequiredEnvironment || effect.Evidence != syncGroupEvidence(next) {
			return fmt.Errorf("effect identity or evidence differs")
		}
		field := effect.Field
		if effect.Operation == syncEffectForecastRetire {
			forecast := mapValue(lane, "forecast")
			prior, exists := previous["forecast"]
			if seen[field] || forecast == nil || (exists && !laneForecastMatchesPrior(forecast, prior)) {
				return fmt.Errorf("forecast ownership changed")
			}
			seen[field] = true
			continue
		}
		value, supplied := next.Fields[field]
		cleared := false
		for _, clear := range next.Clear {
			if clear == field {
				cleared = true
			}
		}
		if seen[field] || (supplied && (effect.Operation != syncEffectSet || effect.Value != value)) || (cleared && effect.Operation != syncEffectClear && effect.Operation != syncEffectCompletedAtClear) || (!supplied && !cleared) {
			return fmt.Errorf("effect differs from correction")
		}
		seen[field] = true
	}
	for _, field := range syncGroupFields(next) {
		if !seen[field] {
			return fmt.Errorf("missing effect %s", field)
		}
	}
	if len(next.Values) != 0 {
		return fmt.Errorf("unsupported correction fields")
	}
	if err := verifyStatusSyncEffects(original, candidate, effects); err != nil {
		return err
	}
	return verifyStatusSyncCanonicalEffects(original, candidate, effects, ledger, forecasts)
}
