package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeLedgerObservationCAS rejects stale writes before applying operations.
//
// Example: an observation from a different database cannot authorize a put.
func TestNativeLedgerObservationCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := createTestLedger(t, t.TempDir())
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	var id string
	var generation int64
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT database_id,generation FROM ledger_meta WHERE id=1").Scan(&id, &generation))
	base := ledgerObservation{DatabaseID: id, Generation: generation}
	put := []ledgerOperation{{Op: ledgerPut, Table: ledgerSections, ID: "s", Value: json.RawMessage(`{"title":"S"}`)}}
	_, err = applyNativeLedgerBatch(ctx, tx, ledgerObservation{DatabaseID: "wrong", Generation: generation}, put)
	require.Error(t, err)
	var conflict ledgerConflictError
	require.True(t, errors.As(err, &conflict))
	_, err = applyNativeLedgerBatch(ctx, tx, ledgerObservation{DatabaseID: id, Generation: generation + 1}, nil)
	require.Error(t, err)
	_, err = applyNativeLedgerBatch(ctx, tx, ledgerObservation{DatabaseID: id, Generation: generation + 1}, []ledgerOperation{{Op: ledgerPatch, Table: ledgerSections, ID: "s", Value: json.RawMessage(`{"title":"T"}`)}})
	require.Error(t, err)
	changed, err := applyNativeLedgerBatch(ctx, tx, base, put)
	require.NoError(t, err)
	require.True(t, changed)
	_, err = applyNativeLedgerBatch(ctx, tx, base, put)
	require.Error(t, err)
	next := ledgerObservation{DatabaseID: id, Generation: generation + 1}
	changed, err = applyNativeLedgerBatch(ctx, tx, next, put)
	require.NoError(t, err)
	require.False(t, changed)
	var got int64
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT generation FROM ledger_meta WHERE id=1").Scan(&got))
	require.Equal(t, generation+1, got)
	var title string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT json_extract(value,'$.title') FROM sections WHERE id='s'").Scan(&title))
	require.Equal(t, "S", title)
}
