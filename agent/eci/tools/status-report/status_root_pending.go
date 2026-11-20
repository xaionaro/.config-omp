package main

import (
	"encoding/json"
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
// Example: An awaiting completion remains available for a later source refresh.
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
