package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// validateReportStructure checks one JSON object and unique decoded member names.
//
// Example: escaped spellings of the same key are rejected as duplicates.
func validateReportStructure(data []byte) error {
	// Only JSON whitespace may precede the object; decode the original bytes.
	first := 0
	for first < len(data) && (data[first] == ' ' || data[first] == '\t' || data[first] == '\r' || data[first] == '\n') {
		first++
	}
	if first == len(data) || data[first] != '{' {
		return fmt.Errorf("report must be a JSON object")
	}
	return validateUniqueJSON(data)
}

// validateUniqueJSON accepts one value with distinct decoded names in each object.
//
// Example: numeric tokens remain unchanged while duplicate keys are rejected.
func validateUniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateUniqueValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("read JSON trailing content: %w", err)
		}
		return fmt.Errorf("JSON has trailing content")
	}
	return nil
}

// validateUniqueObject consumes an opened object and rejects duplicate keys.
//
// Example: nested objects have their own independent key sets.
func validateUniqueObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("read JSON key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("invalid JSON object key")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON key %q", key)
		}
		seen[key] = struct{}{}
		if err := validateUniqueValue(decoder); err != nil {
			return err
		}
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("finish JSON object: %w", err)
	}
	if token != json.Delim('}') {
		return fmt.Errorf("invalid JSON object ending")
	}
	return nil
}

// validateUniqueValue consumes one JSON value while preserving numeric lexemes.
//
// Example: arrays may contain nested objects or arbitrary scalars.
func validateUniqueValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON value: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return validateUniqueObject(decoder)
	case '[':
		return validateUniqueArray(decoder)
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
}

// validateUniqueArray consumes an opened array and validates its values.
//
// Example: objects nested inside arrays still reject duplicate names.
func validateUniqueArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := validateUniqueValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("finish JSON array: %w", err)
	}
	if end != json.Delim(']') {
		return fmt.Errorf("invalid JSON array ending")
	}
	return nil
}
