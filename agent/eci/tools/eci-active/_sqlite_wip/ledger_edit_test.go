package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)
// missingLedgerBatch is a valid observation for testing unavailable storage.
const missingLedgerBatch = `{"base":{"database_id":"00000000000000000000000000000000","generation":"0"},"ops":[]}`

// runNativeBatch submits operations against a freshly observed native ledger.
//
// Example: each independent edit observes the preceding committed generation.
func runNativeBatch(t *testing.T, fx wrapperFixture, batch string) (string, string, int) {
	t.Helper()
	db, err := openLedger(context.Background(), fx.contextDir())
	require.NoError(t, err)
	var id string
	var generation int64
	require.NoError(t, db.QueryRow("SELECT database_id,generation FROM ledger_meta WHERE id=1").Scan(&id, &generation))
	require.NoError(t, db.Close())
	request := fmt.Sprintf(`{"base":{"database_id":%q,"generation":%q},%s`, id, fmt.Sprint(generation), strings.TrimPrefix(batch, "{"))
	return fx.runAsInput(t, wrapperSessionID, cwdNow(t), nil, request, "ledger-edit", "--stdin")
}

// TestLedgerEditBatch validates atomic updates and referential integrity through the CLI.
//
// Example: a batch may repair incoming links before deleting a record.
func TestLedgerEditBatch(t *testing.T) {
	// Fixture construction builds the binary; keep these tests serial to bound host memory.
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	run :=
	// run invokes the CLI for one batch.
	//
	// Example: a malformed batch returns a nonzero status.
	func(batch string) (string, int) {
		_, stderr, code := runNativeBatch(t, fx, batch)
		return stderr, code
	}
	stderr, code := run(`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"Section"}},{"op":"put","table":"records","id":"a","value":{"section":"s","type":"work","data":{},"evidence":[],"links":{"related":["b"]}}},{"op":"put","table":"records","id":"b","value":{"section":"s","type":"work","data":{},"evidence":[]}}]}`)
	require.Zero(t, code, stderr)
	dbPath := filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db")
	mode, err := exec.Command("sqlite3", "-readonly", dbPath, "PRAGMA journal_mode; PRAGMA user_version").Output()
	require.NoError(t, err)
	require.Equal(t, "wal\n1", strings.TrimSpace(string(mode)))
	stderr, code = run(`{"ops":[{"op":"put","table":"forecasts","id":"f","value":{"kind":"lane","status":"active"}},{"op":"patch","table":"forecasts","id":"f","expect":{"kind":"lane"},"value":{"status":"inactive"}}]}`)
	require.Zero(t, code, stderr)
	stderr, code = run(`{"ops":[{"op":"delete","table":"forecasts","id":"f","expect":{"kind":"root"}}]}`)
	require.Equal(t, 1, code, stderr)
	forecastQuery := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "select status from forecasts where id='f'")
	forecastOut, err := forecastQuery.Output()
	require.NoError(t, err)
	require.Equal(t, "inactive", strings.TrimSpace(string(forecastOut)))
	stderr, code = run(`{"ops":[{"op":"delete","table":"forecasts","id":"f","expect":{"kind":"lane"}}]}`)
	require.Zero(t, code, stderr)
	stderr, code = run(`{"ops":[{"op":"put","table":"records","id":"c","value":{"section":"s","type":"work","data":{},"evidence":[]}},{"op":"delete","table":"records","id":"b","expect":{"type":"work"}}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"delete","table":"sections","id":"s"}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"patch","table":"records","id":"missing","expect":{"type":"work"},"value":{"data":{}}}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"patch","table":"records","id":"a","value":{"data":{"owner":"nobody"}}}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"patch","table":"records","id":"a","expect":{"type":"constraint"},"value":{"data":{"changed":true}}}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"put","table":"records","id":"invalid","value":{"section":"missing","type":"work","data":{},"evidence":[]}}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"unknown","table":"records","id":"a"}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"put","table":"forecasts","id":"INVALID","value":{"kind":"lane","status":"active"}}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"put","table":"records","id":"invalid","value":{"section":"s","type":"work","data":{},"evidence":[],"links":{"related":null}}}]}`)
	require.Equal(t, 1, code, stderr)
	stderr, code = run(`{"ops":[{"op":"put","table":"records","id":"invalid","value":{"section":"s","type":"work","data":{},"evidence":[42]}}]}`)
	require.Equal(t, 1, code, stderr)
	query := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "select count(*) from records")
	out, err := query.Output()
	require.NoError(t, err)
	require.Equal(t, "2", strings.TrimSpace(string(out)))
	stderr, code = run(`{"ops":[{"op":"patch","table":"records","id":"a","expect":{"type":"work"},"value":{"links":{"related":[]}}},{"op":"delete","table":"records","id":"b","expect":{"type":"work"}}]}`)
	require.Zero(t, code, stderr)
	query = exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "select count(*) from records")
	out, err = query.Output()
	require.NoError(t, err)
	require.Equal(t, "1", strings.TrimSpace(string(out)))
}

