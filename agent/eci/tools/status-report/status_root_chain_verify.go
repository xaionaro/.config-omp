package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// verifyRootLifecycleChain proves ordered root facts against original log and current sources.
//
// Example: Verify a delayed completion followed by an explicit reopening.
func verifyRootLifecycleChain(
	original []byte,
	candidate []byte,
	logBytes []byte,
	locators []rootLifecycleLocator,
	effects []syncEffect,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	if len(locators) == 0 {
		return fmt.Errorf("empty root lifecycle chain")
	}
	lines := bytes.Split(bytes.TrimSuffix(logBytes, []byte("\n")), []byte("\n"))
	var before, after map[string]any
	if err := yaml.Unmarshal(original, &before); err != nil {
		return err
	}
	if err := yaml.Unmarshal(candidate, &after); err != nil {
		return err
	}
	var workRef, forecastRef string
	var initial, previous, final json.RawMessage
	var initialTarget, previousTarget, finalTarget syncTarget
	var finalAddition syncAddition
	var finalUpdate syncUpdate
	lastLine, lastOrdinal := 0, -1
	for i, locator := range locators {
		if i > 0 && locator.Line == locators[i-1].Line && locator.Update != locators[i-1].Update {
			return fmt.Errorf("ambiguous same-line root operation order")
		}
		if locator.Line < 1 || locator.Line > len(lines) || locator.Ordinal < 0 || locator.Line < lastLine || (locator.Line == lastLine && locator.Ordinal <= lastOrdinal) {
			return fmt.Errorf("invalid root chain locator order")
		}
		lastLine, lastOrdinal = locator.Line, locator.Ordinal
		var event syncEvent
		if json.Unmarshal(lines[locator.Line-1], &event) != nil || event.Details.StatusUpdate == nil {
			return fmt.Errorf("invalid root chain event")
		}
		var expected, record json.RawMessage
		var target, predecessor syncTarget
		if locator.Update {
			updates := event.Details.StatusUpdate.Updates
			if locator.Ordinal >= len(updates) {
				return fmt.Errorf("root update locator out of bounds")
			}
			update := updates[locator.Ordinal]
			if update.Target != locator.Target || !validRootRetirementSyncUpdate(update) {
				return fmt.Errorf("invalid root retirement event")
			}
			if workRef != "" && workRef != update.WorkRef {
				return fmt.Errorf("root work identity changed")
			}
			workRef = update.WorkRef
			if forecastRef != "" && forecastRef != update.ID {
				return fmt.Errorf("root forecast identity changed")
			}
			forecastRef = update.ID
			expected, predecessor, target = update.Values["expected_prior"], syncTargetRootForecast, syncTargetUnknown
			finalUpdate = update
		} else {
			additions := event.Details.StatusUpdate.Additions
			if locator.Ordinal >= len(additions) {
				return fmt.Errorf("root addition locator out of bounds")
			}
			add := additions[locator.Ordinal]
			if add.Target != locator.Target || (add.Target != syncTargetRootCompletion && add.Target != syncTargetRootForecast) || (add.Target == syncTargetRootCompletion && strings.TrimSpace(add.Evidence) == "") || (add.ReopenWorkRef != "" && strings.TrimSpace(add.Evidence) == "") {
				return fmt.Errorf("invalid root lifecycle addition")
			}
			var fields struct {
				WorkRef     string `json:"work_ref"`
				ForecastRef string `json:"forecast_ref"`
			}
			if json.Unmarshal(add.Record, &fields) != nil {
				return fmt.Errorf("invalid root lifecycle record")
			}
			expected, record, target = add.ExpectedPrior, add.Record, add.Target
			switch add.Target {
			case syncTargetRootCompletion:
				if !validRef(fields.WorkRef, "project-understanding.yaml") || (workRef != "" && workRef != fields.WorkRef) || add.ReopenWorkRef != "" {
					return fmt.Errorf("root completion identity mismatch")
				}
				workRef = fields.WorkRef
				switch {
				case add.RetireForecast:
					predecessor = syncTargetRootForecast
				case len(expected) > 0:
					predecessor = syncTargetRootCompletion
				}
			case syncTargetRootForecast:
				if !validRef(fields.ForecastRef, "forecasts.yaml") || (forecastRef != "" && forecastRef != fields.ForecastRef) || add.RetireForecast {
					return fmt.Errorf("root forecast identity mismatch")
				}
				forecastRef = fields.ForecastRef
				switch {
				case add.ReopenWorkRef != "":
					if !validRef(add.ReopenWorkRef, "project-understanding.yaml") || (workRef != "" && workRef != add.ReopenWorkRef) {
						return fmt.Errorf("root reopening identity mismatch")
					}
					workRef = add.ReopenWorkRef
					predecessor = syncTargetRootCompletion
				case len(expected) > 0:
					predecessor = syncTargetRootForecast
				}
			}
			finalAddition = add
		}
		if i == 0 {
			initial, initialTarget = expected, predecessor
			if event.Event == "correction" {
				var corrects int
				if json.Unmarshal(event.Details.Corrects, &corrects) != nil || corrects < 1 || corrects >= locator.Line {
					return fmt.Errorf("invalid root correction reference")
				}
				var priorEvent syncEvent
				if json.Unmarshal(lines[corrects-1], &priorEvent) != nil || priorEvent.Details.StatusUpdate == nil {
					return fmt.Errorf("root correction prior event missing")
				}
				matches := 0
				for _, prior := range priorEvent.Details.StatusUpdate.Additions {
					if prior.Target == predecessor && rootChainJSONEqual(prior.Record, expected) {
						matches++
					}
				}
				if matches != 1 {
					return fmt.Errorf("root correction prior event ambiguous or missing")
				}
			}
		} else {
			if previousTarget != predecessor || !rootChainJSONEqual(previous, expected) {
				return fmt.Errorf("root lifecycle predecessor mismatch")
			}
			if event.Event == "correction" {
				var corrects int
				if json.Unmarshal(event.Details.Corrects, &corrects) != nil || corrects != locators[i-1].Line {
					return fmt.Errorf("root correction locator mismatch")
				}
			}
		}
		previous, previousTarget = record, target
		final, finalTarget = record, target
	}
	if workRef == "" && len(locators) == 1 && finalTarget == syncTargetRootForecast && len(initial) == 0 {
		if err := verifyCanonicalSyncAddition(syncEffect{Target: finalTarget, Value: final, Evidence: finalAddition.Evidence}, ledger, forecasts); err != nil {
			return err
		}
		if err := verifyRootChainBoundary(before, syncTargetUnknown, nil, "", forecastRef); err != nil {
			return err
		}
		if err := verifyRootChainEffects(syncTargetUnknown, nil, finalTarget, final, "", forecastRef, effects); err != nil {
			return err
		}
		if err := verifyStatusSyncEffects(original, candidate, effects); err != nil {
			return err
		}
		return verifyRootChainBoundary(after, finalTarget, final, "", forecastRef)
	}
	if !validRef(workRef, "project-understanding.yaml") {
		return fmt.Errorf("root lifecycle lacks explicit work identity")
	}
	work, ok := ledger.Records[sourceID(workRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || !hasRole(work.Data.Roles, reportingRoleRoot) {
		return fmt.Errorf("canonical root work missing")
	}
	if forecastRef == "" {
		forecastRef = string(work.Data.ForecastRef)
	}
	if forecastRef != string(work.Data.ForecastRef) {
		return fmt.Errorf("root lifecycle forecast differs from canonical work")
	}
	if err := verifyRootChainNoSkippedEvents(lines, locators, workRef, forecastRef); err != nil {
		return err
	}
	if err := verifyRootChainBoundary(before, initialTarget, initial, workRef, forecastRef); err != nil {
		return err
	}
	switch finalTarget {
	case syncTargetUnknown:
		if work.Data.State != workStateCancelled || finalUpdate.WorkRef != workRef || finalUpdate.ID != forecastRef || textField(forecasts.Records[sourceID(forecastRef, "forecasts.yaml")], "kind") != "root" || textField(forecasts.Records[sourceID(forecastRef, "forecasts.yaml")], "status") != "inactive" {
			return fmt.Errorf("root cancellation differs from canonical sources")
		}
	case syncTargetRootCompletion:
		if err := verifyCanonicalSyncAddition(syncEffect{Target: finalTarget, Value: final, Evidence: finalAddition.Evidence}, ledger, forecasts); err != nil {
			return err
		}
	default:
		source := forecasts.Records[sourceID(forecastRef, "forecasts.yaml")]
		if !openWork(work.Data.State) || textField(source, "kind") != "root" || textField(source, "status") != "active" || !outcomeCorresponds(work, ledger.Records, rootChainOutcome(final)) {
			return fmt.Errorf("root reopening differs from canonical sources")
		}
	}
	if err := verifyRootChainEffects(initialTarget, initial, finalTarget, final, workRef, forecastRef, effects); err != nil {
		return err
	}
	if err := verifyStatusSyncEffects(original, candidate, effects); err != nil {
		return err
	}
	if err := verifyRootChainBoundary(after, finalTarget, final, workRef, forecastRef); err != nil {
		return err
	}
	return nil
}

// verifyRootChainNoSkippedEvents rejects omitted root operations between chain endpoints.
//
// Example: Keep a later correction from bypassing an intervening root update.
func verifyRootChainNoSkippedEvents(
	lines [][]byte,
	locators []rootLifecycleLocator,
	workRef string,
	forecastRef string,
) error {
	selected := map[rootLifecycleLocator]bool{}
	for _, locator := range locators {
		selected[locator] = true
	}
	for line := locators[0].Line; line <= locators[len(locators)-1].Line; line++ {
		var event struct {
			Details struct {
				StatusUpdate json.RawMessage `json:"status_update"`
			} `json:"details"`
		}
		if err := json.Unmarshal(lines[line-1], &event); err != nil {
			return fmt.Errorf("invalid intervening root log event")
		}
		var status map[string]json.RawMessage
		if json.Unmarshal(event.Details.StatusUpdate, &status) != nil {
			continue
		}
		additions, additionsArray := rootRawCollection(status["additions"])
		for ordinal, raw := range additions {
			var item map[string]json.RawMessage
			if json.Unmarshal(raw, &item) != nil {
				continue
			}
			var targetName, reopen string
			_ = json.Unmarshal(item["target"], &targetName)
			_ = json.Unmarshal(item["reopen_work_ref"], &reopen)
			target := parseSyncTarget(targetName)
			if target != syncTargetUnknown && target != syncTargetRootForecast && target != syncTargetRootCompletion {
				continue
			}
			if rootRecordIdentifies(item["record"], workRef, forecastRef) || rootRecordIdentifies(item["expected_prior"], workRef, forecastRef) || (reopen == workRef && workRef != "") {
				if target != syncTargetRootForecast && target != syncTargetRootCompletion {
					return fmt.Errorf("ambiguous intervening root addition")
				}
				locator := rootLifecycleLocator{Line: line, Ordinal: ordinal, Target: target}
				if !additionsArray || !selected[locator] {
					return fmt.Errorf("root chain skips an intervening addition")
				}
			}
		}
		updates, updatesArray := rootRawCollection(status["updates"])
		for ordinal, raw := range updates {
			var item map[string]json.RawMessage
			if json.Unmarshal(raw, &item) != nil {
				continue
			}
			var targetName, work, id string
			_ = json.Unmarshal(item["target"], &targetName)
			_ = json.Unmarshal(item["work_ref"], &work)
			_ = json.Unmarshal(item["id"], &id)
			var values map[string]json.RawMessage
			_ = json.Unmarshal(item["values"], &values)
			if !((work == workRef && workRef != "") || (id == forecastRef && forecastRef != "") || rootRecordIdentifies(values["expected_prior"], workRef, forecastRef)) {
				continue
			}
			target := parseSyncTarget(targetName)
			if target != syncTargetUnknown && target != syncTargetRootForecast {
				continue
			}
			if target != syncTargetRootForecast {
				return fmt.Errorf("ambiguous intervening root update")
			}
			locator := rootLifecycleLocator{Line: line, Ordinal: ordinal, Target: target, Update: true}
			if !updatesArray || !selected[locator] {
				return fmt.Errorf("root chain skips an intervening update")
			}
		}
	}
	return nil
}

// rootRawCollection isolates malformed sibling collections while retaining object identity.
//
// Example: A malformed object still exposes its work reference for conflict checks.
func rootRawCollection(raw json.RawMessage) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		return items, true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && object != nil {
		return []json.RawMessage{raw}, false
	}
	return nil, false
}

// rootRecordIdentifies checks only usable identity fields in a supplied record.
//
// Example: Detect a matching work reference despite another malformed field.
func rootRecordIdentifies(
	raw json.RawMessage,
	workRef string,
	forecastRef string,
) bool {
	var record map[string]json.RawMessage
	if json.Unmarshal(raw, &record) != nil {
		return false
	}
	var work, forecast string
	_ = json.Unmarshal(record["work_ref"], &work)
	_ = json.Unmarshal(record["forecast_ref"], &forecast)
	return (workRef != "" && work == workRef) || (forecastRef != "" && forecast == forecastRef)
}

// verifyRootChainEffects limits the net plan to its exact boundary records.
//
// Example: Reject a plan that changes an unrelated task.
func verifyRootChainEffects(
	initialTarget syncTarget,
	initial json.RawMessage,
	finalTarget syncTarget,
	final json.RawMessage,
	workRef string,
	forecastRef string,
	effects []syncEffect,
) error {
	if initialTarget == finalTarget && rootChainJSONEqual(initial, final) {
		if len(effects) != 0 {
			return fmt.Errorf("unchanged root chain has effects")
		}
		return nil
	}
	removed, added := 0, 0
	for _, effect := range effects {
		if effect.Operation == syncEffectAdd && effect.Target == finalTarget {
			raw, ok := effect.Value.(json.RawMessage)
			if !ok || !rootChainJSONEqual(raw, final) || (finalTarget == syncTargetRootCompletion && effect.ID != workRef) || (finalTarget == syncTargetRootForecast && effect.ID != forecastRef) {
				return fmt.Errorf("root chain addition mismatch")
			}
			added++
			continue
		}
		if (effect.Operation == syncEffectRemove || effect.Operation == syncEffectRootForecastReplace || effect.Operation == syncEffectRootCompletionCorrection) && effect.Target == initialTarget {
			addition, ok := effect.Value.(syncAddition)
			if !ok || !rootChainJSONEqual(addition.ExpectedPrior, initial) || (initialTarget == syncTargetRootCompletion && effect.ID != workRef) || (initialTarget == syncTargetRootForecast && effect.ID != forecastRef) {
				return fmt.Errorf("root chain removal mismatch")
			}
			removed++
			continue
		}
		if effect.Operation == syncEffectCancelledRootForecastRetire && initialTarget == syncTargetRootForecast {
			update, ok := effect.Value.(syncUpdate)
			if !ok || effect.ID != forecastRef || effect.WorkRef != workRef || !rootChainJSONEqual(update.Values["expected_prior"], initial) {
				return fmt.Errorf("root chain cancellation mismatch")
			}
			removed++
			continue
		}
		return fmt.Errorf("unexpected root chain effect")
	}
	if (len(initial) > 0 && removed != 1) || (len(initial) == 0 && removed != 0) || (len(final) > 0 && added != 1) || (len(final) == 0 && added != 0) {
		return fmt.Errorf("incomplete root chain effects")
	}
	return nil
}

// rootChainJSONEqual compares complete JSON records by value.
//
// Example: Ignore key order while checking a predecessor.
func rootChainJSONEqual(
	a []byte,
	b []byte,
) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// rootChainOutcome reads the supplied record outcome.
//
// Example: Check a final forecast against its explicit work.
func rootChainOutcome(raw []byte) string {
	var record struct {
		Outcome string `json:"outcome"`
	}
	_ = json.Unmarshal(raw, &record)
	return record.Outcome
}

// verifyRootChainBoundary checks the exact managed record at one chain boundary.
//
// Example: Refuse to replace an owner-edited root forecast.
func verifyRootChainBoundary(
	report map[string]any,
	target syncTarget,
	expected json.RawMessage,
	workRef string,
	forecastRef string,
) error {
	found := 0
	for _, section := range []struct {
		name, key, id string
		target        syncTarget
	}{{"root_completions", "work_ref", workRef, syncTargetRootCompletion}, {"root_forecasts", "forecast_ref", forecastRef, syncTargetRootForecast}} {
		items, exists := report[section.name]
		if !exists || items == nil {
			continue
		}
		list, ok := items.([]any)
		if !ok {
			return fmt.Errorf("invalid root section")
		}
		for _, item := range list {
			record, ok := item.(map[string]any)
			if !ok || record[section.key] != section.id || section.id == "" {
				continue
			}
			found++
			encoded, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if section.target != target || !rootChainJSONEqual(encoded, expected) {
				return fmt.Errorf("root boundary conflicts with supplied record")
			}
		}
	}
	if len(expected) > 0 && found != 1 {
		return fmt.Errorf("root boundary record missing or ambiguous")
	}
	if len(expected) == 0 && found != 0 {
		return fmt.Errorf("unexpected root boundary record")
	}
	return nil
}
