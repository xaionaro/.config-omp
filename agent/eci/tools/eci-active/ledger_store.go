package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const (
	// ledgerVersion identifies the supported database schema.
	//
	// Example: a newer schema version is refused.
	ledgerVersion = 1
	// ledgerVersionQuery reads the database schema version.
	//
	// Example: an unversioned existing database is not initialized.
	ledgerVersionQuery = "PRAGMA user_version"
	// ledgerSchema creates the canonical version-one session store.
	//
	// Example: a newly created session starts with three empty tables.
	ledgerSchema = `
CREATE TABLE sections (id TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL CHECK (json_valid(value) AND json_type(value) IS 'object' AND json_type(value,'$.title') IS 'text')) STRICT;
CREATE TABLE records (id TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL CHECK (json_valid(value) AND json_type(value) IS 'object' AND json_type(value,'$.section') IS 'text' AND json_type(value,'$.type') IS 'text' AND json_type(value,'$.data') IS NOT NULL AND json_type(value,'$.evidence') IS 'array' AND coalesce(json_type(value,'$.links'),'object') IS 'object'), section TEXT GENERATED ALWAYS AS (json_extract(value,'$.section')) VIRTUAL, type TEXT GENERATED ALWAYS AS (json_extract(value,'$.type')) VIRTUAL) STRICT;
CREATE TABLE forecasts (id TEXT PRIMARY KEY NOT NULL CHECK (id <> '' AND id NOT GLOB '*[^a-z0-9_:-]*'), value TEXT NOT NULL CHECK (json_valid(value) AND json_type(value)='object'), kind TEXT GENERATED ALWAYS AS (json_extract(value,'$.kind')) VIRTUAL, status TEXT GENERATED ALWAYS AS (json_extract(value,'$.status')) VIRTUAL) STRICT;
CREATE TABLE ledger_meta (id INTEGER PRIMARY KEY CHECK (id=1), generation INTEGER NOT NULL CHECK (generation>=0), postactivation_mutated INTEGER NOT NULL CHECK (postactivation_mutated IN (0,1)), database_id TEXT NOT NULL, ledger_present INTEGER NOT NULL CHECK (ledger_present IN (0,1)), forecasts_present INTEGER NOT NULL CHECK (forecasts_present IN (0,1))) STRICT;
CREATE TABLE recovery_intent (id INTEGER PRIMARY KEY CHECK (id=1), session_id TEXT NOT NULL, request_id TEXT NOT NULL, database_id TEXT NOT NULL) STRICT;
CREATE TABLE report (id INTEGER PRIMARY KEY CHECK (id=1), document_json TEXT NOT NULL CHECK (json_valid(document_json) AND json_type(document_json) IS 'object'), revision INTEGER NOT NULL CHECK (revision>=0)) STRICT;
CREATE TABLE log (seq INTEGER PRIMARY KEY AUTOINCREMENT, entry TEXT NOT NULL CHECK (json_valid(entry) AND json_type(entry) IS 'object')) STRICT;
CREATE TRIGGER log_append_only_update BEFORE UPDATE ON log BEGIN SELECT RAISE(ABORT,'high-level log is append-only'); END;
CREATE TRIGGER log_append_only_delete BEFORE DELETE ON log BEGIN SELECT RAISE(ABORT,'high-level log is append-only'); END;
PRAGMA user_version=1;`
)

// ledgerOperation is one ordered change to a session store.
//
// Example: a patch checks a record's type before updating its value.
type ledgerOperation struct {
	Op     ledgerOperationKind `json:"op"`
	Table  ledgerTable         `json:"table"`
	ID     string              `json:"id"`
	Expect map[string]*string  `json:"expect,omitempty"`
	Value  json.RawMessage     `json:"value"`
}

