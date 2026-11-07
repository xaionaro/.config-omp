package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// rootLifecycleLocator identifies one exact addition or update in the source log.
//
// Example: An addition at ordinal zero on line two identifies the first addition.
type rootLifecycleLocator struct {
	Line    int        `json:"line" yaml:"line"`
	Ordinal int        `json:"ordinal" yaml:"ordinal"`
	Target  syncTarget `json:"target" yaml:"target"`
	Update  bool       `json:"update,omitempty" yaml:"update,omitempty"`
}

// syncPendingRootAddition retains an exact root addition and its source location.
//
// Example: A blocked completion remains available for a later source refresh.
type syncPendingRootAddition struct {
	Locator  rootLifecycleLocator `json:"locator" yaml:"locator"`
	Addition syncAddition         `json:"addition" yaml:"addition"`
}

// UnmarshalYAML restores JSON record values from persisted YAML replay metadata.
//
// Example: A pending root completion survives a second status refresh.
func (p *syncPendingRootAddition) UnmarshalYAML(node *yaml.Node) error {
	var wire struct {
		Locator  rootLifecycleLocator `yaml:"locator"`
		Addition struct {
			Target         syncTarget `yaml:"target"`
			Record         yaml.Node  `yaml:"record"`
			Evidence       string     `yaml:"evidence"`
			RetireForecast bool       `yaml:"retire_forecast"`
			ReopenWorkRef  string     `yaml:"reopen_work_ref"`
			ExpectedPrior  yaml.Node  `yaml:"expected_prior"`
			RetirePriority string     `yaml:"retire_priority"`
		} `yaml:"addition"`
	}
	if err := node.Decode(&wire); err != nil {
		return err
	}
	record, err := rootPendingJSON(&wire.Addition.Record)
	if err != nil {
		return err
	}
	prior, err := rootPendingJSON(&wire.Addition.ExpectedPrior)
	if err != nil {
		return err
	}
	p.Locator = wire.Locator
	p.Addition = syncAddition{Target: wire.Addition.Target, Record: record, Evidence: wire.Addition.Evidence, RetireForecast: wire.Addition.RetireForecast, ReopenWorkRef: wire.Addition.ReopenWorkRef, ExpectedPrior: prior, RetirePriority: wire.Addition.RetirePriority}
	return nil
}

// rootPendingJSON converts one persisted YAML value to its JSON representation.
//
// Example: A stored mapping becomes a raw JSON object.
func rootPendingJSON(node *yaml.Node) (json.RawMessage, error) {
	if node.Kind == 0 {
		return nil, nil
	}
	var value any
	if err := node.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// queueRootAddition retains a valid root occurrence without overwriting another.
//
// Example: Two root transitions on one line retain separate ordinals.
func queueRootAddition(
	pending map[string]syncPendingRootAddition,
	line int,
	ordinal int,
	addition syncAddition,
) bool {
	if line < 1 || ordinal < 0 || pending == nil {
		return false
	}
	var record map[string]json.RawMessage
	if json.Unmarshal(addition.Record, &record) != nil {
		return false
	}
	var id string
	switch addition.Target {
	case syncTargetRootCompletion:
		if addition.ReopenWorkRef != "" || addition.RetirePriority != "" || json.Unmarshal(record["work_ref"], &id) != nil {
			return false
		}
	case syncTargetRootForecast:
		if addition.RetireForecast || addition.RetirePriority != "" || json.Unmarshal(record["forecast_ref"], &id) != nil {
			return false
		}
	default:
		return false
	}
	if strings.TrimSpace(id) == "" {
		return false
	}
	key := syncGroupKey(line, ordinal)
	if _, exists := pending[key]; exists {
		return false
	}
	var ok bool
	addition.Record, ok = canonicalPendingRootJSON(addition.Record)
	if !ok {
		return false
	}
	addition.ExpectedPrior, ok = canonicalPendingRootJSON(addition.ExpectedPrior)
	if !ok {
		return false
	}
	pending[key] = syncPendingRootAddition{Locator: rootLifecycleLocator{Line: line, Ordinal: ordinal, Target: addition.Target}, Addition: addition}
	return true
}

// canonicalPendingRootJSON matches the encoding used when persisted YAML is read.
//
// Example: A queued record keeps stable object ordering across retries.
func canonicalPendingRootJSON(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) != 1 {
		return nil, false
	}
	encoded, err := rootPendingJSON(doc.Content[0])
	return encoded, err == nil
}

