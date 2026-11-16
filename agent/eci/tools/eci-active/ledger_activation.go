package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
)

const (
	// ledgerMarkerName is the durable selection of database authority.
	//
	// Example: its absence never selects an unpublished database.
	ledgerMarkerName = "ledger-activation.json"
	// ledgerCandidateName is the unpublished same-directory import target.
	//
	// Example: an interrupted candidate requires recovery, not bootstrap.
	ledgerCandidateName = "ledger.db.candidate"
	// ledgerMarkerSchema identifies the activation marker format.
	//
	// Example: a marker of another version fails closed.
	ledgerMarkerSchema = "ledger-activation/v1"
)

// ledgerMarker binds a database identity to untouched legacy snapshot fingerprints.
//
// Example: nil denotes a snapshot that was absent at activation.
type ledgerMarker struct {
	Schema     string
	DatabaseID string
	Legacy     map[string]*string
}

// markerObject decodes one JSON object and rejects duplicate member names.
//
// Example: duplicated database identities are an ambiguous marker.
func markerObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("read marker object: %w", err)
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected marker object")
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("read marker key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid marker key")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate marker key %q", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("read marker value: %w", err)
		}
		result[key] = value
	}
	end, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("finish marker object: %w", err)
	}
	if end != json.Delim('}') {
		return nil, fmt.Errorf("invalid marker object ending")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("read marker trailing content: %w", err)
		}
		return nil, fmt.Errorf("marker has trailing content")
	}
	return result, nil
}

// parseLedgerMarker validates the versioned marker and its complete source set.
//
// Example: null fingerprints identify absent legacy snapshots.
func parseLedgerMarker(raw []byte) (ledgerMarker, error) {
	var marker ledgerMarker
	object, err := markerObject(raw)
	if err != nil {
		return marker, err
	}
	if len(object) != 3 {
		return marker, fmt.Errorf("marker must contain schema, database_id and legacy")
	}
	if err := json.Unmarshal(object["schema"], &marker.Schema); err != nil {
		return marker, fmt.Errorf("decode marker schema: %w", err)
	}
	if marker.Schema != ledgerMarkerSchema {
		return marker, fmt.Errorf("unsupported marker schema")
	}
	if err := json.Unmarshal(object["database_id"], &marker.DatabaseID); err != nil {
		return marker, fmt.Errorf("decode database identity: %w", err)
	}
	if len(marker.DatabaseID) != 32 {
		return marker, fmt.Errorf("invalid database identity")
	}
	identity, err := hex.DecodeString(marker.DatabaseID)
	if err != nil {
		return marker, fmt.Errorf("decode database identity: %w", err)
	}
	if hex.EncodeToString(identity) != marker.DatabaseID {
		return marker, fmt.Errorf("invalid database identity")
	}
	legacy, err := markerObject(object["legacy"])
	if err != nil {
		return marker, fmt.Errorf("invalid legacy fingerprints: %w", err)
	}
	if len(legacy) != 3 {
		return marker, fmt.Errorf("marker must name each legacy snapshot")
	}
	marker.Legacy = make(map[string]*string, 3)
	for _, name := range []string{"project-understanding.yaml", "forecasts.yaml", "latest-status-report.yaml"} {
		rawValue, ok := legacy[name]
		if !ok {
			return marker, fmt.Errorf("missing legacy fingerprint %s", name)
		}
		var fingerprint *string
		if err := json.Unmarshal(rawValue, &fingerprint); err != nil {
			return marker, fmt.Errorf("invalid fingerprint for %s: %w", name, err)
		}
		if fingerprint != nil {
			decoded, err := hex.DecodeString(*fingerprint)
			if err != nil {
				return marker, fmt.Errorf("decode fingerprint for %s: %w", name, err)
			}
			if len(decoded) != sha256.Size || hex.EncodeToString(decoded) != *fingerprint {
				return marker, fmt.Errorf("invalid fingerprint for %s", name)
			}
		}
		marker.Legacy[name] = fingerprint
	}
	return marker, nil
}

