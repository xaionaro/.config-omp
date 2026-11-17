package main

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// createTestLedger creates an isolated versioned database without an activation marker.
//
// Example: transaction tests initialize their own temporary session directory.
func createTestLedger(
	t *testing.T,
	dir string,
) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "ledger.db")}
	db, err := sql.Open("sqlite", u.String())
	require.NoError(t, err)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = initializeLedgerSchema(context.Background(), tx, "", filepath.Base(dir))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	var mode string
	require.NoError(t, db.QueryRowContext(context.Background(), "PRAGMA journal_mode=WAL").Scan(&mode))
	require.Equal(t, "wal", mode)
	return db
}
