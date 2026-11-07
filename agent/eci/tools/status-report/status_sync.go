package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/token"
	"gopkg.in/yaml.v3"
)

// syncUpdate describes an explicit report target and its supplied fields.
//
// Example: Close one named task with supplied acceptance evidence.
type syncUpdate struct {
	Target              syncTarget                 `json:"target" yaml:"target"`
	ID                  string                     `json:"id" yaml:"id"`
	WorkRef             string                     `json:"work_ref,omitempty" yaml:"work_ref,omitempty"`
	RequiredEnvironment syncEnvironment            `json:"required_environment,omitempty" yaml:"required_environment,omitempty"`
	Corrects            int                        `json:"corrects,omitempty" yaml:"corrects,omitempty"`
	CorrectsOrdinal     *int                       `json:"corrects_ordinal,omitempty" yaml:"corrects_ordinal,omitempty"`
	Retract             bool                       `json:"retract,omitempty" yaml:"retract,omitempty"`
	Evidence            string                     `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	Fields              map[string]string          `json:"fields" yaml:"fields"`
	Values              map[string]json.RawMessage `json:"values,omitempty" yaml:"values,omitempty"`
	Clear               []string                   `json:"clear,omitempty" yaml:"clear,omitempty"`
}

// syncEvent selects the status payload and envelope from one log entry.
//
// Example: Read an explicitly supplied status update from a decision event.
type syncEvent struct {
	Event         string `json:"event"`
	Timestamp     string `json:"timestamp"`
	InvalidStatus bool   `json:"-"`
	Details       struct {
		Corrects     json.RawMessage        `json:"corrects"`
		StatusUpdate *syncEventStatusUpdate `json:"status_update"`
	} `json:"details"`
}

// syncEventStatusUpdate holds typed projection items from a log event.
//
// Example: Retain valid updates alongside malformed sibling entries.
type syncEventStatusUpdate struct {
	Updates   []syncUpdate          `json:"updates"`
	Additions []syncAddition        `json:"additions"`
	Timeline  *syncTimelineSnapshot `json:"timeline"`
}

// syncAddition carries a complete record to insert into the report.
//
// Example: Add a newly scoped task.
type syncAddition struct {
	Target         syncTarget      `json:"target" yaml:"target"`
	Record         json.RawMessage `json:"record" yaml:"record"`
	Evidence       string          `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	RetireForecast bool            `json:"retire_forecast,omitempty" yaml:"retire_forecast,omitempty"`
	ReopenWorkRef  string          `json:"reopen_work_ref,omitempty" yaml:"reopen_work_ref,omitempty"`
	ExpectedPrior  json.RawMessage `json:"expected_prior,omitempty" yaml:"expected_prior,omitempty"`
	RetirePriority string          `json:"retire_priority,omitempty" yaml:"retire_priority,omitempty"`
}

// syncPendingAddition retains an unapplied insertion and its log position.
//
// Example: Retry a review whose task does not yet exist.
type syncPendingAddition struct {
	Line                int             `json:"line" yaml:"line"`
	Target              syncTarget      `json:"target" yaml:"target"`
	Record              string          `json:"record" yaml:"record"`
	Evidence            string          `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	ID                  string          `json:"id,omitempty" yaml:"id,omitempty"`
	WorkRef             string          `json:"work_ref,omitempty" yaml:"work_ref,omitempty"`
	RequiredEnvironment syncEnvironment `json:"required_environment,omitempty" yaml:"required_environment,omitempty"`
	RetireForecast      bool            `json:"retire_forecast,omitempty" yaml:"retire_forecast,omitempty"`
	ReopenWorkRef       string          `json:"reopen_work_ref,omitempty" yaml:"reopen_work_ref,omitempty"`
	ExpectedPrior       string          `json:"expected_prior,omitempty" yaml:"expected_prior,omitempty"`
	RetirePriority      string          `json:"retire_priority,omitempty" yaml:"retire_priority,omitempty"`
	PriorLine           int             `json:"prior_line,omitempty" yaml:"prior_line,omitempty"`
	PriorRecord         string          `json:"prior_record,omitempty" yaml:"prior_record,omitempty"`
}

// syncState stores replay progress alongside the report it describes.
//
// Example: Resume pending fields after their targets appear.
type syncState struct {
	Cursor               int                                `json:"cursor" yaml:"cursor"`
	Applied              map[string]string                  `json:"applied" yaml:"applied"`
	Latest               map[string]int                     `json:"latest" yaml:"latest"`
	AppliedOrdinals      map[string]int                     `json:"applied_ordinals,omitempty" yaml:"applied_ordinals,omitempty"`
	Pending              map[string]syncPending             `json:"pending" yaml:"pending"`
	PendingAdditions     map[string]syncPendingAddition     `json:"pending_additions,omitempty" yaml:"pending_additions,omitempty"`
	PendingRootAdditions map[string]syncPendingRootAddition `json:"pending_root_additions,omitempty" yaml:"pending_root_additions,omitempty"`
	Groups               map[string]syncPendingGroup        `json:"groups,omitempty" yaml:"groups,omitempty"`
	PendingTimeline      *syncPendingTimeline               `json:"pending_timeline,omitempty" yaml:"pending_timeline,omitempty"`
	PendingTimelines     map[string]syncPendingTimeline     `json:"pending_timelines,omitempty" yaml:"pending_timelines,omitempty"`
	TimelineAppliedLine  int                                `json:"timeline_applied_line,omitempty" yaml:"timeline_applied_line,omitempty"`
}

// syncPendingTimeline retains an unapplied complete timeline snapshot.
//
// Example: Retry a snapshot after the report timestamp advances.
type syncPendingTimeline struct {
	Line     int    `json:"line" yaml:"line"`
	Snapshot string `json:"snapshot" yaml:"snapshot"`
}

// syncPendingGroup retains one complete target update in canonical order.
//
// Example: Retry an assignment after its named task appears.
type syncPendingGroup struct {
	Line    int    `json:"line" yaml:"line"`
	Ordinal int    `json:"ordinal" yaml:"ordinal"`
	Update  string `json:"update" yaml:"update"`
}

// syncPending retains one unapplied field and its log position.
//
// Example: Retry a task field after a manual conflict is resolved.
type syncPending struct {
	Line       int        `json:"line" yaml:"line"`
	Update     syncUpdate `json:"update" yaml:"update"`
	Acceptance string     `json:"acceptance,omitempty" yaml:"acceptance,omitempty"`
}

// projectLogStatus applies explicit log updates to the supplied report snapshot.
//
// Example: Replay new task updates before publishing a report.
func projectLogStatus(
	dir string,
	original []byte,
	ledger ledgerSource,
	forecasts forecastSource,
) ([]byte, []byte, error) {
	var document struct {
		StatusSync *syncState `yaml:"status_sync"`
	}
	if err := yaml.Unmarshal(original, &document); err != nil {
		return nil, nil, err
	}
	cursor := 0
	if document.StatusSync != nil {
		cursor = document.StatusSync.Cursor
	}
	logBytes, err := os.ReadFile(filepath.Join(dir, "high_level_log.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		if cursor != 0 {
			return nil, nil, fmt.Errorf("status sync cursor exceeds log: %w", err)
		}
		return original, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	// A nil snapshot denotes an absent log; preserve a distinct empty snapshot.
	if logBytes == nil {
		logBytes = []byte{}
	}
	boundaries := make([]int, 0)
	for i, b := range logBytes {
		if b == '\n' {
			boundaries = append(boundaries, i+1)
		}
	}
	incompleteTail := false
	if len(logBytes) > 0 && (len(boundaries) == 0 || boundaries[len(boundaries)-1] != len(logBytes)) {
		start := 0
		if len(boundaries) > 0 {
			start = boundaries[len(boundaries)-1]
		}
		// A complete JSON value is a valid final entry without a newline. An
		// incomplete tail must remain unconsumed so a later append can finish it.
		if json.Valid(logBytes[start:]) {
			boundaries = append(boundaries, len(logBytes))
		} else {
			incompleteTail = true
		}
	}
	if cursor < 0 || cursor > len(boundaries) {
		return nil, nil, fmt.Errorf("status sync cursor exceeds log")
	}
	result := original
	if len(boundaries) > cursor {
		result, err = prepareNullSyncState(result)
		if err != nil {
			return nil, nil, err
		}
	}
	var effects []syncEffect
	start := cursor
	for i := cursor; i < len(boundaries); i++ {
		lineStart := 0
		if i > 0 {
			lineStart = boundaries[i-1]
		}
		if i+1 < len(boundaries) && !syncLineHasUpdate(logBytes[lineStart:boundaries[i]]) {
			continue
		}
		chunkStart := 0
		if start > 0 {
			chunkStart = boundaries[start-1]
		}
		var stepEffects []syncEffect
		result, stepEffects, err = projectLogStatusStep(result, ledger, forecasts, logBytes[chunkStart:boundaries[i]], start, logBytes)
		if err != nil {
			return nil, nil, err
		}
		effects = append(effects, stepEffects...)
		start = i + 1
	}
	if cursor == len(boundaries) {
		var stepEffects []syncEffect
		result, stepEffects, err = projectLogStatusStep(result, ledger, forecasts, nil, cursor, logBytes)
		if err != nil {
			return nil, nil, err
		}
		effects = append(effects, stepEffects...)
	}
	if incompleteTail {
		diagnostic := refreshIssuePrefix + "unterminated invalid final log entry; complete or terminate it before retry"
		result, err = prepareInvalidation(result, diagnostic)
		if err != nil {
			return nil, nil, err
		}
		effects = append(effects, syncEffect{Operation: syncEffectDiagnostic, Value: diagnostic})
	}
	if err := verifyStatusSyncEffects(original, result, effects); err != nil {
		return nil, nil, err
	}
	return result, logBytes, nil
}

// prepareNullSyncState normalizes null syntax for token-based event projection.
//
// Example: An implicit flow null remains null while the first event is applied.
func prepareNullSyncState(data []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	node := mapValue(root.Content[0], "status_sync")
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!null" {
		return data, nil
	}
	if node.Value != "" && node.Style&yaml.TaggedStyle == 0 && node.Anchor == "" {
		return data, nil
	}
	return replaceNullYAMLValue(data, node, "null")
}

// syncLineHasUpdate identifies a log entry requiring an ordered projection step.
//
// Example: Legacy events can be scanned together before the next typed update.
func syncLineHasUpdate(line []byte) bool {
	var envelope struct {
		Details json.RawMessage `json:"details"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return bytes.Contains(line, []byte(`"status_update"`))
	}
	var details map[string]json.RawMessage
	if json.Unmarshal(envelope.Details, &details) != nil {
		return false
	}
	_, exists := details["status_update"]
	return exists
}

