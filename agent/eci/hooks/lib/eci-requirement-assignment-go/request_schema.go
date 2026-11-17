package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// validateUniqueJSON rejects duplicate decoded names throughout a JSON value.
//
// Example: two spellings of the same escaped key cannot select different values.
func validateUniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := visitJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected one JSON value: %v", err)
	}
	return nil
}

// visitJSONValue consumes one JSON value and checks every object for duplicate names.
//
// Example: duplicate keys inside an operation value are rejected before projection.
func visitJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[name] {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = true
			if err := visitJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := visitJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	_, err = decoder.Token()
	return err
}

// exactObjectFields checks a fixed-schema object without case-folding its field names.
//
// Example: Target is not accepted as an alias for target.
func exactObjectFields(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("expected JSON object")
	}
	for key := range fields {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown field %q", key)
		}
	}
	return fields, nil
}

// validateRequestSchema checks only fixed fields; operation values remain open domain data.
//
// Example: actor field names are exact while a record's domain fields remain unrestricted.
func validateRequestSchema(data []byte) error {
	if err := validateUniqueJSON(data); err != nil {
		return err
	}
	fields, err := exactObjectFields(data, "base", "ops", "requirements", "outcome", "target", "instructions", "actor", "reason")
	if err != nil {
		return err
	}
	if base, exists := fields["base"]; exists {
		if _, err := exactObjectFields(base, "database_id", "generation"); err != nil {
			return fmt.Errorf("base: %w", err)
		}
	}
	if actor, exists := fields["actor"]; exists {
		if _, err := exactObjectFields(actor, "id", "role", "turn"); err != nil {
			return fmt.Errorf("actor: %w", err)
		}
	}
	return nil
}
