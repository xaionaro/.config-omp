package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// applySyncGroup applies one supplied target transaction without intermediate publication.
//
// Example: Close a task together with its acceptance evidence.
func applySyncGroup(
	data []byte,
	update syncUpdate,
	ledger ledgerSource,
	forecasts forecastSource,
	previous map[string]string,
) ([]byte, []syncEffect, bool) {
	if validRootRetirementSyncUpdate(update) {
		return applyRootRetirementSyncUpdate(data, update, ledger, forecasts, previous)
	}
	if update.Target == syncTargetLane {
		return applyLaneSyncUpdate(data, update, ledger, forecasts, previous)
	}
	if len(update.Values) > 0 || len(update.Clear) > 0 {
		if !validStructuredSyncUpdate(update) {
			return data, nil, false
		}
		result, ok := applyStructuredSyncUpdate(data, update, previous)
		if !ok {
			return data, nil, false
		}
		var effects []syncEffect
		for field, value := range update.Fields {
			effects = append(effects, syncEffect{Target: update.Target, ID: update.ID, Field: field, Value: value, Operation: syncEffectSet})
		}
		for field, value := range update.Values {
			effects = append(effects, syncEffect{Target: update.Target, ID: update.ID, Field: field, Value: value, Operation: syncEffectSet})
		}
		for _, field := range update.Clear {
			effects = append(effects, syncEffect{Target: update.Target, ID: update.ID, Field: field, Operation: syncEffectClear})
		}
		if update.Target == syncTargetTask && update.Fields["lifecycle"] == "cancelled" {
			result, ok = removeCancelledPriority(result, taskID(update.ID))
			if !ok {
				return data, nil, false
			}
			if _, err := parse(result); err != nil {
				return data, nil, false
			}
			effects = append(effects, syncEffect{ID: update.ID, Operation: syncEffectPriorityRemove})
		}
		return result, effects, true
	}
	if !validSyncUpdate(update) && !validNarrativeSyncUpdate(update) {
		return data, nil, false
	}
	result := data
	var effects []syncEffect
	keys := make([]string, 0, len(update.Fields))
	for key := range update.Fields {
		keys = append(keys, key)
	}
	sort.Slice(keys,
		// Apply narrative text and acceptance evidence before dependent fields.
		//
		// Example: Insert text before its accompanying evidence.
		func(
			i int,
			j int,
		) bool {
			rank := map[string]int{"text": 0, "evidence": 1, "status": 2, "lifecycle": 3}
			left, lok := rank[keys[i]]
			right, rok := rank[keys[j]]
			if lok && rok && left != right {
				return left < right
			}
			if lok != rok {
				return lok
			}
			return keys[i] < keys[j]
		})
	managedText, managedTextPresent := previous["text"]
	for _, field := range keys {
		value := update.Fields[field]
		var next []byte
		var ok bool
		if validNarrativeSyncUpdate(update) {
			owned := narrativeSyncItemOwned(result, update, managedText, managedTextPresent)
			prior, present := previous[field]
			next, ok = applyNarrativeSyncField(result, update, field, value, prior, present, owned)
			if field == "text" && ok {
				managedText = value
				managedTextPresent = true
			}
		} else {
			prior, exists := previous[field]
			next, ok = applySyncField(result, update, field, value, prior, exists)
		}
		if !ok {
			return data, nil, false
		}
		result = next
		effects = append(effects, syncEffect{Target: update.Target, ID: update.ID, WorkRef: update.WorkRef, RequiredEnvironment: update.RequiredEnvironment, Field: field, Value: value, Operation: syncEffectSet})
	}
	if update.Target == syncTargetTask && update.Fields["lifecycle"] == "cancelled" {
		var ok bool
		result, ok = removeCancelledPriority(result, taskID(update.ID))
		if !ok {
			return data, nil, false
		}
		effects = append(effects, syncEffect{ID: update.ID, Operation: syncEffectPriorityRemove})
	}
	if _, err := parse(result); err != nil {
		return data, nil, false
	}
	return result, effects, true
}

// syncGroupFields returns the explicit fields that participate in a transaction.
//
// Example: A clear participates in supersession alongside a supplied value.
func syncGroupFields(update syncUpdate) []string {
	if validRootRetirementSyncUpdate(update) {
		return []string{"record"}
	}
	var fields []string
	for field := range update.Fields {
		fields = append(fields, field)
	}
	for field := range update.Values {
		fields = append(fields, field)
	}
	fields = append(fields, update.Clear...)
	sort.Strings(fields)
	return fields
}

