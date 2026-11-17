package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// reportJSONToReplayYAML presents a typed JSON report to the existing replay engine.
//
// Example: Preserve a numeric task identifier while replaying a correction.
func reportJSONToReplayYAML(data []byte) ([]byte, error) {
	value, err := decodeReportValue(data)
	if err != nil { return nil, err }
	node, err := replayYAMLNode(value)
	if err != nil { return nil, err }
	return yaml.Marshal(node)
}

// replayYAMLNode constructs YAML nodes without converting JSON number tokens.
//
// Example: Keep 1.10 as a numeric scalar rather than a rounded float.
func replayYAMLNode(value any) (*yaml.Node, error) {
	switch v := value.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}, nil
	case json.Number:
		tag := "!!int"
		switch {
		case bytes.ContainsAny([]byte(v), ".eE"):
			tag = "!!float"
		default:
			if _, err := strconv.ParseInt(string(v), 10, 64); err != nil {
				if _, err := strconv.ParseUint(string(v), 10, 64); err != nil { tag = "!!float" }
			}
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(v)}, nil
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range v {
			child, err := replayYAMLNode(item); if err != nil { return nil, err }; n.Content = append(n.Content, child)
		}
		return n, nil
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := make([]string, 0, len(v)); for key := range v { keys = append(keys, key) }; sort.Strings(keys)
		for _, key := range keys {
			child, err := replayYAMLNode(v[key]); if err != nil { return nil, err }
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
		}
		return n, nil
	default:
		return nil, fmt.Errorf("unsupported report value %T", value)
	}
}

// replayYAMLToReportJSON converts the projected tree back without float conversion.
//
// Example: Store unknown report fields alongside updated replay state.
func replayYAMLToReportJSON(data []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil { return nil, err }
	if len(doc.Content) != 1 { return nil, fmt.Errorf("invalid report document") }
	value, err := replayJSONValue(doc.Content[0]); if err != nil { return nil, err }
	return json.Marshal(value)
}

// replayJSONValue translates only JSON-compatible YAML nodes into typed values.
//
// Example: A null scalar remains distinct from the string null.
func replayJSONValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]; if key.Kind != yaml.ScalarNode || key.Tag != "!!str" { return nil, fmt.Errorf("non-string report key") }
			if _, exists := out[key.Value]; exists { return nil, fmt.Errorf("duplicate report key %q", key.Value) }
			value, err := replayJSONValue(n.Content[i+1]); if err != nil { return nil, err }; out[key.Value] = value
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content)); for _, child := range n.Content { value, err := replayJSONValue(child); if err != nil { return nil, err }; out = append(out, value) }; return out, nil
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!timestamp": return n.Value, nil
		case "!!null": return nil, nil
		case "!!bool": return strconv.ParseBool(n.Value)
		case "!!int", "!!float":
			if !json.Valid([]byte(n.Value)) { return nil, fmt.Errorf("non-JSON number %q", n.Value) }; return json.Number(n.Value), nil
		}
	}
	return nil, fmt.Errorf("unsupported projected YAML node %s", n.Tag)
}

// runRefreshDBLocked refreshes a database report under the caller's session lock.
//
// Example: Replay a just-appended event without recursively acquiring the lock.
func runRefreshDBLocked(args []string, errOut io.Writer) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		if err := writeRefreshDiagnostic(errOut, "usage: status-report refresh-db-locked SESSION_DIR EVENT\n"); err != nil { return 1 }; return 2
	}
	if err := refreshDBLocked(context.Background(), args[0], args[1], time.Now().UTC()); err != nil {
		if writeErr := writeRefreshDiagnostic(errOut, fmt.Sprintln("status-report refresh:", err)); writeErr != nil { return 1 }; return 1
	}
	return 0
}

