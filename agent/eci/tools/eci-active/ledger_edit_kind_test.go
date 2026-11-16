package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLedgerEditKindWireNames checks all supported operation and table names.
//
// Example: patch and records survive a JSON round trip without aliases.
func TestLedgerEditKindWireNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		wire string
		kind ledgerOperationKind
	}{{`"put"`, ledgerPut}, {`"patch"`, ledgerPatch}, {`"delete"`, ledgerDelete}} {
		var got ledgerOperationKind
		require.NoError(t, json.Unmarshal([]byte(tc.wire), &got))
		require.Equal(t, tc.kind, got)
		encoded, err := json.Marshal(got)
		require.NoError(t, err)
		require.Equal(t, tc.wire, string(encoded))
	}
	for _, tc := range []struct {
		wire  string
		table ledgerTable
	}{{`"sections"`, ledgerSections}, {`"records"`, ledgerRecords}, {`"forecasts"`, ledgerForecasts}} {
		var got ledgerTable
		require.NoError(t, json.Unmarshal([]byte(tc.wire), &got))
		require.Equal(t, tc.table, got)
		encoded, err := json.Marshal(got)
		require.NoError(t, err)
		require.Equal(t, tc.wire, string(encoded))
	}
	t.Run("unknown operation",
		// rejectUnknownOperation checks errors and the reset enum value.
		//
		// Example: an unrecognized operation cannot be encoded again.
		func(t *testing.T) {
			var kind ledgerOperationKind
			require.Error(t, json.Unmarshal([]byte(`"other"`), &kind))
			require.Equal(t, ledgerOperationUnknown, kind)
			_, err := json.Marshal(kind)
			require.Error(t, err)
			require.Error(t, json.Unmarshal([]byte(`1`), &kind))
		})
	t.Run("unknown table",
		// rejectUnknownTable checks errors and the reset enum value.
		//
		// Example: an unrecognized table cannot be encoded again.
		func(t *testing.T) {
			var table ledgerTable
			require.Error(t, json.Unmarshal([]byte(`"other"`), &table))
			require.Equal(t, ledgerTableUnknown, table)
			_, err := json.Marshal(table)
			require.Error(t, err)
			require.Error(t, json.Unmarshal([]byte(`1`), &table))
		})
}