// syncGroupPriorValues finds only fields previously owned by projection.
//
// Example: An owner-authored value has no projected prior value.
func syncGroupPriorValues(
	state syncState,
	update syncUpdate,
) map[string]string {
	previous := map[string]string{}
	for _, field := range syncGroupFields(update) {
		if value, ok := state.Applied[syncTargetKey(update, field)]; ok {
			previous[field] = value
		}
	}
	if update.Target == syncTargetLane {
		field := update.RequiredEnvironment.String() + "_status"
		_, statusChanged := update.Fields[field]
		lifecycleChanged := update.Fields["lifecycle"] != ""
		for _, cleared := range update.Clear {
			if cleared == "lifecycle" {
				lifecycleChanged = true
			}
		}
		if statusChanged || lifecycleChanged {
			if value, ok := state.Applied[syncTargetKey(update, "forecast")]; ok {
				previous["forecast"] = value
			}
		}
		if value, ok := update.Fields[field]; ok && value != "CLOSED" {
			if value, ok := state.Applied[syncTargetKey(update, "completed_at")]; ok {
				previous["completed_at"] = value
			}
		}
	}
	if validNarrativeSyncUpdate(update) {
		if value, ok := state.Applied[syncTargetKey(update, "text")]; ok {
			previous["text"] = value
		}
	}
	return previous
}

// syncGroupAppliedValues records canonical projected values for future conflict checks.
//
// Example: A structured effort retains its complete JSON value.
func syncGroupAppliedValues(
	data []byte,
	update syncUpdate,
) (map[string]string, bool) {
	if validRootRetirementSyncUpdate(update) {
		return map[string]string{"record": syncAbsentValue}, true
	}
	values := map[string]string{}
	for field, value := range update.Fields {
		if update.Target == syncTargetTask || update.Target == syncTargetLane || update.Target == syncTargetReport {
			values[field] = scalarSyncStoredValue(value)
		} else {
			values[field] = value
		}
	}
	if (len(update.Values) > 0 || len(update.Clear) > 0) && update.Target != syncTargetLane {
		structured, ok := structuredSyncValues(data, update)
		if !ok {
			return nil, false
		}
		for field, value := range structured {
			values[field] = value
		}
	}
	if update.Target == syncTargetLane {
		for _, field := range update.Clear {
			values[field] = syncAbsentValue
		}
	}
	return values, true
}

// encodeSyncGroup preserves a supplied transaction in replay metadata.
//
// Example: Save a lane update until its forecast becomes inactive.
func encodeSyncGroup(update syncUpdate) (string, error) {
	b, err := json.Marshal(update)
	return string(b), err
}

// decodeSyncGroup restores a transaction for an ordered retry.
//
// Example: Retry the exact update without parsing event prose.
func decodeSyncGroup(value string) (syncUpdate, error) {
	var update syncUpdate
	err := json.Unmarshal([]byte(value), &update)
	return update, err
}

// syncGroupKey identifies one target transaction within a canonical log entry.
//
// Example: Two target updates in one event retain their relative order.
func syncGroupKey(
	line int,
	ordinal int,
) string {
	b, _ := json.Marshal([]int{line, ordinal})
	return string(b)
}

// syncGroupOvertaken identifies a transaction superseded by applied newer fields.
//
// Example: An older owner update cannot overwrite a later successful owner update.
func syncGroupOvertaken(
	state syncState,
	update syncUpdate,
	line int,
	ordinal int,
) bool {
	for _, field := range syncGroupFields(update) {
		key := syncTargetKey(update, field)
		if state.Latest[key] > line || (state.Latest[key] == line && state.AppliedOrdinals[key] > ordinal) {
			return true
		}
	}
	return false
}

// syncGroupCorrectionMatches checks exact locator authorization for a correction.
//
// Example: A correction to an older version cannot replace a newer value.
func syncGroupCorrectionMatches(
	state syncState,
	update syncUpdate,
) bool {
	if update.Corrects == 0 {
		return true
	}
	for _, field := range syncGroupFields(update) {
		key := syncTargetKey(update, field)
		if state.Latest[key] != update.Corrects {
			return false
		}
		if update.CorrectsOrdinal != nil {
			if *update.CorrectsOrdinal != state.AppliedOrdinals[key] {
				return false
			}
		} else {
			if state.AppliedOrdinals[key] != 0 {
				return false
			}
		}
	}
	return true
}

