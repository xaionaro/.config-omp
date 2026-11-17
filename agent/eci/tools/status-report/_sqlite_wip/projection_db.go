package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"gopkg.in/yaml.v3"
)


// guardSQLiteFiles refuses aliased or unsafe SQLite files before an open.
//
// Example: A WAL hardlinked into another session requires recovery.
func guardSQLiteFiles(path string) error {
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		name := path + suffix
		info, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect SQLite file %s: %w", name, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Nlink != 1 {
			return fmt.Errorf("SQLite file is not an exclusive regular file: %s", name)
		}
	}
	return nil
}


// projectionSnapshot contains all state needed for one projection.
//
// Example: Compare generation and revision before publishing a refresh.
type projectionSnapshot struct {
	Sources reconciliationSnapshot
	Report []byte
	ReportPresent bool
	DatabaseID string
	Generation int64
	Revision int64
}

// readProjectionDB reads the native session report and sources coherently.
//
// Example: Render a report from its session database.
func readProjectionDB(dir string) (_ projectionSnapshot, _err error) {
	var snapshot projectionSnapshot
	if err := guardSQLiteFiles(filepath.Join(dir, "ledger.db")); err != nil {
		return snapshot, err
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "ledger.db")}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return snapshot, err
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	return loadProjectionSnapshot(context.Background(), db, dir)
}

// loadProjectionSnapshot reads all database sources in a single read transaction.
//
// Example: Validate a report without mixing generations of its ledger and forecasts.
func loadProjectionSnapshot(ctx context.Context, db *sql.DB, dir string) (_ projectionSnapshot, _err error) {
	var result projectionSnapshot
	// The caller selects the database path; metadata binds its contents to the session.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly:true})
	if err != nil { return result, err }
	defer func() { if _err != nil { _err = errors.Join(_err, tx.Rollback()) } }()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return result, err
	}
	if version != 1 {
		return result, fmt.Errorf("unsupported ledger version %d", version)
	}
	var id, sessionID string
	var ledgerPresent, forecastsPresent bool
	if err := tx.QueryRowContext(ctx, "SELECT generation,database_id,session_id,ledger_present,forecasts_present FROM ledger_meta WHERE id=1").Scan(&result.Generation, &id, &sessionID, &ledgerPresent, &forecastsPresent); err != nil { return result, err }
	if sessionID == "" || sessionID != filepath.Base(filepath.Clean(dir)) { return result, fmt.Errorf("ledger session identity mismatch") }
	if result.Generation < 0 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) { return result, fmt.Errorf("invalid ledger metadata") }
	result.DatabaseID = id
	if err := validateProjectionSources(ctx, tx, ledgerPresent, forecastsPresent); err != nil { return result, err }
	var document string
	err = tx.QueryRowContext(ctx, "SELECT document_json,revision FROM report WHERE id=1").Scan(&document, &result.Revision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return result, err
	default:
		raw := bytes.TrimSpace([]byte(document))
		if result.Revision < 0 || len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) {
			return result, fmt.Errorf("invalid stored report")
		}
		if _, err := decodeReportValue(raw); err != nil {
			return result, fmt.Errorf("invalid stored report: %w", err)
		}
		result.ReportPresent = true
		result.Report = []byte(document)
	}
	result.Sources.ledgerPresent = ledgerPresent
	if ledgerPresent {
		result.Sources.ledger = ledgerSource{Schema:"project-understanding/v1", Sections:map[string]any{}, Records:map[string]sourceRecord{}}
		if err := loadProjectionRows(ctx, tx, "SELECT id,value FROM sections", result.Sources.ledger.Sections); err != nil { return result, err }
		if err := loadProjectionRows(ctx, tx, "SELECT id,value FROM records", result.Sources.ledger.Records); err != nil { return result, err }
	}
	result.Sources.forecastPresent = forecastsPresent
	if forecastsPresent {
		result.Sources.forecasts = forecastSource{Schema:"forecasts/v1", Records:map[string]map[string]any{}}
		if err := loadProjectionRows(ctx, tx, "SELECT id,value FROM forecasts", result.Sources.forecasts.Records); err != nil { return result, err }
	}
	if err := validateProjectionEvidence(result.Sources); err != nil { return result, err }
	if err := tx.Commit(); err != nil { return result, err }
	return result, nil
}