// projectLogStatusStep applies one newly observed event and retries pending work.
//
// Example: A closure is applied before a later evidence update.
func projectLogStatusStep(
	original []byte,
	ledger ledgerSource,
	forecasts forecastSource,
	logBytes []byte,
	startLine int,
	fullLog []byte,
) ([]byte, []syncEffect, error) {
	var effects []syncEffect
	var root yaml.Node
	if err := yaml.Unmarshal(original, &root); err != nil {
		return nil, nil, err
	}
	var state syncState
	existingState := mapValue(root.Content[0], "status_sync")
	if n := existingState; n != nil {
		if err := n.Decode(&state); err != nil {
			return nil, nil, err
		}
	}
	if state.Applied == nil {
		state.Applied = map[string]string{}
	}
	if state.Latest == nil {
		state.Latest = map[string]int{}
	}
	if state.AppliedOrdinals == nil {
		state.AppliedOrdinals = map[string]int{}
	}
	if err := normalizeSyncOwnership(&state); err != nil {
		return nil, nil, err
	}
	if state.Pending == nil {
		state.Pending = map[string]syncPending{}
	}
	if state.PendingAdditions == nil {
		state.PendingAdditions = map[string]syncPendingAddition{}
	}
	if state.PendingRootAdditions == nil {
		state.PendingRootAdditions = map[string]syncPendingRootAddition{}
	}
	migratePendingRootAdditions(state.PendingAdditions, state.PendingRootAdditions, fullLog, state.Applied, original, ledger)
	if state.Groups == nil {
		state.Groups = map[string]syncPendingGroup{}
	}
	if state.PendingTimelines == nil {
		state.PendingTimelines = map[string]syncPendingTimeline{}
	}
	if state.PendingTimeline != nil {
		state.PendingTimelines[strconv.Itoa(state.PendingTimeline.Line)] = *state.PendingTimeline
		state.PendingTimeline = nil
	}
	legacyPending := make(map[string]syncPending, len(state.Pending))
	for key, value := range state.Pending {
		legacyPending[key] = value
	}
	legacyApplied := make(map[string]string, len(state.Applied))
	for key, value := range state.Applied {
		legacyApplied[key] = value
	}
	legacyRemovals := map[string]bool{}
	lines := bytes.Split(logBytes, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if state.Cursor != startLine {
		return nil, nil, fmt.Errorf("status sync cursor changed during replay")
	}
	if len(lines) == 0 && existingState == nil {
		return original, nil, nil
	}
	invalidUpdate := false
	latestTimestamp := ""
	for offset := range lines {
		i := startLine + offset
		var event syncEvent
		if err := json.Unmarshal(lines[offset], &event); err != nil {
			if bytes.Contains(lines[offset], []byte(`"status_update"`)) {
				invalidUpdate = true
			}
			continue
		}
		if event.Details.StatusUpdate == nil {
			invalidUpdate = invalidUpdate || event.InvalidStatus
			continue
		}
		invalidUpdate = invalidUpdate || event.InvalidStatus
		if _, err := parseUTC(event.Timestamp); err == nil {
			if latestTimestamp == "" {
				latestTimestamp = event.Timestamp
			} else {
				previous, _ := parseUTC(latestTimestamp)
				current, _ := parseUTC(event.Timestamp)
				if current.After(previous) {
					latestTimestamp = event.Timestamp
				}
			}
		}
		if len(event.Details.StatusUpdate.Updates) == 0 && len(event.Details.StatusUpdate.Additions) == 0 && event.Details.StatusUpdate.Timeline == nil {
			invalidUpdate = true
		}
		if snapshot := event.Details.StatusUpdate.Timeline; snapshot != nil {
			encoded, err := json.Marshal(snapshot)
			if err != nil {
				invalidUpdate = true
			} else {
				state.PendingTimelines[strconv.Itoa(i+1)] = syncPendingTimeline{Line: i + 1, Snapshot: string(encoded)}
			}
		}
		for ordinal, addition := range event.Details.StatusUpdate.Additions {
			if addition.Target != syncTargetRootCompletion && addition.Target != syncTargetRootForecast && addition.Target != syncTargetPriorityEstimate && (addition.RetireForecast || addition.ReopenWorkRef != "" || len(addition.ExpectedPrior) > 0 || addition.RetirePriority != "") {
				invalidUpdate = true
				continue
			}
			if addition.Target != syncTargetPriorityEstimate && addition.RetirePriority != "" {
				invalidUpdate = true
				continue
			}
			if addition.Target == syncTargetPriorityEstimate && (addition.RetireForecast || addition.ReopenWorkRef != "" || (len(addition.ExpectedPrior) > 0 && addition.RetirePriority == "")) {
				invalidUpdate = true
				continue
			}
			if (addition.Target == syncTargetRootCompletion && addition.ReopenWorkRef != "") || (addition.Target == syncTargetRootForecast && addition.RetireForecast) {
				invalidUpdate = true
				continue
			}
			var record map[string]json.RawMessage
			if json.Unmarshal(addition.Record, &record) != nil {
				invalidUpdate = true
				continue
			}
			if addition.Target == syncTargetRootCompletion || addition.Target == syncTargetRootForecast {
				if !queueRootAddition(state.PendingRootAdditions, i+1, ordinal, addition) {
					invalidUpdate = true
				}
				continue
			}
			idKey := "id"
			switch addition.Target {
			case syncTargetReview:
				idKey = "task_id"
			case syncTargetPriorityEstimate:
				idKey = "priority"
			case syncTargetLane:
				idKey = "task_id"
			case syncTargetTask:
			default:
				invalidUpdate = true
				continue
			}
			var id string
			if json.Unmarshal(record[idKey], &id) != nil || strings.TrimSpace(id) == "" {
				invalidUpdate = true
				continue
			}
			key := "addition/" + addition.Target.String() + "/" + id
			var workRef string
			var environment syncEnvironment
			if addition.Target == syncTargetLane {
				if json.Unmarshal(record["work_ref"], &workRef) != nil || json.Unmarshal(record["required_environment"], &environment) != nil || workRef == "" || environment == syncEnvironmentUnknown {
					invalidUpdate = true
					continue
				}
				selector, _ := json.Marshal([]string{id, workRef, environment.String()})
				key = "addition/lane/" + string(selector)
			}
			if _, exists := state.PendingAdditions[key]; exists {
				invalidUpdate = true
				continue
			}
			state.Latest[key] = i + 1
			state.PendingAdditions[key] = syncPendingAddition{Line: i + 1, Target: addition.Target, Record: string(addition.Record), Evidence: addition.Evidence, ID: id, WorkRef: workRef, RequiredEnvironment: environment, RetireForecast: addition.RetireForecast, ReopenWorkRef: addition.ReopenWorkRef, ExpectedPrior: string(addition.ExpectedPrior), RetirePriority: addition.RetirePriority}
		}
		for ordinal, update := range event.Details.StatusUpdate.Updates {
			if !syncCorrectionLocatorPrecedes(update, i+1, ordinal) {
				invalidUpdate = true
				continue
			}
			if !syncUpdateSelectorValid(update) {
				invalidUpdate = true
				continue
			}
			if update.Retract {
				probe := update
				probe.Fields = map[string]string{"text": "probe"}
				probe.Retract = false
				if event.Event != "correction" || len(update.Fields) != 0 || len(update.Values) != 0 || len(update.Clear) != 0 || !validNarrativeSyncUpdate(probe) || update.Corrects <= 0 || (update.CorrectsOrdinal != nil && *update.CorrectsOrdinal < 0) {
					invalidUpdate = true
					continue
				}
				encoded, err := encodeSyncGroup(update)
				if err != nil {
					invalidUpdate = true
					continue
				}
				state.Groups[syncGroupKey(i+1, ordinal)] = syncPendingGroup{Line: i + 1, Ordinal: ordinal, Update: encoded}
				continue
			}
			if (!validSyncUpdate(update) && !validNarrativeSyncUpdate(update) && !validStructuredSyncUpdate(update) && !validLaneSyncUpdate(update) && !validRootRetirementSyncUpdate(update)) || update.Corrects < 0 || (update.CorrectsOrdinal != nil && (*update.CorrectsOrdinal < 0 || update.Corrects == 0)) || (event.Event == "correction" && update.Corrects == 0) || (event.Event != "correction" && update.Corrects != 0) {
				invalidUpdate = true
				continue
			}
			encoded, err := encodeSyncGroup(update)
			if err != nil {
				invalidUpdate = true
				continue
			}
			state.Groups[syncGroupKey(i+1, ordinal)] = syncPendingGroup{Line: i + 1, Ordinal: ordinal, Update: encoded}
		}
	}
	state.Cursor = startLine + len(lines)
	result := original
	var rootEffects []syncEffect
	result, rootEffects = applyPendingRootChains(result, &state, fullLog, ledger, forecasts)
	effects = append(effects, rootEffects...)
	additionKeys := make([]string, 0, len(state.PendingAdditions))
	for key := range state.PendingAdditions {
		additionKeys = append(additionKeys, key)
	}
	sort.Strings(additionKeys)
	// Repeat while insertions unlock dependent tasks or reviews in this batch.
	for progress := true; progress; {
		progress = false
		for _, key := range additionKeys {
			p, exists := state.PendingAdditions[key]
			if !exists {
				continue
			}
			// Root entries remaining after migration lack a uniquely proven source
			// occurrence and must not fall back to identity-only replay.
			if p.Target == syncTargetRootCompletion || p.Target == syncTargetRootForecast {
				continue
			}
			if p.Line != state.Latest[key] {
				delete(state.PendingAdditions, key)
				continue
			}
			if p.Target == syncTargetPriorityEstimate {
				continue
			}
			groupKeys := []string{key}
			group := []syncAddition{{Target: p.Target, Record: json.RawMessage(p.Record), Evidence: p.Evidence, RetireForecast: p.RetireForecast, ReopenWorkRef: p.ReopenWorkRef, ExpectedPrior: json.RawMessage(p.ExpectedPrior)}}
			if !syncAdditionSourceValid(group[0], ledger, forecasts) {
				continue
			}
			if p.Target == syncTargetTask {
				var record struct {
					Priority string `json:"priority"`
				}
				if json.Unmarshal([]byte(p.Record), &record) == nil {
					estimateKey := "addition/priority_estimate/" + record.Priority
					if estimate, ok := state.PendingAdditions[estimateKey]; ok && estimate.Line == p.Line && state.Latest[estimateKey] == estimate.Line && estimate.RetirePriority == "" {
						groupKeys = append(groupKeys, estimateKey)
						group = append(group, syncAddition{Target: estimate.Target, Record: json.RawMessage(estimate.Record)})
					}
				}
			}
			updated, ok := applySyncAdditionGroup(result, group)
			if !ok {
				continue
			}
			var additionEffects []syncEffect
			for _, appliedKey := range groupKeys {
				item := state.PendingAdditions[appliedKey]
				id := item.ID
				if id == "" {
					id = strings.TrimPrefix(appliedKey, "addition/"+item.Target.String()+"/")
				}
				additionEffects = append(additionEffects, syncEffect{Target: item.Target, ID: id, WorkRef: item.WorkRef, RequiredEnvironment: item.RequiredEnvironment, Value: json.RawMessage(item.Record), Evidence: item.Evidence, Operation: syncEffectAdd})
			}
			if err := verifyStatusSyncCanonicalEffects(result, updated, additionEffects, ledger, forecasts); err != nil {
				continue
			}
			ownership := make(map[string]map[string]string, len(groupKeys))
			selectors := make(map[string]syncUpdate, len(groupKeys))
			validOwnership := true
			for _, appliedKey := range groupKeys {
				item := state.PendingAdditions[appliedKey]
				values, valid := additionOwnership(item.Target, json.RawMessage(item.Record))
				selector, identified := pendingAdditionSelector(appliedKey, item)
				if !valid || !identified {
					validOwnership = false
					break
				}
				ownership[appliedKey], selectors[appliedKey] = values, selector
			}
			if !validOwnership {
				continue
			}
			result, progress = updated, true
			effects = append(effects, additionEffects...)
			for _, appliedKey := range groupKeys {
				recordAdditionOwnership(&state, selectors[appliedKey], state.PendingAdditions[appliedKey].Line, ownership[appliedKey])
				state.Applied[appliedKey] = state.PendingAdditions[appliedKey].Record
				delete(state.PendingAdditions, appliedKey)
			}
		}
	}
	// Legacy field entries have no saved ordinal. Hold ambiguous same-line
	// corrections until their source occurrence can be identified uniquely.
	heldLegacy, invalidLegacy := holdAmbiguousLegacyCorrections(&state, legacyPending, fullLog)
	invalidUpdate = invalidUpdate || invalidLegacy
	result = applyTaskCancellations(result, &state)
	result = applyLaneCompletions(result, ledger, forecasts, &state)
	keys := make([]string, 0, len(state.Pending))
	for k := range state.Pending {
		keys = append(keys, k)
	}
	sort.Slice(keys,
		// Order text before evidence for a newly inserted subject.
		//
		// Example: Insert a progress text before its evidence field.
		func(
			i int,
			j int,
		) bool {
			// New keyed narratives need their text before their evidence.
			a, b := strings.TrimSuffix(keys[i], "/text"), strings.TrimSuffix(keys[j], "/text")
			if strings.HasSuffix(keys[i], "/text") && strings.TrimSuffix(keys[j], "/evidence") == a {
				return true
			}
			if strings.HasSuffix(keys[j], "/text") && strings.TrimSuffix(keys[i], "/evidence") == b {
				return false
			}
			return keys[i] < keys[j]
		})
	for _, key := range keys {
		pending := state.Pending[key]
		if state.Latest[key] != pending.Line {
			delete(state.Pending, key)
			continue
		}
		if pending.Update.Retract {
			textKey, evidenceKey := syncTargetKey(pending.Update, "text"), syncTargetKey(pending.Update, "evidence")
			latest := state.Latest[textKey]
			if state.Latest[evidenceKey] > latest {
				latest = state.Latest[evidenceKey]
			}
			if latest != pending.Update.Corrects {
				delete(state.Pending, key)
				continue
			}
			if state.Applied[textKey] == "" {
				delete(state.Pending, textKey)
				delete(state.Pending, evidenceKey)
				state.Latest[textKey], state.Latest[evidenceKey] = pending.Line, pending.Line
				delete(state.Pending, key)
				continue
			}
			text, hasText := state.Applied[textKey]
			evidence, hasEvidence := state.Applied[evidenceKey]
			updated, ok := retractNarrativeSync(result, pending.Update, text, hasText, evidence, hasEvidence)
			if ok {
				if _, err := parse(updated); err != nil {
					continue
				}
				originalText, present := legacyApplied[textKey]
				legacyRemovals[key] = narrativeSyncItemOwned(original, pending.Update, originalText, present)
				result = updated
				delete(state.Applied, textKey)
				delete(state.Applied, evidenceKey)
				state.Latest[textKey], state.Latest[evidenceKey] = pending.Line, pending.Line
				delete(state.Pending, key)
			}
			continue
		}
		field := ""
		value := ""
		for field, value = range pending.Update.Fields {
			break
		}
		if pending.Update.Target == syncTargetTask {
			lifecycle := state.Pending[syncTargetKey(pending.Update, "lifecycle")]
			if lifecycle.Line == pending.Line && (field == "lifecycle" || field == "status" || field == "evidence") {
				continue
			}
		}
		if pending.Update.Target == syncTargetLane && (field == "completed_at" || (strings.HasSuffix(field, "_status") && field == pending.Update.RequiredEnvironment.String()+"_status" && value == "CLOSED")) {
			continue
		}
		if pending.Update.Target == syncTargetTask && field == "status" && value == "CLOSED" && !taskHasEvidence(result, pending.Update.ID, pending.Acceptance) {
			continue
		}
		if pending.Update.Target == syncTargetLane && !laneSyncAllowed(result, ledger, forecasts, pending.Update, field, value) {
			continue
		}
		var updated []byte
		var applied bool
		if validNarrativeSyncUpdate(pending.Update) {
			textKey := syncTargetKey(pending.Update, "text")
			text, hasText := state.Applied[textKey]
			owned := narrativeSyncItemOwned(result, pending.Update, text, hasText)
			prior, exists := state.Applied[key]
			updated, applied = applyNarrativeSyncField(result, pending.Update, field, value, prior, exists, owned)
		} else {
			prior, exists := state.Applied[key]
			updated, applied = applySyncField(result, pending.Update, field, value, prior, exists)
		}
		if applied {
			if _, err := parse(updated); err != nil {
				continue
			}
			result = updated
			if pending.Update.Target == syncTargetTask || pending.Update.Target == syncTargetLane || pending.Update.Target == syncTargetReport {
				state.Applied[key] = scalarSyncStoredValue(value)
			} else {
				state.Applied[key] = value
			}
			delete(state.Pending, key)
		}
	}
	for key, pending := range heldLegacy {
		state.Pending[key] = pending
	}
	legacyEffects := legacySyncEffects(legacyPending, state, legacyRemovals)
	if err := verifyStatusSyncCanonicalEffects(original, result, legacyEffects, ledger, forecasts); err != nil {
		return nil, nil, err
	}
	effects = append(effects, legacyEffects...)
	var groupEffects []syncEffect
	var groupInvalid bool
	result, groupEffects, groupInvalid = applyPendingSyncGroups(result, &state, ledger, forecasts, fullLog)
	effects = append(effects, groupEffects...)
	invalidUpdate = invalidUpdate || groupInvalid
	if latestTimestamp != "" {
		before, err := parse(result)
		if err != nil {
			return nil, nil, err
		}
		result, err = advanceSyncTimestamp(result, latestTimestamp)
		if err != nil {
			return nil, nil, err
		}
		after, err := parse(result)
		if err != nil {
			return nil, nil, err
		}
		if after.Timestamp != before.Timestamp {
			effects = append(effects, syncEffect{Operation: syncEffectTimestamp, Value: after.Timestamp})
		}
		if after.TimelineAsOf != before.TimelineAsOf {
			effects = append(effects, syncEffect{Operation: syncEffectTimelineAnchor, Value: after.TimelineAsOf})
		}
	}
	timelineKeys := make([]string, 0, len(state.PendingTimelines))
	for key := range state.PendingTimelines {
		timelineKeys = append(timelineKeys, key)
	}
	sort.Slice(timelineKeys,
		// Evaluate snapshots in canonical log order.
		//
		// Example: A later snapshot cannot be followed by an older retry.
		func(
			i int,
			j int,
		) bool {
			return state.PendingTimelines[timelineKeys[i]].Line < state.PendingTimelines[timelineKeys[j]].Line
		})
	for _, key := range timelineKeys {
		pending := state.PendingTimelines[key]
		if pending.Line <= state.TimelineAppliedLine {
			delete(state.PendingTimelines, key)
			continue
		}
		var snapshot syncTimelineSnapshot
		if json.Unmarshal([]byte(pending.Snapshot), &snapshot) != nil {
			continue
		}
		if updated, ok := applySyncTimeline(result, snapshot); ok {
			result = updated
			effects = append(effects, syncEffect{Operation: syncEffectTimeline, Value: snapshot})
			state.TimelineAppliedLine = pending.Line
			delete(state.PendingTimelines, key)
		}
	}
	for key, pending := range state.PendingTimelines {
		if pending.Line <= state.TimelineAppliedLine {
			delete(state.PendingTimelines, key)
		}
	}
	// Store state in the report itself so report values and cursor publish atomically.
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, nil, err
	}
	result, err = putSyncState(result, string(encoded))
	if err != nil {
		return nil, nil, err
	}
	effects = append(effects, syncEffect{Operation: syncEffectMetadata, Value: state})
	if invalidUpdate {
		result, err = prepareInvalidation(result, refreshIssuePrefix+"invalid status_update requires manual review")
		if err != nil {
			return nil, nil, err
		}
		effects = append(effects, syncEffect{Operation: syncEffectDiagnostic, Value: refreshIssuePrefix + "invalid status_update requires manual review"})
	}
	if len(state.Pending) > 0 || len(state.PendingAdditions) > 0 || len(state.PendingRootAdditions) > 0 || len(state.Groups) > 0 || len(state.PendingTimelines) > 0 {
		result, err = prepareInvalidation(result, refreshIssuePrefix+"status updates remain pending manual reconciliation")
		if err != nil {
			return nil, nil, err
		}
		effects = append(effects, syncEffect{Operation: syncEffectDiagnostic, Value: refreshIssuePrefix + "status updates remain pending manual reconciliation"})
	}
	return result, effects, nil
}

