package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// ledgerEdit applies a batch under the selected session's mutation lock.
//
// Example: ledger-edit --stdin accepts an observation and ordered operations.
func ledgerEdit(args []string) (_err error) {
	if len(args) != 2 || args[1] != "--stdin" {
		return usageError()
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read ledger edit: %w", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode ledger edit: %w", err)
	}
	if err := requireNativeKeys(envelope, "base", "ops"); err != nil {
		return err
	}
	base, ops, err := decodeNativeBatch(data)
	if err != nil {
		return fmt.Errorf("ledger-edit: %w", err)
	}
	sid, err := currentSession()
	if err != nil {
		return err
	}
	if err := verifyCallerSession("ledger-edit", sid, contextSessionsRoot()); err != nil {
		return err
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return fmt.Errorf("resolve session: %w", err)
	}
	if err := ledgerMarkerCwdBound(sessionDir); err != nil {
		return err
	}
	dir, err := contextSessionDir(filepath.Base(sessionDir))
	if err != nil {
		return err
	}
	if err := proofRootReady(); err != nil {
		return err
	}
	lock, err := acquireSessionLock(dir)
	if err != nil {
		return err
	}
	defer
	// releaseSessionLock preserves errors from releasing the session lock.
	//
	// Example: a close failure is returned to the caller.
	func() { _err = errors.Join(_err, lock.Close()) }()
	ctx := context.Background()
	db, err := openLedger(ctx, dir)
	if err != nil {
		return err
	}
	defer
	// closeLedger preserves errors from closing the active database.
	//
	// Example: a failed edit still closes the database.
	func() { _err = errors.Join(_err, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ledger edit: %w", err)
	}
	defer
	// rollbackLedger releases an uncommitted transaction.
	//
	// Example: a failed validation rolls its batch back.
	func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_err = errors.Join(_err, err)
		}
	}()
	if _, err := validateNativeLedgerTransaction(ctx, tx, filepath.Base(dir)); err != nil {
		return err
	}
	changed, err := applyNativeLedgerBatch(ctx, tx, base, ops)
	if err != nil {
		return fmt.Errorf("ledger-edit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ledger edit: %w", err)
	}
	generation := base.Generation
	if changed {
		generation++
	}
	_, err = fmt.Fprintf(os.Stdout, "{\"database_id\":%q,\"generation\":%q}\n", base.DatabaseID, strconv.FormatInt(generation, 10))
	return err
}

// decodeLedgerOperations reads exact request fields and rejects ambiguous JSON.
//
// Example: duplicate ops keys fail before any row mutation.
func decodeLedgerOperations(data []byte) ([]ledgerOperation, error) {
	if err := validateUniqueJSON(data); err != nil {
		return nil, fmt.Errorf("invalid ledger request: %w", err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode ledger request: %w", err)
	}
	rawOps, ok := envelope["ops"]
	if !ok {
		return nil, fmt.Errorf("ledger request must contain ops")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(rawOps, &entries); err != nil {
		return nil, fmt.Errorf("decode ledger ops: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("ledger ops must be an array")
	}
	ops := make([]ledgerOperation, len(entries))
	for i, raw := range entries {
		if err := json.Unmarshal(raw, &ops[i]); err != nil {
			return nil, fmt.Errorf("op %d: %w", i, err)
		}
	}
	return ops, nil
}
