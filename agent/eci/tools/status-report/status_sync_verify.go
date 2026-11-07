package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// syncEffectOperation identifies an authorized report mutation.
//
// Example: A set operation updates one field.
type syncEffectOperation int

const (
	// syncEffectSet sets one report field.
	//
	// Example: Set a task status.
	syncEffectSet syncEffectOperation = iota
	// syncEffectAdd inserts a supplied record.
	//
	// Example: Insert a task.
	syncEffectAdd
	// syncEffectRemove removes an identified record or lane field.
	//
	// Example: Retract a narrative entry.
	syncEffectRemove
	// syncEffectMetadata replaces replay state.
	//
	// Example: Advance the replay cursor.
	syncEffectMetadata
	// syncEffectTimestamp sets the report timestamp.
	//
	// Example: Advance the report time.
	syncEffectTimestamp
	// syncEffectTimelineAnchor sets the timeline anchor.
	//
	// Example: Preserve an existing timeline anchor.
	syncEffectTimelineAnchor
	// syncEffectDiagnostic appends a diagnostic and marks scope incomplete.
	//
	// Example: Flag a pending update.
	syncEffectDiagnostic
	// syncEffectPriorityRemove removes a task from top priorities.
	//
	// Example: Remove a cancelled task.
	syncEffectPriorityRemove
	// syncEffectTimeline replaces the complete timeline snapshot.
	//
	// Example: Publish a verified empty timeline.
	syncEffectTimeline
	// syncEffectClear removes a field.
	//
	// Example: Clear a resolved blocker.
	syncEffectClear
	// syncEffectForecastRetire removes a lane forecast.
	//
	// Example: Retire an inactive forecast.
	syncEffectForecastRetire
	// syncEffectCompletedAtClear removes a lane completion time.
	//
	// Example: Reopen a lane.
	syncEffectCompletedAtClear
	// syncEffectLaneForecastRestore restores a canonical lane forecast.
	//
	// Example: Restore the active forecast when a lane reopens.
	syncEffectLaneForecastRestore
	// syncEffectCancelledRootForecastRetire removes a cancelled root forecast.
	//
	// Example: Retire an inactive forecast for cancelled root work.
	syncEffectCancelledRootForecastRetire
	// syncEffectRootCompletionCorrection replaces an explicitly matched completion.
	//
	// Example: Correct the canonical completion time of a root.
	syncEffectRootCompletionCorrection
	// syncEffectRootForecastReplace replaces an explicitly matched root forecast.
	//
	// Example: Publish the final forecast after a delayed lifecycle chain.
	syncEffectRootForecastReplace
)

// syncEffect describes one authorized semantic report change.
//
// Example: Identify a task field and its new value.
type syncEffect struct {
	Target              syncTarget
	ID, WorkRef         string
	Evidence            string
	RequiredEnvironment syncEnvironment
	Field               string
	Value               any
	Operation           syncEffectOperation
}

