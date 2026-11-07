package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// planRootLifecycleNet prepares only the boundary change of an explicit root chain.
//
// Example: Replace an initial forecast with the final canonical completion.
func planRootLifecycleNet(
	data []byte,
	chain []syncPendingRootAddition,
) ([]byte, []syncEffect, bool) {
	if len(chain) == 0 {
		return data, nil, false
	}
	first, last := chain[0].Addition, chain[len(chain)-1].Addition
	initial := first.ExpectedPrior
	initialTarget := syncTargetUnknown
	if len(initial) > 0 {
		if first.Target == syncTargetRootCompletion {
			initialTarget = syncTargetRootCompletion
			if first.RetireForecast {
				initialTarget = syncTargetRootForecast
			}
		} else {
			if first.Target != syncTargetRootForecast {
				return data, nil, false
			}
			initialTarget = syncTargetRootForecast
			if first.ReopenWorkRef != "" {
				initialTarget = syncTargetRootCompletion
			}
		}
	}
	var final struct {
		WorkRef     string `json:"work_ref"`
		ForecastRef string `json:"forecast_ref"`
	}
	if json.Unmarshal(last.Record, &final) != nil {
		return data, nil, false
	}
	finalID := final.WorkRef
	if last.Target == syncTargetRootForecast {
		finalID = final.ForecastRef
	}
	if strings.TrimSpace(finalID) == "" {
		return data, nil, false
	}
	if initialTarget == last.Target && rootChainJSONEqual(initial, last.Record) {
		return data, nil, true
	}
	candidate := data
	var effects []syncEffect
	if len(initial) > 0 {
		var boundary struct {
			WorkRef     string `json:"work_ref"`
			ForecastRef string `json:"forecast_ref"`
		}
		if json.Unmarshal(initial, &boundary) != nil {
			return data, nil, false
		}
		initialID := boundary.WorkRef
		if initialTarget == syncTargetRootForecast {
			initialID = boundary.ForecastRef
		}
		var ok bool
		candidate, ok = removeRootSyncRecord(candidate, initialTarget, initialID, initial)
		if !ok {
			return data, nil, false
		}
		net := last
		net.ExpectedPrior = initial
		op := syncEffectRemove
		if initialTarget == last.Target && initialTarget == syncTargetRootForecast {
			op = syncEffectRootForecastReplace
		}
		if initialTarget == last.Target && initialTarget == syncTargetRootCompletion {
			op = syncEffectRootCompletionCorrection
		}
		effects = append(effects, syncEffect{Target: initialTarget, ID: initialID, Value: net, Evidence: last.Evidence, Operation: op})
	}
	var ok bool
	candidate, ok = applySyncAddition(candidate, last)
	if !ok {
		return data, nil, false
	}
	effects = append(effects, syncEffect{Target: last.Target, ID: finalID, Value: last.Record, Evidence: last.Evidence, Operation: syncEffectAdd})
	return candidate, effects, true
}

// applyPendingRootChains publishes source-supported net changes in log order.
//
// Example: Replay completion and reopening as an exact boundary transition.
func applyPendingRootChains(
	data []byte,
	state *syncState,
	logBytes []byte,
	ledger ledgerSource,
	forecasts forecastSource,
) ([]byte, []syncEffect) {
	keys := make([]string, 0, len(state.PendingRootAdditions))
	for key := range state.PendingRootAdditions {
		keys = append(keys, key)
	}
	sort.Slice(keys,
		// Order pending additions by their log locator.
		//
		// Example: Replay earlier lines before later lines.
		func(
			i int,
			j int,
		) bool {
			a, b := state.PendingRootAdditions[keys[i]].Locator, state.PendingRootAdditions[keys[j]].Locator
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Ordinal < b.Ordinal
		})
	var effects []syncEffect
	for i, key := range keys {
		start, exists := state.PendingRootAdditions[key]
		if !exists {
			continue
		}
		if earlierPendingRootConflict(*state, start) {
			continue
		}
		chain := []syncPendingRootAddition{start}
		chainKeys := []string{key}
		locators := []rootLifecycleLocator{start.Locator}
		workRef := rootChainAdditionWorkRef(start.Addition)
		for _, nextKey := range keys[i+1:] {
			next, ok := state.PendingRootAdditions[nextKey]
			if !ok {
				continue
			}
			last := chain[len(chain)-1].Addition
			if len(next.Addition.ExpectedPrior) == 0 || !rootChainJSONEqual(last.Record, next.Addition.ExpectedPrior) {
				continue
			}
			nextWork := rootChainAdditionWorkRef(next.Addition)
			if workRef != "" && nextWork != "" && workRef != nextWork {
				continue
			}
			if workRef == "" {
				workRef = nextWork
			}
			chain = append(chain, next)
			chainKeys = append(chainKeys, nextKey)
			locators = append(locators, next.Locator)
		}
		candidate, changes, ok := planRootLifecycleNet(data, chain)
		valid := ok && verifyRootLifecycleChain(data, candidate, logBytes, locators, changes, ledger, forecasts) == nil
		var retirementKey string
		if !valid {
			candidate, changes, retirementKey, valid = planRootChainRetirement(data, state, chain, locators, logBytes, ledger, forecasts)
		}
		if !valid {
			continue
		}
		data = candidate
		effects = append(effects, changes...)
		for _, consumed := range chainKeys {
			delete(state.PendingRootAdditions, consumed)
		}
		if retirementKey != "" {
			delete(state.Groups, retirementKey)
			continue
		}
		last := chain[len(chain)-1]
		var record struct {
			WorkRef     string `json:"work_ref"`
			ForecastRef string `json:"forecast_ref"`
		}
		if json.Unmarshal(last.Addition.Record, &record) == nil {
			id := record.WorkRef
			if last.Addition.Target == syncTargetRootForecast {
				id = record.ForecastRef
			}
			target := "addition/" + last.Addition.Target.String() + "/" + id
			state.Latest[target] = last.Locator.Line
			state.AppliedOrdinals[target] = last.Locator.Ordinal
			state.Applied[target] = string(last.Addition.Record)
		}
	}
	return data, effects
}