// applyTaskCancellations atomically records cancellation without completing children.
//
// Example: Cancel one task, retaining its independent descendants.
func applyTaskCancellations(
	data []byte,
	state *syncState,
) []byte {
	keys := make([]string, 0, len(state.Pending))
	for key := range state.Pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, lifecycleKey := range keys {
		pending, ok := state.Pending[lifecycleKey]
		if !ok || pending.Update.Target != syncTargetTask || pending.Update.Fields["lifecycle"] != "cancelled" {
			continue
		}
		statusKey, evidenceKey := syncTargetKey(pending.Update, "status"), syncTargetKey(pending.Update, "evidence")
		status, hasStatus := state.Pending[statusKey]
		evidence, hasEvidence := state.Pending[evidenceKey]
		if !hasStatus || !hasEvidence || status.Line != pending.Line || evidence.Line != pending.Line || status.Update.Fields["status"] != "unknown" {
			continue
		}
		if state.Latest[lifecycleKey] != pending.Line || state.Latest[statusKey] != pending.Line || state.Latest[evidenceKey] != pending.Line {
			continue
		}
		priorEvidence, hadEvidence := state.Applied[evidenceKey]
		candidate, applied := applySyncField(data, evidence.Update, "evidence", evidence.Update.Fields["evidence"], priorEvidence, hadEvidence)
		if !applied {
			continue
		}
		priorStatus, hadStatus := state.Applied[statusKey]
		candidate, applied = applySyncField(candidate, status.Update, "status", "unknown", priorStatus, hadStatus)
		if !applied {
			continue
		}
		priorLifecycle, hadLifecycle := state.Applied[lifecycleKey]
		candidate, applied = applySyncField(candidate, pending.Update, "lifecycle", "cancelled", priorLifecycle, hadLifecycle)
		if !applied {
			continue
		}
		candidate, applied = removeCancelledPriority(candidate, taskID(pending.Update.ID))
		if !applied {
			continue
		}
		if _, err := parse(candidate); err != nil {
			continue
		}
		data = candidate
		state.Applied[lifecycleKey], state.Applied[statusKey], state.Applied[evidenceKey] = "cancelled", "unknown", evidence.Update.Fields["evidence"]
		delete(state.Pending, lifecycleKey)
		delete(state.Pending, statusKey)
		delete(state.Pending, evidenceKey)
	}
	return data
}

