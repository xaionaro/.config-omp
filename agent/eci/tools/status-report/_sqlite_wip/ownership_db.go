package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// syncOwnershipKind distinguishes a missing field, null, and an owned value.
//
// Example: An absent field is not the string containing the old absence marker.
type syncOwnershipKind int

const (
	// syncOwnershipAbsent identifies a removed field.
	//
	// Example: A projected clear owns the absence.
	syncOwnershipAbsent syncOwnershipKind = iota + 1
	// syncOwnershipNull identifies an explicitly null field.
	//
	// Example: A null owner is distinct from an empty owner.
	syncOwnershipNull
	// syncOwnershipValue identifies an owned string representation.
	//
	// Example: A structured field owns its canonical JSON representation.
	syncOwnershipValue
	// syncOwnershipLegacyNull retains an ambiguous historical scalar baseline.
	//
	// Example: An old untagged null cannot establish a scalar owner's type.
	syncOwnershipLegacyNull
	// syncOwnershipHeld preserves owner control when historical provenance is unavailable.
	//
	// Example: A retired occurrence does not authorize future overwrite.
	syncOwnershipHeld
)

// syncOwnership preserves field presence separately from its value.
//
// Example: A literal NUL-prefixed string remains a value through replay.
type syncOwnership struct {
	Kind syncOwnershipKind
	Value string
}

var (
	// syncAbsentValue represents an owned absent field.
	//
	// Example: A cleared awaits value remains absent.
	syncAbsentValue = syncOwnership{Kind: syncOwnershipAbsent}
	// syncNullValue represents an owned null field.
	//
	// Example: A null owner is not an empty string.
	syncNullValue = syncOwnership{Kind: syncOwnershipNull}
)

// legacySyncOwnership interprets historical sentinel strings at import only.
//
// Example: Historical escaped values retain their literal leading NUL.
func legacySyncOwnership(value string) syncOwnership {
	switch value {
	case "null":
		return syncOwnership{Kind: syncOwnershipLegacyNull}
	case "\x00absent":
		return syncAbsentValue
	case "\x00null":
		return syncNullValue
	case "\x00literal-null":
		return scalarSyncStoredValue("null")
	default:
		return scalarSyncStoredValue(strings.TrimPrefix(value, "\x00value:"))
	}
}

// ownershipWire is the stable tagged storage representation.
//
// Example: A present empty string retains an explicit value member.
type ownershipWire struct {
	Kind string `json:"kind" yaml:"kind"`
	Value *string `json:"value,omitempty" yaml:"value,omitempty"`
}

// wire returns the storage representation of ownership.
//
// Example: An absent field has no value member.
func (o syncOwnership) wire() (ownershipWire, error) {
	switch o.Kind {
	case syncOwnershipAbsent:
		return ownershipWire{Kind: "absent"}, nil
	case syncOwnershipNull:
		return ownershipWire{Kind: "null"}, nil
	case syncOwnershipValue:
		return ownershipWire{Kind: "value", Value: &o.Value}, nil
	case syncOwnershipLegacyNull:
		return ownershipWire{Kind: "legacy-null"}, nil
	case syncOwnershipHeld:
		return ownershipWire{Kind: "held"}, nil
	default:
		return ownershipWire{}, fmt.Errorf("invalid ownership kind %d", o.Kind)
	}
}

// ownershipFromWire validates a tagged ownership entry.
//
// Example: Reject an absent entry with a value.
func ownershipFromWire(w ownershipWire) (syncOwnership, error) {
	switch w.Kind {
	case "absent":
		if w.Value == nil { return syncAbsentValue, nil }
	case "null":
		if w.Value == nil { return syncNullValue, nil }
	case "value":
		if w.Value != nil { return scalarSyncStoredValue(*w.Value), nil }
	case "legacy-null":
		if w.Value == nil { return syncOwnership{Kind: syncOwnershipLegacyNull}, nil }
	case "held":
		if w.Value == nil { return syncOwnership{Kind: syncOwnershipHeld}, nil }
	}
	return syncOwnership{}, fmt.Errorf("invalid ownership entry %q", w.Kind)
}

// MarshalJSON writes collision-free tagged ownership.
//
// Example: A literal sentinel is emitted as a value.
func (o syncOwnership) MarshalJSON() ([]byte, error) {
	w, err := o.wire()
	if err != nil { return nil, err }
	return json.Marshal(w)
}

// UnmarshalJSON accepts tagged ownership and legacy scalar entries.
//
// Example: An old absence marker migrates to tagged absence.
func (o *syncOwnership) UnmarshalJSON(data []byte) error {
	var legacy string
	if len(data) > 0 && data[0] == '"' && json.Unmarshal(data, &legacy) == nil {
		*o = legacySyncOwnership(legacy)
		return nil
	}
	var w ownershipWire
	if err := json.Unmarshal(data, &w); err != nil { return err }
	value, err := ownershipFromWire(w)
	if err != nil { return err }
	*o = value
	return nil
}

// MarshalYAML writes the same tagged representation used in JSON.
//
// Example: SQLite report snapshots preserve null ownership.
func (o syncOwnership) MarshalYAML() (any, error) {
	return o.wire()
}

// UnmarshalYAML accepts both historical scalar and tagged ownership.
//
// Example: A legacy report can be replayed and rewritten without losing ownership.
func (o *syncOwnership) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		*o = legacySyncOwnership(node.Value)
		return nil
	}
	var w ownershipWire
	if err := node.Decode(&w); err != nil { return err }
	value, err := ownershipFromWire(w)
	if err != nil { return err }
	*o = value
	return nil
}