// UnmarshalJSON reads operation fields by their exact wire names.
//
// Example: an unrelated `Op` extension cannot override the `op` field.
func (op *ledgerOperation) UnmarshalJSON(data []byte) error {
	if err := validateUniqueJSON(data); err != nil {
		return fmt.Errorf("invalid operation JSON: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode operation: %w", err)
	}
	if fields == nil {
		return fmt.Errorf("operation must be an object")
	}
	*op = ledgerOperation{}
	if value, ok := fields["op"]; ok {
		if err := json.Unmarshal(value, &op.Op); err != nil {
			return fmt.Errorf("decode operation op: %w", err)
		}
	}
	if value, ok := fields["table"]; ok {
		if err := json.Unmarshal(value, &op.Table); err != nil {
			return fmt.Errorf("decode operation table: %w", err)
		}
	}
	if value, ok := fields["id"]; ok {
		if err := json.Unmarshal(value, &op.ID); err != nil {
			return fmt.Errorf("decode operation id: %w", err)
		}
	}
	if value, ok := fields["expect"]; ok {
		if err := json.Unmarshal(value, &op.Expect); err != nil {
			return fmt.Errorf("decode operation expect: %w", err)
		}
	}
	op.Value = fields["value"]
	return nil
}

// ledgerRecord is the typed envelope checked before committing a batch.
//
// Example: links refer to other records by their stable IDs.
type ledgerRecord struct {
	Type     string              `json:"type"`
	Data     json.RawMessage     `json:"data"`
	Evidence []json.RawMessage   `json:"evidence"`
	Links    map[string][]string `json:"links"`
}

// UnmarshalJSON reads stored record envelope fields by exact key.
//
// Example: a `Type` extension cannot change the meaning of `type`.
func (r *ledgerRecord) UnmarshalJSON(data []byte) error {
	if err := validateUniqueJSON(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("record must be an object")
	}
	*r = ledgerRecord{Data: fields["data"]}
	if value, ok := fields["type"]; ok {
		if err := json.Unmarshal(value, &r.Type); err != nil {
			return fmt.Errorf("decode type: %w", err)
		}
	}
	if value, ok := fields["evidence"]; ok {
		if err := json.Unmarshal(value, &r.Evidence); err != nil {
			return fmt.Errorf("decode evidence: %w", err)
		}
	}
	if value, ok := fields["links"]; ok {
		if err := json.Unmarshal(value, &r.Links); err != nil {
			return fmt.Errorf("decode links: %w", err)
		}
	}
	return nil
}

