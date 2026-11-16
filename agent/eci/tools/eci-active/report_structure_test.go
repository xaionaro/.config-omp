package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReportStructurePreservesNumericRange checks structural-only JSON handling.
//
// Example: a large exponent is valid structure without floating-point conversion.
func TestReportStructurePreservesNumericRange(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateReportStructure([]byte(`{}`)))
	require.Error(t, validateReportStructure([]byte(`[]`)))
	require.NoError(t, validateReportStructure([]byte(`{"value":1e99999,"nested":[{"x":9007199254740993}]}`)))
	require.Error(t, validateReportStructure([]byte(`{"nested":[{"a":1,"\u0061":2}]}`)))
	require.Error(t, validateReportStructure([]byte(`{"a":1} {"b":2}`)))
}

// TestReportStructureJSONWhitespace enforces JSON's four whitespace bytes.
//
// Example: leading nonbreaking space is not valid JSON whitespace.
func TestReportStructureJSONWhitespace(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"", " ", "\t", "\n", "\r", " \t\n\r"} {
		require.NoError(t, validateReportStructure([]byte(prefix+"{}")))
	}
	for _, prefix := range []string{"\v", "\f", "\u00a0", "\u2003"} {
		require.Error(t, validateReportStructure([]byte(prefix+"{}")))
	}
}

// TestReportStructureNestedArrays checks independent object scopes.
//
// Example: decoded duplicate keys inside arrays are rejected at any depth.
func TestReportStructureNestedArrays(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateReportStructure([]byte(`{"items":[[{"x":1},{"x":2}]]}`)))
	require.Error(t, validateReportStructure([]byte(`{"items":[[{"x":1,"\u0078":2}]]}`)))
}
