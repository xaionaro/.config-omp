package main

import (
	"encoding/json"
	"reflect"
	"strings"
)

// validRootRetirementSyncUpdate accepts an explicit cancellation forecast removal.
//
// Example: Select a root forecast and supply its exact expected prior record.
func validRootRetirementSyncUpdate(update syncUpdate) bool {
	if update.Target != syncTargetRootForecast || !validRef(update.ID, "forecasts.yaml") || !validRef(update.WorkRef, "project-understanding.yaml") || strings.TrimSpace(update.Evidence) == "" || update.Retract || len(update.Fields) != 0 || len(update.Values) != 1 || len(update.Clear) != 1 || update.Clear[0] != "record" {
		return false
	}
	raw, ok := update.Values["expected_prior"]
	if !ok {
		return false
	}
	var prior map[string]any
	return json.Unmarshal(raw, &prior) == nil && prior != nil && prior["forecast_ref"] == update.ID
}

// applyRootRetirementSyncUpdate retires only the supplied cancelled root forecast.
//
// Example: Preserve an owner-edited record when its expected prior no longer matches.
func applyRootRetirementSyncUpdate(
	data []byte,
	update syncUpdate,
	ledger ledgerSource,
	forecasts forecastSource,
	previous map[string]string,
) ([]byte, []syncEffect, bool) {
	if !validRootRetirementSyncUpdate(update) {
		return data, nil, false
	}
	work, ok := ledger.Records[sourceID(update.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || work.Data.State != workStateCancelled || !hasRole(work.Data.Roles, reportingRoleRoot) || string(work.Data.ForecastRef) != update.ID {
		return data, nil, false
	}
	source := forecasts.Records[sourceID(update.ID, "forecasts.yaml")]
	if textField(source, "kind") != "root" || textField(source, "status") != "inactive" {
		return data, nil, false
	}
	prior := update.Values["expected_prior"]
	var record struct {
		Outcome string `json:"outcome"`
	}
	if json.Unmarshal(prior, &record) != nil || !outcomeCorresponds(work, ledger.Records, record.Outcome) {
		return data, nil, false
	}
	if stored, exists := previous["record"]; exists {
		var a, b any
		if json.Unmarshal([]byte(stored), &a) != nil || json.Unmarshal(prior, &b) != nil || !reflect.DeepEqual(a, b) {
			return data, nil, false
		}
	}
	candidate, ok := removeRootSyncRecord(data, syncTargetRootForecast, update.ID, prior)
	if !ok {
		return data, nil, false
	}
	if _, err := parse(candidate); err != nil {
		return data, nil, false
	}
	effect := syncEffect{Target: syncTargetRootForecast, ID: update.ID, WorkRef: update.WorkRef, Evidence: update.Evidence, Value: update, Operation: syncEffectCancelledRootForecastRetire}
	return candidate, []syncEffect{effect}, true
}
