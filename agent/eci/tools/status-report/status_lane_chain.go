package main

import (
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// planLaneLifecycleChain composes supplied cancel, reactivate and close updates.
//
// Example: Preserve a reactivation's next action before the final canonical closure.
func planLaneLifecycleChain(
	data []byte,
	groups []syncPendingGroup,
	ledger ledgerSource,
	forecasts forecastSource,
	previous map[string]string,
) ([]byte, []syncEffect, map[string]syncPendingGroup, bool) {
	if len(groups) < 2 {
		return data, nil, nil, false
	}
	updates := make([]syncUpdate, len(groups))
	for i, group := range groups {
		var err error
		updates[i], err = decodeSyncGroup(group.Update)
		if err != nil || !validLaneSyncUpdate(updates[i]) || updates[i].Corrects != 0 || (i > 0 && (group.Line < groups[i-1].Line || (group.Line == groups[i-1].Line && group.Ordinal <= groups[i-1].Ordinal))) {
			return data, nil, nil, false
		}
		if i > 0 && (updates[i].ID != updates[0].ID || updates[i].WorkRef != updates[0].WorkRef || updates[i].RequiredEnvironment != updates[0].RequiredEnvironment) {
			return data, nil, nil, false
		}
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return data, nil, nil, false
	}
	list := mapValue(doc.Content[0], "lanes")
	if list == nil || list.Kind != yaml.SequenceNode {
		return data, nil, nil, false
	}
	var lane *yaml.Node
	for _, item := range list.Content {
		id, ref, env := mapValue(item, "task_id"), mapValue(item, "work_ref"), mapValue(item, "required_environment")
		if id != nil && ref != nil && env != nil && id.Value == updates[0].ID && ref.Value == updates[0].WorkRef && env.Value == updates[0].RequiredEnvironment.String() {
			if lane != nil {
				return data, nil, nil, false
			}
			lane = item
		}
	}
	if lane == nil {
		return data, nil, nil, false
	}
	cancelled := mapValue(lane, "lifecycle") != nil && mapValue(lane, "lifecycle").Value == "cancelled"
	final := updates[len(updates)-1]
	statusField := final.RequiredEnvironment.String() + "_status"
	if final.Fields[statusField] != "CLOSED" || strings.TrimSpace(final.Fields["completed_at"]) == "" || strings.TrimSpace(final.Evidence) == "" {
		return data, nil, nil, false
	}
	stateCancelled := cancelled
	stateClosed := mapValue(lane, statusField) != nil && mapValue(lane, statusField).Value == "CLOSED"
	reactivated := false
	for i, update := range updates[:len(updates)-1] {
		if update.Fields["lifecycle"] == "cancelled" {
			if stateCancelled || stateClosed || strings.TrimSpace(update.Evidence) == "" {
				return data, nil, nil, false
			}
			stateCancelled = true
			continue
		}
		if status, supplied := update.Fields[statusField]; supplied {
			if status == "CLOSED" {
				if stateCancelled || stateClosed || strings.TrimSpace(update.Fields["completed_at"]) == "" || strings.TrimSpace(update.Evidence) == "" {
					return data, nil, nil, false
				}
				stateClosed = true
			}
			if status != "CLOSED" && update.Fields["completed_at"] != "" {
				return data, nil, nil, false
			}
			if status != "CLOSED" && stateClosed {
				if strings.TrimSpace(update.Evidence) == "" {
					return data, nil, nil, false
				}
				stateClosed, reactivated = false, true
			}
		}
		if _, supplied := update.Fields[statusField]; !supplied && update.Fields["completed_at"] != "" {
			return data, nil, nil, false
		}
		for _, field := range update.Clear {
			if field == "lifecycle" {
				if !stateCancelled || strings.TrimSpace(update.Evidence) == "" {
					return data, nil, nil, false
				}
				stateCancelled, reactivated = false, true
			}
		}
		if i == 0 && !cancelled && !reactivated && !stateClosed {
			return data, nil, nil, false
		}
	}
	if stateCancelled || stateClosed || !reactivated {
		return data, nil, nil, false
	}
	combined := final
	combined.Fields = map[string]string{}
	combined.Clear = nil
	clear := map[string]bool{}
	provenance := map[string]syncPendingGroup{}
	for i, update := range updates {
		for field, value := range update.Fields {
			if field == "lifecycle" && value == "cancelled" && i < len(updates)-1 {
				if prior, exists := previous[field]; exists && scalarSyncOwnership(mapValue(lane, field)) != prior {
					return data, nil, nil, false
				}
				continue
			}
			if field == "lifecycle" {
				return data, nil, nil, false
			}
			if prior, exists := previous[field]; exists && scalarSyncOwnership(mapValue(lane, field)) != prior {
				return data, nil, nil, false
			}
			combined.Fields[field] = value
			delete(clear, field)
			provenance[field] = groups[i]
		}
		for _, field := range update.Clear {
			if field == "lifecycle" {
				provenance[field] = groups[i]
				continue
			}
			if prior, exists := previous[field]; exists && scalarSyncOwnership(mapValue(lane, field)) != prior {
				return data, nil, nil, false
			}
			delete(combined.Fields, field)
			clear[field] = true
			provenance[field] = groups[i]
		}
	}
	for field := range clear {
		combined.Clear = append(combined.Clear, field)
	}
	sort.Strings(combined.Clear)
	candidate := data
	var effects []syncEffect
	if cancelled {
		current := mapValue(lane, "lifecycle")
		if prior, ok := previous["lifecycle"]; ok && scalarSyncOwnership(current) != prior {
			return data, nil, nil, false
		}
		var ok bool
		candidate, ok = removeLaneSyncField(candidate, combined, "lifecycle")
		if !ok {
			return data, nil, nil, false
		}
		effects = append(effects, syncEffect{Target: syncTargetLane, ID: combined.ID, WorkRef: combined.WorkRef, RequiredEnvironment: combined.RequiredEnvironment, Field: "lifecycle", Operation: syncEffectClear})
	}
	candidate, finalEffects, ok := applyLaneSyncUpdate(candidate, combined, ledger, forecasts, previous)
	if !ok {
		return data, nil, nil, false
	}
	effects = append(effects, finalEffects...)
	for _, effect := range finalEffects {
		if effect.Operation == syncEffectForecastRetire {
			provenance["forecast"] = groups[len(groups)-1]
		}
		if effect.Operation == syncEffectCompletedAtClear {
			provenance["completed_at"] = groups[len(groups)-1]
		}
	}
	for i := range effects {
		if group, ok := provenance[effects[i].Field]; ok {
			update, err := decodeSyncGroup(group.Update)
			if err != nil {
				return data, nil, nil, false
			}
			effects[i].Evidence = syncGroupEvidence(update)
		}
	}
	if err := verifyStatusSyncEffects(data, candidate, effects); err != nil {
		return data, nil, nil, false
	}
	if err := verifyStatusSyncCanonicalEffects(data, candidate, effects, ledger, forecasts); err != nil {
		return data, nil, nil, false
	}
	return candidate, effects, provenance, true
}