// removeCancelledPriority removes one cancelled task from the priority list.
//
// Example: Cancelling a top-priority task preserves the remaining priority IDs.
func removeCancelledPriority(
	data []byte,
	id taskID,
) ([]byte, bool) {
	var doc yaml.Node
	if yaml.Unmarshal(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), &doc) != nil || len(doc.Content) != 1 {
		return data, false
	}
	root := doc.Content[0]
	list := mapValue(root, "top_priorities")
	if list == nil || list.Kind != yaml.SequenceNode {
		return data, false
	}
	found := false
	for _, item := range list.Content {
		value, ok := priorityNodeID(item)
		if !ok {
			return data, false
		}
		if value == string(id) {
			found = true
		}
	}
	if !found {
		return data, true
	}
	remaining := make([]string, 0, len(list.Content))
	for _, item := range list.Content {
		value, ok := priorityNodeID(item)
		if !ok {
			return data, false
		}
		if value == string(id) {
			continue
		}
		raw, ok := priorityScalarSource(data, item)
		if !ok {
			return data, false
		}
		remaining = append(remaining, raw)
	}
	encoded := []byte("[" + strings.Join(remaining, ", ") + "]")
	if list.Style&yaml.FlowStyle != 0 {
		start, err := yamlAnchoredCollectionStart(data, list)
		if err != nil {
			return data, false
		}
		end, err := flowEnd(data, start, token.SequenceStartType)
		if err != nil || yamlRangeHasComment(data, start, end+1) {
			return data, false
		}
		return append(append(append([]byte(nil), data[:start]...), encoded...), data[end+1:]...), true
	}
	lines := strings.Split(string(data), "\n")
	startLine := list.Line - 1
	endOffset, err := structuredBlockSequenceEnd(data, root, "top_priorities")
	if err != nil {
		return data, false
	}
	if startLine < 0 || startLine >= len(lines) {
		return data, false
	}
	start := 0
	for _, line := range lines[:startLine] {
		start += len(line) + 1
	}
	end := endOffset
	if end < start || end > len(data) {
		return data, false
	}
	if yamlRangeHasComment(data, start, end) {
		return data, false
	}
	indent := len(lines[startLine]) - len(strings.TrimLeft(lines[startLine], " "))
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "top_priorities" && indent < root.Content[i].Column+1 {
			indent = root.Content[i].Column + 1
		}
	}
	ending := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		ending = "\r\n"
	}
	value := strings.Repeat(" ", indent) + string(encoded) + ending
	return append(append(append([]byte(nil), data[:start]...), []byte(value)...), data[end:]...), true
}