// refreshDBLocked captures coherent sources and publishes the projected report.
//
// Example: Reconcile a report and its replay cursor in one database transaction.
func refreshDBLocked(ctx context.Context, dir, event string, evaluation time.Time) (_err error) {
	if err := guardSQLiteFiles(filepath.Join(dir, "ledger.db")); err != nil { return err }
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "ledger.db")}; q := u.Query(); q.Set("mode", "rw"); q.Add("_pragma", "busy_timeout(5000)"); u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String()); if err != nil { return err }; defer func() { _err = errors.Join(_err, db.Close()) }()
	var version int; if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil { return err }; if version != 1 { return fmt.Errorf("unsupported ledger version %d", version) }
	snapshot, err := loadProjectionSnapshot(ctx, db, dir); if err != nil { return err }
	if !snapshot.ReportPresent { return fmt.Errorf("report is absent") }
	log, err := captureReplayLog(dir); if err != nil { return err }
	candidate, projectionErr := projectDBReport(dir, event, snapshot, evaluation, log)
	if errors.Is(projectionErr, errRefreshRetry) { return projectionErr }
	if projectionErr != nil {
		candidate, err = invalidateDBReport(snapshot.Report, event, projectionErr)
		if err != nil { return errors.Join(projectionErr, fmt.Errorf("could not prepare diagnostic: %w", err)) }
	}
	if err := publishDBReport(ctx, db, dir, snapshot, candidate, log); err != nil { return errors.Join(projectionErr, err) }
	return projectionErr
}

// replayLogSnapshot records both file identity and exact bytes at replay start.
//
// Example: Detect replacement even when a new log has identical content.
type replayLogSnapshot struct { Info os.FileInfo; Bytes []byte }

// captureReplayLog reads the external log without conflating absence and empty content.
//
// Example: A subsequently created empty log invalidates an absent snapshot.
func captureReplayLog(dir string) (_ replayLogSnapshot, _err error) {
	var result replayLogSnapshot
	f, err := os.Open(filepath.Join(dir, "high_level_log.jsonl")); if errors.Is(err, os.ErrNotExist) { return result, nil }; if err != nil { return result, err }
	defer func() { _err = errors.Join(_err, f.Close()) }()
	result.Info, err = f.Stat(); if err != nil { return result, err }; if !result.Info.Mode().IsRegular() { return result, fmt.Errorf("log is not a regular file") }
	result.Bytes, err = io.ReadAll(f); return result, err
}

// verifyReplayLog rejects any identity, presence, or content change.
//
// Example: A concurrent append requires a fresh replay.
func verifyReplayLog(dir string, original replayLogSnapshot) error {
	current, err := captureReplayLog(dir); if err != nil { return err }
	if (original.Info == nil) != (current.Info == nil) { return errRefreshRetry }
	if original.Info != nil && (!os.SameFile(original.Info, current.Info) || !bytes.Equal(original.Bytes, current.Bytes)) { return errRefreshRetry }
	return nil
}

