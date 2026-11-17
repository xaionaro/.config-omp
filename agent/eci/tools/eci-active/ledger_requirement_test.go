package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunRequirementHelperValidatesPrivateEnvelope checks the subprocess boundary.
//
// Example: a helper failure emits no assignment payload.
func TestRunRequirementHelperValidatesPrivateEnvelope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	helper := filepath.Join(dir, "assignment")
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"event\":{\"event\":\"decision\",\"summary\":\"prepared\"},\"payload\":{\"target\":\"t\",\"message\":\"m\"}}'\n"), 0700))
	projection := ledgerProjection{Sections: map[string]json.RawMessage{}, Records: map[string]json.RawMessage{}}
	event, payload, err := runRequirementHelper(context.Background(), helper, json.RawMessage(`{}`), projection)
	require.NoError(t, err)
	require.JSONEq(t, `{"event":"decision","summary":"prepared"}`, string(event))
	require.JSONEq(t, `{"target":"t","message":"m"}`, string(payload))
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\nexit 1\n"), 0700))
	event, payload, err = runRequirementHelper(context.Background(), helper, json.RawMessage(`{}`), projection)
	require.Error(t, err)
	require.Empty(t, event)
	require.Empty(t, payload)
}

// TestDatabaseRequirementRollsBackOnHelperFailure verifies transactional capture.
//
// Example: a failed helper leaves the ledger and generation unchanged.
func TestDatabaseRequirementRollsBackOnHelperFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	db := createTestLedger(t, dir)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	var initial ledgerOperation
	require.NoError(t, json.Unmarshal([]byte(`{"op":"put","table":"sections","id":"s","value":{"title":"S"}}`), &initial))
	changed, err := applyLedgerOperations(ctx, tx, []ledgerOperation{initial})
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, advanceLedgerGeneration(ctx, tx))
	require.NoError(t, tx.Commit())
	var id string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT database_id FROM ledger_meta WHERE id=1").Scan(&id))
	require.NoError(t, db.Close())
	request := []byte(fmt.Sprintf(`{"base":{"database_id":%q,"generation":"1"},"ops":[{"op":"patch","table":"sections","id":"s","value":{"title":"T"}}]}`, id))
	helper := filepath.Join(dir, "helper")
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\nexit 1\n"), 0700))
	event, payload, err := prepareDatabaseRequirement(ctx, dir, request, helper)
	require.Error(t, err)
	require.Empty(t, event)
	require.Empty(t, payload)
	db, err = openLedger(ctx, dir)
	require.NoError(t, err)
	var value string
	var generation int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT value FROM sections WHERE id='s'").Scan(&value))
	require.JSONEq(t, `{"title":"S"}`, value)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT generation FROM ledger_meta WHERE id=1").Scan(&generation))
	require.Equal(t, 1, generation)
	require.NoError(t, db.Close())
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"event\":{\"event\":\"requirement_change\",\"summary\":\"changed\"},\"payload\":{\"target\":\"t\",\"message\":\"m\"}}'\n"), 0700))
	event, payload, err = prepareDatabaseRequirement(ctx, dir, request, helper)
	require.NoError(t, err)
	require.NotEmpty(t, event)
	require.NotEmpty(t, payload)
	db, err = openLedger(ctx, dir)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT value FROM sections WHERE id='s'").Scan(&value))
	require.JSONEq(t, `{"title":"T"}`, value)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT generation FROM ledger_meta WHERE id=1").Scan(&generation))
	require.Equal(t, 2, generation)
	require.NoError(t, db.Close())
}

// TestDatabaseRequirementRejectsStaleObservation prevents old assignments.
//
// Example: a stale request cannot reach its helper or mutate the store.
func TestDatabaseRequirementRejectsStaleObservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	db := createTestLedger(t, dir)
	var id string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT database_id FROM ledger_meta WHERE id=1").Scan(&id))
	require.NoError(t, db.Close())
	request := []byte(fmt.Sprintf(`{"base":{"database_id":%q,"generation":"1"},"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`, id))
	_, _, err := prepareDatabaseRequirement(ctx, dir, request, filepath.Join(dir, "missing-helper"))
	require.Error(t, err)
	db, err = openLedger(ctx, dir)
	require.NoError(t, err)
	defer db.Close()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM sections").Scan(&count))
	require.Zero(t, count)
}

// TestDatabaseRequirementRejectsDuplicateRequestKeys protects capture input.
//
// Example: duplicate ops fields cannot select different requirement changes.
func TestDatabaseRequirementRejectsDuplicateRequestKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	db := createTestLedger(t, dir)
	require.NoError(t, db.Close())
	request := []byte(`{"ops":[],"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`)
	helper := filepath.Join(dir, "helper")
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"event\":{\"event\":\"requirement_change\",\"summary\":\"changed\"},\"payload\":{\"target\":\"t\",\"message\":\"m\"}}'\n"), 0700))
	event, payload, err := prepareDatabaseRequirement(ctx, dir, request, helper)
	require.Error(t, err)
	require.Empty(t, event)
	require.Empty(t, payload)
}
