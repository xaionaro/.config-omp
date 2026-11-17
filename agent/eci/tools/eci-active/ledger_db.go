package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// ledgerDBName is the per-session SQLite store holding sections, records and forecasts.
//
// Example: <home>/.agent/context/sessions/<sid>/ledger.db.
const ledgerDBName = "ledger.db"

// ledgerDocument is the whole current state of one session store.
//
// Example: `ledger-read` prints it as one JSON object.
type ledgerDocument struct {
	Generation int64                      `json:"generation"`
	Sections   map[string]json.RawMessage `json:"sections"`
	Records    map[string]json.RawMessage `json:"records"`
	Forecasts  map[string]json.RawMessage `json:"forecasts"`
	Log        []json.RawMessage          `json:"log"`
}

// currentLedgerDir resolves and verifies the calling session's context directory for verb.
//
// Example: currentLedgerDir("ledger-apply") returns <sessions-root>/<sid>.
func currentLedgerDir(verb string) (string, error) {
	sid, err := currentSession()
	if err != nil {
		return "", err
	}
	if err := verifyCallerSession(verb, sid, contextSessionsRoot()); err != nil {
		return "", err
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return "", exitError{code: 1, msg: "eci-active: no current session directory found under the proof root."}
	}
	return contextSessionDir(filepath.Base(sessionDir))
}

