package main

import (
	"encoding/json"
	"fmt"
)

// ledgerOperationKind identifies a supported row mutation.
//
// Example: ledgerPut replaces a whole row value.
type ledgerOperationKind int

const (
	// ledgerOperationUnknown denotes a missing or unsupported operation.
	//
	// Example: an unknown operation is rejected at its batch index.
	ledgerOperationUnknown ledgerOperationKind = iota
	// ledgerPut inserts or replaces a row.
	//
	// Example: put creates a section.
	ledgerPut
	// ledgerPatch merges a patch into an existing row.
	//
	// Example: patch changes one field without replacing the other fields.
	ledgerPatch
	// ledgerDelete removes an existing row.
	//
	// Example: delete removes a retired forecast.
	ledgerDelete
)

// UnmarshalJSON decodes the operation wire name.
//
// Example: "patch" selects ledgerPatch.
func (kind *ledgerOperationKind) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("decode operation: %w", err)
	}
	switch name {
	case "put":
		*kind = ledgerPut
	case "patch":
		*kind = ledgerPatch
	case "delete":
		*kind = ledgerDelete
	default:
		*kind = ledgerOperationUnknown
	}
	return nil
}

// MarshalJSON encodes the operation wire name.
//
// Example: ledgerPut encodes as "put".
func (kind ledgerOperationKind) MarshalJSON() ([]byte, error) {
	var name string
	switch kind {
	case ledgerPut:
		name = "put"
	case ledgerPatch:
		name = "patch"
	case ledgerDelete:
		name = "delete"
	default:
		return nil, fmt.Errorf("unknown operation %d", kind)
	}
	return json.Marshal(name)
}

// ledgerTable identifies a canonical record collection.
//
// Example: ledgerRecords selects the project record collection.
type ledgerTable int

const (
	// ledgerTableUnknown denotes a missing or unsupported collection.
	//
	// Example: an unknown collection fails at its batch index.
	ledgerTableUnknown ledgerTable = iota
	// ledgerSections stores section titles.
	//
	// Example: a work record refers to a section.
	ledgerSections
	// ledgerRecords stores typed project records.
	//
	// Example: an outcome is a project record.
	ledgerRecords
	// ledgerForecasts stores current forecasts.
	//
	// Example: a lane forecast has a stable ID.
	ledgerForecasts
)

// UnmarshalJSON decodes the collection wire name.
//
// Example: "records" selects ledgerRecords.
func (table *ledgerTable) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("decode table: %w", err)
	}
	switch name {
	case "sections":
		*table = ledgerSections
	case "records":
		*table = ledgerRecords
	case "forecasts":
		*table = ledgerForecasts
	default:
		*table = ledgerTableUnknown
	}
	return nil
}

// MarshalJSON encodes the collection wire name.
//
// Example: ledgerForecasts encodes as "forecasts".
func (table ledgerTable) MarshalJSON() ([]byte, error) {
	var name string
	switch table {
	case ledgerSections:
		name = "sections"
	case ledgerRecords:
		name = "records"
	case ledgerForecasts:
		name = "forecasts"
	default:
		return nil, fmt.Errorf("unknown table %d", table)
	}
	return json.Marshal(name)
}
