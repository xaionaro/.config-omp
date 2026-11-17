package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)


// TestGuardSQLiteFilesRejectsAliases checks all SQLite-managed file names.
//
// Example: A hardlinked WAL must never be opened under another session name.
func TestGuardSQLiteFilesRejectsAliases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	main := filepath.Join(dir, "ledger.db")
	require.Error(t, guardSQLiteFiles(main))
	require.NoError(t, os.WriteFile(main, []byte("database"), 0600))
	require.NoError(t, guardSQLiteFiles(main))
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := main + suffix
		if suffix != "" {
			require.NoError(t, os.WriteFile(path, []byte("sidecar"), 0600))
		}
		alias := filepath.Join(dir, "alias")
		require.NoError(t, os.Link(path, alias))
		require.Error(t, guardSQLiteFiles(main))
		require.NoError(t, os.Remove(alias))
		require.NoError(t, guardSQLiteFiles(main))
	}
}

// TestProjectionSnapshotPreservesPresence checks empty sources and absent reports.
//
// Example: A present empty forecast differs from an absent forecast.
func TestProjectionSnapshotPreservesPresence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, session_id TEXT, generation INTEGER, database_id TEXT, ledger_present INTEGER, forecasts_present INTEGER);
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT, revision INTEGER);
CREATE TABLE sections(id TEXT, value TEXT);
CREATE TABLE records(id TEXT, value TEXT);
CREATE TABLE forecasts(id TEXT, value TEXT);
INSERT INTO ledger_meta VALUES(1, ?, 4, '0123456789abcdef0123456789abcdef', 1, 1);`, filepath.Base(dir))
	require.NoError(t, err)
	absent, err := loadProjectionSnapshot(context.Background(), db, dir)
	require.NoError(t, err)
	require.Equal(t, "0123456789abcdef0123456789abcdef", absent.DatabaseID)
	_, err = db.Exec(`UPDATE ledger_meta SET session_id='other'`)
	require.NoError(t, err)
	_, err = loadProjectionSnapshot(context.Background(), db, dir)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE ledger_meta SET session_id=?`, filepath.Base(dir))
	require.NoError(t, err)
	require.False(t, absent.ReportPresent)
	_, err = db.Exec(`INSERT INTO report VALUES(1, '{"unknown":1.10}', 7)`)
	require.NoError(t, err)
	snapshot, err := readProjectionDB(dir)
	require.NoError(t, err)
	require.True(t, snapshot.ReportPresent)
	require.Equal(t, int64(4), snapshot.Generation)
	require.Equal(t, int64(7), snapshot.Revision)
	require.JSONEq(t, `{"unknown":1.10}`, string(snapshot.Report))
	require.NotNil(t, snapshot.Sources.ledger.Records)
	require.True(t, snapshot.Sources.forecastPresent)
	require.NotNil(t, snapshot.Sources.forecasts.Records)
	_, err = db.Exec(`UPDATE report SET document_json='[]', revision=-1`)
	require.NoError(t, err)
	_, err = readProjectionDB(dir)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE report SET document_json='{"nested":{"key":1,"\u006bey":2}}', revision=7`)
	require.NoError(t, err)
	_, err = readProjectionDB(dir)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE report SET document_json='{"unknown":1.10}', revision=7`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sections VALUES('work', '{"title":"Work","opaque":1e400}');
INSERT INTO records VALUES('work:one', '{"section":"work","type":"work","data":{"owner":"alice","state":"active"},"evidence":[]}');
INSERT INTO forecasts VALUES('forecast:one', '{"kind":"lane","status":"active","outcome":"Result","opaque":{"large":1e400,"precise":1.10}}');`)
	require.NoError(t, err)
	snapshot, err = readProjectionDB(dir)
	require.NoError(t, err)
	require.Equal(t, "alice", snapshot.Sources.ledger.Records["work:one"].Data.Owner)
	require.Equal(t, json.Number("1e400"), snapshot.Sources.ledger.Sections["work"].(map[string]any)["opaque"])
	require.Equal(t, json.Number("1.10"), snapshot.Sources.forecasts.Records["forecast:one"]["opaque"].(map[string]any)["precise"])
	require.Equal(t, json.Number("1e400"), snapshot.Sources.forecasts.Records["forecast:one"]["opaque"].(map[string]any)["large"])
	require.Equal(t, "Result", snapshot.Sources.forecasts.Records["forecast:one"]["outcome"])
	_, err = db.Exec(`UPDATE records SET value='{"section":"work","type":"work","data":{"owner":7},"evidence":[]}'`)
	require.NoError(t, err)
	_, err = readProjectionDB(dir)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE records SET value='{"section":"work","type":"work","data":{"owner":"alice","state":"active"},"evidence":[]}'`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE ledger_meta SET forecasts_present=0`)
	require.NoError(t, err)
	_, err = readProjectionDB(dir)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE ledger_meta SET forecasts_present=1;
UPDATE records SET value='{"section":"work","type":"work","data":{},"evidence":[],"links":{"outcomes":["missing"]}}'`)
	require.NoError(t, err)
	_, err = readProjectionDB(dir)
	require.Error(t, err)
	_, err = db.Exec(`UPDATE records SET value='{"section":"work","type":"work","data":{},"evidence":[]}';
UPDATE ledger_meta SET database_id='different'`)
	require.NoError(t, err)
	_, err = readProjectionDB(dir)
	require.Error(t, err)
}