// openLedger opens an existing versioned session store for mutation.
//
// Example: a missing active database is an error and is never recreated.
func openLedger(
	ctx context.Context,
	dir string,
) (*sql.DB, error) {
	path := filepath.Join(dir, "ledger.db")
	if err := guardSQLiteFiles(path); err != nil {
		return nil, fmt.Errorf("unsafe ledger database: %w", err)
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	q.Set("mode", "rw")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	var version int
	if err = db.QueryRowContext(ctx, ledgerVersionQuery).Scan(&version); err != nil {
		return nil, errors.Join(fmt.Errorf("read ledger version: %w", err), db.Close())
	}
	if version != ledgerVersion {
		return nil, errors.Join(fmt.Errorf("unsupported ledger version %d", version), db.Close())
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return nil, errors.Join(fmt.Errorf("enable ledger WAL: %w", err), db.Close())
	}
	return db, nil
}

// initializeLedgerSchema initializes a new store inside its import transaction.
//
// Example: a candidate import can commit schema, identity and rows together.
func initializeLedgerSchema(
	ctx context.Context,
	tx *sql.Tx,
	databaseID string,
) (string, error) {
	id := databaseID
	if id == "" {
		var identity [16]byte
		if _, err := rand.Read(identity[:]); err != nil {
			return "", fmt.Errorf("generate ledger identity: %w", err)
		}
		id = hex.EncodeToString(identity[:])
	} else {
		identity, err := hex.DecodeString(id)
		if err != nil || len(identity) != 16 || hex.EncodeToString(identity) != id {
			return "", fmt.Errorf("invalid database identity")
		}
	}
	if _, err := tx.ExecContext(ctx, ledgerSchema); err != nil {
		return "", fmt.Errorf("initialize ledger schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO ledger_meta(id,generation,postactivation_mutated,database_id,ledger_present,forecasts_present) VALUES(1,0,0,?,0,0)", id); err != nil {
		return "", fmt.Errorf("initialize ledger identity: %w", err)
	}
	return id, nil
}

// applyLedgerOperations applies and validates one batch inside its caller's transaction.
//
// Example: deleting a linked row requires repairing its incoming links in the batch.
func applyLedgerOperations(
	ctx context.Context,
	tx *sql.Tx,
	ops []ledgerOperation,
) (bool, error) {
	for i, op := range ops {
		if op.Op == ledgerPut || op.Op == ledgerPatch {
			if err := validateUniqueJSON(op.Value); err != nil {
				return false, fmt.Errorf("op %d value: %w", i, err)
			}
		}
	}
	ledgerPresent, forecastsPresent, err := ledgerPresence(ctx, tx)
	if err != nil {
		return false, err
	}
	if err := validateLedgerPresence(ctx, tx, ledgerPresent, forecastsPresent); err != nil {
		return false, err
	}
	if err := validateLedger(ctx, tx); err != nil {
		return false, fmt.Errorf("validate existing ledger: %w", err)
	}
	snapshot, err := snapshotLedgerRows(ctx, tx, ops)
	if err != nil {
		return false, err
	}
	ledgerPutSeen, forecastPutSeen := false, false
	for i, op := range ops {
		if err := applyLedgerOperation(ctx, tx, op); err != nil {
			return false, fmt.Errorf("op %d: %w", i, err)
		}
		if op.Op == ledgerPut {
			switch op.Table {
			case ledgerSections, ledgerRecords:
				ledgerPutSeen = true
			case ledgerForecasts:
				forecastPutSeen = true
			}
		}
	}
	presenceChanged := (ledgerPutSeen && !ledgerPresent) || (forecastPutSeen && !forecastsPresent)
	if presenceChanged {
		result, err := tx.ExecContext(ctx, "UPDATE ledger_meta SET ledger_present=CASE WHEN ? THEN 1 ELSE ledger_present END, forecasts_present=CASE WHEN ? THEN 1 ELSE forecasts_present END WHERE id=1", ledgerPutSeen, forecastPutSeen)
		if err != nil {
			return false, fmt.Errorf("update snapshot presence: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("count snapshot presence rows: %w", err)
		}
		if count != 1 {
			return false, fmt.Errorf("snapshot presence row is missing")
		}
	}
	if err := validateLedger(ctx, tx); err != nil {
		if len(ops) == 0 {
			return false, fmt.Errorf("batch validation: %w", err)
		}
		return false, fmt.Errorf("batch validation after op %d: %w", len(ops)-1, err)
	}
	changed, err := changedLedgerRows(ctx, tx, snapshot)
	return presenceChanged || changed, err
}

// applyLedgerOperation executes a single bounded mutation.
//
// Example: patch requires an existing row and matching expected type.
func applyLedgerOperation(
	ctx context.Context,
	tx *sql.Tx,
	op ledgerOperation,
) error {
	var put, patch, remove, key string
	switch op.Table {
	case ledgerSections:
		put = "INSERT INTO sections(id,value) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value"
		patch = "UPDATE sections SET value=json_patch(value,?) WHERE id=?"
		remove = "DELETE FROM sections WHERE id=?"
	case ledgerRecords:
		put = "INSERT INTO records(id,value) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value"
		patch = "UPDATE records SET value=json_patch(value,?) WHERE id=? AND type=?"
		remove = "DELETE FROM records WHERE id=? AND type=?"
		key = "type"
	case ledgerForecasts:
		put = "INSERT INTO forecasts(id,value) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value"
		patch = "UPDATE forecasts SET value=json_patch(value,?) WHERE id=? AND kind=?"
		remove = "DELETE FROM forecasts WHERE id=? AND kind=?"
		key = "kind"
	default:
		return fmt.Errorf("unknown table %d", op.Table)
	}
	if op.ID == "" {
		return fmt.Errorf("empty id")
	}
	switch op.Op {
	case ledgerPut:
		_, err := tx.ExecContext(ctx, put, op.ID, string(op.Value))
		if err != nil {
			return fmt.Errorf("put row: %w", err)
		}
		return nil
	case ledgerPatch, ledgerDelete:
	default:
		return fmt.Errorf("unknown operation %d", op.Op)
	}
	if key != "" {
		value, present := op.Expect[key]
		if len(op.Expect) != 1 || !present || value == nil {
			return fmt.Errorf("expected %s must be a string", key)
		}
	}
	if key == "" && len(op.Expect) != 0 {
		return fmt.Errorf("sections do not support expectations")
	}
	query := remove
	args := []any{op.ID}
	if op.Op == ledgerPatch {
		query = patch
		args = []any{string(op.Value), op.ID}
	}
	if key != "" {
		args = append(args, *op.Expect[key])
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("mutate row: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count affected rows: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("row missing or expectation mismatch: %s", op.ID)
	}
	return nil
}

// validateLedger checks cross-row and JSON envelope constraints before commit.
//
// Example: dangling record links are rejected even when each row is valid JSON.
func validateLedger(
	ctx context.Context,
	tx *sql.Tx,
) (_err error) {
	if err := validateStoredValues(ctx, tx); err != nil {
		return err
	}
	var missing string
	err := tx.QueryRowContext(ctx, "SELECT r.id FROM records AS r LEFT JOIN sections AS s ON s.id=r.section WHERE s.id IS NULL LIMIT 1").Scan(&missing)
	if err == nil {
		return fmt.Errorf("record %s has missing section", missing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check sections: %w", err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,value FROM records")
	if err != nil {
		return fmt.Errorf("read records: %w", err)
	}
	defer
	// closeRecords preserves errors when an early return closes the cursor.
	//
	// Example: decoding failure still releases the database cursor.
	func() { _err = errors.Join(_err, rows.Close()) }()
	records := map[string]ledgerRecord{}
	for rows.Next() {
		var id string
		var value []byte
		var record ledgerRecord
		if err := rows.Scan(&id, &value); err != nil {
			return fmt.Errorf("scan record: %w", err)
		}
		if err := json.Unmarshal(value, &record); err != nil {
			return fmt.Errorf("decode record %s: %w", id, err)
		}
		records[id] = record
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate records: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close records: %w", err)
	}
	// The table CHECK verifies JSON syntax, so the leading token establishes each value's type.
	for id, r := range records {
		if r.Type != "context" && r.Type != "constraint" {
			data := bytes.TrimSpace(r.Data)
			if len(data) == 0 || data[0] != '{' {
				return fmt.Errorf("record %s data must be an object", id)
			}
		}
		for _, evidence := range r.Evidence {
			value := bytes.TrimSpace(evidence)
			if len(value) == 0 || value[0] != '"' {
				return fmt.Errorf("record %s evidence must contain strings", id)
			}
		}
		for _, links := range r.Links {
			if links == nil {
				return fmt.Errorf("record %s links must be arrays", id)
			}
			for _, target := range links {
				if _, ok := records[target]; !ok {
					return fmt.Errorf("record %s links to missing record %s", id, target)
				}
			}
		}
	}
	return nil
}

// validateStoredValues rejects ambiguous objects in the non-record collections.
//
// Example: duplicate section titles cannot be silently overwritten by a batch.
func validateStoredValues(
	ctx context.Context,
	tx *sql.Tx,
) (_err error) {
	rows, err := tx.QueryContext(ctx, "SELECT 'sections',id,value FROM sections UNION ALL SELECT 'forecasts',id,value FROM forecasts")
	if err != nil {
		return fmt.Errorf("read stored values: %w", err)
	}
	defer
	// closeStoredValues preserves cursor close errors on every return path.
	//
	// Example: invalid stored JSON still releases the cursor.
	func() { _err = errors.Join(_err, rows.Close()) }()
	for rows.Next() {
		var table, id string
		var value []byte
		if err := rows.Scan(&table, &id, &value); err != nil {
			return fmt.Errorf("scan stored value: %w", err)
		}
		if err := validateUniqueJSON(value); err != nil {
			return fmt.Errorf("%s %s: %w", table, id, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate stored values: %w", err)
	}
	return nil
}