// syncCorrectionLocatorPrecedes checks that a correction names an earlier update.
//
// Example: An earlier ordinal on the same line can be corrected.
func syncCorrectionLocatorPrecedes(
	update syncUpdate,
	line int,
	ordinal int,
) bool {
	if update.Corrects == 0 {
		return update.CorrectsOrdinal == nil
	}
	if update.Corrects < 0 || (update.CorrectsOrdinal != nil && *update.CorrectsOrdinal < 0) {
		return false
	}
	if update.Corrects != line {
		return update.Corrects < line
	}
	priorOrdinal := 0
	if update.CorrectsOrdinal != nil {
		priorOrdinal = *update.CorrectsOrdinal
	}
	return priorOrdinal < ordinal
}

// syncCorrectionAwaitsPending checks whether a correction's named version is unresolved.
//
// Example: Retain a correction until its referenced assignment can be projected.
func syncCorrectionAwaitsPending(
	state syncState,
	update syncUpdate,
) bool {
	for _, pending := range state.Groups {
		if pending.Line != update.Corrects {
			continue
		}
		if (update.CorrectsOrdinal == nil && pending.Ordinal != 0) || (update.CorrectsOrdinal != nil && pending.Ordinal != *update.CorrectsOrdinal) {
			continue
		}
		prior, err := decodeSyncGroup(pending.Update)
		if err != nil {
			continue
		}
		for _, field := range syncGroupFields(update) {
			for _, priorField := range syncGroupFields(prior) {
				if syncTargetKey(update, field) == syncTargetKey(prior, priorField) {
					return true
				}
			}
		}
	}
	return false
}

// syncGroupEvidence returns the accepted evidence provided by a task or lane update.
//
// Example: Lane completion carries its explicit supporting evidence.
func syncGroupEvidence(update syncUpdate) string {
	if strings.TrimSpace(update.Evidence) != "" {
		return update.Evidence
	}
	return update.Fields["evidence"]
}