// priorityNodeID reads the task identity from a scalar or its YAML alias.
//
// Example: A priority alias resolves to the original task identifier.
func priorityNodeID(node *yaml.Node) (string, bool) {
	if node == nil || (node.Kind != yaml.ScalarNode && node.Kind != yaml.AliasNode) {
		return "", false
	}
	var value string
	if node.Decode(&value) != nil {
		return "", false
	}
	return value, true
}

// priorityScalarSource retains the original scalar spelling for a surviving task ID.
//
// Example: An unquoted numeric 1.10 is not rewritten as a quoted string.
func priorityScalarSource(
	data []byte,
	node *yaml.Node,
) (string, bool) {
	if node.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return "", false
	}
	start, err := yamlByteOffset(data, node.Line, node.Column)
	if err != nil || start >= len(data) {
		return "", false
	}
	if node.Kind == yaml.AliasNode {
		alias := "*" + node.Value
		if !strings.HasPrefix(string(data[start:]), alias) {
			return "", false
		}
		return alias, true
	}
	valueStart, ok := priorityScalarValueStart(data, node, start)
	if !ok || valueStart >= len(data) {
		return "", false
	}
	end := valueStart + len(node.Value)
	if data[valueStart] == '\'' || data[valueStart] == '"' {
		end, err = quotedNullYAMLValueEnd(data, valueStart)
		if err != nil {
			return "", false
		}
		return string(data[start:end]), true
	}
	if end > len(data) || string(data[valueStart:end]) != node.Value {
		return "", false
	}
	return string(data[start:end]), true
}