// loadProjectionRows decodes typed JSON rows into a source map.
//
// Example: Read every ledger record under the same transaction.
func loadProjectionRows[T any](ctx context.Context, tx *sql.Tx, query string, target map[string]T) (_err error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil { return err }
	defer func() { _err = errors.Join(_err, rows.Close()) }()
	for rows.Next() {
		var id, value string
		if err := rows.Scan(&id, &value); err != nil { return err }
		var decoded T
		raw := []byte(value)
		switch dst := any(&decoded).(type) {
		case *any:
			v, err := decodeReportValue(raw)
			if err != nil { return fmt.Errorf("row %s: %w", id, err) }
			*dst = v
		case *map[string]any:
			v, err := decodeReportValue(raw)
			if err != nil { return fmt.Errorf("row %s: %w", id, err) }
			object, ok := v.(map[string]any)
			if !ok { return fmt.Errorf("row %s: expected object", id) }
			*dst = object
		case *sourceRecord:
			if err := validateProjectionRecordShape(raw); err != nil { return fmt.Errorf("row %s: %w", id, err) }
			if err := yaml.Unmarshal(raw, dst); err != nil { return fmt.Errorf("row %s: %w", id, err) }
		default:
			return fmt.Errorf("unsupported projection row type")
		}
		target[id] = decoded
	}
	return rows.Err()
}

// validateProjectionRecordShape rejects coercion of consumed JSON source fields.
//
// Example: A numeric owner is not silently interpreted as a string.
func validateProjectionRecordShape(raw []byte) error {
	value, err := decodeReportValue(raw)
	if err != nil { return err }
	record, ok := value.(map[string]any)
	if !ok { return fmt.Errorf("record must be an object") }
	for _, key := range []string{"type", "section"} {
		if _, ok := record[key].(string); !ok { return fmt.Errorf("%s must be a string", key) }
	}
	kind := record["type"].(string)
	if kind != "work" && kind != "outcome" { return nil }
	data, ok := record["data"].(map[string]any)
	if !ok { return fmt.Errorf("record data must be an object") }
	fields := []string{"description"}
	if kind == "work" {
		fields = []string{"completed_at", "owner", "next_action", "state", "forecast_ref"}
	}
	for _, field := range fields {
		value, present := data[field]
		if !present || value == nil { continue }
		if _, ok := value.(string); !ok { return fmt.Errorf("%s must be a string", field) }
	}
	if roles, present := data["reporting_roles"]; present && roles != nil && kind == "work" {
		list, ok := roles.([]any)
		if !ok { return fmt.Errorf("reporting_roles must be an array") }
		for _, role := range list {
			if _, ok := role.(string); !ok { return fmt.Errorf("reporting_roles must contain strings") }
		}
	}
	return nil
}

// projectionRecord contains the envelope needed to validate stored references.
//
// Example: A missing linked record invalidates an activated snapshot.
type projectionRecord struct {
	Section string `json:"section"`
	Type string `json:"type"`
	Data json.RawMessage `json:"data"`
	Evidence []json.RawMessage `json:"evidence"`
	Links map[string][]string `json:"links"`
}

// validateProjectionSources rejects inconsistent presence and record envelopes.
//
// Example: A forecast row cannot exist when its source is marked absent.
func validateProjectionSources(ctx context.Context, tx *sql.Tx, ledgerPresent, forecastsPresent bool) error {
	var sections, records, forecasts int
	if err := tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM sections), (SELECT count(*) FROM records), (SELECT count(*) FROM forecasts)").Scan(&sections, &records, &forecasts); err != nil {
		return err
	}
	if (!ledgerPresent && (sections != 0 || records != 0)) || (!forecastsPresent && forecasts != 0) {
		return fmt.Errorf("source presence does not match stored rows")
	}
	sectionRows := map[string]any{}
	if err := loadProjectionRows(ctx, tx, "SELECT id,value FROM sections", sectionRows); err != nil {
		return err
	}
	envelopes := map[string]projectionRecord{}
	rows, err := tx.QueryContext(ctx, "SELECT id,value FROM records")
	if err != nil { return err }
	for rows.Next() {
		var id, value string
		var record projectionRecord
		if err := rows.Scan(&id, &value); err != nil {
			return errors.Join(err, rows.Close())
		}
		if err := json.Unmarshal([]byte(value), &record); err != nil {
			return errors.Join(err, rows.Close())
		}
		envelopes[id] = record
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil { return err }
	for id, record := range envelopes {
		if _, ok := sectionRows[record.Section]; !ok {
			return fmt.Errorf("record %s has missing section", id)
		}
		data := bytes.TrimSpace(record.Data)
		if record.Type != "context" && record.Type != "constraint" && (len(data) == 0 || data[0] != '{') {
			return fmt.Errorf("record %s data must be an object", id)
		}
		for _, evidence := range record.Evidence {
			value := bytes.TrimSpace(evidence)
			if len(value) == 0 || value[0] != '"' {
				return fmt.Errorf("record %s evidence must contain strings", id)
			}
		}
		for _, links := range record.Links {
			if links == nil {
				return fmt.Errorf("record %s links must be arrays", id)
			}
			for _, target := range links {
				if _, ok := envelopes[target]; !ok {
					return fmt.Errorf("record %s links to missing record %s", id, target)
				}
			}
		}
	}
	return nil
}
