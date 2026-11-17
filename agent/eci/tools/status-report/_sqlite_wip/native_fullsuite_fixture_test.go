package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// nativeReplayFixture holds an isolated native session and its SQLite connection.
//
// Example: Mutate a source row and advance generation before refreshing.
type nativeReplayFixture struct {
	T   *testing.T
	Dir string
	DB  *sql.DB
}

// newNativeReplayFixture loads supplied YAML source fixtures into native storage.
//
// Example: Empty ledger and forecast strings represent absent collections.
func newNativeReplayFixture(
	t *testing.T,
	ledgerYAML string,
	forecastsYAML string,
	reportYAML string,
) *nativeReplayFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	require.NoError(t, err)
	t.Cleanup(
		// Close the fixture database after the test finishes.
		//
		// Example: Release SQLite resources before the temporary directory is removed.
		func() {
			assert.NoError(t, db.Close())
		},
	)
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY,session_id TEXT,database_id TEXT,generation INTEGER,ledger_present INTEGER,forecasts_present INTEGER);
CREATE TABLE report(id INTEGER PRIMARY KEY,document_json TEXT,revision INTEGER);
CREATE TABLE sections(id TEXT PRIMARY KEY,value TEXT); CREATE TABLE records(id TEXT PRIMARY KEY,value TEXT); CREATE TABLE forecasts(id TEXT PRIMARY KEY,value TEXT);
INSERT INTO ledger_meta VALUES(1,?,'0123456789abcdef0123456789abcdef',0,?,?);`, filepath.Base(dir), ledgerYAML != "", forecastsYAML != "")
	require.NoError(t, err)
	fixture := &nativeReplayFixture{T: t, Dir: dir, DB: db}
	if ledgerYAML != "" {
		fixture.insertYAMLRows(ledgerYAML, "sections", "INSERT INTO sections VALUES(?,?)")
		fixture.insertYAMLRows(ledgerYAML, "records", "INSERT INTO records VALUES(?,?)")
	}
	if forecastsYAML != "" {
		fixture.insertYAMLRows(forecastsYAML, "records", "INSERT INTO forecasts VALUES(?,?)")
	}
	if reportYAML != "" {
		document, err := replayYAMLToReportJSON([]byte(reportYAML))
		require.NoError(t, err)
		_, err = db.Exec("INSERT INTO report VALUES(1,?,0)", string(document))
		require.NoError(t, err)
	}
	return fixture
}

// insertYAMLRows converts one fixture mapping into typed JSON database rows.
//
// Example: Populate records without treating YAML files as session authority.
func (fixture *nativeReplayFixture) insertYAMLRows(
	source string,
	field string,
	statement string,
) {
	fixture.T.Helper()
	var document yaml.Node
	require.NoError(fixture.T, yaml.Unmarshal([]byte(source), &document))
	require.Len(fixture.T, document.Content, 1, "invalid source fixture")
	node := mapValue(document.Content[0], field)
	require.NotNil(fixture.T, node, "missing source field %s", field)
	value, err := replayJSONValue(node)
	require.NoError(fixture.T, err)
	rows, ok := value.(map[string]any)
	require.True(fixture.T, ok, "source field %s is not a mapping", field)
	for id, row := range rows {
		encoded, err := json.Marshal(row)
		require.NoError(fixture.T, err)
		_, err = fixture.DB.Exec(statement, id, string(encoded))
		require.NoError(fixture.T, err)
	}
}

// Report returns the exact current report document.
//
// Example: Compare persisted state after a failed refresh.
func (fixture *nativeReplayFixture) Report() []byte {
	fixture.T.Helper()
	var document string
	err := fixture.DB.QueryRow("SELECT document_json FROM report WHERE id=1").Scan(&document)
	require.NoError(fixture.T, err)
	return []byte(document)
}

// SetReport replaces a fixture report and advances its observation token.
//
// Example: Simulate an owner edit before retrying a pending event.
func (fixture *nativeReplayFixture) SetReport(document []byte) {
	fixture.T.Helper()
	require.True(fixture.T, json.Valid(document), "report fixture must be JSON")
	tx, err := fixture.DB.Begin()
	require.NoError(fixture.T, err)
	defer
	// Roll back an uncommitted fixture transaction and report cleanup errors.
	//
	// Example: Preserve the database when the replacement assertion fails.
	func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			assert.NoError(fixture.T, err)
		}
	}()
	result, err := tx.Exec("UPDATE report SET document_json=?,revision=revision+1 WHERE id=1", string(document))
	require.NoError(fixture.T, err)
	count, err := result.RowsAffected()
	require.NoError(fixture.T, err)
	require.Equal(fixture.T, int64(1), count, "replace report")
	_, err = tx.Exec("UPDATE ledger_meta SET generation=generation+1 WHERE id=1")
	require.NoError(fixture.T, err)
	require.NoError(fixture.T, tx.Commit())
}

// AppendLog appends exact bytes to the external canonical event log.
//
// Example: Add one complete JSONL event before refreshing.
func (fixture *nativeReplayFixture) AppendLog(data string) {
	fixture.T.Helper()
	file, err := os.OpenFile(filepath.Join(fixture.Dir, "high_level_log.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	require.NoError(fixture.T, err)
	_, writeErr := file.WriteString(data)
	require.NoError(fixture.T, errors.Join(writeErr, file.Close()))
}

// Refresh runs the real locked database refresh and returns its persisted report.
//
// Example: Inspect retained pending state even when refresh returns an error.
func (fixture *nativeReplayFixture) Refresh(event string) ([]byte, error) {
	return fixture.RefreshAt(event, time.Now().UTC())
}

// RefreshAt runs a locked refresh at one deterministic evaluation instant.
//
// Example: Reconcile an elapsed forecast against a fixed timestamp.
func (fixture *nativeReplayFixture) RefreshAt(
	event string,
	at time.Time,
) ([]byte, error) {
	fixture.T.Helper()
	lock, err := os.OpenFile(filepath.Join(fixture.Dir, ".eci-session.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	refreshErr := refreshDBLocked(context.Background(), fixture.Dir, event, at)
	unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	closeErr := lock.Close()
	var document string
	readErr := fixture.DB.QueryRow("SELECT document_json FROM report WHERE id=1").Scan(&document)
	if readErr != nil {
		readErr = fmt.Errorf("read refreshed report: %w", readErr)
	}
	return []byte(document), errors.Join(refreshErr, unlockErr, closeErr, readErr)
}
