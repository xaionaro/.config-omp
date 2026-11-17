package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// TestReportEditOperations preserves unrelated typed fields and rejects conflicting edits.
//
// Example: An owner can replace an explicit null without touching sync state.
func TestReportEditOperations(t *testing.T) {
	t.Parallel()
	original := []byte(`{"state":null,"details":{"count":12345678901234567890},"status_sync":{"cursor":4}}`)
	var edit reportEdit
	require.NoError(t, json.Unmarshal([]byte(`{"ops":[{"op":"set","path":["state"],"expect":{"kind":"null"},"value":"ready"},{"op":"set","path":["details","new"],"expect":{"kind":"absent"},"value":false}]}`), &edit))
	updated, err := applyReportEdit(original, edit)
	require.NoError(t, err)
	actual, err := decodeReportValue(updated)
	require.NoError(t, err)
	require.Equal(t, json.Number("12345678901234567890"), actual.(map[string]any)["details"].(map[string]any)["count"])
	require.Equal(t, "ready", actual.(map[string]any)["state"])
	require.Equal(t, false, actual.(map[string]any)["details"].(map[string]any)["new"])
	require.Equal(t, json.Number("4"), actual.(map[string]any)["status_sync"].(map[string]any)["cursor"])

	var conflict reportEdit
	require.NoError(t, json.Unmarshal([]byte(`{"ops":[{"op":"delete","path":["state"],"expect":{"kind":"absent"}}]}`), &conflict))
	_, err = applyReportEdit(updated, conflict)
	require.Error(t, err)

	var syncEdit reportEdit
	require.NoError(t, json.Unmarshal([]byte(`{"ops":[{"op":"delete","path":["status_sync"],"expect":{"kind":"value","value":{"cursor":4}}}]}`), &syncEdit))
	_, err = applyReportEdit(updated, syncEdit)
	require.Error(t, err)
}

// TestReportEditArrayPaths changes one member without altering its siblings.
//
// Example: Update an owner field inside a task list.
func TestReportEditArrayPaths(t *testing.T) {
	t.Parallel()
	var edit reportEdit
	require.NoError(t, json.Unmarshal([]byte(`{"ops":[{"op":"set","path":["tasks","0","owner"],"expect":{"kind":"value","value":"A"},"value":"B"},{"op":"delete","path":["tasks","1"],"expect":{"kind":"value","value":{"owner":"C"}}}]}`), &edit))
	result, err := applyReportEdit([]byte(`{"tasks":[{"owner":"A"},{"owner":"C"}]}`), edit)
	require.NoError(t, err)
	require.JSONEq(t, `{"tasks":[{"owner":"B"}]}`, string(result))
	var appendEdit reportEdit
	require.NoError(t, json.Unmarshal([]byte(`{"ops":[{"op":"set","path":["tasks","1"],"expect":{"kind":"absent"},"value":{"owner":"D"}}]}`), &appendEdit))
	result, err = applyReportEdit(result, appendEdit)
	require.NoError(t, err)
	require.JSONEq(t, `{"tasks":[{"owner":"B"},{"owner":"D"}]}`, string(result))
}

// TestReportEditNumericIdentity preserves numeric spelling and rejects duplicate keys.
//
// Example: An expected 1.0 does not match a stored 1.
func TestReportEditNumericIdentity(t *testing.T) {
	t.Parallel()
	var edit reportEdit
	require.NoError(t, json.Unmarshal([]byte(`{"ops":[{"op":"set","path":["n"],"expect":{"kind":"value","value":1.0},"value":2}]}`), &edit))
	_, err := applyReportEdit([]byte(`{"n":1}`), edit)
	require.Error(t, err)
	_, err = applyReportEdit([]byte(`{"n":1,"n":1.0}`), edit)
	require.Error(t, err)
}