// priorityScalarValueStart skips a scalar's retained anchor and explicit tag.
//
// Example: Both !!str and &owner remain in the copied source spelling.
func priorityScalarValueStart(
	data []byte,
	node *yaml.Node,
	start int,
) (int, bool) {
	position := start
	for i := 0; i < 2; i++ {
		position = skipNullPropertyTrivia(data, position)
		if node.Anchor != "" && strings.HasPrefix(string(data[position:]), "&"+node.Anchor) {
			position += len(node.Anchor) + 1
			continue
		}
		if position < len(data) && data[position] == '!' {
			end, err := yamlTagEnd(data, position)
			if err != nil {
				return 0, false
			}
			position = end
			continue
		}
		break
	}
	return skipNullPropertyTrivia(data, position), true
}

// syncTargetKey keeps target identity distinct even when an ID contains slashes.
//
// Example: Distinguish a task field from the same field on another task.
func syncTargetKey(
	update syncUpdate,
	field string,
) string {
	workRef, environment := "", ""
	if update.Target == syncTargetLane {
		workRef, environment = update.WorkRef, update.RequiredEnvironment.String()
	}
	encoded, _ := json.Marshal([]string{update.Target.String(), update.ID, workRef, environment})
	return string(encoded) + "/" + field
}

// advanceSyncTimestamp preserves the original anchor of any existing timeline.
//
// Example: Advance a report without changing its timeline bins.
func advanceSyncTimestamp(
	data []byte,
	timestamp string,
) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	old := mapValue(root, "timestamp")
	if old == nil {
		return nil, fmt.Errorf("status report timestamp missing")
	}
	previous, err := parseUTC(old.Value)
	if err != nil {
		return nil, err
	}
	next, err := parseUTC(timestamp)
	if err != nil {
		return nil, err
	}
	if !next.After(previous) {
		return data, nil
	}
	timeline := mapValue(root, "timeline")
	timelineState := mapValue(root, "timeline_state")
	hasTimelineEvidence := (timeline != nil && len(timeline.Content) > 0) || (timelineState != nil && timelineState.Value == "verified_empty")
	if hasTimelineEvidence && mapValue(root, "timeline_as_of") == nil {
		data, err = appendReportField(data, "timeline_as_of", strconv.Quote(old.Value))
		if err != nil {
			return nil, err
		}
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	old = mapValue(doc.Content[0], "timestamp")
	edit, err := scalarReplacement(&flowDocument{data: data}, old, timestamp)
	if err != nil {
		return nil, err
	}
	return append(append(append([]byte(nil), data[:edit.Start]...), []byte(edit.Value)...), data[edit.End:]...), nil
}

// applyLaneCompletions publishes the required status and completion time together.
//
// Example: Close a lane only after canonical work completion.
func applyLaneCompletions(
	data []byte,
	ledger ledgerSource,
	forecasts forecastSource,
	state *syncState,
) []byte {
	keys := make([]string, 0, len(state.Pending))
	for key := range state.Pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, timeKey := range keys {
		if !strings.HasSuffix(timeKey, "/completed_at") {
			continue
		}
		timePending, exists := state.Pending[timeKey]
		if !exists || timePending.Update.Target != syncTargetLane {
			continue
		}
		u := timePending.Update
		field := u.RequiredEnvironment.String() + "_status"
		statusKey := strings.TrimSuffix(timeKey, "/completed_at") + "/" + field
		statusPending, exists := state.Pending[statusKey]
		if !exists || statusPending.Line != timePending.Line || statusPending.Update.Fields[field] != "CLOSED" {
			continue
		}
		if state.Latest[timeKey] != timePending.Line || state.Latest[statusKey] != statusPending.Line {
			continue
		}
		completed := u.Fields["completed_at"]
		if !laneSyncAllowed(data, ledger, forecasts, u, "completed_at", completed) {
			continue
		}
		priorTime, hadTime := state.Applied[timeKey]
		candidate, ok := applySyncField(data, u, "completed_at", completed, priorTime, hadTime)
		if !ok || !laneSyncAllowed(candidate, ledger, forecasts, statusPending.Update, field, "CLOSED") {
			continue
		}
		priorStatus, hadStatus := state.Applied[statusKey]
		candidate, ok = applySyncField(candidate, statusPending.Update, field, "CLOSED", priorStatus, hadStatus)
		if !ok {
			continue
		}
		if _, err := parse(candidate); err != nil {
			continue
		}
		data = candidate
		state.Applied[timeKey], state.Applied[statusKey] = completed, "CLOSED"
		delete(state.Pending, timeKey)
		delete(state.Pending, statusKey)
	}
	return data
}