// earlierPendingRootConflict keeps unresolved earlier transactions ahead of the same root.
//
// Example: A later addition cannot bypass a pending retirement of its forecast.
func earlierPendingRootConflict(
	state syncState,
	start syncPendingRootAddition,
) bool {
	work := rootChainAdditionWorkRef(start.Addition)
	forecast := rootChainAdditionForecastRef(start.Addition)
	for _, prior := range state.PendingAdditions {
		if prior.Line > start.Locator.Line || (prior.Target != syncTargetRootCompletion && prior.Target != syncTargetRootForecast) {
			continue
		}
		addition := syncAddition{Target: prior.Target, Record: json.RawMessage(prior.Record), ReopenWorkRef: prior.ReopenWorkRef, ExpectedPrior: json.RawMessage(prior.ExpectedPrior)}
		priorWork := rootChainAdditionWorkRef(addition)
		if work != "" && priorWork == work {
			return true
		}
		if forecast != "" && rootChainAdditionForecastRef(addition) == forecast && (work == "" || priorWork == "") {
			return true
		}
	}
	for _, prior := range state.PendingRootAdditions {
		if prior.Locator.Line > start.Locator.Line || (prior.Locator.Line == start.Locator.Line && prior.Locator.Ordinal >= start.Locator.Ordinal) {
			continue
		}
		priorWork := rootChainAdditionWorkRef(prior.Addition)
		if work != "" && priorWork == work {
			return true
		}
		if forecast != "" && rootChainAdditionForecastRef(prior.Addition) == forecast && (work == "" || priorWork == "") {
			return true
		}
	}
	for _, group := range state.Groups {
		if group.Line > start.Locator.Line {
			continue
		}
		update, err := decodeSyncGroup(group.Update)
		if err != nil || !validRootRetirementSyncUpdate(update) {
			continue
		}
		if (work != "" && update.WorkRef == work) || (work == "" && forecast != "" && update.ID == forecast) {
			return true
		}
	}
	return false
}

// rootChainAdditionForecastRef reads an explicit root forecast identity.
//
// Example: A completion identifies its predecessor forecast in expected_prior.
func rootChainAdditionForecastRef(addition syncAddition) string {
	var record struct {
		ForecastRef string `json:"forecast_ref"`
	}
	raw := addition.Record
	if addition.Target == syncTargetRootCompletion {
		raw = addition.ExpectedPrior
	}
	if json.Unmarshal(raw, &record) != nil {
		return ""
	}
	return record.ForecastRef
}

// rootChainAdditionWorkRef reads the explicit work selector of a root addition.
//
// Example: A reopening names its work independently of forecast identity.
func rootChainAdditionWorkRef(addition syncAddition) string {
	if addition.Target == syncTargetRootForecast {
		return addition.ReopenWorkRef
	}
	var record struct {
		WorkRef string `json:"work_ref"`
	}
	if json.Unmarshal(addition.Record, &record) != nil {
		return ""
	}
	return record.WorkRef
}

// planRootChainRetirement verifies an explicit final cancellation against a root chain.
//
// Example: Retire the initial forecast after unprojected completion and reopening.
func planRootChainRetirement(
	data []byte,
	state *syncState,
	chain []syncPendingRootAddition,
	locators []rootLifecycleLocator,
	logBytes []byte,
	ledger ledgerSource,
	forecasts forecastSource,
) ([]byte, []syncEffect, string, bool) {
	if len(chain) == 0 {
		return data, nil, "", false
	}
	last := chain[len(chain)-1]
	if last.Addition.Target != syncTargetRootForecast {
		return data, nil, "", false
	}
	for key, group := range state.Groups {
		if group.Line <= last.Locator.Line {
			continue
		}
		update, err := decodeSyncGroup(group.Update)
		if err != nil || !validRootRetirementSyncUpdate(update) || !rootChainJSONEqual(update.Values["expected_prior"], last.Addition.Record) {
			continue
		}
		initial := chain[0].Addition.ExpectedPrior
		if len(initial) > 0 && !(chain[0].Addition.Target == syncTargetRootCompletion && chain[0].Addition.RetireForecast) {
			continue
		}
		candidate := data
		var effects []syncEffect
		if len(initial) > 0 {
			net := update
			net.Values = map[string]json.RawMessage{"expected_prior": initial}
			var ok bool
			candidate, effects, ok = applyRootRetirementSyncUpdate(data, net, ledger, forecasts, syncGroupPriorValues(*state, update))
			if !ok {
				continue
			}
		}
		locator := rootLifecycleLocator{Line: group.Line, Ordinal: group.Ordinal, Target: update.Target, Update: true}
		source := append(append([]rootLifecycleLocator(nil), locators...), locator)
		if verifyRootLifecycleChain(data, candidate, logBytes, source, effects, ledger, forecasts) != nil {
			continue
		}
		target := syncTargetKey(update, "record")
		state.Applied[target] = syncAbsentValue
		state.Latest[target] = group.Line
		state.AppliedOrdinals[target] = group.Ordinal
		return candidate, effects, key, true
	}
	return data, nil, "", false
}