// ledgerActivationMarker validates filesystem authority without opening SQLite.
//
// Example: a clean markerless session returns nil without initializing storage.
func ledgerActivationMarker(dir string) (*ledgerMarker, error) {
	markerPath := filepath.Join(dir, ledgerMarkerName)
	if err := refuseUnsafeLeaf(markerPath); err != nil {
		return nil, fmt.Errorf("unsafe activation marker: %w", err)
	}
	raw, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		for _, base := range []string{"ledger.db", ledgerCandidateName} {
			for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
				name := base + suffix
				_, err := os.Lstat(filepath.Join(dir, name))
				switch {
				case err == nil:
					return nil, fmt.Errorf("unpublished ledger state requires recovery: %s", name)
				case !errors.Is(err, os.ErrNotExist):
					return nil, fmt.Errorf("inspect ledger state: %w", err)
				}
			}
		}
		_, err := os.Lstat(markerPath + ".tmp")
		switch {
		case err == nil:
			return nil, fmt.Errorf("unpublished marker requires recovery")
		case !errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("inspect unpublished marker: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read activation marker: %w", err)
	}
	marker, err := parseLedgerMarker(raw)
	if err != nil {
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		name := ledgerCandidateName + suffix
		_, err := os.Lstat(filepath.Join(dir, name))
		switch {
		case err == nil:
			return nil, fmt.Errorf("unexpected import candidate requires recovery: %s", name)
		case !errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("inspect import candidate: %w", err)
		}
	}
	for name, expected := range marker.Legacy {
		path := filepath.Join(dir, name)
		if err := refuseUnsafeLeaf(path); err != nil {
			return nil, fmt.Errorf("unsafe legacy snapshot: %w", err)
		}
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) && expected == nil {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("legacy snapshot changed: %s: %w", name, err)
		}
		if expected == nil {
			return nil, errors.Join(fmt.Errorf("legacy snapshot unexpectedly appeared: %s", name), file.Close())
		}
		hasher := sha256.New()
		_, copyErr := io.Copy(hasher, file)
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return nil, fmt.Errorf("hash legacy snapshot %s: %w", name, err)
		}
		var digest [sha256.Size]byte
		if hex.EncodeToString(hasher.Sum(digest[:0])) != *expected {
			return nil, fmt.Errorf("legacy snapshot changed: %s", name)
		}
	}
	return &marker, nil
}

// validateLedgerActivation verifies authority and the active database together.
//
// Example: a markerless installed database requires recovery rather than fallback.
func validateLedgerActivation(ctx context.Context, dir string) (_active bool, _err error) {
	marker, err := ledgerActivationMarker(dir)
	if err != nil || marker == nil {
		return false, err
	}
	database := filepath.Join(dir, "ledger.db")
	if err := guardSQLiteFiles(database); err != nil {
		return false, fmt.Errorf("unsafe ledger database: %w", err)
	}
	u := url.URL{Scheme: "file", Path: database}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return false, fmt.Errorf("open active ledger: %w", err)
	}
	defer
	// closeActivationStore preserves connection close errors.
	//
	// Example: a failed identity check closes its read-only connection.
	func() { _err = errors.Join(_err, db.Close()) }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, fmt.Errorf("begin activation validation: %w", err)
	}
	defer
	// rollbackActivationRead closes an uncommitted read transaction.
	//
	// Example: invalid presence returns without retaining a snapshot.
	func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_err = errors.Join(_err, err)
		}
	}()
	if err := validateLedgerTransaction(ctx, tx, marker); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("finish activation validation: %w", err)
	}
	return true, nil
}

// validateLedgerTransaction checks an active store within its caller's snapshot.
//
// Example: capture validates identity before streaming rows from the same transaction.
func validateLedgerTransaction(ctx context.Context, tx *sql.Tx, marker *ledgerMarker) error {
	var version int
	if err := tx.QueryRowContext(ctx, ledgerVersionQuery).Scan(&version); err != nil {
		return fmt.Errorf("read ledger version: %w", err)
	}
	if version != ledgerVersion {
		return fmt.Errorf("unsupported ledger version %d", version)
	}
	var identity string
	if err := tx.QueryRowContext(ctx, "SELECT database_id FROM ledger_meta WHERE id=1").Scan(&identity); err != nil {
		return fmt.Errorf("read ledger identity: %w", err)
	}
	if marker == nil || identity != marker.DatabaseID {
		return fmt.Errorf("ledger identity does not match marker")
	}
	ledgerPresent, forecastsPresent, err := ledgerPresence(ctx, tx)
	if err != nil {
		return err
	}
	if err := validateLedgerPresence(ctx, tx, ledgerPresent, forecastsPresent); err != nil {
		return err
	}
	if err := validateLedger(ctx, tx); err != nil {
		return fmt.Errorf("invalid active ledger: %w", err)
	}
	var report []byte
	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT document_json,revision FROM report WHERE id=1").Scan(&report, &revision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("read active report: %w", err)
	case revision < 0:
		return fmt.Errorf("invalid active report revision")
	default:
		if err := validateReportStructure(report); err != nil {
			return fmt.Errorf("invalid active report storage: %w", err)
		}
		return nil
	}
}

// guardSQLiteFiles rejects linked or nonregular mutable SQLite files before open.
//
// Example: a WAL hardlinked into another session cannot be opened accidentally.
func guardSQLiteFiles(path string) error {
	for i, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		name := path + suffix
		info, err := os.Lstat(name)
		if i != 0 && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect SQLite file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("SQLite file is not regular: %s", name)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Nlink != 1 {
			return fmt.Errorf("SQLite file link count is not one: %s", name)
		}
	}
	return nil
}