// taskHasEvidence requires the exact accompanying acceptance before closing a task.
//
// Example: Verify the evidence supplied with a closure is present.
func taskHasEvidence(
	data []byte,
	id string,
	evidence string,
) bool {
	if strings.TrimSpace(evidence) == "" {
		return false
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	tasks := mapValue(doc.Content[0], "tasks")
	if tasks == nil || tasks.Kind != yaml.SequenceNode {
		return false
	}
	for _, node := range tasks.Content {
		key, value := mapValue(node, "id"), mapValue(node, "evidence")
		if key != nil && key.Value == id && value != nil && value.Value == evidence {
			return true
		}
	}
	return false
}

// scalarSyncFields returns the allowed scalar fields for a projection target.
//
// Example: A task update may set its owner.
func scalarSyncFields(target syncTarget) map[string]bool {
	switch target {
	case syncTargetTask:
		return map[string]bool{"status": true, "evidence": true, "owner": true, "context": true, "lifecycle": true}
	case syncTargetLane:
		return map[string]bool{"owner": true, "implementation_status": true, "test_status": true, "prod_status": true, "blocker": true, "next_action": true, "next_milestone": true, "context": true, "completed_at": true, "lifecycle": true, "root_task_status": true}
	case syncTargetReport:
		return map[string]bool{"state": true, "next_focus": true}
	default:
		return nil
	}
}

// validSyncUpdate rejects unsupported and incomplete operations without rejecting the log.
//
// Example: A task closure without acceptance evidence remains unapplied.
func validSyncUpdate(update syncUpdate) bool {
	allowed := scalarSyncFields(update.Target)
	if strings.TrimSpace(update.ID) == "" || len(update.Fields) == 0 || allowed == nil {
		return false
	}
	if update.Target == syncTargetLane && (strings.TrimSpace(update.WorkRef) == "" || update.RequiredEnvironment == syncEnvironmentUnknown) {
		return false
	}
	if update.Target == syncTargetReport && update.ID != "current" {
		return false
	}
	if update.Target == syncTargetTask && update.Fields["lifecycle"] != "" {
		if update.Fields["lifecycle"] != "cancelled" || update.Fields["status"] != "unknown" || strings.TrimSpace(update.Fields["evidence"]) == "" {
			return false
		}
	}
	if update.Target == syncTargetLane {
		if update.Fields["lifecycle"] != "" && update.Fields["lifecycle"] != "cancelled" {
			return false
		}
		if update.Fields["lifecycle"] == "cancelled" && strings.TrimSpace(update.Evidence) == "" {
			return false
		}
		statusField := update.RequiredEnvironment.String() + "_status"
		if update.RequiredEnvironment == syncEnvironmentProd {
			statusField = "prod_status"
		}
		if update.Fields[statusField] == "CLOSED" && (strings.TrimSpace(update.Evidence) == "" || strings.TrimSpace(update.Fields["completed_at"]) == "") {
			return false
		}
	}
	for field, value := range update.Fields {
		if !allowed[field] || strings.TrimSpace(value) == "" {
			return false
		}
		if field == "root_task_status" {
			if value != "FOLLOWUP" && value != "FOLLOWUP_PAUSED" {
				return false
			}
			continue
		}
		if strings.HasSuffix(field, "status") {
			switch value {
			case "NEW", "IN PROGRESS", "PAUSED", "BLOCKED", "CLOSED", "unknown":
			default:
				return false
			}
		}
	}
	if update.Target == syncTargetTask && update.Fields["status"] == "CLOSED" && strings.TrimSpace(update.Fields["evidence"]) == "" {
		return false
	}
	return true
}

// laneSyncAllowed keeps terminal readiness aligned with the canonical work record.
//
// Example: Reject closure while the canonical work is active.
func laneSyncAllowed(
	data []byte,
	ledger ledgerSource,
	forecasts forecastSource,
	update syncUpdate,
	field string,
	value string,
) bool {
	terminal := field == "lifecycle" || field == "completed_at" || (strings.HasSuffix(field, "_status") && value == "CLOSED" && field == update.RequiredEnvironment.String()+"_status")
	if !terminal {
		return true
	}
	// These fields are applied as an atomic pair by applyLaneCompletions.
	if field == "completed_at" && update.Fields["completed_at"] == "" {
		return false
	}
	if !validRef(update.WorkRef, "project-understanding.yaml") {
		return false
	}
	work, ok := ledger.Records[sourceID(update.WorkRef, "project-understanding.yaml")]
	if !ok || work.Type != "work" || strings.TrimSpace(update.Evidence) == "" {
		return false
	}
	if work.Data.ForecastRef != "" {
		ref := string(work.Data.ForecastRef)
		if !validRef(ref, "forecasts.yaml") || textField(forecasts.Records[sourceID(ref, "forecasts.yaml")], "status") != "inactive" {
			return false
		}
	}
	if field == "lifecycle" {
		return work.Data.State == workStateCancelled
	}
	if work.Data.State != workStateCompleted || work.Data.CompletedAt == "" {
		return false
	}
	if field == "completed_at" {
		return sameValue(value, work.Data.CompletedAt)
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	lanes := mapValue(doc.Content[0], "lanes")
	if lanes == nil {
		return false
	}
	for _, lane := range lanes.Content {
		id, ref, env := mapValue(lane, "task_id"), mapValue(lane, "work_ref"), mapValue(lane, "required_environment")
		if id == nil || ref == nil || env == nil || id.Value != update.ID || ref.Value != update.WorkRef || env.Value != update.RequiredEnvironment.String() {
			continue
		}
		completed := mapValue(lane, "completed_at")
		return completed != nil && sameValue(completed.Value, work.Data.CompletedAt) && mapValue(lane, "forecast") == nil
	}
	return false
}

// applySyncField changes one explicitly identified scalar while preserving other bytes.
//
// Example: Update the named task owner if its prior projection remains intact.
func applySyncField(
	data []byte,
	update syncUpdate,
	field string,
	value string,
	previous string,
	hasPrevious bool,
) ([]byte, bool) {
	target, id := update.Target, update.ID
	allowed := scalarSyncFields(target)
	if !allowed[field] || strings.TrimSpace(id) == "" || strings.TrimSpace(value) == "" {
		return data, false
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return data, false
	}
	root := doc.Content[0]
	if target == syncTargetReport {
		node := mapValue(root, field)
		if node == nil || (hasPrevious && (previous == syncAbsentValue || scalarSyncOwnership(node) != previous)) {
			return data, false
		}
		if node.Value == value {
			return data, true
		}
		edit, err := scalarReplacement(&flowDocument{data: data}, node, value)
		if err != nil {
			return data, false
		}
		return append(append(append([]byte(nil), data[:edit.Start]...), []byte(edit.Value)...), data[edit.End:]...), true
	}
	list := mapValue(root, target.String()+"s")
	if list == nil || list.Kind != yaml.SequenceNode {
		return data, false
	}
	var node *yaml.Node
	for _, n := range list.Content {
		key := "id"
		if target == syncTargetLane {
			key = "task_id"
		}
		if v := mapValue(n, key); v != nil && v.Value == id {
			if target == syncTargetLane {
				work, environment := mapValue(n, "work_ref"), mapValue(n, "required_environment")
				if work == nil || environment == nil || work.Value != update.WorkRef || environment.Value != update.RequiredEnvironment.String() {
					continue
				}
			}
			if node != nil {
				return data, false
			}
			node = n
		}
	}
	if node == nil {
		return data, false
	}
	existing := mapValue(node, field)
	// Owner-authored content remains untouched once it diverges from our last value.
	if hasPrevious && previous == syncAbsentValue && existing != nil {
		return data, false
	}
	if hasPrevious && previous != syncAbsentValue && (existing == nil || scalarSyncOwnership(existing) != previous) {
		return data, false
	}
	var fields forecastFieldEdits
	if err := fields.add(data, node, field, value, &flowDocument{data: data}); err != nil {
		return data, false
	}
	edits := fields.Edits
	if len(fields.Missing) > 0 {
		docflow := &flowDocument{data: data}
		if node.Style&yaml.FlowStyle != 0 {
			end, e := flowMappingEndCached(docflow, node)
			if e != nil {
				return data, false
			}
			edits = append(edits, scalarEdit{Start: reportByteOffset(end), End: reportByteOffset(end), Value: ", " + strings.Join(fields.Missing, ", ")})
		} else {
			end, indent, e := blockMappingEnd(data, node)
			if e != nil {
				return data, false
			}
			edits = append(edits, scalarEdit{Start: reportByteOffset(end), End: reportByteOffset(end), Value: strings.Repeat(" ", indent) + fields.Missing[0] + "\n"})
		}
	}
	sort.Slice(edits,
		// Apply later byte offsets before earlier offsets.
		//
		// Example: Insert a field without shifting a later replacement.
		func(
			i int,
			j int,
		) bool {
			return edits[i].Start > edits[j].Start
		})
	out := append([]byte(nil), data...)
	for _, e := range edits {
		out = append(append(append([]byte(nil), out[:e.Start]...), []byte(e.Value)...), out[e.End:]...)
	}
	return out, true
}

// putSyncState saves replay state in the same report snapshot as its effects.
//
// Example: Atomically publish an advanced cursor with updated task fields.
func putSyncState(
	data []byte,
	state string,
) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	node := mapValue(root, "status_sync")
	if node == nil {
		return appendReportField(data, "status_sync", state)
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return replaceNullYAMLValue(data, node, state)
	}
	if node.Kind == yaml.MappingNode && node.Style&yaml.FlowStyle != 0 {
		start, err := yamlAnchoredCollectionStart(data, node)
		if err != nil {
			return nil, err
		}
		end, err := flowEnd(data, start, token.MappingStartType)
		if err != nil {
			return nil, err
		}
		comments, err := yamlComments(data[start : end+1])
		if err != nil || len(comments) > 0 {
			return nil, fmt.Errorf("status sync metadata contains comments")
		}
		return append(append(append([]byte(nil), data[:start]...), []byte(state)...), data[end+1:]...), nil
	}
	edit, err := projectionReplacement(data, node, state, &flowDocument{data: data})
	if err != nil {
		return nil, err
	}
	return append(append(append([]byte(nil), data[:edit.Start]...), []byte(edit.Value)...), data[edit.End:]...), nil
}

// yamlAnchoredCollectionStart locates the opening delimiter after any preserved anchor.
//
// Example: An anchored flow mapping keeps its anchor when metadata advances.
func yamlAnchoredCollectionStart(
	data []byte,
	node *yaml.Node,
) (int, error) {
	start, err := yamlByteOffset(data, node.Line, node.Column)
	if err != nil || node.Anchor == "" {
		return start, err
	}
	prefix := "&" + node.Anchor
	if !strings.HasPrefix(string(data[start:]), prefix) {
		return 0, fmt.Errorf("unsupported anchored sync metadata")
	}
	start += len(prefix)
	return skipNullPropertyTrivia(data, start), nil
}

// replaceNullYAMLValue replaces an explicitly or implicitly null YAML value.
//
// Example: Replace a null value without consuming its adjacent owner comment.
func replaceNullYAMLValue(
	data []byte,
	node *yaml.Node,
	state string,
) ([]byte, error) {
	start, err := yamlByteOffset(data, node.Line, node.Column)
	if err != nil {
		return nil, err
	}
	end := start
	if node.Style&yaml.TaggedStyle != 0 || node.Anchor != "" {
		return replaceDecoratedNullYAMLValue(data, node, state, start)
	}
	if node.Value != "" {
		start, end, err = (&flowDocument{data: data}).scalarSpan(node)
		if err != nil {
			return nil, err
		}
	}
	if node.Value == "" && start > 0 && data[start-1] == ':' {
		state = " " + state
	}
	return append(append(append([]byte(nil), data[:start]...), []byte(state)...), data[end:]...), nil
}

// replaceDecoratedNullYAMLValue retains an anchor while replacing null's type tag.
//
// Example: A tagged null becomes a mapping without consuming the following comment.
func replaceDecoratedNullYAMLValue(
	data []byte,
	node *yaml.Node,
	state string,
	start int,
) ([]byte, error) {
	propertyEnd, tagStart, tagEnd, err := nullYAMLPropertyBounds(data, node, start)
	if err != nil {
		return nil, err
	}
	valueStart, valueEnd := propertyEnd, propertyEnd
	if node.Value == "" && node.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle) == 0 {
		state = " " + state
	} else {
		valueStart = skipNullPropertyTrivia(data, propertyEnd)
		valueEnd, err = nullYAMLValueEnd(data, node, valueStart)
		if err != nil {
			return nil, err
		}
	}
	result := append(append(append([]byte(nil), data[:valueStart]...), []byte(state)...), data[valueEnd:]...)
	if tagStart >= 0 {
		result = append(result[:tagStart], result[tagEnd:]...)
	}
	return result, nil
}

