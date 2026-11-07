package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// verifyLaneLifecycleChain independently checks the selected log transactions and their net effects.
//
// Example: Reject a retained field whose supplying log update differs.
func verifyLaneLifecycleChain(
	original []byte,
	candidate []byte,
	groups []syncPendingGroup,
	effects []syncEffect,
	provenance map[string]syncPendingGroup,
	previous map[string]string,
	log []byte,
	ledger ledgerSource,
	forecasts forecastSource,
	states ...*syncState,
) error {
	if len(groups) == 0 {
		return fmt.Errorf("empty lane chain")
	}
	lines := bytes.Split(log, []byte("\n"))
	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil || len(doc.Content) != 1 {
		return fmt.Errorf("invalid original report")
	}
	lanes := mapValue(doc.Content[0], "lanes")
	if lanes == nil || lanes.Kind != yaml.SequenceNode {
		return fmt.Errorf("missing lanes")
	}
	first, err := decodeSyncGroup(groups[0].Update)
	if err != nil {
		return err
	}
	var lane *yaml.Node
	for _, item := range lanes.Content {
		id, ref, env := mapValue(item, "task_id"), mapValue(item, "work_ref"), mapValue(item, "required_environment")
		if id != nil && ref != nil && env != nil && id.Value == first.ID && ref.Value == first.WorkRef && env.Value == first.RequiredEnvironment.String() {
			if lane != nil {
				return fmt.Errorf("ambiguous lane")
			}
			lane = item
		}
	}
	if lane == nil {
		return fmt.Errorf("missing lane")
	}
	owned := map[string]string{}
	last := map[string]syncPendingGroup{}
	values := map[string]string{}
	cleared := map[string]bool{}
	cancelled := mapValue(lane, "lifecycle") != nil && mapValue(lane, "lifecycle").Value == "cancelled"
	statusField := first.RequiredEnvironment.String() + "_status"
	closed := mapValue(lane, statusField) != nil && mapValue(lane, statusField).Value == "CLOSED"
	reactivated := false
	for i, group := range groups {
		if group.Line < 1 || group.Line > len(lines) || group.Ordinal < 0 || (i > 0 && (group.Line < groups[i-1].Line || (group.Line == groups[i-1].Line && group.Ordinal <= groups[i-1].Ordinal))) {
			return fmt.Errorf("invalid chain position")
		}
		var envelope struct {
			Event string `json:"event"`
		}
		updates, array := laneRawUpdates(lines[group.Line-1])
		if json.Unmarshal(lines[group.Line-1], &envelope) != nil || envelope.Event == "correction" || !array || group.Ordinal >= len(updates) {
			return fmt.Errorf("missing log update")
		}
		update, err := decodeSyncGroup(group.Update)
		if err != nil {
			return err
		}
		var logged syncUpdate
		if json.Unmarshal(updates[group.Ordinal], &logged) != nil || !reflect.DeepEqual(update, logged) || update.Target != syncTargetLane || syncTargetKey(update, "") != syncTargetKey(first, "") || !validLaneSyncUpdate(update) || update.Corrects != 0 || update.Retract {
			return fmt.Errorf("chain update differs from log")
		}
		status := update.RequiredEnvironment.String() + "_status"
		if i == len(groups)-1 && (update.Fields[status] != "CLOSED" || update.Fields["completed_at"] == "" || strings.TrimSpace(update.Evidence) == "") {
			return fmt.Errorf("final update lacks completion")
		}
		if update.Fields["lifecycle"] == "cancelled" && cancelled {
			return fmt.Errorf("invalid cancellation transition")
		}
		if value, supplied := update.Fields[status]; supplied {
			if value == "CLOSED" {
				if cancelled || closed || update.Fields["completed_at"] == "" || strings.TrimSpace(update.Evidence) == "" {
					return fmt.Errorf("invalid completion transition")
				}
				closed = true
			}
			if value != "CLOSED" && closed {
				if strings.TrimSpace(update.Evidence) == "" {
					return fmt.Errorf("reopening lacks evidence")
				}
				closed = false
				reactivated = true
			}
		}
		for _, field := range syncGroupFields(update) {
			prior, seen := owned[field]
			if !seen {
				prior = scalarSyncOwnership(mapValue(lane, field))
			}
			if expected, exists := previous[field]; !seen && exists && prior != expected {
				return fmt.Errorf("lane field %s ownership changed", field)
			}
			if value, exists := update.Fields[field]; exists {
				owned[field] = scalarSyncStoredValue(value)
				values[field] = value
				cleared[field] = false
			} else {
				owned[field] = syncAbsentValue
				delete(values, field)
				cleared[field] = true
			}
			last[field] = group
		}
		if update.Fields["lifecycle"] == "cancelled" {
			if strings.TrimSpace(update.Evidence) == "" {
				return fmt.Errorf("cancellation lacks evidence")
			}
			cancelled = true
			reactivated = false
		}
		for _, field := range update.Clear {
			if field == "lifecycle" {
				if !cancelled || strings.TrimSpace(update.Evidence) == "" {
					return fmt.Errorf("reactivation lacks cancellation or evidence")
				}
				cancelled = false
				reactivated = true
			}
		}
	}
	dependencies := map[string]bool{}
	selected := map[string]bool{}
	for _, group := range groups {
		update, _ := decodeSyncGroup(group.Update)
		for field := range syncGroupDependencies(update) {
			dependencies[field] = true
		}
		selected[syncGroupKey(group.Line, group.Ordinal)] = true
	}
	start, end := groups[0], groups[len(groups)-1]
	for line := start.Line; line <= end.Line; line++ {
		updates, array := laneRawUpdates(lines[line-1])
		for ordinal, raw := range updates {
			if (line == start.Line && ordinal <= start.Ordinal) || (line == end.Line && ordinal >= end.Ordinal) || selected[syncGroupKey(line, ordinal)] {
				continue
			}
			if !laneRawIdentifies(raw, first) {
				continue
			}
			var update syncUpdate
			if !array || json.Unmarshal(raw, &update) != nil {
				return fmt.Errorf("malformed intervening lane update")
			}
			if update.Target == syncTargetLane && syncTargetKey(update, "") == syncTargetKey(first, "") && syncDependenciesOverlap(dependencies, syncGroupDependencies(update)) {
				if len(states) != 1 || states[0] == nil || update.Corrects != 0 || update.Retract || len(update.Values) != 0 || !validLaneSyncUpdate(update) {
					return fmt.Errorf("chain skips dependent update")
				}
				for _, field := range syncGroupFields(update) {
					key := syncTargetKey(update, field)
					value := syncAbsentValue
					if supplied, ok := update.Fields[field]; ok {
						value = scalarSyncStoredValue(supplied)
					}
					applied, exists := states[0].Applied[key]
					if !exists || states[0].Latest[key] != line || states[0].AppliedOrdinals[key] != ordinal || applied != value || scalarSyncOwnership(mapValue(lane, field)) != value {
						return fmt.Errorf("skipped update lacks applied ownership")
					}
				}
			}
		}
	}
	if cancelled || !reactivated || values[statusField] != "CLOSED" || values["completed_at"] == "" {
		return fmt.Errorf("chain lacks final completion")
	}
	if _, ok := provenance["forecast"]; ok {
		last["forecast"] = groups[len(groups)-1]
	}
	if len(provenance) != len(last) {
		return fmt.Errorf("incomplete field provenance")
	}
	for field, group := range last {
		if provenance[field] != group {
			return fmt.Errorf("field %s provenance differs", field)
		}
	}
	seen := map[string]bool{}
	for _, effect := range effects {
		if effect.Target != syncTargetLane || effect.ID != first.ID || effect.WorkRef != first.WorkRef || effect.RequiredEnvironment != first.RequiredEnvironment {
			return fmt.Errorf("effect targets another lane")
		}
		field := effect.Field
		if effect.Operation == syncEffectForecastRetire {
			field = "forecast"
		}
		if field == "forecast" {
			prior, exists := previous["forecast"]
			forecast := mapValue(lane, "forecast")
			final, _ := decodeSyncGroup(groups[len(groups)-1].Update)
			if provenance[field] != groups[len(groups)-1] || effect.Evidence != syncGroupEvidence(final) || effect.Operation != syncEffectForecastRetire || seen[field] || forecast == nil || (exists && !laneForecastMatchesPrior(forecast, prior)) {
				return fmt.Errorf("forecast ownership changed")
			}
			seen[field] = true
			continue
		}
		group, ok := last[field]
		if !ok || seen[field] {
			return fmt.Errorf("unattributed lane effect")
		}
		seen[field] = true
		update, _ := decodeSyncGroup(group.Update)
		if effect.Evidence != syncGroupEvidence(update) {
			return fmt.Errorf("effect evidence differs")
		}
		if cleared[field] {
			if effect.Operation != syncEffectClear && effect.Operation != syncEffectCompletedAtClear {
				return fmt.Errorf("clear effect differs")
			}
			continue
		}
		if effect.Operation != syncEffectSet || effect.Value != values[field] {
			return fmt.Errorf("set effect differs")
		}
	}
	for field := range last {
		if !seen[field] && !(field == "lifecycle" && cleared[field] && mapValue(lane, field) == nil) {
			return fmt.Errorf("missing field effect %s", field)
		}
	}
	if err := verifyStatusSyncEffects(original, candidate, effects); err != nil {
		return err
	}
	return verifyStatusSyncCanonicalEffects(original, candidate, effects, ledger, forecasts)
}
