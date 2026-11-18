package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeReportPrepareRejectsInvalidEnvelope checks that preparation does not accept incomplete requests.
//
// Example: Missing candidate identity cannot generate a publishable report.
func TestNativeReportPrepareRejectsInvalidEnvelope(t *testing.T) {
	t.Parallel()
	var out, diagnostics bytes.Buffer
	code := run([]string{"report-prepare", "--stdin"}, bytes.NewBufferString(`{"document":{},"decisions":{},"log":""}`), &out, &diagnostics)
	require.Equal(t, 1, code)
	require.Empty(t, out.String())
	require.NotEmpty(t, diagnostics.String())
}

// TestNativeEnvelopeKeys rejects aliases and duplicate members.
//
// Example: A case-folded field name is not an authorized request field.
func TestNativeEnvelopeKeys(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateNativeEnvelopeKeys([]byte(`{"document":{}}`), "document"))
	require.Error(t, validateNativeEnvelopeKeys([]byte(`{"Document":{}}`), "document"))
	require.Error(t, validateNativeEnvelopeKeys([]byte(`{"document":{},"document":{}}`), "document"))
}

// TestNativeReportPrepareUsesPrivateSnapshot prepares without mutating the candidate.
//
// Example: A mismatched captured log produces no prepared document.
func TestNativeReportPrepareUsesPrivateSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "candidate.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, session_id TEXT, database_id TEXT, generation INTEGER, ledger_present INTEGER, forecasts_present INTEGER);
CREATE TABLE sections(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE records(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE forecasts(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT, revision INTEGER);
INSERT INTO ledger_meta VALUES(1, ?, '0123456789abcdef0123456789abcdef', 0, 1, 0)`, filepath.Base(dir))
	require.NoError(t, err)
	require.NoError(t, db.Close())
	document := json.RawMessage(`{"timestamp":"2026-11-02T15:24:00Z","state":"Working","progress":[],"decisions":[],"stuck_work_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}`)
	request := nativeReportPreparation{CandidatePath:path, SessionDir:dir, Event:"report_create", Document:document, Decisions:map[string]string{}, Log:json.RawMessage(`{"present":true,"device":"1","inode":"1","bytes_base64":""}`)}
	input, err := json.Marshal(request)
	require.NoError(t, err)
	var out, diagnostics bytes.Buffer
	code := run([]string{"report-prepare","--stdin"}, bytes.NewReader(input), &out, &diagnostics)
	require.Equal(t, 1, code)
	require.Empty(t, out.String())
	request.Log = json.RawMessage(`{"present":false,"device":"","inode":"","bytes_base64":""}`)
	input, err = json.Marshal(request)
	require.NoError(t, err)
	out.Reset(); diagnostics.Reset()
	code = run([]string{"report-prepare","--stdin"}, bytes.NewReader(input), &out, &diagnostics)
	require.Equal(t, 0, code, diagnostics.String())
	var result struct { Document json.RawMessage `json:"document"` }
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.NotEmpty(t, result.Document)
	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	var count, generation int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM report`).Scan(&count))
	require.NoError(t, db.QueryRow(`SELECT generation FROM ledger_meta WHERE id=1`).Scan(&generation))
	require.Zero(t, count)
	require.Zero(t, generation)
}
