package main

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeReportCreateWithoutSourcesPublishesIncomplete verifies real sparse birth.
//
// Example: An absent ledger does not prevent an owner from creating a visibly incomplete report.
func TestNativeReportCreateWithoutSourcesPublishesIncomplete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db")); require.NoError(t, err)
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, session_id TEXT, database_id TEXT, generation INTEGER, ledger_present INTEGER, forecasts_present INTEGER);
CREATE TABLE sections(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE records(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE forecasts(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT, revision INTEGER);
INSERT INTO ledger_meta VALUES(1, ?, '0123456789abcdef0123456789abcdef', 3, 0, 0)`, filepath.Base(dir)); require.NoError(t, err)
	require.NoError(t, db.Close())
	input := `{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"3"},"document":{"timestamp":"2026-11-02T15:24:00Z","state":"Working","progress":[],"decisions":[],"blockers_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}},"decisions":{},"log":{"present":false,"device":"","inode":"","bytes_base64":""}}`
	var out, diagnostics bytes.Buffer
	code := run([]string{"report-create-locked",dir,"--stdin"},bytes.NewBufferString(input),&out,&diagnostics)
	require.Equal(t, 0, code, diagnostics.String())
	db, err = sql.Open("sqlite",filepath.Join(dir,"ledger.db")); require.NoError(t,err); defer db.Close()
	var document string; var generation int
	require.NoError(t,db.QueryRow(`SELECT document_json FROM report`).Scan(&document))
	require.NoError(t,db.QueryRow(`SELECT generation FROM ledger_meta`).Scan(&generation))
	require.Equal(t,4,generation)
	report, err := parseDBReport([]byte(document)); require.NoError(t,err)
	require.NotEmpty(t,report.Blockers)
}