// TestLedgerEditRequiresInitialization verifies edits never initialize storage.
//
// Example: a valid empty batch cannot create a database.
func TestLedgerEditRequiresInitialization(t *testing.T) {
	fx := newWrapperFixture(t)
	_, stderr, code := fx.runAsInput(t, wrapperSessionID, cwdNow(t), nil, missingLedgerBatch, "ledger-edit", "--stdin")
	require.Equal(t, 1, code, stderr)
	_, err := os.Stat(filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"))
	require.True(t, os.IsNotExist(err), err)
	initWrapperLedger(t, fx)
	_, stderr, code = runNativeBatch(t, fx, `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`)
	require.Zero(t, code, stderr)
}

// TestLedgerEditRejectsUnversionedDatabase protects an existing unknown schema.
//
// Example: a database with user_version zero is not initialized in place.
func TestLedgerEditRejectsUnversionedDatabase(t *testing.T) {
	fx := newWrapperFixture(t)
	dir := ctxDir(fx, wrapperSessionID)
	require.NoError(t, os.MkdirAll(dir, 0700))
	path := filepath.Join(dir, "ledger.db")
	require.NoError(t, exec.Command("sqlite3", path, "CREATE TABLE unrelated (id INTEGER)").Run())
	_, stderr, code := fx.runAsInput(t, wrapperSessionID, cwdNow(t), nil, missingLedgerBatch, "ledger-edit", "--stdin")
	require.Equal(t, 1, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", path, "SELECT count(*) FROM sqlite_master WHERE name='unrelated'").Output()
	require.NoError(t, err)
	require.Equal(t, "1", strings.TrimSpace(string(out)))
}

// TestLedgerEditConcurrentPatchesPreserveAllFields tests compare-and-swap and retry.
//
// Example: eight callers sharing one observation produce one winner.
func TestLedgerEditConcurrentPatchesPreserveAllFields(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	_, stderr, code := runNativeBatch(t, fx, `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"s"}},{"op":"put","table":"records","id":"r","value":{"section":"s","type":"work","data":{},"evidence":[]}}]}`)
	require.Zero(t, code, stderr)
	db, err := openLedger(context.Background(), fx.contextDir())
	require.NoError(t, err)
	var id string
	var generation int
	require.NoError(t, db.QueryRow("SELECT database_id,generation FROM ledger_meta WHERE id=1").Scan(&id, &generation))
	require.NoError(t, db.Close())
	var commands []*exec.Cmd
	var batches []string
	var diagnostics [8]strings.Builder
	for i := range 8 {
		batch := fmt.Sprintf(`{"ops":[{"op":"patch","table":"records","id":"r","expect":{"type":"work"},"value":{"data":{"field%d":true}}}]}`, i)
		request := fmt.Sprintf(`{"base":{"database_id":%q,"generation":%q},%s`, id, fmt.Sprint(generation), strings.TrimPrefix(batch, "{"))
		cmd := exec.Command(fx.binary, "ledger-edit", "--stdin")
		cmd.Stdin = strings.NewReader(request)
		cmd.Stderr = &diagnostics[i]
		cmd.Dir = cwdNow(t)
		cmd.Env = append(os.Environ(), "HOME="+fx.home, "ECI_PROOF_ROOT="+fx.proof, "ECI_SESSION_ID="+wrapperSessionID, "ECI_LOCK_WAIT_SECONDS=10")
		require.NoError(t, cmd.Start())
		commands = append(commands, cmd)
		batches = append(batches, batch)
	}
	var failed []string
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			require.Contains(t, diagnostics[i].String(), "observation conflict")
			failed = append(failed, batches[i])
		}
	}
	require.Len(t, failed, 7)
	for _, batch := range failed {
		_, stderr, code := runNativeBatch(t, fx, batch)
		require.Zero(t, code, stderr)
	}
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "select count(*) from records,json_each(records.value,'$.data') where records.id='r'").Output()
	require.NoError(t, err)
	require.Equal(t, "8", strings.TrimSpace(string(out)))
}

