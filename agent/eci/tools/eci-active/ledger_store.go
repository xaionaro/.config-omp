package main

import (
	"encoding/json"
	"fmt"
)

// ledgerOperation is one ordered change to a session store.
//
// Example: a patch checks a record's type before updating its value.
type ledgerOperation struct {
	Op     ledgerOperationKind `json:"op"`
	Table  ledgerTable         `json:"table"`
	ID     string              `json:"id"`
	Expect map[string]*string  `json:"expect,omitempty"`
	Value  json.RawMessage     `json:"value"`
}

// UnmarshalJSON reads operation fields by their exact wire names.
//
// Example: an unrelated `Op` extension cannot override the `op` field.
func (op *ledgerOperation) UnmarshalJSON(data []byte) error {
	if err := validateUniqueJSON(data); err != nil {
		return fmt.Errorf("invalid operation JSON: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode operation: %w", err)
	}
	if fields == nil {
		return fmt.Errorf("operation must be an object")
	}
	*op = ledgerOperation{}
	if value, ok := fields["op"]; ok {
		if err := json.Unmarshal(value, &op.Op); err != nil {
			return fmt.Errorf("decode operation op: %w", err)
		}
	}
	if value, ok := fields["table"]; ok {
		if err := json.Unmarshal(value, &op.Table); err != nil {
			return fmt.Errorf("decode operation table: %w", err)
		}
	}
	if value, ok := fields["id"]; ok {
		if err := json.Unmarshal(value, &op.ID); err != nil {
			return fmt.Errorf("decode operation id: %w", err)
		}
	}
	if value, ok := fields["expect"]; ok {
		if err := json.Unmarshal(value, &op.Expect); err != nil {
			return fmt.Errorf("decode operation expect: %w", err)
		}
	}
	op.Value = fields["value"]
	return nil
}

// ledgerRecord is the typed envelope checked before committing a batch.
//
// Example: links refer to other records by their stable IDs.
type ledgerRecord struct {
	Type     string              `json:"type"`
	Data     json.RawMessage     `json:"data"`
	Evidence []json.RawMessage   `json:"evidence"`
	Links    map[string][]string `json:"links"`
}

// UnmarshalJSON reads stored record envelope fields by exact key.
//
// Example: a `Type` extension cannot change the meaning of `type`.
func (r *ledgerRecord) UnmarshalJSON(data []byte) error {
	if err := validateUniqueJSON(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("record must be an object")
	}
	*r = ledgerRecord{Data: fields["data"]}
	if value, ok := fields["type"]; ok {
		if err := json.Unmarshal(value, &r.Type); err != nil {
			return fmt.Errorf("decode type: %w", err)
		}
	}
	if value, ok := fields["evidence"]; ok {
		if err := json.Unmarshal(value, &r.Evidence); err != nil {
			return fmt.Errorf("decode evidence: %w", err)
		}
	}
	if value, ok := fields["links"]; ok {
		if err := json.Unmarshal(value, &r.Links); err != nil {
			return fmt.Errorf("decode links: %w", err)
		}
	}
	return nil
}