// skipNullPropertyTrivia locates the next YAML token without consuming owner comments.
//
// Example: An anchor followed by a comment and newline still precedes its value.
func skipNullPropertyTrivia(
	data []byte,
	start int,
) int {
	for start < len(data) {
		switch data[start] {
		case ' ', '\t', '\r', '\n':
			start++
		case '#':
			for start < len(data) && data[start] != '\n' {
				start++
			}
		default:
			return start
		}
	}
	return start
}

// nullYAMLPropertyBounds identifies the tag and anchor without consuming trivia.
//
// Example: A tag and anchor may occur on separate lines before null.
func nullYAMLPropertyBounds(
	data []byte,
	node *yaml.Node,
	start int,
) (int, int, int, error) {
	end, tagStart, tagEnd := start, -1, -1
	for {
		pos := skipNullPropertyTrivia(data, end)
		property := nullYAMLPropertyAt(data[pos:], node.Anchor)
		if property == "" {
			break
		}
		end = pos + len(property)
		if property[0] == '!' {
			tagStart, tagEnd = pos, end
		}
	}
	if end == start {
		return 0, 0, 0, fmt.Errorf("unsupported null metadata properties")
	}
	return end, tagStart, tagEnd, nil
}

// nullYAMLPropertyAt recognizes the original null tag or its retained anchor.
//
// Example: A long-form YAML null tag is removed before storing a mapping.
func nullYAMLPropertyAt(
	data []byte,
	anchor string,
) string {
	for _, candidate := range []string{"!!null", "!<tag:yaml.org,2002:null>", "&" + anchor} {
		if candidate != "&" && strings.HasPrefix(string(data), candidate) {
			return candidate
		}
	}
	return ""
}

// nullYAMLValueEnd finds a supported explicit null scalar's final byte.
//
// Example: Tagged quoted null is replaced without consuming its comment.
func nullYAMLValueEnd(
	data []byte,
	node *yaml.Node,
	start int,
) (int, error) {
	if start < len(data) && (data[start] == '\'' || data[start] == '"') {
		return quotedNullYAMLValueEnd(data, start)
	}
	if node.Value != "" && strings.HasPrefix(string(data[start:]), node.Value) {
		return start + len(node.Value), nil
	}
	return 0, fmt.Errorf("unsupported null metadata layout")
}

// quotedNullYAMLValueEnd finds the actual source boundary of a quoted null.
//
// Example: An escaped double-quoted null keeps its trailing owner comment.
func quotedNullYAMLValueEnd(
	data []byte,
	start int,
) (int, error) {
	quote := data[start]
	for i := start + 1; i < len(data); i++ {
		if quote == '"' && data[i] == '\\' {
			i++
			continue
		}
		if data[i] != quote {
			continue
		}
		if quote == '\'' && i+1 < len(data) && data[i+1] == quote {
			i++
			continue
		}
		return i + 1, nil
	}
	return 0, fmt.Errorf("unterminated quoted null metadata")
}