// TestReportEditDistinguishesJSONKinds preserves exact owner expectations.
//
// Example: Numeric 1.0, string "1.0", null and absence are different.
func TestReportEditDistinguishesJSONKinds(t *testing.T) {
	t.Parallel()
	original := []byte(`{"value":1.0,"text":"1.0","empty":[],"unknown":{"number":1e400},"status_sync":{"cursor":7}}`)
	edit, err := parseReportEdit(`{"ops":[{"op":"set","path":["value"],"expect":{"kind":"value","value":1.0},"value":null},{"op":"set","path":["new"],"expect":{"kind":"absent"},"value":"created"}]}`)
	require.NoError(t, err)
	updated, err := applyReportEdit(original, edit)
	require.NoError(t, err)
	got, err := decodeReportValue(updated)
	require.NoError(t, err)
	root := got.(map[string]any)
	require.Nil(t, root["value"])
	require.Equal(t, "1.0", root["text"])
	require.Empty(t, root["empty"])
	require.Equal(t, json.Number("1e400"), root["unknown"].(map[string]any)["number"])
	require.Equal(t, json.Number("7"), root["status_sync"].(map[string]any)["cursor"])
	for _, expectation := range []string{`{"kind":"absent"}`, `{"kind":"value","value":"1.0"}`, `{"kind":"value","value":1e0}`} {
		conflict, err := parseReportEdit(`{"ops":[{"op":"delete","path":["value"],"expect":` + expectation + `}]}`)
		require.NoError(t, err)
		_, err = applyReportEdit(original, conflict)
		require.Error(t, err)
	}
}

// TestReportEditWireRoundTrip preserves omitted fields and explicit null values.
//
// Example: A typed delete remains valid while a typed null set stays null.
func TestReportEditWireRoundTrip(t *testing.T) {
	t.Parallel()
	edit := reportEdit{Ops: []reportEditOperation{
		{Op: "delete", Path: []string{"remove"}, Expect: reportEditExpectation{Kind: "value", Value: json.RawMessage(`"old"`)}},
		{Op: "set", Path: []string{"create"}, Expect: reportEditExpectation{Kind: "absent"}, Value: json.RawMessage(`null`)},
		{Op: "set", Path: []string{"null"}, Expect: reportEditExpectation{Kind: "null"}, Value: json.RawMessage(`"updated"`)},
	}}
	wire, err := json.Marshal(edit)
	require.NoError(t, err)
	decoded, err := parseReportEdit(string(wire))
	require.NoError(t, err)
	result, err := applyReportEdit([]byte(`{"remove":"old","null":null}`), decoded)
	require.NoError(t, err)
	require.JSONEq(t, `{"create":null,"null":"updated"}`, string(result))
	invalid, err := parseReportEdit(`{"ops":[{"op":"delete","path":["remove"],"expect":{"kind":"value","value":"old"},"value":null}]}`)
	require.NoError(t, err)
	_, err = applyReportEdit([]byte(`{"remove":"old"}`), invalid)
	require.Error(t, err)
	for _, invalidOp := range []reportEditOperation{
		{Op: "delete", Path: []string{"remove"}, Expect: reportEditExpectation{Kind: "value", Value: json.RawMessage(`"old"`)}, Value: json.RawMessage(`null`)},
		{Op: "delete", Path: []string{"remove"}, Expect: reportEditExpectation{Kind: "absent", Value: json.RawMessage(`null`)}},
		{Op: "set", Path: []string{"remove"}, Expect: reportEditExpectation{Kind: "value", Value: json.RawMessage(`null`)}, Value: json.RawMessage(`null`)},
	} {
		wire, err := json.Marshal(reportEdit{Ops: []reportEditOperation{invalidOp}})
		require.NoError(t, err)
		decoded, err := parseReportEdit(string(wire))
		require.NoError(t, err)
		_, err = applyReportEdit([]byte(`{"remove":"old"}`), decoded)
		require.Error(t, err)
	}
}

