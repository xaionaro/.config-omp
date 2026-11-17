package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProjectLedgerReadsTransactionState checks the helper's projection boundary.
//
// Example: an uncommitted record is visible to the helper projection.
func TestProjectLedgerReadsTransactionState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := createTestLedger(t, t.TempDir())
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tx.Rollback()) })
	_, err = tx.ExecContext(ctx, `INSERT INTO sections(id,value) VALUES('s','{"title":"S"}')`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO records(id,value) VALUES('r','{"section":"s","type":"work","data":{},"evidence":[]}')`)
	require.NoError(t, err)
	projection, err := projectLedger(ctx, tx, true)
	require.NoError(t, err)
	encoded, err := json.Marshal(projection)
	require.NoError(t, err)
	require.JSONEq(t, `{"sections":{"s":{"title":"S"}},"records":{"r":{"section":"s","type":"work","data":{},"evidence":[]}},"snapshot_changed":true}`, string(encoded))
}

// TestLedgerEditRejectsRowsInAbsentSnapshot detects inconsistent source state.
//
// Example: a stored section cannot coexist with an absent project snapshot.
func TestLedgerEditRejectsRowsInAbsentSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := createTestLedger(t, t.TempDir())
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO sections(id,value) VALUES('s','{"title":"S"}')`)
	require.NoError(t, err)
	_, err = applyLedgerOperations(ctx, tx, nil)
	require.Error(t, err)
}

// TestEmptyOperationsLeavePresenceUntouched checks no-op transaction semantics.
//
// Example: empty ops do not initialize either optional snapshot.
func TestEmptyOperationsLeavePresenceUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := createTestLedger(t, t.TempDir())
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	changed, err := applyLedgerOperations(ctx, tx, nil)
	require.NoError(t, err)
	require.False(t, changed)
	var ledger, forecasts, generation int
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT ledger_present,forecasts_present,generation FROM ledger_meta WHERE id=1").Scan(&ledger, &forecasts, &generation))
	require.Equal(t, []int{0, 0, 0}, []int{ledger, forecasts, generation})
}

// TestProgrammaticOperationsRejectAmbiguousValues shares the CLI boundary.
//
// Example: a duplicate key in a caller-supplied value is rejected before insertion.
func TestProgrammaticOperationsRejectAmbiguousValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := createTestLedger(t, t.TempDir())
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	ops := []ledgerOperation{{Op: ledgerPut, Table: ledgerSections, ID: "s", Value: json.RawMessage(`{"title":"S","nested":{"x":1,"\u0078":2}}`)}}
	_, err = applyLedgerOperations(ctx, tx, ops)
	require.Error(t, err)
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM sections").Scan(&count))
	require.Zero(t, count)
}

// TestOperationsRefuseAmbiguousStoredRows preserves corrupt source evidence.
//
// Example: a put cannot silently overwrite a section with duplicate names.
func TestOperationsRefuseAmbiguousStoredRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := createTestLedger(t, t.TempDir())
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO sections(id,value) VALUES('s','{"title":"one","title":"two"}'); UPDATE ledger_meta SET ledger_present=1 WHERE id=1`)
	require.NoError(t, err)
	ops := []ledgerOperation{{Op: ledgerPut, Table: ledgerSections, ID: "s", Value: json.RawMessage(`{"title":"replaced"}`)}}
	_, err = applyLedgerOperations(ctx, tx, ops)
	require.Error(t, err)
	var value string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT value FROM sections WHERE id='s'").Scan(&value))
	require.Contains(t, value, `"title":"one"`)
	require.Contains(t, value, `"title":"two"`)
}

// TestInitializeLedgerSchemaUsesSessionIdentity binds metadata to its session.
//
// Example: a caller-supplied database identity remains bound to the selected session.
func TestInitializeLedgerSchemaUsesSessionIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	const identity = "0123456789abcdef0123456789abcdef"
	got, err := initializeLedgerSchema(ctx, tx, identity, "test-session")
	require.NoError(t, err)
	require.Equal(t, identity, got)
	var session, storedID string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT session_id,database_id FROM ledger_meta WHERE id=1").Scan(&session, &storedID))
	require.Equal(t, "test-session", session)
	require.Equal(t, identity, storedID)
}
