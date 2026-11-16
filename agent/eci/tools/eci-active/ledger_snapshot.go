package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// ledgerRow identifies one row within a collection.
//
// Example: a forecast and a record may share an ID without colliding.
type ledgerRow struct {
	Table ledgerTable
	ID    string
}

// ledgerSnapshot records each touched row's original value or absence.
//
// Example: a put followed by its inverse can be recognized as unchanged.
type ledgerSnapshot map[ledgerRow]json.RawMessage

// snapshotLedgerRows captures pre-batch values inside the write transaction.
//
// Example: repeated operations on one row capture its original value only once.
func snapshotLedgerRows(
	ctx context.Context,
	tx *sql.Tx,
	ops []ledgerOperation,
) (ledgerSnapshot, error) {
	snapshot := make(ledgerSnapshot, len(ops))
	for i, op := range ops {
		row := ledgerRow{Table: op.Table, ID: op.ID}
		if _, ok := snapshot[row]; ok {
			continue
		}
		value, err := readLedgerRow(ctx, tx, row)
		if err != nil {
			return nil, fmt.Errorf("op %d: %w", i, err)
		}
		snapshot[row] = value
	}
	return snapshot, nil
}

// readLedgerRow returns nil only when the named row is absent.
//
// Example: an absent forecast differs from an existing JSON null value.
func readLedgerRow(
	ctx context.Context,
	tx *sql.Tx,
	row ledgerRow,
) (json.RawMessage, error) {
	var query string
	switch row.Table {
	case ledgerSections:
		query = "SELECT value FROM sections WHERE id=?"
	case ledgerRecords:
		query = "SELECT value FROM records WHERE id=?"
	case ledgerForecasts:
		query = "SELECT value FROM forecasts WHERE id=?"
	default:
		return nil, fmt.Errorf("unknown table %d", row.Table)
	}
	var value []byte
	err := tx.QueryRowContext(ctx, query, row.ID).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("read row %s: %w", row.ID, err)
	default:
		return value, nil
	}
}

// changedLedgerRows compares final typed values and presence with a snapshot.
//
// Example: object member ordering alone does not change a row.
func changedLedgerRows(
	ctx context.Context,
	tx *sql.Tx,
	snapshot ledgerSnapshot,
) (bool, error) {
	for row, before := range snapshot {
		after, err := readLedgerRow(ctx, tx, row)
		if err != nil {
			return false, err
		}
		if (before == nil) != (after == nil) {
			return true, nil
		}
		if before == nil || bytes.Equal(before, after) {
			continue
		}
		var left, right any
		decoder := json.NewDecoder(bytes.NewReader(before))
		decoder.UseNumber()
		if err := decoder.Decode(&left); err != nil {
			return false, fmt.Errorf("decode previous row: %w", err)
		}
		decoder = json.NewDecoder(bytes.NewReader(after))
		decoder.UseNumber()
		if err := decoder.Decode(&right); err != nil {
			return false, fmt.Errorf("decode current row: %w", err)
		}
		if !reflect.DeepEqual(left, right) {
			return true, nil
		}
	}
	return false, nil
}

// advanceLedgerGeneration records one committed change to the shared store.
//
// Example: multiple row updates in a single batch advance the generation once.
func advanceLedgerGeneration(
	ctx context.Context,
	tx *sql.Tx,
) error {
	result, err := tx.ExecContext(ctx, "UPDATE ledger_meta SET generation=generation+1,postactivation_mutated=1 WHERE id=1")
	if err != nil {
		return fmt.Errorf("advance generation: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count generation rows: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("ledger generation row is missing")
	}
	return nil
}

// ledgerProjection is the transactional input for requirement assignment.
//
// Example: sections and records reflect one uncommitted batch.
type ledgerProjection struct {
	Sections        map[string]json.RawMessage `json:"sections"`
	Records         map[string]json.RawMessage `json:"records"`
	SnapshotChanged bool                       `json:"snapshot_changed"`
}

// projectLedger reads the selected rows without committing their transaction.
//
// Example: a helper may validate the proposed requirement chain before commit.
func projectLedger(
	ctx context.Context,
	tx *sql.Tx,
	changed bool,
) (ledgerProjection, error) {
	projection := ledgerProjection{
		Sections:        make(map[string]json.RawMessage),
		Records:         make(map[string]json.RawMessage),
		SnapshotChanged: changed,
	}
	if err := projectLedgerRows(ctx, tx, "SELECT id,value FROM sections", projection.Sections); err != nil {
		return ledgerProjection{}, fmt.Errorf("project sections: %w", err)
	}
	if err := projectLedgerRows(ctx, tx, "SELECT id,value FROM records", projection.Records); err != nil {
		return ledgerProjection{}, fmt.Errorf("project records: %w", err)
	}
	return projection, nil
}

// projectLedgerRows collects one collection for a requirement projection.
//
// Example: record values retain their original JSON scalar types.
func projectLedgerRows(
	ctx context.Context,
	tx *sql.Tx,
	query string,
	target map[string]json.RawMessage,
) (_err error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query projection: %w", err)
	}
	defer
	// closeProjection preserves a cursor close error.
	//
	// Example: a failed scan still releases its cursor.
	func() { _err = errors.Join(_err, rows.Close()) }()
	for rows.Next() {
		var id string
		var value []byte
		if err := rows.Scan(&id, &value); err != nil {
			return fmt.Errorf("scan projection: %w", err)
		}
		target[id] = value
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate projection: %w", err)
	}
	return nil
}

// ledgerPresence reads the independent presence of the two row snapshots.
//
// Example: an absent forecast snapshot differs from a present-empty snapshot.
func ledgerPresence(
	ctx context.Context,
	tx *sql.Tx,
) (bool, bool, error) {
	var ledger, forecasts bool
	if err := tx.QueryRowContext(ctx, "SELECT ledger_present,forecasts_present FROM ledger_meta WHERE id=1").Scan(&ledger, &forecasts); err != nil {
		return false, false, fmt.Errorf("read snapshot presence: %w", err)
	}
	return ledger, forecasts, nil
}

// validateLedgerPresence rejects rows that claim to belong to an absent snapshot.
//
// Example: any forecast row requires forecasts_present.
func validateLedgerPresence(
	ctx context.Context,
	tx *sql.Tx,
	ledgerPresent bool,
	forecastsPresent bool,
) error {
	var ledgerRows, forecastRows bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sections UNION ALL SELECT 1 FROM records), EXISTS(SELECT 1 FROM forecasts)").Scan(&ledgerRows, &forecastRows); err != nil {
		return fmt.Errorf("check snapshot rows: %w", err)
	}
	if (!ledgerPresent && ledgerRows) || (!forecastsPresent && forecastRows) {
		return fmt.Errorf("snapshot rows exist while snapshot is absent")
	}
	return nil
}
