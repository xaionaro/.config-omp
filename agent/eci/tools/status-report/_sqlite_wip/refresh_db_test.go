package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestReplayJSONRoundTrip preserves unknown content and exact numeric identities.
//
// Example: A task ID of 1.10 remains distinct from 1.1 after projection.
func TestReplayJSONRoundTrip(t *testing.T) {
	t.Parallel()
	original := []byte(`{"unknown":{"n":1.10,"large":123456789012345678901234567890,"huge":1e400,"empty":[],"null":null,"string":"null"},"other":1e0}`)
	yamlBytes, err := reportJSONToReplayYAML(original)
	if err != nil { t.Fatal(err) }
	actual, err := replayYAMLToReportJSON(yamlBytes)
	if err != nil { t.Fatal(err) }
	before, err := decodeReportValue(original)
	if err != nil { t.Fatal(err) }
	after, err := decodeReportValue(actual)
	if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(before, after) { t.Fatalf("typed values changed: before %s after %s", original, actual) }
	value := after.(map[string]any)["unknown"].(map[string]any)["n"]
	if value != json.Number("1.10") { t.Fatalf("numeric lexeme lost: %#v", value) }
}

// TestPublishDBReportRejectsChangedLogAndGeneration protects concurrent edits.
//
// Example: An append after capture leaves the report and cursor unchanged.
func TestPublishDBReportRejectsChangedLogAndGeneration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, generation INTEGER, database_id TEXT);
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT, revision INTEGER);
INSERT INTO ledger_meta VALUES(1, 3, '0123456789abcdef0123456789abcdef');
INSERT INTO report VALUES(1, '{"state":"before"}', 2);`)
	if err != nil { t.Fatal(err) }
	snapshot := projectionSnapshot{Report: []byte(`{"state":"before"}`), DatabaseID: "0123456789abcdef0123456789abcdef", Generation: 3, Revision: 2}
	log, err := captureReplayLog(dir); if err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte("{}\n"), 0600); err != nil { t.Fatal(err) }
	err = publishDBReport(context.Background(), db, dir, snapshot, []byte(`{"state":"after"}`), log)
	if !errors.Is(err, errRefreshRetry) { t.Fatalf("expected log conflict, got %v", err) }
	log, err = captureReplayLog(dir); if err != nil { t.Fatal(err) }
	if _, err := db.Exec(`UPDATE ledger_meta SET generation=4 WHERE id=1`); err != nil { t.Fatal(err) }
	err = publishDBReport(context.Background(), db, dir, snapshot, []byte(`{"state":"after"}`), log)
	if !errors.Is(err, errRefreshRetry) { t.Fatalf("expected generation conflict, got %v", err) }
	var document string; var revision int
	if err := db.QueryRow(`SELECT document_json,revision FROM report WHERE id=1`).Scan(&document, &revision); err != nil { t.Fatal(err) }
	if document != `{"state":"before"}` || revision != 2 { t.Fatalf("conflict changed report: %s revision %d", document, revision) }
	snapshot.Generation = 4
	if err := publishDBReport(context.Background(), db, dir, snapshot, []byte(`{"state":"after"}`), log); err != nil { t.Fatal(err) }
	if err := db.QueryRow(`SELECT document_json,revision FROM report WHERE id=1`).Scan(&document, &revision); err != nil { t.Fatal(err) }
	if document != `{"state":"after"}` || revision != 3 { t.Fatalf("publication missing: %s revision %d", document, revision) }
}

// TestDBReplayAppliesLogWithoutReadingLegacySources checks the projection boundary.
//
// Example: Replay a task owner update against the captured ledger snapshot.
func TestDBReplayAppliesLogWithoutReadingLegacySources(t *testing.T) {
	t.Parallel()
	dir := writeLinkedSources(t, linkedLedger, linkedForecasts)
	ledger, err := readSource[ledgerSource](filepath.Join(dir, "project-understanding.yaml")); if err != nil { t.Fatal(err) }
	forecasts, err := readSource[forecastSource](filepath.Join(dir, "forecasts.yaml")); if err != nil { t.Fatal(err) }
	document, err := replayYAMLToReportJSON([]byte(linkedReport)); if err != nil { t.Fatal(err) }
	value, err := decodeReportValue(document); if err != nil { t.Fatal(err) }
	root := value.(map[string]any); root["details"] = map[string]any{"opaque": json.Number("1e400")}
	document, err = json.Marshal(root); if err != nil { t.Fatal(err) }
	if err := os.Remove(filepath.Join(dir, "project-understanding.yaml")); err != nil { t.Fatal(err) }
	if err := os.Remove(filepath.Join(dir, "forecasts.yaml")); err != nil { t.Fatal(err) }
	event := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1","fields":{"owner":"selected owner"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600); err != nil { t.Fatal(err) }
	log, err := captureReplayLog(dir); if err != nil { t.Fatal(err) }
	snapshot := projectionSnapshot{Report: document, Sources: reconciliationSnapshot{ledger: ledger, ledgerPresent: true, forecasts: forecasts, forecastPresent: true}}
	result, err := projectDBReport(dir, "assignment_change", snapshot, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), log)
	if err != nil { t.Fatal(err) }
	yamlResult, err := reportJSONToReplayYAML(result); if err != nil { t.Fatal(err) }
	report, err := parseDBReport(yamlResult); if err != nil { t.Fatal(err) }
	value, err = decodeReportValue(result); if err != nil { t.Fatal(err) }
	if value.(map[string]any)["details"].(map[string]any)["opaque"] != json.Number("1e400") { t.Fatal("unknown numeric lexeme changed") }
	if report.Tasks[0].Owner != "selected owner" || report.Tasks[1].Owner == "selected owner" { t.Fatalf("unexpected task owners: %+v", report.Tasks) }
}

// TestLockedRefreshDoesNotFallbackWithoutDatabase preserves unrelated files.
//
// Example: A missing database cannot cause a legacy YAML report to be edited.
func TestLockedRefreshDoesNotFallbackWithoutDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "latest-status-report.yaml")
	if err := os.WriteFile(path, []byte(linkedReport), 0600); err != nil { t.Fatal(err) }
	var errorsOut bytes.Buffer
	if code := runRefreshDBLocked([]string{dir, "requirement_change"}, &errorsOut); code != 1 { t.Fatalf("expected missing database error, got %d: %s", code, errorsOut.String()) }
	result, err := os.ReadFile(path); if err != nil { t.Fatal(err) }
	if string(result) != linkedReport { t.Fatal("legacy report changed without a database") }
}

// TestLockedRefreshRejectsLinkedDatabase verifies mutable files are exclusive.
//
// Example: A hard-linked database must not be opened for refresh.
func TestLockedRefreshRejectsLinkedDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	database := filepath.Join(dir, "ledger.db")
	if err := os.WriteFile(database, []byte("original"), 0600); err != nil { t.Fatal(err) }
	if err := os.Link(database, filepath.Join(dir, "other-link")); err != nil { t.Fatal(err) }
	var diagnostics bytes.Buffer
	if code := runRefreshDBLocked([]string{dir, "decision"}, &diagnostics); code != 1 { t.Fatalf("expected linked database rejection, got %d", code) }
	data, err := os.ReadFile(database); if err != nil { t.Fatal(err) }
	if string(data) != "original" { t.Fatalf("database changed: %q", data) }
	if !strings.Contains(diagnostics.String(), "exclusive regular file") { t.Fatalf("missing exclusivity diagnostic: %s", diagnostics.String()) }
}

// TestLockedDBReplayPreservesOpaqueNumbers exercises real database publication.
//
// Example: Updating 1.10 leaves 1.1 and an opaque 1e400 unchanged.
func TestLockedDBReplayPreservesOpaqueNumbers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db")); if err != nil { t.Fatal(err) }; defer db.Close()
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY,generation INTEGER,database_id TEXT,session_id TEXT,ledger_present INTEGER,forecasts_present INTEGER);
CREATE TABLE report(id INTEGER PRIMARY KEY,document_json TEXT,revision INTEGER);
CREATE TABLE sections(id TEXT,value TEXT); CREATE TABLE records(id TEXT,value TEXT); CREATE TABLE forecasts(id TEXT,value TEXT);
INSERT INTO ledger_meta VALUES(1,0,'0123456789abcdef0123456789abcdef',?,1,1);`, filepath.Base(dir))
	if err != nil { t.Fatal(err) }
	var ledger, forecasts yaml.Node
	if err := yaml.Unmarshal([]byte(linkedLedger), &ledger); err != nil { t.Fatal(err) }
	if err := yaml.Unmarshal([]byte(linkedForecasts), &forecasts); err != nil { t.Fatal(err) }
	for _, source := range []struct{ query string; root *yaml.Node; field string }{
		{"INSERT INTO sections VALUES(?,?)", ledger.Content[0], "sections"},
		{"INSERT INTO records VALUES(?,?)", ledger.Content[0], "records"},
		{"INSERT INTO forecasts VALUES(?,?)", forecasts.Content[0], "records"},
	} {
		rows, err := replayJSONValue(mapValue(source.root, source.field)); if err != nil { t.Fatal(err) }
		for id, row := range rows.(map[string]any) {
			value, err := json.Marshal(row); if err != nil { t.Fatal(err) }
			if _, err := db.Exec(source.query, id, string(value)); err != nil { t.Fatal(err) }
		}
	}
	input := strings.Replace(linkedReport, "top_priorities:", "  - {id: 1.10, parent_id: '1', title: Ten, priority: high, impact: Useful, effort: {min_minutes: 1, max_minutes: 2, kind: elapsed, basis: guess}, status: NEW}\n  - {id: 1.1, parent_id: '1', title: One, priority: high, impact: Useful, effort: {min_minutes: 1, max_minutes: 2, kind: elapsed, basis: guess}, status: NEW}\ntop_priorities:", 1)
	document, err := replayYAMLToReportJSON([]byte(input)); if err != nil { t.Fatal(err) }
	value, err := decodeReportValue(document); if err != nil { t.Fatal(err) }
	root := value.(map[string]any)
	opaque, err := decodeReportValue([]byte(`{"nested":[1e400,0.123456789012345678901,123456789012345678901234567890,-0,1e0,1E+0,null,"null",[],{}]}`)); if err != nil { t.Fatal(err) }
	root["details"], root["owner_extension"] = opaque, opaque
	document, err = json.Marshal(root); if err != nil { t.Fatal(err) }
	if _, err := db.Exec(`INSERT INTO report VALUES(1,?,0)`, string(document)); err != nil { t.Fatal(err) }
	if _, err := parse([]byte(input + "\nowner_extension: {}\n")); err == nil { t.Fatal("legacy accepted unknown field") }
	event := `{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"task","id":"1.10","fields":{"owner":"selected"}}]}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "high_level_log.jsonl"), []byte(event), 0600); err != nil { t.Fatal(err) }
	var diagnostics bytes.Buffer
	lock, err := os.OpenFile(filepath.Join(dir, ".eci-session.lock"), os.O_CREATE|os.O_RDWR, 0600); if err != nil { t.Fatal(err) }
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil { t.Fatal(err) }
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if code := runRefreshDBLocked([]string{dir,"assignment_change"}, &diagnostics); code != 0 { t.Fatalf("refresh %d: %s", code, diagnostics.String()) }
	var result string
	if err := db.QueryRow(`SELECT document_json FROM report WHERE id=1`).Scan(&result); err != nil { t.Fatal(err) }
	value, err = decodeReportValue([]byte(result)); if err != nil { t.Fatal(err) }; root = value.(map[string]any)
	if !reflect.DeepEqual(root["details"], opaque) || !reflect.DeepEqual(root["owner_extension"], opaque) { t.Fatal("opaque values changed") }
	tasks := root["tasks"].([]any); selected := tasks[2].(map[string]any); other := tasks[3].(map[string]any)
	if selected["id"] != json.Number("1.10") || selected["owner"] != "selected" || other["id"] != json.Number("1.1") || other["owner"] == "selected" { t.Fatalf("incorrect numeric selection: %v %v", selected, other) }
}

// TestDBScalarValidationAcceptsLegacyTimestamps checks string-compatible YAML.
//
// Example: An unquoted as_of timestamp remains valid but a numeric owner does not.
func TestDBScalarValidationAcceptsLegacyTimestamps(t *testing.T) {
	t.Parallel()
	valid := []byte("priority_estimates: [{as_of: 2026-11-02T15:24:00Z}]\ntasks: [{owner: person}]\n")
	if err := validateDBReportScalars(valid); err != nil { t.Fatal(err) }
	invalid := []byte("priority_estimates: [{as_of: 2026-11-02T15:24:00Z}]\ntasks: [{owner: 7}]\n")
	if err := validateDBReportScalars(invalid); err == nil { t.Fatal("numeric owner accepted") }
}