// TestLedgerEditPreservesOrphanSidecars prevents overwriting pre-existing data.
//
// Example: a session with a WAL but no database is refused.
func TestLedgerEditPreservesOrphanSidecars(t *testing.T) {
	fx := newWrapperFixture(t)
	dir := ctxDir(fx, wrapperSessionID)
	require.NoError(t, os.MkdirAll(dir, 0700))
	path := filepath.Join(dir, "ledger.db-wal")
	require.NoError(t, os.WriteFile(path, []byte("existing"), 0600))
	_, stderr, code := fx.runAsInput(t, wrapperSessionID, cwdNow(t), nil, missingLedgerBatch, "ledger-edit", "--stdin")
	require.Equal(t, 1, code, stderr)
	value, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "existing", string(value))
}

// TestLedgerEditRequiresEnvelopeFields checks required object members.
//
// Example: a record without evidence cannot enter the store.
func TestLedgerEditRequiresEnvelopeFields(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	batches := []string{
		`{"ops":[{"op":"put","table":"sections","id":"s","value":{}}]}`,
		`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}},{"op":"put","table":"records","id":"r","value":{"section":"s","data":{},"evidence":[]}}]}`,
		`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}},{"op":"put","table":"records","id":"r","value":{"section":"s","type":"work","data":{}}}]}`,
	}
	for _, batch := range batches {
		_, stderr, code := runNativeBatch(t, fx, batch)
		require.Equal(t, 1, code, stderr)
	}
}

// TestLedgerOperationJSONRoundTrip preserves the public batch wire format.
//
// Example: a put operation carries the sections table name.
func TestLedgerOperationJSONRoundTrip(t *testing.T) {
	t.Parallel()
	var op ledgerOperation
	require.NoError(t, json.Unmarshal([]byte(`{"op":"put","table":"sections","id":"s","value":{"title":"S"}}`), &op))
	encoded, err := json.Marshal(op)
	require.NoError(t, err)
	require.JSONEq(t, `{"op":"put","table":"sections","id":"s","value":{"title":"S"}}`, string(encoded))
}

// TestLedgerEditRejectsSymlinkDatabase avoids writing another session's database.
//
// Example: an accidental ledger.db symlink is not followed.
func TestLedgerEditRejectsSymlinkDatabase(t *testing.T) {
	fx := newWrapperFixture(t)
	dir := ctxDir(fx, wrapperSessionID)
	require.NoError(t, os.MkdirAll(dir, 0700))
	target := filepath.Join(t.TempDir(), "other.db")
	require.NoError(t, exec.Command("sqlite3", target, "CREATE TABLE original (id INTEGER); CREATE TABLE sections(id TEXT); CREATE TABLE records(id TEXT,section TEXT,value TEXT); PRAGMA user_version=1").Run())
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "ledger.db")))
	_, stderr, code := fx.runAsInput(t, wrapperSessionID, cwdNow(t), nil, missingLedgerBatch, "ledger-edit", "--stdin")
	require.Equal(t, 1, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", target, "SELECT count(*) FROM sqlite_master WHERE name='original'").Output()
	require.NoError(t, err)
	require.Equal(t, "1", strings.TrimSpace(string(out)))
}

// TestLedgerEditReportsFailingOperation identifies the rejected operation.
//
// Example: an invalid second operation reports index one and rolls back the first.
func TestLedgerEditReportsFailingOperation(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	_, stderr, code := runNativeBatch(t, fx, `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}},{"op":"delete","table":"records","id":"missing","expect":{"type":"work"}}]}`)
	require.Equal(t, 1, code, stderr)
	require.Contains(t, stderr, "op 1")
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(fx.contextDir(), "ledger.db"), "SELECT count(*) FROM sections").Output()
	require.NoError(t, err)
	require.Equal(t, "0", strings.TrimSpace(string(out)))
}

