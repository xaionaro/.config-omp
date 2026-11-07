package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// applyPendingReprioritizations applies a supplied estimate and existing task change together.
//
// Example: Move a task to a new priority without publishing an invalid intermediate report.
func applyPendingReprioritizations(
	data []byte,
	state *syncState,
	ledger ledgerSource,
	forecasts forecastSource,
	onlyKey string,
) ([]byte, []syncEffect) {
	keys := make([]string, 0, len(state.Groups))
	for key := range state.Groups {
		keys = append(keys, key)
	}
	sort.Slice(keys,
		// Preserve the event and within-event transaction order.
		//
		// Example: An earlier task update remains ahead of a later reprioritization.
		func(
			i int,
			j int,
		) bool {
			a, b := state.Groups[keys[i]], state.Groups[keys[j]]
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Ordinal < b.Ordinal
		})
	var effects []syncEffect
	for _, key := range keys {
		if key != onlyKey {
			continue
		}
		group, exists := state.Groups[key]
		if !exists {
			continue
		}
		update, err := decodeSyncGroup(group.Update)
		if err != nil || update.Target != syncTargetTask || len(update.Values) == 0 {
			continue
		}
		priority, ok := syncPriorityLabelFromJSON(update.Values["priority"])
		if !ok {
			continue
		}
		estimateKey := "addition/priority_estimate/" + priority
		estimate, exists := state.PendingAdditions[estimateKey]
		if !exists || estimate.Line != group.Line || state.Latest[estimateKey] != estimate.Line {
			continue
		}
		if syncGroupOvertaken(*state, update, group.Line, group.Ordinal) || !syncGroupCorrectionMatches(*state, update) || syncHasPriorTargetGroup(*state, update, group) {
			continue
		}
		addition := syncAddition{Target: syncTargetPriorityEstimate, Record: json.RawMessage(estimate.Record), RetirePriority: estimate.RetirePriority, ExpectedPrior: json.RawMessage(estimate.ExpectedPrior)}
		candidate := data
		var removalEffects []syncEffect
		if addition.RetirePriority != "" {
			if addition.RetirePriority == priority || !syncTaskHasPriority(data, update.ID, addition.RetirePriority) {
				continue
			}
			var ok bool
			candidate, ok = removeRootSyncRecord(candidate, syncTargetPriorityEstimate, addition.RetirePriority, addition.ExpectedPrior)
			if !ok {
				continue
			}
			removalEffects = append(removalEffects, syncEffect{Target: syncTargetPriorityEstimate, ID: addition.RetirePriority, Value: addition, Operation: syncEffectRemove})
		}
		exists, matches := existingPriorityEstimate(data, priority, addition.Record)
		if exists && !matches {
			continue
		}
		if !exists {
			var ok bool
			candidate, ok = insertSyncAddition(candidate, addition)
			if !ok {
				continue
			}
		}
		candidate, changes, ok := applySyncGroup(candidate, update, ledger, forecasts, syncGroupPriorValues(*state, update))
		if !ok {
			continue
		}
		values, ok := syncGroupAppliedValues(candidate, update)
		if !ok {
			continue
		}
		groupEffects := removalEffects
		if !exists {
			additionEffect := syncEffect{Target: syncTargetPriorityEstimate, ID: priority, Value: json.RawMessage(estimate.Record), Operation: syncEffectAdd}
			groupEffects = append(groupEffects, additionEffect)
		}
		groupEffects = append(groupEffects, changes...)
		if verifyPriorityRetirementEffects(data, groupEffects) != nil {
			continue
		}
		if verifyStatusSyncCanonicalEffects(data, candidate, groupEffects, ledger, forecasts) != nil {
			continue
		}
		ownership, valid := additionOwnership(syncTargetPriorityEstimate, json.RawMessage(estimate.Record))
		if !valid {
			continue
		}
		data = candidate
		effects = append(effects, groupEffects...)
		if !exists {
			recordAdditionOwnership(state, syncUpdate{Target: syncTargetPriorityEstimate, ID: priority}, estimate.Line, ownership)
			state.Applied[estimateKey] = estimate.Record
		}
		delete(state.PendingAdditions, estimateKey)
		for _, field := range syncGroupFields(update) {
			target := syncTargetKey(update, field)
			state.Latest[target] = group.Line
			state.AppliedOrdinals[target] = group.Ordinal
			state.Applied[target] = values[field]
		}
		delete(state.Groups, key)
	}
	return data, effects
}