// applyPendingSyncGroups retries target transactions in canonical event order.
//
// Example: A closure precedes a later evidence revision in a replay.
func applyPendingSyncGroups(
	data []byte,
	state *syncState,
	ledger ledgerSource,
	forecasts forecastSource,
	fullLog []byte,
) ([]byte, []syncEffect, bool) {
	keys := make([]string, 0, len(state.Groups))
	for key := range state.Groups {
		keys = append(keys, key)
	}
	sort.Slice(keys,
		// Order by canonical line, then by update position in that event.
		//
		// Example: Two updates in one entry retain their supplied order.
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
	invalid := false
	blocked := map[string]map[string]bool{}
	for _, key := range keys {
		group, ok := state.Groups[key]
		if !ok {
			continue
		}
		update, err := decodeSyncGroup(group.Update)
		if err != nil {
			invalid = true
			continue
		}
		if !syncCorrectionLocatorPrecedes(update, group.Line, group.Ordinal) {
			delete(state.Groups, key)
			invalid = true
			continue
		}
		if !syncUpdateSelectorValid(update) {
			// Older queued non-lane updates may contain selectors that did not
			// participate in locating their target. Preserve their payload while
			// applying them through the actual target identity.
			if update.Target != syncTargetLane && update.Target != syncTargetRootForecast {
				update.WorkRef = ""
				update.RequiredEnvironment = syncEnvironmentUnknown
			}
			if !syncUpdateSelectorValid(update) {
				invalid = true
				continue
			}
		}
		targetIdentity := syncTargetKey(update, "")
		dependencies := syncGroupDependencies(update)
		if syncDependenciesOverlap(blocked[targetIdentity], dependencies) {
			markSyncBlocked(blocked, targetIdentity, dependencies)
			continue
		}
		if update.Retract {
			textKey, evidenceKey := syncTargetKey(update, "text"), syncTargetKey(update, "evidence")
			latest := state.Latest[textKey]
			ordinal := state.AppliedOrdinals[textKey]
			switch {
			case state.Latest[evidenceKey] > latest:
				latest = state.Latest[evidenceKey]
				ordinal = state.AppliedOrdinals[evidenceKey]
			case state.Latest[evidenceKey] == latest && state.AppliedOrdinals[evidenceKey] > ordinal:
				ordinal = state.AppliedOrdinals[evidenceKey]
			}
			ordinalMatches := (update.CorrectsOrdinal == nil && ordinal == 0) || (update.CorrectsOrdinal != nil && *update.CorrectsOrdinal == ordinal)
			if latest != update.Corrects || !ordinalMatches {
				probe := update
				probe.Fields = map[string]string{"text": "probe", "evidence": "probe"}
				if syncCorrectionAwaitsPending(*state, probe) {
					markSyncBlocked(blocked, targetIdentity, dependencies)
					continue
				}
				if latest > update.Corrects || (latest == update.Corrects && !ordinalMatches) {
					delete(state.Groups, key)
					invalid = true
				}
				markSyncBlocked(blocked, targetIdentity, dependencies)
				continue
			}
			text, hasText := state.Applied[textKey]
			evidence, hasEvidence := state.Applied[evidenceKey]
			candidate, ok := retractNarrativeSync(data, update, text, hasText, evidence, hasEvidence)
			if !ok {
				markSyncBlocked(blocked, targetIdentity, dependencies)
				continue
			}
			if _, err := parse(candidate); err != nil {
				markSyncBlocked(blocked, targetIdentity, dependencies)
				continue
			}
			data = candidate
			effects = append(effects, syncEffect{Target: update.Target, ID: update.ID, Operation: syncEffectRemove})
			delete(state.Applied, textKey)
			delete(state.Applied, evidenceKey)
			state.Latest[textKey], state.Latest[evidenceKey] = group.Line, group.Line
			state.AppliedOrdinals[textKey], state.AppliedOrdinals[evidenceKey] = group.Ordinal, group.Ordinal
			delete(state.Groups, key)
			continue
		}
		if syncGroupOvertaken(*state, update, group.Line, group.Ordinal) {
			delete(state.Groups, key)
			invalid = true
			continue
		}
		if !syncGroupCorrectionMatches(*state, update) {
			if syncCorrectionAwaitsPending(*state, update) {
				markSyncBlocked(blocked, targetIdentity, dependencies)
				continue
			}
			invalid = true
			delete(state.Groups, key)
			continue
		}
		var groupedEffects []syncEffect
		data, groupedEffects = applyPendingReprioritizations(data, state, ledger, forecasts, key)
		if _, exists := state.Groups[key]; !exists {
			effects = append(effects, groupedEffects...)
			continue
		}
		if syncGroupAwaitsPriorityEstimate(*state, update, group) {
			markSyncBlocked(blocked, targetIdentity, dependencies)
			continue
		}
		previous := syncGroupPriorValues(*state, update)
		candidate, changes, ok := applySyncGroup(data, update, ledger, forecasts, previous)
		if !ok {
			chainResult, chainEffects, applied := applyPendingLaneLifecycleChain(data, state, key, keys, fullLog, ledger, forecasts)
			if applied {
				data = chainResult
				effects = append(effects, chainEffects...)
				continue
			}
			corrected, correctionEffects, correctionKey, applied := applyPendingLaneCorrection(data, state, update, group, keys, fullLog, ledger, forecasts)
			if applied {
				data = corrected
				effects = append(effects, correctionEffects...)
				delete(state.Groups, key)
				delete(state.Groups, correctionKey)
				continue
			}
			if staleLaneTransitionReplaced(data, *state, update, group, keys, ledger, forecasts) {
				delete(state.Groups, key)
				continue
			}
			markSyncBlocked(blocked, targetIdentity, dependencies)
			continue
		}
		for i := range changes {
			changes[i].Evidence = syncGroupEvidence(update)
		}
		if err := verifyStatusSyncCanonicalEffects(data, candidate, changes, ledger, forecasts); err != nil {
			markSyncBlocked(blocked, targetIdentity, dependencies)
			continue
		}
		values, ok := syncGroupAppliedValues(candidate, update)
		if !ok {
			markSyncBlocked(blocked, targetIdentity, dependencies)
			continue
		}
		validEffects := true
		for _, change := range changes {
			if change.Operation == syncEffectLaneForecastRestore {
				if _, ok := change.Value.(json.RawMessage); !ok {
					validEffects = false
				}
			}
		}
		if !validEffects {
			markSyncBlocked(blocked, targetIdentity, dependencies)
			continue
		}
		data = candidate
		effects = append(effects, changes...)
		for _, field := range syncGroupFields(update) {
			target := syncTargetKey(update, field)
			state.Latest[target] = group.Line
			state.AppliedOrdinals[target] = group.Ordinal
			state.Applied[target] = values[field]
		}
		for _, change := range changes {
			if change.Operation == syncEffectCompletedAtClear {
				key := syncTargetKey(update, "completed_at")
				state.Applied[key] = syncAbsentValue
				state.Latest[key] = group.Line
				state.AppliedOrdinals[key] = group.Ordinal
			}
			if change.Operation == syncEffectForecastRetire {
				key := syncTargetKey(update, "forecast")
				state.Applied[key] = syncAbsentValue
				state.Latest[key] = group.Line
				state.AppliedOrdinals[key] = group.Ordinal
			}
			if change.Operation == syncEffectLaneForecastRestore {
				raw := change.Value.(json.RawMessage)
				key := syncTargetKey(update, "forecast")
				state.Applied[key] = string(raw)
				state.Latest[key] = group.Line
				state.AppliedOrdinals[key] = group.Ordinal
			}
		}
		delete(state.Groups, key)
	}
	for key, group := range state.Groups {
		update, err := decodeSyncGroup(group.Update)
		if err != nil {
			continue
		}
		if syncGroupOvertaken(*state, update, group.Line, group.Ordinal) {
			delete(state.Groups, key)
			invalid = true
		}
	}
	return data, effects, invalid
}

// legacySyncEffects describes successfully replayed field-wise pending work.
//
// Example: Preserve a pending assignment created by an earlier report version.
func legacySyncEffects(
	before map[string]syncPending,
	after syncState,
	removed map[string]bool,
) []syncEffect {
	keys := make([]string, 0, len(before))
	for key := range before {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var effects []syncEffect
	for _, key := range keys {
		pending := before[key]
		if _, exists := after.Pending[key]; exists {
			continue
		}
		update := pending.Update
		if update.Retract {
			if removed[key] {
				effects = append(effects, syncEffect{Target: update.Target, ID: update.ID, Operation: syncEffectRemove})
			}
			continue
		}
		for field, value := range update.Fields {
			expected := value
			if update.Target == syncTargetTask || update.Target == syncTargetLane || update.Target == syncTargetReport {
				expected = scalarSyncStoredValue(value)
			}
			if after.Applied[key] != expected {
				continue
			}
			effects = append(effects, syncEffect{Target: update.Target, ID: update.ID, WorkRef: update.WorkRef, RequiredEnvironment: update.RequiredEnvironment, Field: field, Value: value, Evidence: syncGroupEvidence(update), Operation: syncEffectSet})
			if update.Target == syncTargetTask && field == "lifecycle" && value == "cancelled" {
				effects = append(effects, syncEffect{ID: update.ID, Operation: syncEffectPriorityRemove})
			}
		}
	}
	return effects
}

// syncGroupDependencies describes fields that must remain ordered together.
//
// Example: Task acceptance depends on both status and supporting evidence.
func syncGroupDependencies(update syncUpdate) map[string]bool {
	fields := map[string]bool{}
	for _, field := range syncGroupFields(update) {
		fields[field] = true
	}
	switch update.Target {
	case syncTargetProgress, syncTargetDecisions, syncTargetBlockers, syncTargetVerification, syncTargetRootForecast:
		fields["*"] = true
	case syncTargetTask:
		if fields["status"] || fields["evidence"] || fields["lifecycle"] {
			fields["status"], fields["evidence"], fields["lifecycle"] = true, true, true
		}
	case syncTargetLane:
		status := update.RequiredEnvironment.String() + "_status"
		if fields[status] || fields["completed_at"] || fields["forecast"] || fields["lifecycle"] {
			fields[status], fields["completed_at"], fields["forecast"], fields["lifecycle"] = true, true, true, true
		}
	}
	return fields
}

// syncDependenciesOverlap checks whether two transactions share a semantic dependency.
//
// Example: A pending owner assignment does not block task acceptance.
func syncDependenciesOverlap(
	left map[string]bool,
	right map[string]bool,
) bool {
	if left["*"] || right["*"] {
		return len(left) > 0 && len(right) > 0
	}
	for field := range left {
		if right[field] {
			return true
		}
	}
	return false
}

// markSyncBlocked retains unresolved dependencies for one target.
//
// Example: Keep a blocked evidence update ahead of dependent closure.
func markSyncBlocked(
	blocked map[string]map[string]bool,
	target string,
	fields map[string]bool,
) {
	if blocked[target] == nil {
		blocked[target] = map[string]bool{}
	}
	for field := range fields {
		blocked[target][field] = true
	}
}