// TestLedgerEditMatchesEmptyExpectedKind preserves exact expectation semantics.
//
// Example: an existing empty forecast kind can be matched and patched.
func TestLedgerEditMatchesEmptyExpectedKind(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	batch := `{"ops":[{"op":"put","table":"forecasts","id":"f","value":{"kind":"","status":"active"}},{"op":"patch","table":"forecasts","id":"f","expect":{"kind":""},"value":{"status":"inactive"}}]}`
	_, stderr, code := runNativeBatch(t, fx, batch)
	require.Zero(t, code, stderr)
	_, stderr, code = runNativeBatch(t, fx, `{"ops":[{"op":"patch","table":"forecasts","id":"f","expect":{"kind":null},"value":{"status":"active"}}]}`)
	require.Equal(t, 1, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "SELECT status FROM forecasts WHERE id='f'").Output()
	require.NoError(t, err)
	require.Equal(t, "inactive", strings.TrimSpace(string(out)))
}

// TestLedgerEditAllowsTypedContextValues retains project-specific context data.
//
// Example: context data may be an array rather than an object.
func TestLedgerEditAllowsTypedContextValues(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	batch := `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}},{"op":"put","table":"records","id":"c","value":{"section":"s","type":"context","data":["typed",42],"evidence":[]}}]}`
	_, stderr, code := runNativeBatch(t, fx, batch)
	require.Zero(t, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "SELECT json_extract(value,'$.data[1]') FROM records WHERE id='c'").Output()
	require.NoError(t, err)
	require.Equal(t, "42", strings.TrimSpace(string(out)))
}

// TestLedgerEditRejectsNonObjectWorkData enforces typed record envelopes.
//
// Example: an array is valid context data but not valid work data.
func TestLedgerEditRejectsNonObjectWorkData(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	batch := `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}},{"op":"put","table":"records","id":"r","value":{"section":"s","type":"work","data":[],"evidence":[]}}]}`
	_, stderr, code := runNativeBatch(t, fx, batch)
	require.Equal(t, 1, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(fx.contextDir(), "ledger.db"), "SELECT count(*) FROM sections").Output()
	require.NoError(t, err)
	require.Equal(t, "0", strings.TrimSpace(string(out)))
}

// TestLedgerEditRejectsEachInvalidRequiredField verifies schema checks and rollback.
//
// Example: missing and null evidence are both rejected.
func TestLedgerEditRejectsEachInvalidRequiredField(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	_, stderr, code := runNativeBatch(t, fx, `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`)
	require.Zero(t, code, stderr)
	values := []struct{ table, value string }{
		{"sections", `{}`}, {"sections", `{"title":null}`}, {"sections", `{"title":42}`},
		{"records", `{"type":"work","data":{},"evidence":[]}`}, {"records", `{"section":null,"type":"work","data":{},"evidence":[]}`}, {"records", `{"section":42,"type":"work","data":{},"evidence":[]}`},
		{"records", `{"section":"s","data":{},"evidence":[]}`}, {"records", `{"section":"s","type":null,"data":{},"evidence":[]}`}, {"records", `{"section":"s","type":42,"data":{},"evidence":[]}`},
		{"records", `{"section":"s","type":"work","data":{}}`}, {"records", `{"section":"s","type":"work","data":{},"evidence":null}`}, {"records", `{"section":"s","type":"work","data":{},"evidence":"wrong"}`},
		{"records", `{"section":"s","type":"work","evidence":[]}`}, {"records", `{"section":"s","type":"work","data":null,"evidence":[]}`}, {"records", `{"section":"s","type":"work","data":[],"evidence":[]}`},
	}
	for _, item := range values {
		batch := fmt.Sprintf(`{"ops":[{"op":"put","table":"sections","id":"temporary","value":{"title":"Temporary"}},{"op":"put","table":%q,"id":"candidate","value":%s}]}`, item.table, item.value)
		_, stderr, code = runNativeBatch(t, fx, batch)
		require.Equal(t, 1, code, "%s: %s", item.value, stderr)
		out, err := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "SELECT count(*) FROM sections WHERE id='temporary'").Output()
		require.NoError(t, err)
		require.Equal(t, "0", strings.TrimSpace(string(out)), item.value)
	}
}