// syncGroupAwaitsPriorityEstimate keeps a paired transaction atomic while pending.
//
// Example: A task move waits when its same-event estimate cannot yet be retired.
func syncGroupAwaitsPriorityEstimate(
	state syncState,
	update syncUpdate,
	group syncPendingGroup,
) bool {
	if update.Target != syncTargetTask {
		return false
	}
	priority, ok := syncPriorityLabelFromJSON(update.Values["priority"])
	if !ok {
		return false
	}
	key := "addition/priority_estimate/" + priority
	estimate, ok := state.PendingAdditions[key]
	return ok && estimate.Line == group.Line && state.Latest[key] == estimate.Line
}

// syncPriorityLabelFromJSON reads a supplied label without numeric coercion.
//
// Example: Numeric JSON 1.10 identifies the priority label "1.10".
func syncPriorityLabelFromJSON(raw json.RawMessage) (string, bool) {
	if !json.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var priority string
	if yaml.Unmarshal(raw, &priority) != nil || strings.TrimSpace(priority) == "" {
		return "", false
	}
	return priority, true
}

// existingPriorityEstimate checks whether a supplied estimate already exists unchanged.
//
// Example: Reuse an identical owner-authored low-priority estimate without taking ownership.
func existingPriorityEstimate(
	data []byte,
	priority string,
	record json.RawMessage,
) (bool, bool) {
	report, err := decodeSyncVerificationReport(data)
	var supplied map[string]any
	if err != nil || yaml.Unmarshal(record, &supplied) != nil {
		return true, false
	}
	estimates, ok := report["priority_estimates"].([]any)
	if !ok {
		return true, false
	}
	for _, item := range estimates {
		estimate, ok := item.(map[string]any)
		if !ok {
			return true, false
		}
		if estimate["priority"] == priority {
			return true, equalPriorityEstimateRecord(estimate, supplied)
		}
	}
	return false, false
}

// equalPriorityEstimateRecord compares every supplied field and YAML timestamp value.
//
// Example: An unquoted YAML timestamp matches the same JSON timestamp string.
func equalPriorityEstimateRecord(
	existing map[string]any,
	supplied map[string]any,
) bool {
	if reflect.DeepEqual(existing, supplied) {
		return true
	}
	var stamp time.Time
	switch value := existing["as_of"].(type) {
	case time.Time:
		stamp = value
	case string:
		var err error
		stamp, err = parseUTC(value)
		if err != nil {
			return false
		}
	default:
		return false
	}
	text, ok := supplied["as_of"].(string)
	if !ok {
		return false
	}
	parsed, err := parseUTC(text)
	if err != nil || !stamp.Equal(parsed) {
		return false
	}
	normalized := make(map[string]any, len(existing))
	for key, value := range existing {
		normalized[key] = value
	}
	normalized["as_of"] = text
	return reflect.DeepEqual(normalized, supplied)
}

// syncTaskHasPriority confirms an explicit retirement belongs to the selected task.
//
// Example: Reprioritizing one task cannot remove an unrelated estimate.
func syncTaskHasPriority(
	data []byte,
	id string,
	priority string,
) bool {
	report, err := parse(data)
	if err != nil {
		return false
	}
	for _, task := range report.Tasks {
		if string(task.ID) == id {
			return string(task.Priority) == priority
		}
	}
	return false
}

// syncHasPriorTargetGroup keeps target-local updates in their original order.
//
// Example: A blocked earlier task update is not bypassed by reprioritization.
func syncHasPriorTargetGroup(
	state syncState,
	update syncUpdate,
	group syncPendingGroup,
) bool {
	for _, other := range state.Groups {
		if other.Line > group.Line || (other.Line == group.Line && other.Ordinal >= group.Ordinal) {
			continue
		}
		prior, err := decodeSyncGroup(other.Update)
		if err != nil {
			continue
		}
		if syncTargetKey(prior, "") == syncTargetKey(update, "") && syncDependenciesOverlap(syncGroupDependencies(prior), syncGroupDependencies(update)) {
			return true
		}
	}
	return false
}
