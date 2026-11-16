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
	for _, space := range []string{"", " ", "\t", "\n", "\r", " \t\n\r"} {
		require.NoError(t, validateReportStructure([]byte(space+"{}")))
		require.NoError(t, validateReportStructure([]byte("{}"+space)))
		require.NoError(t, validateReportStructure([]byte(space+"{}"+space)))
	}
	t.Run("leading",
		// rejectLeadingNonJSONWhitespace checks each invalid prefix.
		//
		// Example: a nonbreaking space cannot precede a JSON object.
		func(t *testing.T) {
			for _, space := range []string{"\v", "\f", "\u00a0", "\u2003"} {
				if err := validateReportStructure([]byte(space + "{}")); err == nil {
					t.Errorf("leading %q accepted", space)
				}
			}
		})
	t.Run("trailing",
		// rejectTrailingNonJSONWhitespace checks each invalid suffix.
		//
		// Example: a nonbreaking space cannot follow a JSON object.
		func(t *testing.T) {
			for _, space := range []string{"\v", "\f", "\u00a0", "\u2003"} {
				if err := validateReportStructure([]byte("{}" + space)); err == nil {
					t.Errorf("trailing %q accepted", space)
				}
			}
		})
}

// TestReportStructureNestedArrays checks independent object scopes.
//
// Example: decoded duplicate keys inside arrays are rejected at any depth.
func TestReportStructureNestedArrays(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateReportStructure([]byte(`{"items":[[{"x":1},{"x":2}]]}`)))
	require.Error(t, validateReportStructure([]byte(`{"items":[[{"x":1,"\u0078":2}]]}`)))
}

// TestReportStructureRejectsIncompleteValues checks complete JSON structure.
//
// Example: a report containing a truncated array is not accepted.
func TestReportStructureRejectsIncompleteValues(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", " \t\n\r", "{", `{"x":`, `{"x":[`, `{"x":[1,]}`, `{"x":{"a":1`, `{"x":[1,2}`} {
		require.Error(t, validateReportStructure([]byte(input)), "%q", input)
	}
}
