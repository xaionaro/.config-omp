package main

import (
	"encoding/json"
	"strings"
)

// legacyAppliedRootCorrectionLineage verifies each historical correction against the immutable log.
//
// Example: A redundant applied correction may be cleared only after its exact predecessor is proven.
func legacyAppliedRootCorrectionLineage(
	lines [][]byte,
	old syncPendingAddition,
	event syncEvent,
	ordinal int,
	ledger ledgerSource,
) bool {
	if old.Target != syncTargetRootCompletion || old.PriorLine < 1 || old.PriorLine >= old.Line || old.PriorRecord == "" || event.Event != "correction" || event.Details.StatusUpdate == nil || ordinal < 0 || ordinal >= len(event.Details.StatusUpdate.Additions) {
		return false
	}
	current := event.Details.StatusUpdate.Additions[ordinal]
	if !rootChainJSONEqual(current.ExpectedPrior, []byte(old.PriorRecord)) {
		return false
	}
	work := rootChainAdditionWorkRef(current)
	if !validRef(work, "project-understanding.yaml") {
		return false
	}
	canonical, exists := ledger.Records[sourceID(work, "project-understanding.yaml")]
	if !exists || canonical.Type != "work" || !hasRole(canonical.Data.Roles, reportingRoleRoot) {
		return false
	}
	forecast := string(canonical.Data.ForecastRef)
	if forecast != "" && !validRef(forecast, "forecasts.yaml") {
		return false
	}
	currentLine, currentOrdinal := old.Line, ordinal
	for event.Event == "correction" {
		var priorLine int
		if json.Unmarshal(event.Details.Corrects, &priorLine) != nil || priorLine < 1 || priorLine >= currentLine || priorLine > len(lines) || len(current.ExpectedPrior) == 0 || strings.TrimSpace(current.Evidence) == "" {
			return false
		}
		if currentLine == old.Line && priorLine != old.PriorLine {
			return false
		}
		var priorEvent syncEvent
		if json.Unmarshal(lines[priorLine-1], &priorEvent) != nil || priorEvent.Details.StatusUpdate == nil {
			return false
		}
		priorOrdinal, count := -1, 0
		var priorAddition syncAddition
		for index, addition := range priorEvent.Details.StatusUpdate.Additions {
			if addition.Target == syncTargetRootCompletion && rootChainJSONEqual(addition.Record, current.ExpectedPrior) && rootChainAdditionWorkRef(addition) == work {
				priorOrdinal, count, priorAddition = index, count+1, addition
			}
		}
		if count != 1 || strings.TrimSpace(priorAddition.Evidence) == "" {
			return false
		}
		locators := []rootLifecycleLocator{{Line: priorLine, Ordinal: priorOrdinal, Target: syncTargetRootCompletion}, {Line: currentLine, Ordinal: currentOrdinal, Target: syncTargetRootCompletion}}
		if verifyRootChainNoSkippedEvents(lines, locators, work, forecast) != nil {
			return false
		}
		event, current, currentLine, currentOrdinal = priorEvent, priorAddition, priorLine, priorOrdinal
	}
	return true
}
