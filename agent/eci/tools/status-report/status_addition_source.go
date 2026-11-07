package main

import (
	"encoding/json"
	"strings"
)

// syncAdditionSourceValid checks supplied lane identities against their sources.
//
// Example: Validate a lane addition against canonical work and forecasts.
func syncAdditionSourceValid(
	addition syncAddition,
	ledger ledgerSource,
	forecasts forecastSource,
) bool {
	if addition.Target != syncTargetLane {
		return true
	}
	var record struct {
		WorkRef     string `json:"work_ref"`
		Outcome     string `json:"outcome"`
		CompletedAt string `json:"completed_at"`
		Lifecycle   string `json:"lifecycle"`
		Forecast    *struct {
			ForecastRef string `json:"forecast_ref"`
		} `json:"forecast"`
	}
	if json.Unmarshal(addition.Record, &record) != nil {
		return false
	}
	if !validRef(record.WorkRef, "project-understanding.yaml") {
		return false
	}
	work, ok := ledger.Records[sourceID(record.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || !outcomeCorresponds(work, ledger.Records, record.Outcome) {
		return false
	}
	if !hasRole(work.Data.Roles, reportingRoleLane) {
		return false
	}
	if openWork(work.Data.State) {
		if record.Forecast == nil || string(work.Data.ForecastRef) != record.Forecast.ForecastRef || !validRef(record.Forecast.ForecastRef, "forecasts.yaml") {
			return false
		}
		src := forecasts.Records[sourceID(record.Forecast.ForecastRef, "forecasts.yaml")]
		return textField(src, "status") == "active" && textField(src, "kind") == "lane"
	}
	if record.Forecast != nil {
		return false
	}
	if strings.TrimSpace(addition.Evidence) == "" {
		return false
	}
	if work.Data.ForecastRef != "" {
		ref := string(work.Data.ForecastRef)
		if !validRef(ref, "forecasts.yaml") {
			return false
		}
		src := forecasts.Records[sourceID(ref, "forecasts.yaml")]
		if textField(src, "status") != "inactive" || textField(src, "kind") != "lane" {
			return false
		}
	}
	if work.Data.State == workStateCompleted {
		return sameValue(work.Data.CompletedAt, record.CompletedAt)
	}
	return work.Data.State == workStateCancelled && record.Lifecycle == "cancelled"
}