// TestNativeReportBaseValidation requires a complete, canonical observation.
//
// Example: A generation with leading zeroes is not a valid base.
func TestNativeReportBaseValidation(t *testing.T) {
	t.Parallel()
	valid := `{"database_id":"0123456789abcdef0123456789abcdef","generation":"42"}`
	base, err := decodeNativeReportBase(json.RawMessage(valid))
	require.NoError(t, err)
	require.Equal(t, int64(42), base.Generation)
	for _, raw := range []string{
		`null`, `{}`, `{"database_id":"0123456789abcdef0123456789abcdef","generation":42}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"01"}`,
		`{"database_id":"0123456789abcdef0123456789abcdeF","generation":"0"}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"-1"}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"9223372036854775808"}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"0","extra":1}`,
		`{"database_id":"0123456789abcdef0123456789abcdef","generation":"0","generation":"0"}`,
	} {
		_, err := decodeNativeReportBase(json.RawMessage(raw))
		require.Error(t, err, raw)
	}
}

// TestReportEditTransaction checks atomic compare-and-swap and no-op behavior.
//
// Example: A stale database identity cannot authorize an otherwise valid edit.
func TestReportEditTransaction(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT NOT NULL, revision INTEGER NOT NULL);
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, database_id TEXT NOT NULL, session_id TEXT NOT NULL, generation INTEGER NOT NULL);
INSERT INTO ledger_meta VALUES(1, '0123456789abcdef0123456789abcdef', 'session', 3);
INSERT INTO report VALUES(1, '{"timestamp":"2026-11-02T15:24:00Z","state":"old","progress":[],"decisions":[],"blockers_risks":[],"verification":[],"next_focus":"next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"},"status_sync":{"cursor":7,"applied":{},"latest":{},"pending":{"p":{"line":1,"update":{}}}}}', 2)`)
	require.NoError(t, err)
	base := nativeReportBase{DatabaseID: "0123456789abcdef0123456789abcdef", Generation: 3}
	edit, err := parseReportEdit(`{"ops":[{"op":"set","path":["state"],"expect":{"kind":"value","value":"old"},"value":"new"}]}`)
	require.NoError(t, err)
	wrong := base
	wrong.DatabaseID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	require.Error(t, editReportDB(context.Background(), db, "session", &wrong, edit))
	require.Error(t, editReportDB(context.Background(), db, "other", &base, edit))
	stale := base
	require.NoError(t, editReportDB(context.Background(), db, "session", &base, edit))
	var revision, generation int
	var document string
	require.NoError(t, db.QueryRow(`SELECT document_json, revision FROM report WHERE id=1`).Scan(&document, &revision))
	require.NoError(t, db.QueryRow(`SELECT generation FROM ledger_meta WHERE id=1`).Scan(&generation))
	require.Contains(t, document, `"state":"new"`)
	require.Contains(t, document, `"pending":{"p":{"line":1,"update":{}}}`)
	require.Equal(t, 3, revision)
	require.Equal(t, 4, generation)
	require.Error(t, editReportDB(context.Background(), db, "session", &stale, edit))
	require.Equal(t, int64(4), base.Generation)
	noop, err := parseReportEdit(`{"ops":[{"op":"set","path":["state"],"expect":{"kind":"value","value":"new"},"value":"new"}]}`)
	require.NoError(t, err)
	require.NoError(t, editReportDB(context.Background(), db, "session", &base, noop))
	partial, err := parseReportEdit(`{"ops":[{"op":"set","path":["state"],"expect":{"kind":"value","value":"new"},"value":"partial"},{"op":"delete","path":["next_focus"],"expect":{"kind":"value","value":"wrong"}}]}`)
	require.NoError(t, err)
	require.Error(t, editReportDB(context.Background(), db, "session", &base, partial))
	require.NoError(t, db.QueryRow(`SELECT document_json, revision FROM report WHERE id=1`).Scan(&document, &revision))
	require.NoError(t, db.QueryRow(`SELECT generation FROM ledger_meta WHERE id=1`).Scan(&generation))
	require.NotContains(t, document, `"partial"`)
	require.Equal(t, 3, revision)
	require.Equal(t, 4, generation)
	_, err = db.Exec(`DELETE FROM report`)
	require.NoError(t, err)
	require.Error(t, editReportDB(context.Background(), db, "session", &base, noop))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM report`).Scan(&count))
	require.Zero(t, count)
}

