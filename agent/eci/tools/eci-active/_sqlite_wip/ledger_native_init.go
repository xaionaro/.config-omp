package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
)

// initializeNativeLedger creates one explicitly authored native database.
//
// Example: existing YAML files are left untouched and do not select authority.
func initializeNativeLedger(
	ctx context.Context,
	dir, sid string,
	data []byte,
) (_obs ledgerObservation, _err error) {
	if err := validateUniqueJSON(data); err != nil {
		return _obs, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return _obs, err
	}
	if err := requireNativeKeys(envelope, "expect", "ledger", "forecasts", "report"); err != nil {
		return _obs, err
	}
	for _, spec := range []struct {
		key     string
		allowed []string
	}{
		{"ledger", []string{"present", "sections", "records"}},
		{"forecasts", []string{"present", "records"}},
		{"report", []string{"present", "document", "decisions", "log"}},
	} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(envelope[spec.key], &fields); err != nil || fields == nil {
			return _obs, fmt.Errorf("invalid %s snapshot", spec.key)
		}
		present, ok := fields["present"]
		if !ok || (string(present) != "true" && string(present) != "false") {
			return _obs, fmt.Errorf("%s presence must be boolean", spec.key)
		}
		if string(present) == "false" && len(fields) != 1 {
			return _obs, fmt.Errorf("absent %s has content", spec.key)
		}
		if string(present) == "true" {
			for _, name := range spec.allowed[1:] {
				if _, ok := fields[name]; !ok {
					if spec.key == "report" && name == "log" {
						return _obs, fmt.Errorf("caller log observation is required")
					}
					return _obs, fmt.Errorf("%s requires %s", spec.key, name)
				}
			}
		}
		if err := requireNativeKeys(fields, spec.allowed...); err != nil {
			return _obs, err
		}
	}
	var request struct {
		Expect string `json:"expect"`
		Ledger struct {
			Present  bool                       `json:"present"`
			Sections map[string]json.RawMessage `json:"sections"`
			Records  map[string]json.RawMessage `json:"records"`
		} `json:"ledger"`
		Forecasts struct {
			Present bool                       `json:"present"`
			Records map[string]json.RawMessage `json:"records"`
		} `json:"forecasts"`
		Report struct {
			Present   bool            `json:"present"`
			Document  json.RawMessage `json:"document"`
			Decisions json.RawMessage `json:"decisions"`
			Log       json.RawMessage `json:"log"`
		} `json:"report"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return _obs, fmt.Errorf("decode initial state: %w", err)
	}
	if request.Ledger.Present && (request.Ledger.Sections == nil || request.Ledger.Records == nil) {
		return _obs, fmt.Errorf("present ledger requires section and record objects")
	}
	if request.Forecasts.Present && request.Forecasts.Records == nil {
		return _obs, fmt.Errorf("present forecasts require a records object")
	}
	if request.Report.Present {
		if err := validateReportStructure(request.Report.Document); err != nil {
			return _obs, fmt.Errorf("invalid report document: %w", err)
		}
		var decisions map[string]json.RawMessage
		if err := json.Unmarshal(request.Report.Decisions, &decisions); err != nil || decisions == nil {
			return _obs, fmt.Errorf("report decisions must be an object")
		}
	}
	if request.Expect != "uninitialized" || (!request.Ledger.Present && !request.Forecasts.Present && !request.Report.Present) {
		return _obs, fmt.Errorf("initial state must contain a present snapshot")
	}
	if (!request.Ledger.Present && (len(request.Ledger.Sections) > 0 || len(request.Ledger.Records) > 0)) || (!request.Forecasts.Present && len(request.Forecasts.Records) > 0) {
		return _obs, fmt.Errorf("absent snapshot has rows")
	}
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		if _, err := os.Lstat(filepath.Join(dir, "ledger.db") + suffix); err == nil {
			return _obs, fmt.Errorf("native ledger already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return _obs, err
		}
	}
	stage, err := os.CreateTemp(dir, ".ledger-init-*")
	if err != nil {
		return _obs, err
	}
	path := stage.Name()
	if err := stage.Close(); err != nil {
		return _obs, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "journal_mode(DELETE)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	if err := guardSQLiteFiles(path); err != nil {
		return _obs, err
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return _obs, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return _obs, errors.Join(err, db.Close())
	}
	id, err := initializeLedgerSchema(ctx, tx, "", sid)
	if err == nil {
		for _, collection := range []struct {
			table  string
			values map[string]json.RawMessage
		}{
			{"sections", request.Ledger.Sections},
			{"records", request.Ledger.Records},
			{"forecasts", request.Forecasts.Records},
		} {
			for key, value := range collection.values {
				if err = validateUniqueJSON(value); err != nil {
					break
				}
				_, err = tx.ExecContext(ctx, "INSERT INTO "+collection.table+"(id,value) VALUES(?,?)", key, string(value))
				if err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE ledger_meta SET ledger_present=?,forecasts_present=? WHERE id=1", request.Ledger.Present, request.Forecasts.Present)
	}
	if err == nil {
		err = validateLedger(ctx, tx)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		err = errors.Join(err, tx.Rollback())
	}
	if err = errors.Join(err, db.Close()); err != nil {
		return _obs, fmt.Errorf("prepare native ledger: %w", err)
	}
	if request.Report.Present {
		logPath := filepath.Join(dir, ledgerLogName)
		var before os.FileInfo
		var logData []byte
		logFile, readErr := os.Open(logPath)
		if readErr == nil {
			before, err = logFile.Stat()
			if err == nil {
				logData, err = io.ReadAll(logFile)
			}
			err = errors.Join(err, logFile.Close())
			if err != nil {
				return _obs, err
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return _obs, readErr
		}
		observation, err := makeNativeLogObservation(before, logData)
		if err != nil {
			return _obs, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(request.Report.Log, &fields); err != nil || len(fields) != 4 {
			return _obs, fmt.Errorf("invalid caller log observation")
		}
		if err := requireNativeKeys(fields, "present", "device", "inode", "bytes_base64"); err != nil {
			return _obs, err
		}
		if string(fields["present"]) != "true" && string(fields["present"]) != "false" {
			return _obs, fmt.Errorf("invalid caller log observation presence")
		}
		for _, name := range []string{"device", "inode", "bytes_base64"} {
			var value *string
			if err := json.Unmarshal(fields[name], &value); err != nil || value == nil {
				return _obs, fmt.Errorf("invalid caller log observation %s", name)
			}
		}
		var supplied nativeLogObservation
		if err := json.Unmarshal(request.Report.Log, &supplied); err != nil || supplied != observation {
			return _obs, fmt.Errorf("caller log observation differs")
		}
		payload, err := json.Marshal(struct {
			Document      json.RawMessage      `json:"document"`
			Decisions     json.RawMessage      `json:"decisions"`
			Log           nativeLogObservation `json:"log"`
			CandidatePath string               `json:"candidate_path"`
			SessionDir    string               `json:"session_dir"`
			Event         string               `json:"event"`
		}{request.Report.Document, request.Report.Decisions, observation, path, dir, "report_create"})
		if err != nil {
			return _obs, err
		}
		spill, err := os.CreateTemp("", "eci-report-prepare-*")
		if err != nil {
			return _obs, err
		}
		if err = os.Remove(spill.Name()); err != nil {
			return _obs, errors.Join(err, spill.Close())
		}
		cmd := exec.CommandContext(ctx, filepath.Join(eciBaseDir(), "bin", "status-report"), "report-prepare", "--stdin")
		cmd.Stdin = bytes.NewReader(payload)
		cmd.Stdout = spill
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			return _obs, errors.Join(fmt.Errorf("prepare report: %w", err), spill.Close())
		}
		if _, err = spill.Seek(0, io.SeekStart); err != nil {
			return _obs, errors.Join(err, spill.Close())
		}
		output, err := io.ReadAll(spill)
		err = errors.Join(err, spill.Close())
		if err != nil {
			return _obs, err
		}
		if err := validateUniqueJSON(output); err != nil {
			return _obs, err
		}
		var prepared map[string]json.RawMessage
		if err := json.Unmarshal(output, &prepared); err != nil {
			return _obs, err
		}
		if len(prepared) != 1 || prepared["document"] == nil {
			return _obs, fmt.Errorf("invalid prepared report envelope")
		}
		if err := validateReportStructure(prepared["document"]); err != nil {
			return _obs, err
		}
		if err := guardSQLiteFiles(path); err != nil {
			return _obs, err
		}
		db, err = sql.Open("sqlite", u.String())
		if err != nil {
			return _obs, err
		}
		_, err = db.ExecContext(ctx, "INSERT INTO report(id,document_json,revision) VALUES(1,?,0)", string(prepared["document"]))
		if err = errors.Join(err, db.Close()); err != nil {
			return _obs, err
		}
		current, openErr := os.Open(logPath)
		if before == nil {
			if current != nil {
				_ = current.Close()
			}
			if !errors.Is(openErr, os.ErrNotExist) {
				return _obs, fmt.Errorf("log changed during preparation")
			}
		} else {
			if openErr != nil {
				return _obs, fmt.Errorf("log changed during preparation")
			}
			after, statErr := current.Stat()
			now, readErr := io.ReadAll(current)
			closeErr := current.Close()
			if statErr != nil || readErr != nil || closeErr != nil || !os.SameFile(before, after) || !bytes.Equal(now, logData) {
				return _obs, fmt.Errorf("log changed during preparation")
			}
		}
	}
	if err := guardSQLiteFiles(path); err != nil {
		return _obs, err
	}
	db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return _obs, err
	}
	var journalMode string
	err = db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode)
	if err == nil && journalMode != "wal" {
		err = fmt.Errorf("could not enable native WAL")
	}
	if err = errors.Join(err, db.Close()); err != nil {
		return _obs, err
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, sideErr := os.Lstat(path + suffix); sideErr == nil {
			return _obs, fmt.Errorf("staged database has remaining sidecar %s", suffix)
		} else if !errors.Is(sideErr, os.ErrNotExist) {
			return _obs, sideErr
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return _obs, err
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return _obs, err
	}
	if err := renameNativeNoReplace(path, filepath.Join(dir, "ledger.db")); err != nil {
		return _obs, err
	}
	d, err := os.Open(dir)
	if err != nil {
		return _obs, fmt.Errorf("ledger installed; directory sync uncertain: %w", err)
	}
	if err = errors.Join(d.Sync(), d.Close()); err != nil {
		return _obs, fmt.Errorf("ledger installed; directory sync uncertain: %w", err)
	}
	return ledgerObservation{DatabaseID: id, Generation: 0}, nil
}

// ledgerInit authorizes and serializes one native initialization request.
//
// Example: a session owner initializes current state through standard input.
func ledgerInit(args []string) (_err error) {
	if len(args) != 2 || args[1] != "--stdin" {
		return usageError()
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	sid, err := currentSession()
	if err != nil {
		return err
	}
	if err = verifyCallerSession("ledger-init", sid, contextSessionsRoot()); err != nil {
		return err
	}
	session, err := resolveSessionDir(sid)
	if err != nil {
		return err
	}
	if err = ledgerMarkerCwdBound(session); err != nil {
		return err
	}
	dir, err := contextSessionDir(filepath.Base(session))
	if err != nil {
		return err
	}
	if err = proofRootReady(); err != nil {
		return err
	}
	lock, err := acquireSessionLock(dir)
	if err != nil {
		return err
	}
	defer func() { _err = errors.Join(_err, lock.Close()) }()
	obs, err := initializeNativeLedger(context.Background(), dir, filepath.Base(dir), data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "{\"database_id\":%q,\"generation\":\"0\"}\n", obs.DatabaseID)
	return err
}

// requireNativeKeys rejects noncanonical fixed-schema field names.
//
// Example: Present cannot be interpreted as the distinct key present.
func requireNativeKeys(
	fields map[string]json.RawMessage,
	allowed ...string,
) error {
	for key := range fields {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}