// TestLedgerEditGenerationTracksNetChanges tests shared snapshot versioning.
//
// Example: a batch that restores its original values does not advance generation.
func TestLedgerEditGenerationTracksNetChanges(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	run :=
	// run submits one ledger batch in the fixture session.
	//
	// Example: a patch and its inverse can be submitted together.
	func(batch string) {
		_, stderr, code := runNativeBatch(t, fx, batch)
		require.Zero(t, code, stderr)
	}
	run(`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`)
	path := filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db")
	identity, err := exec.Command("sqlite3", "-readonly", path, "SELECT database_id FROM ledger_meta WHERE id=1").Output()
	require.NoError(t, err)
	require.Regexp(t, `^[a-f0-9]{32}$`, strings.TrimSpace(string(identity)))
	readGeneration :=
	// readGeneration reads the committed database-wide generation.
	//
	// Example: a single transaction advances it once.
	func() string {
		out, err := exec.Command("sqlite3", "-readonly", path, "SELECT generation FROM ledger_meta WHERE id=1").Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}
	first := readGeneration()
	run(`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`)
	require.Equal(t, first, readGeneration())
	run(`{"ops":[{"op":"patch","table":"sections","id":"s","value":{"title":"T"}},{"op":"patch","table":"sections","id":"s","value":{"title":"S"}}]}`)
	require.Equal(t, first, readGeneration())
	run(`{"ops":[{"op":"patch","table":"sections","id":"s","value":{"title":"T"}}]}`)
	require.NotEqual(t, first, readGeneration())
	run(`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S","a":1,"b":9007199254740993}}]}`)
	ordered := readGeneration()
	run(`{"ops":[{"op":"put","table":"sections","id":"s","value":{"b":9007199254740993,"a":1,"title":"S"}}]}`)
	require.Equal(t, ordered, readGeneration())
	run(`{"ops":[{"op":"patch","table":"sections","id":"s","value":{"a":1.0}}]}`)
	decimal := readGeneration()
	require.NotEqual(t, ordered, decimal)
	run(`{"ops":[{"op":"patch","table":"sections","id":"s","value":{"a":1e0}}]}`)
	require.NotEqual(t, decimal, readGeneration())
	run(`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S","value":null}}]}`)
	withNull := readGeneration()
	run(`{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`)
	require.NotEqual(t, withNull, readGeneration())
}

// TestLedgerEditPreservesAbsentAndEmptySnapshots checks source presence metadata.
//
// Example: deleting the last forecast leaves a present-empty forecast snapshot.
func TestLedgerEditPreservesAbsentAndEmptySnapshots(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	_, stderr, code := runNativeBatch(t, fx, `{"ops":[{"op":"put","table":"forecasts","id":"f","value":{"kind":"lane"}}]}`)
	require.Zero(t, code, stderr)
	_, stderr, code = runNativeBatch(t, fx, `{"ops":[{"op":"delete","table":"forecasts","id":"f","expect":{"kind":"lane"}}]}`)
	require.Zero(t, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "SELECT ledger_present,forecasts_present FROM ledger_meta WHERE id=1").Output()
	require.NoError(t, err)
	require.Equal(t, "1|1", strings.TrimSpace(string(out)))
}

// TestLedgerEditNetEmptyCreatedSnapshotIsPresent distinguishes absence from emptiness.
//
// Example: create then delete a forecast still creates a present-empty snapshot.
func TestLedgerEditNetEmptyCreatedSnapshotIsPresent(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	batch := `{"ops":[{"op":"put","table":"forecasts","id":"f","value":{"kind":"lane"}},{"op":"delete","table":"forecasts","id":"f","expect":{"kind":"lane"}}]}`
	_, stderr, code := runNativeBatch(t, fx, batch)
	require.Zero(t, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "SELECT forecasts_present,generation FROM ledger_meta WHERE id=1").Output()
	require.NoError(t, err)
	require.Equal(t, "1|1", strings.TrimSpace(string(out)))
}

// TestLedgerEditEmptyBatchDoesNotBootstrap preserves absent snapshot storage.
//
// Example: an empty batch in a log-only session leaves the database absent.
func TestLedgerEditEmptyBatchDoesNotBootstrap(t *testing.T) {
	fx := newWrapperFixture(t)
	dir := ctxDir(fx, wrapperSessionID)
	require.NoError(t, os.MkdirAll(dir, 0700))
	log := filepath.Join(dir, ledgerLogName)
	require.NoError(t, os.WriteFile(log, []byte("history\n"), 0600))
	_, stderr, code := fx.runAsInput(t, wrapperSessionID, cwdNow(t), nil, missingLedgerBatch, "ledger-edit", "--stdin")
	require.Equal(t, 1, code, stderr)
	_, err := os.Stat(filepath.Join(dir, "ledger.db"))
	require.True(t, os.IsNotExist(err), err)
	value, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, "history\n", string(value))
}