// TestReportEditLockedCommand consumes the complete streamed envelope.
//
// Example: Unknown operation fields fail without changing the report.
func TestReportEditLockedCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	require.NoError(t, err)
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT NOT NULL, revision INTEGER NOT NULL);
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, database_id TEXT NOT NULL, session_id TEXT NOT NULL, generation INTEGER NOT NULL);
INSERT INTO ledger_meta VALUES(1, '0123456789abcdef0123456789abcdef', ?, 0);
INSERT INTO report VALUES(1, '{"timestamp":"2026-11-02T15:24:00Z","state":"old","progress":[],"decisions":[],"blockers_risks":[],"verification":[],"next_focus":"next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}', 0)`, filepath.Base(dir))
	require.NoError(t, err)
	require.NoError(t, db.Close())
	var diagnostics, output bytes.Buffer
	base := `"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"0"}`
	bad := `{` + base + `,"ops":[{"op":"set","path":["state"],"expect":{"kind":"value","value":"old"},"value":"new","unexpected":true}]}`
	code := runReportEditLocked([]string{dir, "--stdin"}, bytes.NewBufferString(bad), &output, &diagnostics)
	require.Equal(t, 1, code)
	require.NotEmpty(t, diagnostics.String())
	require.Empty(t, output.String())
	diagnostics.Reset()
	good := `{` + base + `,"ops":[{"op":"set","path":["state"],"expect":{"kind":"value","value":"old"},"value":"new"}]}`
	code = runReportEditLocked([]string{dir, "--stdin"}, bytes.NewBufferString(good), &output, &diagnostics)
	require.Equal(t, 0, code)
	require.Empty(t, diagnostics.String())
	require.JSONEq(t, `{"database_id":"0123456789abcdef0123456789abcdef","generation":"1"}`, output.String())
	output.Reset()
	noop := `{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"1"},"ops":[]}`
	code = runReportEditLocked([]string{dir, "--stdin"}, bytes.NewBufferString(noop), &output, &diagnostics)
	require.Equal(t, 0, code)
	require.JSONEq(t, `{"database_id":"0123456789abcdef0123456789abcdef","generation":"1"}`, output.String())
	output.Reset()
	code = runReportEditLocked([]string{dir, "--stdin"}, bytes.NewBufferString(good), &output, &diagnostics)
	require.Equal(t, 1, code)
	require.Empty(t, output.String())
}

// TestReportEditRejectsLinkedDatabase verifies that SQLite is not opened through aliases.
//
// Example: A hardlinked main database remains unchanged after a rejected edit.
func TestReportEditRejectsLinkedDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT NOT NULL, revision INTEGER NOT NULL);
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, database_id TEXT NOT NULL, session_id TEXT NOT NULL, generation INTEGER NOT NULL);
INSERT INTO ledger_meta VALUES(1, '0123456789abcdef0123456789abcdef', 'session', 0);
INSERT INTO report VALUES(1, '{"timestamp":"2026-11-02T15:24:00Z","state":"old","progress":[],"decisions":[],"blockers_risks":[],"verification":[],"next_focus":"next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}', 0)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Link(path, filepath.Join(dir, "alias")))
	var diagnostics, output bytes.Buffer
	code := runReportEditLocked([]string{dir, "--stdin"}, bytes.NewBufferString(`{"base":{"database_id":"0123456789abcdef0123456789abcdef","generation":"0"},"ops":[]}`), &output, &diagnostics)
	require.Equal(t, 1, code)
	require.NotEmpty(t, diagnostics.String())
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, after)
}
