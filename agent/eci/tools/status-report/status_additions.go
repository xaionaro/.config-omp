package main

import (
	"bytes"
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// applySyncAddition inserts a complete task or review without altering existing records.
//
// Example: A supplied task with a unique ID is appended to the task list.
func applySyncAddition(
	data []byte,
	addition syncAddition,
) ([]byte, bool) {
	return applySyncAdditionGroup(data, []syncAddition{addition})
}

// applySyncAdditionGroup publishes a task and its matching priority estimate together.
//
// Example: A new priority and its first task become valid in one projection.
func applySyncAdditionGroup(
	data []byte,
	additions []syncAddition,
) ([]byte, bool) {
	if len(additions) == 1 && (additions[0].Target == syncTargetLane || additions[0].Target == syncTargetRootCompletion || additions[0].Target == syncTargetRootForecast) {
		candidate, ok := insertSyncAddition(data, additions[0])
		if !ok {
			return data, false
		}
		if _, err := parse(candidate); err != nil {
			return data, false
		}
		return candidate, true
	}
	if len(additions) == 0 || len(additions) > 2 {
		return data, false
	}
	var taskPriority, estimatePriority string
	var tasks, estimates, reviews int
	for _, addition := range additions {
		var record map[string]json.RawMessage
		if json.Unmarshal(addition.Record, &record) != nil {
			return data, false
		}
		switch addition.Target {
		case syncTargetTask:
			tasks++
			if json.Unmarshal(record["priority"], &taskPriority) != nil {
				return data, false
			}
		case syncTargetPriorityEstimate:
			estimates++
			if json.Unmarshal(record["priority"], &estimatePriority) != nil {
				return data, false
			}
		case syncTargetReview:
			reviews++
		default:
			return data, false
		}
	}
	if !((tasks == 1 && reviews == 0 && estimates <= 1) || (reviews == 1 && tasks == 0 && estimates == 0)) || (estimates == 1 && taskPriority != estimatePriority) {
		return data, false
	}
	candidate := data
	for _, addition := range additions {
		var ok bool
		candidate, ok = insertSyncAddition(candidate, addition)
		if !ok {
			return data, false
		}
	}
	if _, err := parse(candidate); err != nil {
		return data, false
	}
	return candidate, true
}

// insertSyncAddition inserts one complete record before group validation.
//
// Example: Append an estimate alongside its task before validating the report.
func insertSyncAddition(
	data []byte,
	addition syncAddition,
) ([]byte, bool) {
	var record map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(addition.Record))
	if decoder.Decode(&record) != nil || len(record) == 0 {
		return data, false
	}
	var section, identity string
	var required []string
	switch addition.Target {
	case syncTargetLane:
		section, identity = "lanes", "task_id"
		required = []string{"task_id", "work_ref", "outcome", "required_environment", "implementation_status", "test_status", "prod_status"}
	case syncTargetRootCompletion:
		section, identity = "root_completions", "work_ref"
		required = []string{"work_ref", "outcome", "completed_at"}
	case syncTargetRootForecast:
		section, identity = "root_forecasts", "forecast_ref"
		required = []string{"forecast_ref", "outcome", "target", "assessment"}
	case syncTargetTask:
		section, identity = "tasks", "id"
		required = []string{"id", "title", "priority", "impact", "effort", "status"}
	case syncTargetReview:
		section, identity = "reviews", "task_id"
		required = []string{"task_id", "reporter", "critic", "rounds", "estimate", "impact", "priority", "unresolved"}
	case syncTargetPriorityEstimate:
		section, identity = "priority_estimates", "priority"
		required = []string{"priority", "as_of", "min_minutes", "max_minutes", "basis", "assumptions", "dependencies", "critical_path"}
	default:
		return data, false
	}
	for _, field := range required {
		value, exists := record[field]
		if !exists {
			return data, false
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			if addition.Target == syncTargetTask && field == "status" {
				continue
			}
			return data, false
		}
		if field != "effort" && field != "rounds" && field != "min_minutes" && field != "max_minutes" {
			var text string
			if json.Unmarshal(value, &text) != nil || strings.TrimSpace(text) == "" {
				return data, false
			}
		}
	}
	var id string
	if json.Unmarshal(record[identity], &id) != nil {
		return data, false
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return data, false
	}
	root := doc.Content[0]
	list := mapValue(root, section)
	if list == nil && (section == "reviews" || section == "lanes" || section == "root_completions" || section == "root_forecasts") {
		entry, err := json.Marshal(record)
		if err != nil {
			return data, false
		}
		candidate, err := appendReportField(data, section, "["+string(entry)+"]")
		return candidate, err == nil
	}
	if list != nil && list.Kind == yaml.ScalarNode && list.Tag == "!!null" && (section == "reviews" || section == "lanes" || section == "root_completions" || section == "root_forecasts") {
		entry, err := json.Marshal(record)
		if err != nil {
			return data, false
		}
		candidate, err := replaceNullYAMLValue(data, list, "["+string(entry)+"]")
		return candidate, err == nil
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		return data, false
	}
	for _, item := range list.Content {
		if existing := mapValue(item, identity); existing != nil && existing.Value == id {
			if addition.Target == syncTargetLane {
				var ref, environment string
				if json.Unmarshal(record["work_ref"], &ref) != nil || json.Unmarshal(record["required_environment"], &environment) != nil {
					return data, false
				}
				oldRef, oldEnvironment := mapValue(item, "work_ref"), mapValue(item, "required_environment")
				if oldRef != nil && oldEnvironment != nil && (oldRef.Value != ref || oldEnvironment.Value != environment) {
					continue
				}
			}
			return data, false
		}
	}
	entry, err := json.Marshal(record)
	if err != nil {
		return data, false
	}
	candidate, ok := appendNarrativeEntry(data, root, list, section, string(entry))
	if !ok {
		return data, false
	}
	return candidate, true
}
