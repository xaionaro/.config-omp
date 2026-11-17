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
	"math"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"

	_ "modernc.org/sqlite"
)

// reportEdit contains an ordered batch of owner changes.
//
// Example: Replace a field only if its previous value matches.
type reportEdit struct {
	Ops []reportEditOperation `json:"ops"`
}

// reportEditOperation describes one selective change and its precondition.
//
// Example: Delete an existing owner field.
type reportEditOperation struct {
	Op string `json:"op"`
	Path []string `json:"path"`
	Expect reportEditExpectation `json:"expect"`
	Value json.RawMessage `json:"value,omitempty"`
}

// reportEditExpectation distinguishes absence, null, and a typed value.
//
// Example: An absent field differs from an explicit JSON null.
type reportEditExpectation struct {
	Kind string `json:"kind"`
	Value json.RawMessage `json:"value,omitempty"`
}

// nativeReportBase identifies the complete database observation authorizing a write.
//
// Example: Compare both identity and generation before editing a report.
type nativeReportBase struct {
	DatabaseID string
	Generation int64
}

// decodeNativeReportBase validates the canonical wire form of a database observation.
//
// Example: A generation of "0" is valid but "00" is not.
func decodeNativeReportBase(raw json.RawMessage) (nativeReportBase, error) {
	var base nativeReportBase
	value, err := decodeReportValue(raw)
	if err != nil {
		return base, fmt.Errorf("invalid base: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 2 {
		return base, fmt.Errorf("base must contain database_id and generation")
	}
	id, ok := object["database_id"].(string)
	if !ok || len(id) != 32 {
		return base, fmt.Errorf("invalid database_id")
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return base, fmt.Errorf("invalid database_id")
		}
	}
	generation, ok := object["generation"].(string)
	if !ok {
		return base, fmt.Errorf("invalid generation")
	}
	n, err := strconv.ParseInt(generation, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != generation {
		return base, fmt.Errorf("invalid generation")
	}
	return nativeReportBase{DatabaseID: id, Generation: n}, nil
}

// runReportEditLocked applies an edit after the caller has authorized and locked the session.
//
// Example: The session owner passes a selective JSON batch.
func runReportEditLocked(
	args []string,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
) int {
	if len(args) != 2 || args[0] == "" || args[1] != "--stdin" {
		fmt.Fprintln(errOut, "usage: status-report report-edit-locked SESSION_DIR --stdin")
		return 2
	}
	raw, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintln(errOut, "status-report report-edit:", err)
		return 1
	}
	var envelope struct {
		Base json.RawMessage `json:"base"`
		Ops []reportEditOperation `json:"ops"`
	}
	if _, err = decodeReportValue(raw); err == nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&envelope)
	}
	var base nativeReportBase
	if err == nil {
		base, err = decodeNativeReportBase(envelope.Base)
	}
	edit := reportEdit{Ops: envelope.Ops}
	if err != nil {
		fmt.Fprintln(errOut, "status-report report-edit:", err)
		return 1
	}
	path := filepath.Join(args[0], "ledger.db")
	if err := guardSQLiteFiles(path); err != nil {
		fmt.Fprintln(errOut, "status-report report-edit:", err)
		return 1
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err == nil {
		var version int
		err = db.QueryRow("PRAGMA user_version").Scan(&version)
		if err == nil && version != 1 {
			err = fmt.Errorf("unsupported ledger version %d", version)
		}
		if err == nil {
			err = editReportDB(context.Background(), db, filepath.Base(filepath.Clean(args[0])), &base, edit)
		}
		err = errors.Join(err, db.Close())
	}
	if err != nil {
		fmt.Fprintln(errOut, "status-report report-edit:", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(struct {
		DatabaseID string `json:"database_id"`
		Generation string `json:"generation"`
	}{DatabaseID: base.DatabaseID, Generation: strconv.FormatInt(base.Generation, 10)}); err != nil {
		fmt.Fprintln(errOut, "status-report report-edit: writing observation:", err)
		return 1
	}
	return 0
}

// parseReportEdit validates one edit batch without losing numeric tokens.
//
// Example: Reject duplicate or unknown operation fields.
func parseReportEdit(raw string) (reportEdit, error) {
	var edit reportEdit
	if _, err := decodeReportValue([]byte(raw)); err != nil {
		return edit, err
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edit); err != nil {
		return edit, err
	}
	return edit, nil
}

// decodeReportValue decodes JSON without converting exact numbers to float64.
//
// Example: Preserve the distinction between 1.0 and 1.
func decodeReportValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeReportToken(decoder)
	if err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	return value, nil
}

// decodeReportToken reads one JSON value and rejects duplicate object members.
//
// Example: Two fields named state are ambiguous and invalid.
func decodeReportToken(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delim {
	case '{':
		result := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key must be a string")
			}
			if _, exists := result[key]; exists {
				return nil, fmt.Errorf("duplicate object key %q", key)
			}
			value, err := decodeReportToken(decoder)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return result, nil
	case '[':
		result := []any{}
		for decoder.More() {
			value, err := decodeReportToken(decoder)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

// editReportDB atomically validates and commits one owner edit batch.
//
// Example: An expected-value conflict rolls back without advancing generation.
func editReportDB(
	ctx context.Context,
	db *sql.DB,
	sessionID string,
	base *nativeReportBase,
	edit reportEdit,
) (_err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _err = errors.Join(_err, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
			_err = errors.Join(_err, rollbackErr)
			if rollbackErr != nil {
				discardErr := conn.Raw(discardReportConnection)
				if !errors.Is(discardErr, driver.ErrBadConn) {
					_err = errors.Join(_err, discardErr)
				}
			}
		}
	}()
	var original, databaseID, actualSession string
	var revision, generation int64
	if err := conn.QueryRowContext(ctx, "SELECT database_id, session_id, generation FROM ledger_meta WHERE id=1").Scan(&databaseID, &actualSession, &generation); err != nil {
		return err
	}
	if actualSession == "" || actualSession != sessionID {
		return fmt.Errorf("ledger session identity mismatch")
	}
	if databaseID != base.DatabaseID || generation != base.Generation {
		return fmt.Errorf("ledger observation conflict: %w", errRefreshRetry)
	}
	if err := conn.QueryRowContext(ctx, "SELECT document_json, revision FROM report WHERE id=1").Scan(&original, &revision); err != nil {
		return fmt.Errorf("report unavailable: %w", err)
	}
	updated, err := applyReportEdit([]byte(original), edit)
	if err != nil {
		return err
	}
	if _, err := parseReport(updated, false); err != nil {
		return fmt.Errorf("invalid edited report: %w", err)
	}
	before, err := decodeReportValue([]byte(original))
	if err != nil {
		return err
	}
	after, err := decodeReportValue(updated)
	if err != nil {
		return err
	}
	changed := !reflect.DeepEqual(before, after)
	if changed {
		if generation == math.MaxInt64 || revision == math.MaxInt64 {
			return fmt.Errorf("report version exhausted")
		}
		result, err := conn.ExecContext(ctx, "UPDATE report SET document_json=?, revision=revision+1 WHERE id=1 AND revision=?", string(updated), revision)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return fmt.Errorf("report revision conflict: %w", errors.Join(err, errRefreshRetry))
		}
		result, err = conn.ExecContext(ctx, "UPDATE ledger_meta SET generation=generation+1 WHERE id=1 AND database_id=? AND generation=?", databaseID, generation)
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		if err != nil || count != 1 {
			return fmt.Errorf("ledger generation conflict: %w", errors.Join(err, errRefreshRetry))
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	if changed {
		base.Generation++
	}
	return nil
}

// discardReportConnection prevents reuse of a connection with uncertain transaction state.
//
// Example: A failed rollback removes the connection from the pool.
func discardReportConnection(any) error {
	return driver.ErrBadConn
}

// applyReportEdit applies owner edits without changing unrelated report data.
//
// Example: Set a field only while it remains absent.
func applyReportEdit(
	original []byte,
	edit reportEdit,
) ([]byte, error) {
	value, err := decodeReportValue(original)
	if err != nil {
		return nil, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("report must be an object")
	}
	if edit.Ops == nil {
		return nil, fmt.Errorf("ops must be an array")
	}
	for _, op := range edit.Ops {
		if len(op.Path) == 0 || op.Path[0] == "status_sync" {
			return nil, fmt.Errorf("invalid owner edit path")
		}
		updated, err := editReportPath(root, op.Path, op)
		if err != nil {
			return nil, err
		}
		root = updated.(map[string]any)
	}
	return json.Marshal(root)
}

// editReportPath descends into objects or arrays and replaces only the selected value.
//
// Example: Select the first task with the path component "0".
func editReportPath(
	container any,
	path []string,
	op reportEditOperation,
) (any, error) {
	key := path[0]
	if key == "" {
		return nil, fmt.Errorf("empty path component")
	}
	var current any
	var present bool
	var index int
	switch parent := container.(type) {
	case map[string]any:
		current, present = parent[key]
	case []any:
		var err error
		index, err = strconv.Atoi(key)
		if err != nil || index < 0 || strconv.Itoa(index) != key || index > len(parent) {
			return nil, fmt.Errorf("invalid array index %q", key)
		}
		if index < len(parent) {
			current, present = parent[index], true
		}
	default:
		return nil, fmt.Errorf("path component %q has no container", key)
	}
	if len(path) > 1 {
		if !present {
			return nil, fmt.Errorf("missing path component %q", key)
		}
		updated, err := editReportPath(current, path[1:], op)
		if err != nil {
			return nil, err
		}
		switch parent := container.(type) {
		case map[string]any:
			parent[key] = updated
		case []any:
			parent[index] = updated
		}
		return container, nil
	}
	if err := verifyReportEditExpectation(current, present, op.Expect); err != nil {
		return nil, fmt.Errorf("field %q: %w", key, err)
	}
	switch op.Op {
	case "set":
		updated, err := decodeReportValue(op.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid set value: %w", err)
		}
		switch parent := container.(type) {
		case map[string]any:
			parent[key] = updated
		case []any:
			if !present {
				return append(parent, updated), nil
			}
			parent[index] = updated
		}
	case "delete":
		if op.Value != nil || !present {
			return nil, fmt.Errorf("invalid delete of %q", key)
		}
		switch parent := container.(type) {
		case map[string]any:
			delete(parent, key)
		case []any:
			return append(parent[:index], parent[index+1:]...), nil
		}
	default:
		return nil, fmt.Errorf("invalid operation %q", op.Op)
	}
	return container, nil
}

// verifyReportEditExpectation checks exact owner preconditions for edits and reconciliation.
//
// Example: Explicit null, absence, and the string null remain distinct.
func verifyReportEditExpectation(
	current any,
	present bool,
	expect reportEditExpectation,
) error {
	switch expect.Kind {
	case "absent":
		if present || expect.Value != nil {
			return fmt.Errorf("expected absent field")
		}
	case "null":
		if !present || current != nil || expect.Value != nil {
			return fmt.Errorf("expected null field")
		}
	case "value":
		expected, err := decodeReportValue(expect.Value)
		if err != nil {
			return err
		}
		if expected == nil || !present || !reflect.DeepEqual(current, expected) {
			return fmt.Errorf("expected value mismatch")
		}
	default:
		return fmt.Errorf("invalid expectation %q", expect.Kind)
	}
	return nil
}
