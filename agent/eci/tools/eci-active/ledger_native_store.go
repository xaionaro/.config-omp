package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"net/url"
	"path/filepath"
	"strconv"
)

// ledgerObservation identifies one complete database observation.
//
// Example: a recreated database cannot reuse an earlier observation.
type ledgerObservation struct {
	DatabaseID string
	Generation int64
}

// ledgerConflictError reports a stale database observation.
//
// Example: a replaced database cannot accept an old generation token.
type ledgerConflictError struct{}

// Error identifies a conflict that requires a fresh capture and re-evaluation.
//
// Example: callers must not blindly replay the rejected batch.
func (ledgerConflictError) Error() string { return "ledger observation conflict" }

// openNativeLedgerRead opens existing native storage without creating or modifying it.
//
// Example: a missing database is an uninitialized session, not an empty snapshot.
func openNativeLedgerRead(
	ctx context.Context,
	dir string,
) (*sql.DB, error) {
	path := filepath.Join(dir, "ledger.db")
	if err := guardSQLiteFiles(path); err != nil {
		return nil, fmt.Errorf("native ledger unavailable: %w", err)
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open native ledger: %w", err)
	}
	return db, nil
}

// validateNativeLedgerTransaction validates authority and contents in one snapshot.
//
// Example: a database copied from another session cannot satisfy this check.
func validateNativeLedgerTransaction(
	ctx context.Context,
	tx *sql.Tx,
	sid string,
) (*ledgerObservation, error) {
	var version int
	if err := tx.QueryRowContext(ctx, ledgerVersionQuery).Scan(&version); err != nil {
		return nil, fmt.Errorf("read ledger version: %w", err)
	}
	if version != ledgerVersion {
		return nil, fmt.Errorf("unsupported ledger version %d", version)
	}
	var session string
	var obs ledgerObservation
	if err := tx.QueryRowContext(ctx, "SELECT session_id,database_id,generation FROM ledger_meta WHERE id=1").Scan(&session, &obs.DatabaseID, &obs.Generation); err != nil {
		return nil, fmt.Errorf("read ledger identity: %w", err)
	}
	id, idErr := hex.DecodeString(obs.DatabaseID)
	if session != sid || obs.Generation < 0 || idErr != nil || len(id) != 16 || hex.EncodeToString(id) != obs.DatabaseID {
		return nil, fmt.Errorf("native ledger identity mismatch")
	}
	ledger, forecasts, err := ledgerPresence(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := validateLedgerPresence(ctx, tx, ledger, forecasts); err != nil {
		return nil, err
	}
	if err := validateLedger(ctx, tx); err != nil {
		return nil, fmt.Errorf("invalid native ledger: %w", err)
	}
	var report []byte
	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT document_json,revision FROM report WHERE id=1").Scan(&report, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read report: %w", err)
	}
	if err == nil {
		if revision < 0 {
			return nil, fmt.Errorf("invalid report revision")
		}
		if err := validateReportStructure(report); err != nil {
			return nil, fmt.Errorf("invalid report storage: %w", err)
		}
	}
	return &obs, nil
}

// applyNativeLedgerBatch checks the captured version before any mutation.
//
// Example: a stale no-op conflicts instead of silently authorizing an old intent.
func applyNativeLedgerBatch(
	ctx context.Context,
	tx *sql.Tx,
	base ledgerObservation,
	ops []ledgerOperation,
) (bool, error) {
	var current ledgerObservation
	if err := tx.QueryRowContext(ctx, "SELECT database_id,generation FROM ledger_meta WHERE id=1").Scan(&current.DatabaseID, &current.Generation); err != nil {
		return false, fmt.Errorf("read ledger observation: %w", err)
	}
	if current != base {
		return false, ledgerConflictError{}
	}
	changed, err := applyLedgerOperations(ctx, tx, ops)
	if err != nil {
		return false, err
	}
	if changed {
		if err := advanceLedgerGeneration(ctx, tx); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// decodeNativeBatch validates an exact decimal observation and operation batch.
//
// Example: generation "01" is rejected rather than normalized.
func decodeNativeBatch(data []byte) (ledgerObservation, []ledgerOperation, error) {
	var base ledgerObservation
	ops, err := decodeLedgerOperations(data)
	if err != nil {
		return base, nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return base, nil, err
	}
	base, err = decodeNativeObservation(envelope["base"])
	if err != nil {
		return base, nil, err
	}
	return base, ops, nil
}

// decodeNativeObservation validates one exact wire-format observation.
//
// Example: child output cannot substitute an uppercase identity or numeric generation.
func decodeNativeObservation(data []byte) (ledgerObservation, error) {
	var base ledgerObservation
	if err := validateUniqueJSON(data); err != nil {
		return base, fmt.Errorf("invalid observation: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 2 {
		return base, fmt.Errorf("invalid observation")
	}
	var generation string
	if err := json.Unmarshal(fields["database_id"], &base.DatabaseID); err != nil {
		return base, fmt.Errorf("invalid database identity")
	}
	id, err := hex.DecodeString(base.DatabaseID)
	if err != nil || len(id) != 16 || hex.EncodeToString(id) != base.DatabaseID {
		return base, fmt.Errorf("invalid database identity")
	}
	if err := json.Unmarshal(fields["generation"], &generation); err != nil {
		return base, fmt.Errorf("invalid generation")
	}
	base.Generation, err = strconv.ParseInt(generation, 10, 64)
	if err != nil || base.Generation < 0 || strconv.FormatInt(base.Generation, 10) != generation {
		return base, fmt.Errorf("invalid generation")
	}
	return base, nil
}

// validateNativeIfPresent checks an existing store without initializing it.
//
// Example: log-only sessions can append while a missing database stays absent.
func validateNativeIfPresent(
	ctx context.Context,
	dir, sid string,
) (_err error) {
	_, err := os.Lstat(filepath.Join(dir, "ledger.db"))
	if errors.Is(err, os.ErrNotExist) {
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			_, sideErr := os.Lstat(filepath.Join(dir, "ledger.db") + suffix)
			if sideErr == nil {
				return fmt.Errorf("native ledger sidecar exists without database: %s", suffix)
			}
			if !errors.Is(sideErr, os.ErrNotExist) {
				return sideErr
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	db, err := openNativeLedgerRead(ctx, dir)
	if err != nil {
		return err
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_err = errors.Join(_err, err)
		}
	}()
	_, err = validateNativeLedgerTransaction(ctx, tx, sid)
	return err
}
