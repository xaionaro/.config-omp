package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeReportMutationRejectsMissingObservation checks that mutation requires an observed database token.
//
// Example: A report cannot be created by an unversioned request.
func TestNativeReportMutationRejectsMissingObservation(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"report-create-locked", "report-reconcile-locked"} {
		var out, diagnostics bytes.Buffer
		code := run([]string{command, t.TempDir(), "--stdin"}, bytes.NewBufferString(`{}`), &out, &diagnostics)
		require.Equal(t, 1, code)
		require.Empty(t, out.String())
		require.NotEmpty(t, diagnostics.String())
	}
}

// TestNativeReportCreateChecksObservationAndCommitsBaseline exercises late native creation.
//
// Example: A stale generation cannot insert a report or advance the store.
func TestNativeReportCreateChecksObservationAndCommitsBaseline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	require.NoError(t, err)
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, session_id TEXT, database_id TEXT, generation INTEGER, ledger_present INTEGER, forecasts_present INTEGER);
CREATE TABLE sections(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE records(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE forecasts(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT, revision INTEGER);
INSERT INTO ledger_meta VALUES(1, ?, '0123456789abcdef0123456789abcdef', 3, 1, 0)`, filepath.Base(dir))
	require.NoError(t, err)
	require.NoError(t, db.Close())
	document := `{"timestamp":"2026-11-02T15:24:00Z","state":"Working","progress":[],"decisions":[],"stuck_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}`
	envelope := `{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"2"},"document":` + document + `,"decisions":{},"log":{"present":false,"device":"","inode":"","bytes_base64":""}}`
	var out, diagnostics bytes.Buffer
	code := run([]string{"report-create-locked",dir,"--stdin"}, bytes.NewBufferString(envelope), &out, &diagnostics)
	require.Equal(t, 1, code)
	require.Empty(t, out.String())
	envelope = `{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"3"},"document":` + document + `,"decisions":{},"log":{"present":false,"device":"","inode":"","bytes_base64":""}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600))
	out.Reset(); diagnostics.Reset()
	code = run([]string{"report-create-locked",dir,"--stdin"}, bytes.NewBufferString(envelope), &out, &diagnostics)
	require.Equal(t, 1, code)
	require.Empty(t, out.String())
	require.NoError(t, os.Remove(filepath.Join(dir, "high_level_log.jsonl")))
	out.Reset(); diagnostics.Reset()
	code = run([]string{"report-create-locked",dir,"--stdin"}, bytes.NewBufferString(envelope), &out, &diagnostics)
	require.Equal(t, 0, code, diagnostics.String())
	var token map[string]string
	require.NoError(t, json.Unmarshal(out.Bytes(), &token))
	require.Equal(t, "4", token["generation"])
	db, err = sql.Open("sqlite", filepath.Join(dir, "ledger.db")); require.NoError(t, err); defer db.Close()
	var generation, revision int
	require.NoError(t, db.QueryRow(`SELECT generation FROM ledger_meta WHERE id=1`).Scan(&generation))
	require.NoError(t, db.QueryRow(`SELECT revision FROM report WHERE id=1`).Scan(&revision))
	require.Equal(t, 4, generation)
	require.Equal(t, 1, revision)
}
