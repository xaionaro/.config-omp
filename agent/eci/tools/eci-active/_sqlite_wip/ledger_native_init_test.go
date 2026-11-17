package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeInitializationPreservesExistingFiles checks no-replace publication.
//
// Example: legacy files are untouched and a second initializer conflicts.
func TestNativeInitializationPreservesExistingFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	legacy := filepath.Join(dir, "project-understanding.yaml")
	require.NoError(t, os.WriteFile(legacy, []byte("old bytes"), 0600))
	input := []byte(`{"expect":"uninitialized","ledger":{"present":true,"sections":{"s":{"title":"S"}},"records":{}},"forecasts":{"present":false},"report":{"present":false}}`)
	observation, err := initializeNativeLedger(context.Background(), dir, filepath.Base(dir), input)
	require.NoError(t, err)
	require.NotEmpty(t, observation.DatabaseID)
	db, err := openNativeLedgerRead(context.Background(), dir)
	require.NoError(t, err)
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	actual, err := validateNativeLedgerTransaction(context.Background(), tx, filepath.Base(dir))
	require.NoError(t, err)
	require.Equal(t, observation, *actual)
	require.NoError(t, tx.Rollback())
	require.NoError(t, db.Close())
	_, err = initializeNativeLedger(context.Background(), dir, filepath.Base(dir), input)
	require.Error(t, err)
	value, err := os.ReadFile(legacy)
	require.NoError(t, err)
	require.Equal(t, "old bytes", string(value))
}

// TestNativeInitializationRejectsUnclearPresence avoids fabricated snapshots.
//
// Example: an absent report cannot carry a hidden document.
func TestNativeInitializationRejectsUnclearPresence(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"expect":"uninitialized","ledger":{"present":false},"forecasts":{"present":false},"report":{"present":false}}`,
		`{"expect":"uninitialized","ledger":{"present":true,"sections":null,"records":{}},"forecasts":{"present":false},"report":{"present":false}}`,
		`{"expect":"uninitialized","ledger":{"present":true,"sections":{},"records":{}},"forecasts":{"present":false},"report":{"present":false,"document":{}}}`,
		`{"expect":"uninitialized","ledger":{"Present":true,"sections":{},"records":{}},"forecasts":{"present":false},"report":{"present":false}}`,
	} {
		dir := t.TempDir()
		_, err := initializeNativeLedger(context.Background(), dir, filepath.Base(dir), []byte(input))
		require.Error(t, err, "%s", input)
		_, err = os.Stat(filepath.Join(dir, "ledger.db"))
		require.True(t, os.IsNotExist(err))
	}
}

// TestNativeInitializationRequiresObservedNonemptyLog rejects an unbound inventory.
//
// Example: decisions against existing log bytes require their caller observation.
func TestNativeInitializationRequiresObservedNonemptyLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerLogName), []byte("{}\n"), 0600))
	input := []byte(`{"expect":"uninitialized","ledger":{"present":true,"sections":{},"records":{}},"forecasts":{"present":false},"report":{"present":true,"document":{},"decisions":{}}}`)
	_, err := initializeNativeLedger(context.Background(), dir, filepath.Base(dir), input)
	require.ErrorContains(t, err, "log observation")
	_, err = os.Stat(filepath.Join(dir, "ledger.db"))
	require.True(t, os.IsNotExist(err))
}

// TestNativeInitializationRejectsStaleLogIdentity keeps caller inventory bound.
//
// Example: an absent observation cannot authorize a present empty log.
func TestNativeInitializationRejectsStaleLogIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ledgerLogName), nil, 0600))
	input := []byte(`{"expect":"uninitialized","ledger":{"present":true,"sections":{},"records":{}},"forecasts":{"present":false},"report":{"present":true,"document":{},"decisions":{},"log":{"present":false,"device":"","inode":"","bytes_base64":""}}}`)
	_, err := initializeNativeLedger(context.Background(), dir, filepath.Base(dir), input)
	require.ErrorContains(t, err, "log observation differs")
	_, err = os.Stat(filepath.Join(dir, "ledger.db"))
	require.True(t, os.IsNotExist(err))
}

// TestNativeInitializationRequiresObservedAbsentLog binds even an empty inventory.
//
// Example: absence cannot be inferred from an omitted caller observation.
func TestNativeInitializationRequiresObservedAbsentLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	input := []byte(`{"expect":"uninitialized","ledger":{"present":true,"sections":{},"records":{}},"forecasts":{"present":false},"report":{"present":true,"document":{},"decisions":{}}}`)
	_, err := initializeNativeLedger(context.Background(), dir, filepath.Base(dir), input)
	require.ErrorContains(t, err, "log observation is required")
	_, err = os.Stat(filepath.Join(dir, "ledger.db"))
	require.True(t, os.IsNotExist(err))
}