// createLedger creates a new, empty version-one store at dir/ledger.db; an existing file is an error.
//
// Example: the first `ledger-init` of a session creates its store.
func createLedger(
	ctx context.Context,
	dir string,
) (_err error) {
	path := filepath.Join(dir, ledgerDBName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create ledger database: %w", err)
	}
	if err := f.Close(); err != nil {
		return errors.Join(fmt.Errorf("close new ledger database: %w", err), os.Remove(path))
	}
	defer func() {
		if _err != nil {
			_err = errors.Join(_err, os.Remove(path))
		}
	}()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw&_txlock=immediate")
	if err != nil {
		return fmt.Errorf("open new ledger: %w", err)
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ledger creation: %w", err)
	}
	if _, err := initializeLedgerSchema(ctx, tx, ""); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if _, err := tx.ExecContext(ctx, "UPDATE ledger_meta SET ledger_present=1,forecasts_present=1 WHERE id=1"); err != nil {
		return errors.Join(fmt.Errorf("mark new ledger present: %w", err), tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ledger creation: %w", err)
	}
	return nil
}

// applyLedgerBatch applies ops atomically and returns the resulting generation.
//
// Example: a batch that changes rows advances the generation once.
func applyLedgerBatch(
	ctx context.Context,
	dir string,
	ops []ledgerOperation,
) (_ int64, _err error) {
	db, err := openLedger(ctx, dir)
	if err != nil {
		return 0, err
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin ledger batch: %w", err)
	}
	changed, err := applyLedgerOperations(ctx, tx, ops)
	if err == nil && changed {
		err = advanceLedgerGeneration(ctx, tx)
	}
	var generation int64
	if err == nil {
		err = tx.QueryRowContext(ctx, "SELECT generation FROM ledger_meta WHERE id=1").Scan(&generation)
	}
	if err != nil {
		return 0, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit ledger batch: %w", err)
	}
	return generation, nil
}

// readLedgerDocument reads every collection of the store at dir in one transaction.
//
// Example: a status report projects the returned records.
func readLedgerDocument(
	ctx context.Context,
	dir string,
) (_ ledgerDocument, _err error) {
	db, err := openLedger(ctx, dir)
	if err != nil {
		return ledgerDocument{}, err
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ledgerDocument{}, fmt.Errorf("begin ledger read: %w", err)
	}
	defer func() { _err = errors.Join(_err, tx.Rollback()) }()
	doc := ledgerDocument{
		Sections:  map[string]json.RawMessage{},
		Records:   map[string]json.RawMessage{},
		Forecasts: map[string]json.RawMessage{},
		Log:       []json.RawMessage{},
	}
	if err := tx.QueryRowContext(ctx, "SELECT generation FROM ledger_meta WHERE id=1").Scan(&doc.Generation); err != nil {
		return ledgerDocument{}, fmt.Errorf("read generation: %w", err)
	}
	for query, target := range map[string]map[string]json.RawMessage{
		"SELECT id,value FROM sections":  doc.Sections,
		"SELECT id,value FROM records":   doc.Records,
		"SELECT id,value FROM forecasts": doc.Forecasts,
	} {
		if err := projectLedgerRows(ctx, tx, query, target); err != nil {
			return ledgerDocument{}, err
		}
	}
	logRows := map[string]json.RawMessage{}
	if err := projectLedgerRows(ctx, tx, "SELECT printf('%020d',seq),entry FROM log", logRows); err != nil {
		return ledgerDocument{}, err
	}
	seqs := slices.Sorted(maps.Keys(logRows))
	for _, seq := range seqs {
		doc.Log = append(doc.Log, logRows[seq])
	}
	return doc, nil
}

// ensureLedger opens the session store at dir, creating an empty one when none exists.
//
// Example: the first `ledger-append` of a session creates its store.
func ensureLedger(
	ctx context.Context,
	dir string,
) (*sql.DB, error) {
	switch _, err := os.Lstat(filepath.Join(dir, ledgerDBName)); {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		if err := createLedger(ctx, dir); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("inspect ledger database: %w", err)
	}
	return openLedger(ctx, dir)
}

// appendLedgerLog appends one validated high-level log entry and returns its sequence number.
//
// Example: `ledger-append` stores each event as one row of the append-only log table.
func appendLedgerLog(
	ctx context.Context,
	dir string,
	entry []byte,
) (_ int64, _err error) {
	db, err := ensureLedger(ctx, dir)
	if err != nil {
		return 0, err
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	result, err := db.ExecContext(ctx, "INSERT INTO log(entry) VALUES(?)", string(entry))
	if err != nil {
		return 0, fmt.Errorf("append high-level log entry: %w", err)
	}
	return result.LastInsertId()
}

// ledgerInit implements `ledger-init`: create the session store unless it already exists.
//
// Example: `eci-active ledger-init` prints the store path.
func ledgerInit(args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	dir, err := currentLedgerDir("ledger-init")
	if err != nil {
		return err
	}
	db, err := ensureLedger(context.Background(), dir)
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	fmt.Println(filepath.Join(dir, ledgerDBName))
	return nil
}

// ledgerApply implements `ledger-apply --json <operations-array>`.
//
// Example: `ledger-apply --json '[{"op":"put","table":"sections","id":"s","value":{"title":"S"}}]'`.
func ledgerApply(args []string) error {
	if len(args) != 3 || args[1] != "--json" {
		return usageError()
	}
	if err := validateUniqueJSON([]byte(args[2])); err != nil {
		return exitError{code: 2, msg: fmt.Sprintf("ledger-apply: invalid operations JSON: %v", err)}
	}
	var ops []ledgerOperation
	if err := json.Unmarshal([]byte(args[2]), &ops); err != nil || ops == nil {
		return exitError{code: 2, msg: fmt.Sprintf("ledger-apply: operations must be a JSON array of operations: %v", err)}
	}
	dir, err := currentLedgerDir("ledger-apply")
	if err != nil {
		return err
	}
	generation, err := applyLedgerBatch(context.Background(), dir, ops)
	if err != nil {
		return exitError{code: 1, msg: fmt.Sprintf("ledger-apply: no change was committed: %v", err)}
	}
	fmt.Printf("ledger generation %d\n", generation)
	return nil
}

// ledgerRead implements `ledger-read`: print the whole session store as JSON.
//
// Example: `eci-active ledger-read | jq '.records'`.
func ledgerRead(args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	dir, err := currentLedgerDir("ledger-read")
	if err != nil {
		return err
	}
	doc, err := readLedgerDocument(context.Background(), dir)
	if err != nil {
		return err
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
