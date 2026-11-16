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
