package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// sessionLogTestSchema mirrors the log table of eci-active's ledger.db schema, the store's only writer.
//
// Example: writeSessionLog creates it in a fresh test store.
const sessionLogTestSchema = `CREATE TABLE log (seq INTEGER PRIMARY KEY AUTOINCREMENT, entry TEXT NOT NULL CHECK (json_valid(entry) AND json_type(entry) IS 'object')) STRICT;`

// writeSessionLog replaces dir's session store with one whose log holds each newline-terminated line of log.
// A trailing line without a newline is an error: the store holds only complete entries.
//
// Example: writeSessionLog(dir, []byte(event+"\n")) seeds a one-event log.
func writeSessionLog(dir string, log []byte) (_err error) {
	path := filepath.Join(dir, sessionLedgerDBName)
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(log) > 0 && log[len(log)-1] != '\n' {
		return fmt.Errorf("session log fixture has an unterminated final entry")
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer func() { _err = errors.Join(_err, db.Close()) }()
	if _, err := db.Exec(sessionLogTestSchema); err != nil {
		return err
	}
	for _, line := range bytes.SplitAfter(log, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if _, err := db.Exec("INSERT INTO log(entry) VALUES(?)", string(bytes.TrimSuffix(line, []byte("\n")))); err != nil {
			return fmt.Errorf("insert session log entry %q: %w", line, err)
		}
	}
	return nil
}

// TestReadSessionLogRoundTrip reads seeded entries back as newline-terminated lines in order.
//
// Example: two entries read back exactly as written.
func TestReadSessionLogRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := readSessionLog(dir)
	require.ErrorIs(t, err, os.ErrNotExist)

	log := "{\"event\":\"a\"}\n{\"event\":\"b\"}\n"
	require.NoError(t, writeSessionLog(dir, []byte(log)))
	got, err := readSessionLog(dir)
	require.NoError(t, err)
	require.Equal(t, log, string(got))
}