// verifyStatusSyncCanonicalEffects checks source-owned effects against canonical work and forecasts.
//
// Example: Reject closure backed by an active forecast.
func verifyStatusSyncCanonicalEffects(
	original []byte,
	candidate []byte,
	effects []syncEffect,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	before, err := decodeSyncVerificationReport(original)
	if err != nil {
		return err
	}
	after, err := decodeSyncVerificationReport(candidate)
	if err != nil {
		return err
	}
	for _, effect := range effects {
		if effect.Operation == syncEffectRootCompletionCorrection {
			if err := verifyRootCompletionCorrection(before, effect, effects, ledger, forecasts); err != nil {
				return err
			}
			continue
		}
		if effect.Target == syncTargetLane && effect.Operation == syncEffectSet && effect.Field == "completed_at" {
			if err := verifyLaneCompletionTime(after, effect, ledger); err != nil {
				return err
			}
		}
		if effect.Operation == syncEffectCancelledRootForecastRetire {
			if err := verifyCancelledRootForecastRetirement(before, effect, ledger, forecasts); err != nil {
				return err
			}
			continue
		}
		if effect.Operation == syncEffectLaneForecastRestore {
			if err := verifyLaneForecastRestore(before, after, effect, effects, ledger, forecasts); err != nil {
				return err
			}
			continue
		}
		if effect.Operation == syncEffectRemove && (effect.Target == syncTargetRootForecast || effect.Target == syncTargetRootCompletion) {
			if err := verifyCanonicalRootRemoval(before, effect, effects, ledger, forecasts); err != nil {
				return err
			}
			continue
		}
		if effect.Operation == syncEffectAdd && (effect.Target == syncTargetLane || effect.Target == syncTargetRootCompletion || effect.Target == syncTargetRootForecast) {
			if err := verifyCanonicalSyncAddition(effect, ledger, forecasts); err != nil {
				return err
			}
			continue
		}
		terminal := effect.Target == syncTargetLane && effect.Operation == syncEffectSet && ((effect.Field == effect.RequiredEnvironment.String()+"_status" && effect.Value == "CLOSED") || (effect.Field == "lifecycle" && effect.Value == "cancelled"))
		retirement := effect.Operation == syncEffectForecastRetire || (effect.Operation == syncEffectRemove && effect.Target == syncTargetLane && effect.Field == "forecast")
		if !terminal && !retirement {
			continue
		}
		if effect.Target != syncTargetLane || !validRef(effect.WorkRef, "project-understanding.yaml") || strings.TrimSpace(effect.Evidence) == "" {
			return fmt.Errorf("terminal lane effect lacks canonical identity or evidence")
		}
		work, ok := ledger.Records[sourceID(effect.WorkRef, "project-understanding.yaml")]
		if !ok || work.Type != "work" {
			return fmt.Errorf("terminal lane work missing")
		}
		items, ok := after["lanes"].([]any)
		if !ok {
			return fmt.Errorf("terminal lane missing")
		}
		index, err := syncEffectIndex(items, "task_id", effect)
		if err != nil || index < 0 {
			return fmt.Errorf("terminal lane identity missing or ambiguous")
		}
		lane := items[index].(map[string]any)
		cancelled := lane["lifecycle"] == "cancelled"
		if cancelled {
			if work.Data.State != workStateCancelled {
				return fmt.Errorf("lane cancellation disagrees with canonical work")
			}
		} else {
			completed, ok := lane["completed_at"].(string)
			if work.Data.State != workStateCompleted || !ok || !sameValue(completed, work.Data.CompletedAt) || lane[effect.RequiredEnvironment.String()+"_status"] != "CLOSED" {
				return fmt.Errorf("lane completion disagrees with canonical work")
			}
		}
		ref := string(work.Data.ForecastRef)
		if ref != "" && (!validRef(ref, "forecasts.yaml") || textField(forecasts.Records[sourceID(ref, "forecasts.yaml")], "status") != "inactive") {
			return fmt.Errorf("terminal lane forecast is not inactive")
		}
		if retirement {
			oldItems, _ := before["lanes"].([]any)
			oldIndex, err := syncEffectIndex(oldItems, "task_id", effect)
			if err != nil || oldIndex < 0 {
				return fmt.Errorf("retired forecast lane missing")
			}
			oldLane := oldItems[oldIndex].(map[string]any)
			forecast, ok := oldLane["forecast"].(map[string]any)
			if !ok || forecast["forecast_ref"] != ref || ref == "" {
				return fmt.Errorf("retired forecast does not match canonical work")
			}
		}
	}
	return nil
}