// TestLedgerEditPatchPreservesUntouchedNumericTokens protects typed fidelity.
//
// Example: patching a title preserves a large integer and exponent elsewhere.
func TestLedgerEditPatchPreservesUntouchedNumericTokens(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	batch := `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S","large":9007199254740993,"exponent":1e0}},{"op":"patch","table":"sections","id":"s","value":{"title":"T"}}]}`
	_, stderr, code := runNativeBatch(t, fx, batch)
	require.Zero(t, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(ctxDir(fx, wrapperSessionID), "ledger.db"), "SELECT value FROM sections WHERE id='s'").Output()
	require.NoError(t, err)
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	decoder.UseNumber()
	var value map[string]any
	require.NoError(t, decoder.Decode(&value))
	require.Equal(t, json.Number("9007199254740993"), value["large"])
	require.Equal(t, json.Number("1e0"), value["exponent"])
}

// TestLedgerEditRejectsDuplicateRecordKeys prevents conflicting JSON interpretations.
//
// Example: duplicate type keys cannot select different schemas in different consumers.
func TestLedgerEditRejectsDuplicateRecordKeys(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	batch := `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}},{"op":"put","table":"records","id":"r","value":{"section":"s","type":"work","type":"constraint","data":1,"evidence":[]}}]}`
	_, stderr, code := runNativeBatch(t, fx, batch)
	require.Equal(t, 1, code, stderr)
	out, err := exec.Command("sqlite3", "-readonly", filepath.Join(fx.contextDir(), "ledger.db"), "SELECT count(*) FROM sections").Output()
	require.NoError(t, err)
	require.Equal(t, "0", strings.TrimSpace(string(out)))
}

// TestLedgerEditRejectsAmbiguousJSON checks exact keys and atomic rollback.
//
// Example: an escaped duplicate in a nested value cannot mutate the store.
func TestLedgerEditRejectsAmbiguousJSON(t *testing.T) {
	fx := newWrapperFixture(t)
	initWrapperLedger(t, fx)
	_, stderr, code := runNativeBatch(t, fx, `{"ops":[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]}`)
	require.Zero(t, code, stderr)
	path := filepath.Join(fx.contextDir(), "ledger.db")
	for _, batch := range []string{
		`{"Ops":[]}`,
		`{"ops":[{"Op":"put","table":"sections","id":"x","value":{"title":"X"}}]}`,
		`{"ops":[],"ops":[]}`,
		`{"ops":[{"op":"put","op":"delete","table":"sections","id":"x","value":{"title":"X"}}]}`,
		`{"ops":[{"op":"put","table":"sections","id":"ok","value":{"title":"OK"}},{"op":"put","table":"sections","id":"x","value":{"title":"X","nested":{"a":1,"\u0061":2}}}]}`,
		`{"ops":[{"op":"patch","table":"sections","id":"s","value":{"nested":{"a":1,"a":2}}}]}`,
		`{"ops":[{"op":"put","table":"records","id":"x","value":{"section":"s","type":"work","Type":"constraint","data":1,"evidence":[]}}]}`,
		`{"ops":[{"op":"patch","table":"records","id":"x","expect":{"type":"work","type":"work"},"value":{}}]}`,
	} {
		_, stderr, code = runNativeBatch(t, fx, batch)
		require.Equal(t, 1, code, "%s: %s", batch, stderr)
	}
	out, err := exec.Command("sqlite3", "-readonly", path, "SELECT generation,(SELECT count(*) FROM sections) FROM ledger_meta").Output()
	require.NoError(t, err)
	require.Equal(t, "1|1", strings.TrimSpace(string(out)))
	valid := `{"ops":[{"op":"put","Op":"delete","table":"sections","id":"x","value":{"title":"X","one":{"a":1},"two":{"a":2},"x":1,"X":2,"precise":9007199254740993}}]}`
	_, stderr, code = runNativeBatch(t, fx, valid)
	require.Zero(t, code, stderr)
}