// publishDBReport compares the coherent snapshot and commits report and cursor together.
//
// Example: A concurrent owner edit prevents overwriting its report revision.
func publishDBReport(ctx context.Context, db *sql.DB, dir string, snapshot projectionSnapshot, candidate []byte, log replayLogSnapshot) (_err error) {
	before, err := decodeReportValue(snapshot.Report); if err != nil { return err }
	after, err := decodeReportValue(candidate); if err != nil { return err }
	conn, err := db.Conn(ctx); if err != nil { return err }; defer func() { _err = errors.Join(_err, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil { return err }
	committed := false
	defer func() { if !committed { _, e := conn.ExecContext(context.Background(), "ROLLBACK"); _err = errors.Join(_err, e); if e != nil { discard := conn.Raw(discardReportConnection); if !errors.Is(discard, driver.ErrBadConn) { _err = errors.Join(_err, discard) } } } }()
	var generation, revision int64
	var databaseID string
	if err := conn.QueryRowContext(ctx, "SELECT database_id,generation FROM ledger_meta WHERE id=1").Scan(&databaseID,&generation); err != nil { return err }
	if err := conn.QueryRowContext(ctx, "SELECT revision FROM report WHERE id=1").Scan(&revision); err != nil { return err }
	if databaseID != snapshot.DatabaseID || generation != snapshot.Generation || revision != snapshot.Revision { return errRefreshRetry }
	if err := verifyReplayLog(dir, log); err != nil { return err }
	if !reflect.DeepEqual(before, after) {
		result, err := conn.ExecContext(ctx, "UPDATE report SET document_json=?, revision=revision+1 WHERE id=1 AND revision=?", string(candidate), revision); if err != nil { return err }; count, err := result.RowsAffected(); if err != nil { return err }; if count != 1 { return errRefreshRetry }
		result, err = conn.ExecContext(ctx, "UPDATE ledger_meta SET generation=generation+1 WHERE id=1 AND database_id=? AND generation=?", databaseID,generation); if err != nil { return err }; count, err = result.RowsAffected(); if err != nil { return err }; if count != 1 { return errRefreshRetry }
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil { return err }; committed = true; return nil
}

// dbReportProjection is published only after all replay and reconciliation checks succeed.
type dbReportProjection struct {
	Document []byte
	Receipts []syncOperationLocator
}

// projectDBReport produces a report for callers that do not require receipts.
func projectDBReport(dir, event string, snapshot projectionSnapshot, evaluation time.Time, log replayLogSnapshot) ([]byte, error) {
	result, err := projectDBReportResult(dir, event, snapshot, evaluation, log, false)
	return result.Document, err
}

// projectDBReportResult carries verified receipts together with the complete projection.
func projectDBReportResult(dir, event string, snapshot projectionSnapshot, evaluation time.Time, log replayLogSnapshot, collectReceipts bool) (dbReportProjection, error) {
	original, err := reportJSONToReplayYAML(snapshot.Report); if err != nil { return dbReportProjection{}, err }
	if _, err := parseDBReport(original); err != nil { return dbReportProjection{}, fmt.Errorf("invalid report: %w", err) }
	candidate := original
	if event == "requirement_change" {
		candidate, err = prepareInvalidation(candidate, refreshDiagnostic); if err != nil { return dbReportProjection{}, err }
	}
	ledger, forecasts := snapshot.Sources.ledger, snapshot.Sources.forecasts
	if err := validateProjectionEvidence(snapshot.Sources); err != nil { return dbReportProjection{}, err }
	replayed, err := projectLogStatusResult(dir, candidate, ledger, forecasts, collectReceipts); if err != nil { return dbReportProjection{}, err }
	if (log.Info == nil) != (replayed.Log == nil) || !bytes.Equal(log.Bytes, replayed.Log) { return dbReportProjection{}, errRefreshRetry }
	if err := verifyReplayLog(dir, log); err != nil { return dbReportProjection{}, err }
	candidate = replayed.Document
	var doc yaml.Node
	if err := yaml.Unmarshal(candidate, &doc); err != nil { return dbReportProjection{}, err }
	if len(doc.Content) != 1 { return dbReportProjection{}, fmt.Errorf("invalid report candidate") }
	report, err := parseDBReport(candidate); if err != nil { return dbReportProjection{}, err }
	edits, err := collectForecastEdits(candidate, doc.Content[0], ledger, forecasts, &flowDocument{data: candidate}, reportLaneRefs(report)); if err != nil { return dbReportProjection{}, err }
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
	updated := append([]byte(nil), candidate...)
	for _, e := range edits { updated = append(append(append([]byte(nil), updated[:e.Start]...), []byte(e.Value)...), updated[e.End:]...) }
	report, err = parseDBReport(updated); if err != nil { return dbReportProjection{}, fmt.Errorf("refreshed report invalid: %w", err) }
	issues := reconcileSnapshotAt(report, snapshot.Sources, evaluation); sort.Strings(issues)
	for _, issue := range issues {
		if issue == "report declares scope incomplete" { continue }
		updated, err = prepareInvalidation(updated, refreshIssuePrefix+issue); if err != nil { return dbReportProjection{}, err }
	}
	if err := verifyRefreshDocument(candidate, updated, ledger, forecasts, true); err != nil { return dbReportProjection{}, fmt.Errorf("unsafe refresh: %w", err) }
	updated, err = refreshOwnedForecasts(candidate, updated); if err != nil { return dbReportProjection{}, err }
	document, err := replayYAMLToReportJSON(updated); if err != nil { return dbReportProjection{}, err }
	return dbReportProjection{Document:document, Receipts:replayed.Receipts}, nil
}

// invalidateDBReport preserves the report while recording a failed projection.
//
// Example: Mark a source reconciliation failure without advancing the cursor.
func invalidateDBReport(original []byte, event string, projectionErr error) ([]byte, error) {
	data, err := reportJSONToReplayYAML(original); if err != nil { return nil, err }
	if event == "requirement_change" { data, err = prepareInvalidation(data, refreshDiagnostic); if err != nil { return nil, err } }
	data, err = prepareInvalidation(data, refreshIssuePrefix+projectionErr.Error()); if err != nil { return nil, err }
	return replayYAMLToReportJSON(data)
}
