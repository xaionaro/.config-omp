package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// sessionLedgerDBName is the per-session SQLite store that eci-active owns; its log table holds the high-level log.
//
// Example: <home>/.agent/context/sessions/<sid>/ledger.db.
const sessionLedgerDBName = "ledger.db"

// readSessionLog returns the session's high-level log as newline-terminated JSON lines in sequence order.
// A session without a store yields an error matching os.ErrNotExist.
//
// Example: two appended events read back as "{...}\n{...}\n".
func readSessionLog(dir string) (_ []byte, _err error) {
	path := filepath.Join(dir, sessionLedgerDBName)
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open session ledger: %w", err)
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	rows, err := db.QueryContext(context.Background(), "SELECT entry FROM log ORDER BY seq")
	if err != nil {
		return nil, fmt.Errorf("read session log: %w", err)
	}
	defer func() { _err = errors.Join(_err, rows.Close()) }()
	var log bytes.Buffer
	for rows.Next() {
		var entry []byte
		if err := rows.Scan(&entry); err != nil {
			return nil, fmt.Errorf("scan session log: %w", err)
		}
		log.Write(entry)
		log.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session log: %w", err)
	}
	return log.Bytes(), nil
}

// readRefreshInput rereads one refresh input for the publish-time change check; the session store yields its log.
//
// Example: a log entry appended during a refresh makes the reread differ and the refresh retry.
func readRefreshInput(path string) ([]byte, error) {
	if filepath.Base(path) == sessionLedgerDBName {
		return readSessionLog(filepath.Dir(path))
	}
	return os.ReadFile(path)
}