// migratePendingRootAdditions moves legacy entries only when their exact log occurrence is unique.
//
// Example: An ambiguous duplicate stays in the legacy map for manual reconciliation.
func migratePendingRootAdditions(
	legacy map[string]syncPendingAddition,
	pending map[string]syncPendingRootAddition,
	fullLog []byte,
	applied map[string]string,
	report []byte,
	ledger ledgerSource,
) bool {
	lines := bytes.Split(fullLog, []byte("\n"))
	complete := true
	for key, old := range legacy {
		if old.Target != syncTargetRootCompletion && old.Target != syncTargetRootForecast {
			continue
		}
		if old.Line < 1 || old.Line > len(lines) {
			complete = false
			continue
		}
		var event syncEvent
		if json.Unmarshal(lines[old.Line-1], &event) != nil || event.Details.StatusUpdate == nil {
			complete = false
			continue
		}
		match, count := -1, 0
		for ordinal, addition := range event.Details.StatusUpdate.Additions {
			if legacyRootAdditionMatches(old, addition) {
				match, count = ordinal, count+1
			}
		}
		if count != 1 {
			complete = false
			continue
		}
		if _, exists := pending[syncGroupKey(old.Line, match)]; exists {
			complete = false
			continue
		}
		if !queueRootAddition(map[string]syncPendingRootAddition{}, old.Line, match, event.Details.StatusUpdate.Additions[match]) {
			complete = false
			continue
		}
		if previous, exists := applied[key]; exists && rootChainJSONEqual([]byte(previous), []byte(old.Record)) {
			lineageValid := old.PriorLine == 0 && old.PriorRecord == ""
			if !lineageValid {
				lineageValid = legacyAppliedRootCorrectionLineage(lines, old, event, match, ledger)
			}
			if lineageValid && legacyRootRecordMatchesReport(report, old) {
				delete(legacy, key)
				continue
			}
			complete = false
			continue
		}
		if old.PriorLine != 0 || old.PriorRecord != "" {
			if !queueLegacyRootPrior(pending, lines, old, event) {
				complete = false
				continue
			}
		}
		if !queueRootAddition(pending, old.Line, match, event.Details.StatusUpdate.Additions[match]) {
			complete = false
			continue
		}
		delete(legacy, key)
	}
	return complete
}

// legacyRootRecordMatchesReport checks an applied legacy record against its current owner-visible value.
//
// Example: An intact completion permits removal of redundant pending metadata.
func legacyRootRecordMatchesReport(
	report []byte,
	old syncPendingAddition,
) bool {
	var doc yaml.Node
	if yaml.Unmarshal(report, &doc) != nil || len(doc.Content) != 1 {
		return false
	}
	section, identity := "root_completions", "work_ref"
	if old.Target == syncTargetRootForecast {
		section, identity = "root_forecasts", "forecast_ref"
	}
	var record map[string]json.RawMessage
	var id string
	if json.Unmarshal([]byte(old.Record), &record) != nil || json.Unmarshal(record[identity], &id) != nil || strings.TrimSpace(id) == "" || (old.ID != "" && old.ID != id) {
		return false
	}
	list := mapValue(doc.Content[0], section)
	if list == nil || list.Kind != yaml.SequenceNode {
		return false
	}
	matches := 0
	for _, item := range list.Content {
		value := mapValue(item, identity)
		if value == nil || value.Value != id {
			continue
		}
		var actual any
		if item.Decode(&actual) != nil {
			return false
		}
		encoded, err := json.Marshal(actual)
		if err != nil || !rootChainJSONEqual(encoded, []byte(old.Record)) {
			return false
		}
		matches++
	}
	return matches == 1
}

// queueLegacyRootPrior retains a uniquely evidenced predecessor of a correction.
//
// Example: Restore the original completion occurrence before its correction.
func queueLegacyRootPrior(
	pending map[string]syncPendingRootAddition,
	lines [][]byte,
	old syncPendingAddition,
	correction syncEvent,
) bool {
	var corrects int
	if old.PriorLine < 1 || old.PriorLine >= old.Line || old.PriorLine > len(lines) || correction.Event != "correction" || json.Unmarshal(correction.Details.Corrects, &corrects) != nil || corrects != old.PriorLine || !sameRootPendingJSON([]byte(old.PriorRecord), []byte(old.ExpectedPrior)) {
		return false
	}
	var prior syncEvent
	if json.Unmarshal(lines[old.PriorLine-1], &prior) != nil || prior.Details.StatusUpdate == nil {
		return false
	}
	match, count := -1, 0
	for ordinal, addition := range prior.Details.StatusUpdate.Additions {
		if addition.Target == old.Target && sameRootPendingJSON(addition.Record, []byte(old.PriorRecord)) {
			match, count = ordinal, count+1
		}
	}
	if count != 1 {
		return false
	}
	return queueRootAddition(pending, old.PriorLine, match, prior.Details.StatusUpdate.Additions[match])
}

// legacyRootAdditionMatches compares all represented legacy request fields.
//
// Example: Changed evidence prevents an unsafe migration.
func legacyRootAdditionMatches(
	old syncPendingAddition,
	addition syncAddition,
) bool {
	if old.Target != addition.Target || old.Evidence != addition.Evidence || old.RetireForecast != addition.RetireForecast || old.ReopenWorkRef != addition.ReopenWorkRef || old.RetirePriority != addition.RetirePriority {
		return false
	}
	var record map[string]json.RawMessage
	var id string
	if json.Unmarshal(addition.Record, &record) != nil {
		return false
	}
	identity := "work_ref"
	if addition.Target == syncTargetRootForecast {
		identity = "forecast_ref"
	}
	if json.Unmarshal(record[identity], &id) != nil || (old.ID != "" && old.ID != id) {
		return false
	}
	if !sameRootPendingJSON([]byte(old.Record), addition.Record) {
		return false
	}
	if old.ExpectedPrior == "" && len(addition.ExpectedPrior) == 0 {
		return true
	}
	return sameRootPendingJSON([]byte(old.ExpectedPrior), addition.ExpectedPrior)
}

// sameRootPendingJSON compares JSON values without relying on object key order.
//
// Example: A recorded object matches its whitespace-formatted log form.
func sameRootPendingJSON(
	left []byte,
	right []byte,
) bool {
	var a, b any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	leftDecoder.UseNumber()
	rightDecoder.UseNumber()
	return leftDecoder.Decode(&a) == nil && rightDecoder.Decode(&b) == nil && reflect.DeepEqual(a, b)
}