// verifyLaneCompletionTime checks a lane completion timestamp against canonical work.
//
// Example: Reject a correction that differs from the canonical completion instant.
func verifyLaneCompletionTime(
	after map[string]any,
	effect syncEffect,
	ledger ledgerSource,
) error {
	if !validRef(effect.WorkRef, "project-understanding.yaml") || effect.RequiredEnvironment == syncEnvironmentUnknown {
		return fmt.Errorf("invalid lane completion identity")
	}
	work, ok := ledger.Records[sourceID(effect.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || work.Data.State != workStateCompleted || work.Data.CompletedAt == "" {
		return fmt.Errorf("lane completion lacks completed canonical work")
	}
	value, ok := effect.Value.(string)
	if !ok || !sameValue(value, work.Data.CompletedAt) {
		return fmt.Errorf("lane completion time differs from canonical work")
	}
	items, ok := after["lanes"].([]any)
	if !ok {
		return fmt.Errorf("lane completion target missing")
	}
	index, err := syncEffectIndex(items, "task_id", effect)
	if err != nil || index < 0 {
		return fmt.Errorf("lane completion identity missing or ambiguous")
	}
	lane := items[index].(map[string]any)
	completed, ok := lane["completed_at"].(string)
	if !ok || !sameValue(completed, work.Data.CompletedAt) || lane[effect.RequiredEnvironment.String()+"_status"] != "CLOSED" {
		return fmt.Errorf("lane completion state differs from canonical work")
	}
	return nil
}

// verifyStatusSyncEffects checks the entire original-to-candidate semantic delta
// against explicit accepted effects and preserves the ordered YAML comments.
//
// Example: Reject a candidate that changes an unrelated task.
func verifyStatusSyncEffects(
	original []byte,
	candidate []byte,
	effects []syncEffect,
) error {
	if err := verifyTaskIdentityStyles(original, candidate, effects); err != nil {
		return err
	}
	if err := verifyTopPriorityStyles(original, candidate, effects); err != nil {
		return err
	}
	beforeComments, err := yamlComments(original)
	if err != nil {
		return err
	}
	afterComments, err := yamlComments(candidate)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(beforeComments, afterComments) {
		return fmt.Errorf("status sync would change YAML comments")
	}
	before, err := decodeSyncVerificationReport(original)
	if err != nil {
		return err
	}
	after, err := decodeSyncVerificationReport(candidate)
	if err != nil {
		return err
	}
	for _, effect := range effects {
		if err := applyExpectedSyncEffect(before, effect); err != nil {
			return fmt.Errorf("invalid status sync effect: %w", err)
		}
	}
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("status sync candidate differs from authorized effects")
	}
	return nil
}

// applyExpectedSyncEffect applies an authorized change to the expected report.
//
// Example: Add a supplied narrative entry.
func applyExpectedSyncEffect(
	root map[string]any,
	effect syncEffect,
) error {
	switch effect.Operation {
	case syncEffectRootForecastReplace:
		effect.Operation = syncEffectRemove
		return applyExpectedSyncEffect(root, effect)
	case syncEffectRootCompletionCorrection:
		effect.Operation = syncEffectRemove
		return applyExpectedSyncEffect(root, effect)
	case syncEffectCancelledRootForecastRetire:
		update, ok := effect.Value.(syncUpdate)
		if !ok {
			return fmt.Errorf("invalid root retirement")
		}
		effect.Operation = syncEffectRemove
		effect.Value = syncAddition{ExpectedPrior: update.Values["expected_prior"]}
		return applyExpectedSyncEffect(root, effect)
	case syncEffectLaneForecastRestore:
		effect.Operation = syncEffectSet
		effect.Field = "forecast"
		return applyExpectedSyncEffect(root, effect)
	case syncEffectMetadata:
		encoded, err := json.Marshal(effect.Value)
		if err != nil {
			return err
		}
		var value any
		if err := yaml.Unmarshal(encoded, &value); err != nil {
			return err
		}
		root["status_sync"] = value
		return nil
	case syncEffectTimestamp, syncEffectTimelineAnchor:
		value, ok := effect.Value.(string)
		if !ok {
			return fmt.Errorf("timestamp must be a string")
		}
		field := "timestamp"
		if effect.Operation == syncEffectTimelineAnchor {
			field = "timeline_as_of"
		}
		root[field] = value
		return nil
	case syncEffectTimeline:
		snapshot, ok := effect.Value.(syncTimelineSnapshot)
		if !ok {
			return fmt.Errorf("invalid timeline snapshot")
		}
		encoded, err := yaml.Marshal(snapshot.Entries)
		if err != nil {
			return err
		}
		var entries any
		if err := yaml.Unmarshal(encoded, &entries); err != nil {
			return err
		}
		root["timeline"] = entries
		root["timeline_state"] = snapshot.State.String()
		root["timeline_as_of"] = snapshot.AsOf
		return nil
	case syncEffectPriorityRemove:
		items, ok := root["top_priorities"].([]any)
		if !ok {
			return fmt.Errorf("missing top priorities")
		}
		remaining := make([]any, 0, len(items))
		for _, item := range items {
			id, ok := item.(string)
			if !ok {
				return fmt.Errorf("invalid top priority")
			}
			if id != effect.ID {
				remaining = append(remaining, item)
			}
		}
		root["top_priorities"] = remaining
		return nil
	case syncEffectDiagnostic:
		value, ok := effect.Value.(string)
		if !ok {
			return fmt.Errorf("diagnostic must be a string")
		}
		items, ok := root["blockers_risks"].([]any)
		if !ok {
			return fmt.Errorf("missing blockers")
		}
		root["scope_complete"] = false
		for _, item := range items {
			if text, ok := item.(string); ok && text == value {
				return nil
			}
		}
		root["blockers_risks"] = append(items, value)
		return nil
	case syncEffectSet:
		value, err := decodeSyncEffectValue(effect)
		if err != nil {
			return err
		}
		if effect.Target == syncTargetReport {
			if effect.ID != "current" {
				return fmt.Errorf("bad report identity")
			}
			root[effect.Field] = value
			return nil
		}
		section, identity, err := syncEffectSection(effect.Target)
		if err != nil {
			return err
		}
		items, ok := root[section].([]any)
		if !ok {
			return fmt.Errorf("missing %s", section)
		}
		index, err := syncEffectIndex(items, identity, effect)
		if err != nil {
			return err
		}
		if index < 0 {
			if identity != "subject_ref" || effect.Field != "text" {
				return fmt.Errorf("missing target %s", effect.ID)
			}
			items = append(items, map[string]any{identity: effect.ID, effect.Field: value})
			root[section] = items
			return nil
		}
		items[index].(map[string]any)[effect.Field] = value
		return nil
	case syncEffectAdd:
		section, identity, err := syncEffectSection(effect.Target)
		if err != nil {
			return err
		}
		raw, ok := effect.Value.(json.RawMessage)
		if !ok {
			return fmt.Errorf("addition must be raw JSON")
		}
		var record map[string]any
		if err := yaml.Unmarshal(raw, &record); err != nil {
			return err
		}
		if record[identity] != effect.ID {
			return fmt.Errorf("addition identity mismatch")
		}
		items, exists := root[section].([]any)
		if !exists && section != "reviews" && section != "lanes" && section != "root_completions" && section != "root_forecasts" {
			return fmt.Errorf("missing %s", section)
		}
		index, err := syncEffectIndex(items, identity, effect)
		if err != nil || index >= 0 {
			return fmt.Errorf("duplicate addition")
		}
		root[section] = append(items, record)
		return nil
	case syncEffectRemove:
		if effect.Target == syncTargetLane && (effect.Field == "forecast" || effect.Field == "completed_at") {
			effect.Operation = syncEffectClear
			return applyExpectedSyncEffect(root, effect)
		}
		section, identity, err := syncEffectSection(effect.Target)
		if err != nil {
			return err
		}
		if identity != "subject_ref" && effect.Target != syncTargetRootForecast && effect.Target != syncTargetRootCompletion && effect.Target != syncTargetPriorityEstimate {
			return fmt.Errorf("unsupported removal")
		}
		items, ok := root[section].([]any)
		if !ok {
			return fmt.Errorf("missing %s", section)
		}
		index, err := syncEffectIndex(items, identity, effect)
		if err != nil || index < 0 {
			return fmt.Errorf("missing removal target")
		}
		if identity != "subject_ref" {
			addition, ok := effect.Value.(syncAddition)
			if !ok {
				return fmt.Errorf("record removal lacks authorization")
			}
			if effect.Target == syncTargetPriorityEstimate && addition.RetirePriority != effect.ID {
				return fmt.Errorf("priority retirement not authorized")
			}
			var expected any
			if err := json.Unmarshal(addition.ExpectedPrior, &expected); err != nil {
				return fmt.Errorf("invalid expected prior")
			}
			encoded, err := json.Marshal(items[index])
			if err != nil {
				return err
			}
			var actual any
			if err := json.Unmarshal(encoded, &actual); err != nil || !equalSyncPriorRecord(effect.Target, actual, expected) {
				return fmt.Errorf("root prior mismatch")
			}
		}
		root[section] = append(items[:index], items[index+1:]...)
		return nil
	case syncEffectClear, syncEffectForecastRetire, syncEffectCompletedAtClear:
		field := effect.Field
		if effect.Operation == syncEffectForecastRetire {
			field = "forecast"
		}
		if effect.Operation == syncEffectCompletedAtClear {
			field = "completed_at"
		}
		if effect.Target == syncTargetReport {
			if effect.ID != "current" {
				return fmt.Errorf("bad report identity")
			}
			delete(root, field)
			return nil
		}
		section, identity, err := syncEffectSection(effect.Target)
		if err != nil {
			return err
		}
		items, ok := root[section].([]any)
		if !ok {
			return fmt.Errorf("missing %s", section)
		}
		index, err := syncEffectIndex(items, identity, effect)
		if err != nil || index < 0 {
			return fmt.Errorf("missing target")
		}
		delete(items[index].(map[string]any), field)
		return nil
	default:
		return fmt.Errorf("unknown effect")
	}
}

// decodeSyncEffectValue retains lexical task labels in explicitly supplied values.
//
// Example: A numeric JSON priority 1.10 remains the task label "1.10".
func decodeSyncEffectValue(effect syncEffect) (any, error) {
	switch raw := effect.Value.(type) {
	case json.RawMessage:
		if effect.Target == syncTargetTask && (effect.Field == "priority" || effect.Field == "parent_id") {
			var text string
			err := yaml.Unmarshal(raw, &text)
			return text, err
		}
		if effect.Target == syncTargetReport && (effect.Field == "top_priorities" || effect.Field == "priority_order") {
			return decodeSyncStringList(raw)
		}
		var value any
		err := yaml.Unmarshal(raw, &value)
		return value, err
	case string:
		return raw, nil
	default:
		return nil, fmt.Errorf("invalid field value")
	}
}

// decodeSyncStringList converts supplied labels to generic verifier list values.
//
// Example: A lexical priority label remains a string in the expected report.
func decodeSyncStringList(raw json.RawMessage) ([]any, error) {
	var labels []string
	if err := yaml.Unmarshal(raw, &labels); err != nil {
		return nil, err
	}
	values := make([]any, len(labels))
	for i, label := range labels {
		values[i] = label
	}
	return values, nil
}

// syncEffectSection maps a target to its report section and identity key.
//
// Example: Select tasks by id.
func syncEffectSection(target syncTarget) (string, string, error) {
	switch target {
	case syncTargetTask:
		return "tasks", "id", nil
	case syncTargetLane:
		return "lanes", "task_id", nil
	case syncTargetReview:
		return "reviews", "task_id", nil
	case syncTargetPriorityEstimate:
		return "priority_estimates", "priority", nil
	case syncTargetRootCompletion:
		return "root_completions", "work_ref", nil
	case syncTargetRootForecast:
		return "root_forecasts", "forecast_ref", nil
	case syncTargetProgress, syncTargetDecisions, syncTargetBlockers, syncTargetVerification:
		return target.String(), "subject_ref", nil
	default:
		return "", "", fmt.Errorf("unknown target")
	}
}

// syncEffectIndex finds a unique matching record.
//
// Example: Find a lane by its task, work, and environment.
func syncEffectIndex(
	items []any,
	identity string,
	effect syncEffect,
) (int, error) {
	found := -1
	for i, item := range items {
		record, ok := item.(map[string]any)
		if !ok || record[identity] != effect.ID {
			continue
		}
		if effect.Target == syncTargetLane && (record["work_ref"] != effect.WorkRef || record["required_environment"] != effect.RequiredEnvironment.String()) {
			continue
		}
		if found >= 0 {
			return -1, fmt.Errorf("duplicate target")
		}
		found = i
	}
	return found, nil
}

// syncForecastMatches checks the referenced source status and kind.
//
// Example: Require an inactive root forecast.
func syncForecastMatches(
	forecasts forecastSource,
	ref string,
	status string,
	kind string,
) bool {
	if !validRef(ref, "forecasts.yaml") {
		return false
	}
	source := forecasts.Records[sourceID(ref, "forecasts.yaml")]
	return textField(source, "status") == status && textField(source, "kind") == kind
}

// verifyCanonicalSyncAddition validates source-owned records independently of insertion.
//
// Example: Reject a root completion without canonical evidence.
func verifyCanonicalSyncAddition(
	effect syncEffect,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	raw, ok := effect.Value.(json.RawMessage)
	if !ok {
		return fmt.Errorf("addition lacks source record")
	}
	var record struct {
		WorkRef     string `json:"work_ref"`
		ForecastRef string `json:"forecast_ref"`
		Outcome     string `json:"outcome"`
		CompletedAt string `json:"completed_at"`
		Lifecycle   string `json:"lifecycle"`
		Forecast    *struct {
			ForecastRef string `json:"forecast_ref"`
		} `json:"forecast"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	if effect.Target == syncTargetRootForecast {
		if !syncForecastMatches(forecasts, record.ForecastRef, "active", "root") {
			return fmt.Errorf("invalid canonical root forecast")
		}
		found := false
		for _, work := range ledger.Records {
			if work.Type == "work" && hasRole(work.Data.Roles, reportingRoleRoot) && openWork(work.Data.State) && string(work.Data.ForecastRef) == record.ForecastRef {
				if !outcomeCorresponds(work, ledger.Records, record.Outcome) {
					return fmt.Errorf("root outcome mismatch")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("root forecast lacks active canonical work")
		}
		return nil
	}
	if !validRef(record.WorkRef, "project-understanding.yaml") {
		return fmt.Errorf("invalid canonical work reference")
	}
	work, ok := ledger.Records[sourceID(record.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || !outcomeCorresponds(work, ledger.Records, record.Outcome) {
		return fmt.Errorf("canonical work or outcome mismatch")
	}
	ref := string(work.Data.ForecastRef)
	if effect.Target == syncTargetRootCompletion {
		if strings.TrimSpace(effect.Evidence) == "" || !hasRole(work.Data.Roles, reportingRoleRoot) || work.Data.State != workStateCompleted || !sameValue(work.Data.CompletedAt, record.CompletedAt) || (ref != "" && !syncForecastMatches(forecasts, ref, "inactive", "root")) {
			return fmt.Errorf("invalid canonical root completion")
		}
		return nil
	}
	if !hasRole(work.Data.Roles, reportingRoleLane) {
		return fmt.Errorf("canonical work is not a lane")
	}
	if openWork(work.Data.State) {
		if record.Forecast == nil || record.Forecast.ForecastRef != ref || !syncForecastMatches(forecasts, ref, "active", "lane") {
			return fmt.Errorf("invalid active lane forecast")
		}
		return nil
	}
	if record.Forecast != nil || strings.TrimSpace(effect.Evidence) == "" || (ref != "" && !syncForecastMatches(forecasts, ref, "inactive", "lane")) {
		return fmt.Errorf("invalid terminal lane forecast or evidence")
	}
	if work.Data.State == workStateCompleted && sameValue(work.Data.CompletedAt, record.CompletedAt) {
		return nil
	}
	if work.Data.State == workStateCancelled && record.Lifecycle == "cancelled" {
		return nil
	}
	return fmt.Errorf("invalid canonical lane completion")
}

// verifyCanonicalRootRemoval validates the explicit transition and its exact prior record.
//
// Example: Verify a root forecast retirement and its prior record.
func verifyCanonicalRootRemoval(
	before map[string]any,
	effect syncEffect,
	effects []syncEffect,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	addition, ok := effect.Value.(syncAddition)
	if !ok || strings.TrimSpace(addition.Evidence) == "" || effect.Evidence != addition.Evidence {
		return fmt.Errorf("root transition lacks evidence")
	}
	section, identity, err := syncEffectSection(effect.Target)
	if err != nil {
		return err
	}
	items, ok := before[section].([]any)
	if !ok {
		return fmt.Errorf("root prior missing")
	}
	index, err := syncEffectIndex(items, identity, effect)
	if err != nil || index < 0 {
		return fmt.Errorf("root prior identity missing")
	}
	actual, err := json.Marshal(items[index])
	if err != nil {
		return err
	}
	var got, expected any
	if json.Unmarshal(actual, &got) != nil || json.Unmarshal(addition.ExpectedPrior, &expected) != nil || !reflect.DeepEqual(got, expected) {
		return fmt.Errorf("root prior mismatch")
	}
	matched := false
	for _, add := range effects {
		raw, ok := add.Value.(json.RawMessage)
		if add.Operation == syncEffectAdd && add.Target == addition.Target && ok && string(raw) == string(addition.Record) && add.Evidence == addition.Evidence {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("root transition missing accompanying addition")
	}
	if err := verifyCanonicalSyncAddition(syncEffect{Target: addition.Target, Value: addition.Record, Evidence: addition.Evidence}, ledger, forecasts); err != nil {
		return err
	}
	var record struct {
		WorkRef     string `json:"work_ref"`
		ForecastRef string `json:"forecast_ref"`
	}
	if json.Unmarshal(addition.Record, &record) != nil {
		return fmt.Errorf("invalid root addition")
	}
	switch effect.Target {
	case syncTargetRootForecast:
		if addition.Target != syncTargetRootCompletion || !addition.RetireForecast || addition.ReopenWorkRef != "" || !validRef(record.WorkRef, "project-understanding.yaml") {
			return fmt.Errorf("root retirement not authorized")
		}
		work := ledger.Records[sourceID(record.WorkRef, "project-understanding.yaml")]
		if string(work.Data.ForecastRef) != effect.ID {
			return fmt.Errorf("root retirement forecast mismatch")
		}
	case syncTargetRootCompletion:
		if addition.Target != syncTargetRootForecast || addition.RetireForecast || addition.ReopenWorkRef != effect.ID || !validRef(effect.ID, "project-understanding.yaml") {
			return fmt.Errorf("root reopening not authorized")
		}
		work, ok := ledger.Records[sourceID(effect.ID, "project-understanding.yaml")]
		if !ok || work.Type != "work" || !hasRole(work.Data.Roles, reportingRoleRoot) || !openWork(work.Data.State) || string(work.Data.ForecastRef) != record.ForecastRef {
			return fmt.Errorf("root reopening work mismatch")
		}
	}
	return nil
}

// verifyPriorityRetirementEffects checks that retirement is paired with its task and new estimate.
//
// Example: Reject removal of an estimate unrelated to the reprioritized task.
func verifyPriorityRetirementEffects(
	original []byte,
	effects []syncEffect,
) error {
	report, err := decodeSyncVerificationReport(original)
	if err != nil {
		return err
	}
	for _, removal := range effects {
		if removal.Operation != syncEffectRemove || removal.Target != syncTargetPriorityEstimate {
			continue
		}
		addition, ok := removal.Value.(syncAddition)
		if !ok || addition.Target != syncTargetPriorityEstimate || addition.RetirePriority != removal.ID {
			return fmt.Errorf("invalid priority retirement authorization")
		}
		var supplied struct {
			Priority string `json:"priority"`
		}
		if json.Unmarshal(addition.Record, &supplied) != nil || supplied.Priority == "" || supplied.Priority == removal.ID {
			return fmt.Errorf("invalid new priority")
		}
		addCount, taskCount := 0, 0
		for _, effect := range effects {
			if effect.Operation == syncEffectAdd && effect.Target == syncTargetPriorityEstimate && effect.ID == supplied.Priority {
				raw, ok := effect.Value.(json.RawMessage)
				if !ok {
					return fmt.Errorf("invalid new estimate")
				}
				var expected, actual any
				if json.Unmarshal(addition.Record, &expected) != nil || json.Unmarshal(raw, &actual) != nil || !reflect.DeepEqual(expected, actual) {
					return fmt.Errorf("new estimate differs from supplied record")
				}
				addCount++
			}
			if effect.Operation == syncEffectSet && effect.Target == syncTargetTask && effect.Field == "priority" {
				value := ""
				switch v := effect.Value.(type) {
				case string:
					value = v
				case json.RawMessage:
					value, _ = syncPriorityLabelFromJSON(v)
				}
				if value != supplied.Priority {
					return fmt.Errorf("task priority differs from new estimate")
				}
				tasks, ok := report["tasks"].([]any)
				if !ok {
					return fmt.Errorf("tasks missing")
				}
				index, err := syncEffectIndex(tasks, "id", effect)
				if err != nil || index < 0 || tasks[index].(map[string]any)["priority"] != removal.ID {
					return fmt.Errorf("task does not have retired priority")
				}
				taskCount++
			}
		}
		if addCount == 0 && priorityRetirementReusesEstimate(report, supplied.Priority, addition.Record) {
			addCount = 1
		}
		if addCount != 1 || taskCount != 1 {
			return fmt.Errorf("priority retirement requires one estimate and task update")
		}
	}
	return nil
}

// priorityRetirementReusesEstimate verifies an unchanged destination in the original report.
//
// Example: An existing estimate must exactly match the record supplied with retirement.
func priorityRetirementReusesEstimate(
	report map[string]any,
	priority string,
	raw json.RawMessage,
) bool {
	var supplied map[string]any
	if yaml.Unmarshal(raw, &supplied) != nil {
		return false
	}
	items, ok := report["priority_estimates"].([]any)
	if !ok {
		return false
	}
	matched := 0
	for _, item := range items {
		estimate, ok := item.(map[string]any)
		if !ok || estimate["priority"] != priority {
			continue
		}
		if !equalPriorityEstimateRecord(estimate, supplied) {
			return false
		}
		matched++
	}
	return matched == 1
}

// verifyLaneForecastRestore checks a reopened lane's forecast against canonical sources.
//
// Example: Reject restoration from an inactive forecast.
func verifyLaneForecastRestore(
	before map[string]any,
	after map[string]any,
	effect syncEffect,
	effects []syncEffect,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	if effect.Target != syncTargetLane || !validRef(effect.WorkRef, "project-understanding.yaml") {
		return fmt.Errorf("invalid forecast restore identity")
	}
	work, ok := ledger.Records[sourceID(effect.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || !openWork(work.Data.State) {
		return fmt.Errorf("forecast restore requires open canonical work")
	}
	ref := string(work.Data.ForecastRef)
	if !validRef(ref, "forecasts.yaml") {
		return fmt.Errorf("invalid restored forecast reference")
	}
	source := forecasts.Records[sourceID(ref, "forecasts.yaml")]
	if textField(source, "kind") != "lane" || textField(source, "status") != "active" {
		return fmt.Errorf("restored forecast is not active lane source")
	}
	oldItems, ok := before["lanes"].([]any)
	if !ok {
		return fmt.Errorf("missing original lanes")
	}
	oldIndex, err := syncEffectIndex(oldItems, "task_id", effect)
	if err != nil || oldIndex < 0 {
		return fmt.Errorf("missing original lane")
	}
	oldLane := oldItems[oldIndex].(map[string]any)
	if _, exists := oldLane["forecast"]; exists {
		return fmt.Errorf("forecast restore would replace existing forecast")
	}
	newItems, ok := after["lanes"].([]any)
	if !ok {
		return fmt.Errorf("missing final lanes")
	}
	newIndex, err := syncEffectIndex(newItems, "task_id", effect)
	if err != nil || newIndex < 0 {
		return fmt.Errorf("missing final lane")
	}
	newLane := newItems[newIndex].(map[string]any)
	raw, ok := effect.Value.(json.RawMessage)
	if !ok {
		return fmt.Errorf("invalid forecast restore value")
	}
	var actual map[string]any
	if err := yaml.Unmarshal(raw, &actual); err != nil {
		return err
	}
	expected := map[string]any{"forecast_ref": ref}
	nested := map[string]any{}
	for _, field := range forecastFieldPolicies() {
		if field.Report != "" {
			if value := textField(source, field.Source); value != "" {
				expected[field.Report] = value
			}
		}
		if field.Nested {
			if value, exists := source[field.Source]; exists {
				nested[field.Source] = value
			}
		}
	}
	if len(nested) > 0 {
		expected["source_projection"] = nested
	}
	if recal, ok := source["recalibration"].(map[string]any); ok {
		for _, field := range forecastHistoryFields() {
			if value := textField(recal, field); value != "" {
				expected[field] = value
			}
		}
	}
	if !projectionEqual(actual, expected) || !projectionEqual(newLane["forecast"], expected) {
		return fmt.Errorf("restored forecast differs from canonical projection")
	}
	reopened := false
	for _, change := range effects {
		if change.Target != syncTargetLane || change.ID != effect.ID || change.WorkRef != effect.WorkRef || change.RequiredEnvironment != effect.RequiredEnvironment {
			continue
		}
		if change.Operation == syncEffectSet && change.Field == effect.RequiredEnvironment.String()+"_status" {
			if value, ok := change.Value.(string); ok && value != "CLOSED" {
				reopened = true
			}
		}
		if change.Operation == syncEffectClear && change.Field == "lifecycle" {
			reopened = true
		}
	}
	if !reopened {
		return fmt.Errorf("forecast restore lacks reopening effect")
	}
	return nil
}

// verifyCancelledRootForecastRetirement checks explicit removal against prior and canonical sources.
//
// Example: Reject retirement while the canonical forecast remains active.
func verifyCancelledRootForecastRetirement(
	before map[string]any,
	effect syncEffect,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	update, ok := effect.Value.(syncUpdate)
	if !ok || effect.Target != syncTargetRootForecast || update.Target != effect.Target || update.ID != effect.ID || update.WorkRef != effect.WorkRef || update.Evidence != effect.Evidence || strings.TrimSpace(effect.Evidence) == "" || len(update.Clear) != 1 || update.Clear[0] != "record" || len(update.Values) != 1 || len(update.Fields) != 0 || update.Retract {
		return fmt.Errorf("invalid root forecast retirement authorization")
	}
	if !validRef(effect.WorkRef, "project-understanding.yaml") || !validRef(effect.ID, "forecasts.yaml") {
		return fmt.Errorf("invalid root forecast retirement references")
	}
	work, ok := ledger.Records[sourceID(effect.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || !hasRole(work.Data.Roles, reportingRoleRoot) || work.Data.State != workStateCancelled || string(work.Data.ForecastRef) != effect.ID {
		return fmt.Errorf("root forecast retirement disagrees with canonical work")
	}
	forecast := forecasts.Records[sourceID(effect.ID, "forecasts.yaml")]
	if textField(forecast, "kind") != "root" || textField(forecast, "status") != "inactive" {
		return fmt.Errorf("root forecast retirement requires inactive root forecast")
	}
	items, ok := before["root_forecasts"].([]any)
	if !ok {
		return fmt.Errorf("root forecast missing")
	}
	index, err := syncEffectIndex(items, "forecast_ref", effect)
	if err != nil || index < 0 {
		return fmt.Errorf("root forecast identity missing or ambiguous")
	}
	actual, err := json.Marshal(items[index])
	if err != nil {
		return err
	}
	var got, expected any
	if json.Unmarshal(actual, &got) != nil || json.Unmarshal(update.Values["expected_prior"], &expected) != nil || !reflect.DeepEqual(got, expected) {
		return fmt.Errorf("root forecast prior mismatch")
	}
	return nil
}

// verifyRootCompletionCorrection checks the exact prior and source-backed replacement.
//
// Example: Reject a completion correction whose timestamp differs from canonical work.
func verifyRootCompletionCorrection(
	before map[string]any,
	effect syncEffect,
	effects []syncEffect,
	ledger ledgerSource,
	forecasts forecastSource,
) error {
	addition, ok := effect.Value.(syncAddition)
	if !ok || effect.Target != syncTargetRootCompletion || addition.Target != effect.Target || strings.TrimSpace(addition.Evidence) == "" || addition.Evidence != effect.Evidence || addition.RetireForecast || addition.ReopenWorkRef != "" {
		return fmt.Errorf("invalid root completion correction")
	}
	var record struct {
		WorkRef string `json:"work_ref"`
	}
	if json.Unmarshal(addition.Record, &record) != nil || record.WorkRef != effect.ID {
		return fmt.Errorf("root completion correction identity mismatch")
	}
	items, ok := before["root_completions"].([]any)
	if !ok {
		return fmt.Errorf("root completion prior missing")
	}
	index, err := syncEffectIndex(items, "work_ref", effect)
	if err != nil || index < 0 {
		return fmt.Errorf("root completion prior ambiguous or missing")
	}
	encoded, err := json.Marshal(items[index])
	if err != nil {
		return err
	}
	var actual, prior any
	if json.Unmarshal(encoded, &actual) != nil || json.Unmarshal(addition.ExpectedPrior, &prior) != nil || !reflect.DeepEqual(actual, prior) {
		return fmt.Errorf("root completion prior mismatch")
	}
	matched := 0
	for _, change := range effects {
		raw, ok := change.Value.(json.RawMessage)
		if change.Operation != syncEffectAdd || change.Target != effect.Target || change.ID != effect.ID || change.Evidence != effect.Evidence || !ok {
			continue
		}
		var supplied, projected any
		if json.Unmarshal(addition.Record, &supplied) != nil || json.Unmarshal(raw, &projected) != nil || !reflect.DeepEqual(supplied, projected) {
			return fmt.Errorf("root completion replacement mismatch")
		}
		matched++
	}
	if matched != 1 {
		return fmt.Errorf("root completion correction requires one replacement")
	}
	return verifyCanonicalSyncAddition(syncEffect{Target: effect.Target, Value: addition.Record, Evidence: addition.Evidence}, ledger, forecasts)
}
