package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReportDBRouting exercises session reports and external inputs against DB sources.
//
// Example: Rendering stdin retains its content while reconciling database sources.
func TestReportDBRouting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := `{"schema":"ledger-activation/v1","database_id":"0123456789abcdef0123456789abcdef","legacy":{"project-understanding.yaml":null,"forecasts.yaml":null,"latest-status-report.yaml":null}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ledger-activation.json"), []byte(marker), 0600))
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, generation INTEGER, database_id TEXT, ledger_present INTEGER, forecasts_present INTEGER);
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT, revision INTEGER);
CREATE TABLE sections(id TEXT, value TEXT);
CREATE TABLE records(id TEXT, value TEXT);
CREATE TABLE forecasts(id TEXT, value TEXT);
INSERT INTO ledger_meta VALUES(1, 0, '0123456789abcdef0123456789abcdef', 1, 0);
INSERT INTO report VALUES(1, '{"timestamp":"2026-11-02T15:24:00Z","state":"Database state","progress":[],"decisions":[],"stuck_work_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}', 0);`)
	require.NoError(t, err)
	instant := time.Date(2026, 11, 2, 15, 25, 0, 0, time.UTC)
	var out, diagnostics bytes.Buffer
	code := runAt([]string{"render", "--session-dir", dir}, bytes.NewBufferString("not a report"), &out, &diagnostics, instant)
	require.Equal(t, 0, code, diagnostics.String())
	require.Contains(t, out.String(), "Database state")
	require.NotContains(t, out.String(), "not a report")
	out.Reset()
	diagnostics.Reset()
	input := `{"timestamp":"2026-11-02T15:24:00Z","state":"Input state","progress":[],"decisions":[],"stuck_work_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}`
	code = runAt([]string{"render", "--session-dir", dir, "-"}, bytes.NewBufferString(input), &out, &diagnostics, instant)
	require.Equal(t, 0, code, diagnostics.String())
	require.Contains(t, out.String(), "Input state")
	require.NotContains(t, out.String(), "ledger unavailable")
	external := filepath.Join(dir, "external.json")
	require.NoError(t, os.WriteFile(external, []byte(input), 0600))
	out.Reset()
	diagnostics.Reset()
	code = runAt([]string{"render", "--session-dir", dir, external}, bytes.NewReader(nil), &out, &diagnostics, instant)
	require.Equal(t, 0, code, diagnostics.String())
	require.Contains(t, out.String(), "Input state")
	require.NotContains(t, out.String(), "ledger unavailable")
	_, err = db.Exec(`DELETE FROM report`)
	require.NoError(t, err)
	out.Reset()
	diagnostics.Reset()
	code = runAt([]string{"render", "--session-dir", dir}, bytes.NewReader(nil), &out, &diagnostics, instant)
	require.Equal(t, 1, code)
	require.Empty(t, out.String())
	require.Contains(t, diagnostics.String(), "absent")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ledger-activation.json"), []byte(`{}`), 0600))
	diagnostics.Reset()
	code = runAt([]string{"render", "--session-dir", dir}, bytes.NewReader(nil), &out, &diagnostics, instant)
	require.Equal(t, 1, code)
	require.Empty(t, out.String())
}

// TestReportLegacyRouting retains canonical file rendering before activation.
//
// Example: A legacy report still reads its YAML file without a database.
func TestReportLegacyRouting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	report := `timestamp: '2026-11-02T15:24:00Z'
state: Legacy state
progress: []
decisions: []
stuck_work_risks: []
verification: []
next_focus: Next
scope_complete: true
tasks: []
top_priorities: []
completion_estimate: {min_minutes: 10, max_minutes: 20, as_of: '2026-11-02T15:24:00Z', basis: guess, assumptions: none, dependencies: none, critical_path: work}
`
	require.NoError(t, os.WriteFile(path, []byte(report), 0600))
	var out, diagnostics bytes.Buffer
	code := runAt([]string{"render", path}, bytes.NewReader(nil), &out, &diagnostics, time.Date(2026, 11, 2, 15, 25, 0, 0, time.UTC))
	require.Equal(t, 0, code, diagnostics.String())
	require.Contains(t, out.String(), "Legacy state")
	require.NotContains(t, out.String(), "Database state")
}
