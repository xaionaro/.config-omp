package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeReconcileRequiresVerifiedReceipt exercises the real database transaction.
//
// Example: Invalid cleanup removes a pending entry but cannot authorize publication.
func TestNativeReconcileRequiresVerifiedReceipt(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(strconv.FormatBool(invalid), func(t *testing.T) {
			dir := t.TempDir()
			log := []byte(`{"event":"assignment_change","details":{"status_update":{"updates":[{"target":"report","id":"current","fields":{"next_focus":"applied"}}]}}}` + "\n")
			document := []byte(`{"timestamp":"2026-11-02T15:24:00Z","state":"Working","progress":[],"decisions":[],"stuck_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}}`)
			prepared, err := prepareBaseline(document, log, map[string]baselineMode{"1/u/0":baselinePending})
			require.NoError(t, err)
			if invalid {
				log = []byte(`{"event":"correction","details":{"status_update":{"updates":[{"target":"report","id":"current","corrects":1,"corrects_ordinal":0,"fields":{"next_focus":"applied"}}]}}}` + "\n")
				value, err := decodeReportValue(prepared); require.NoError(t, err)
				root := value.(map[string]any)
				raw, err := json.Marshal(root["status_sync"]); require.NoError(t, err)
				var state syncState; require.NoError(t, json.Unmarshal(raw, &state))
				group := state.Groups[syncGroupKey(1, 0)]
				update, err := decodeSyncGroup(group.Update); require.NoError(t, err)
				ordinal := 0; update.Corrects = 1; update.CorrectsOrdinal = &ordinal
				group.Update, err = encodeSyncGroup(update); require.NoError(t, err)
				state.Groups[syncGroupKey(1, 0)] = group; root["status_sync"] = state
				prepared, err = json.Marshal(root); require.NoError(t, err)
			}
			path := filepath.Join(dir, "high_level_log.jsonl")
			require.NoError(t, os.WriteFile(path, log, 0600))
			info, err := os.Stat(path); require.NoError(t, err)
			stat := info.Sys().(*syscall.Stat_t)
			wire := map[string]any{
				"base":map[string]string{"database_id":"0123456789abcdef0123456789abcdef", "generation":"3"},
				"log":map[string]any{"present":true,"device":strconv.FormatUint(uint64(stat.Dev),10),"inode":strconv.FormatUint(uint64(stat.Ino),10),"bytes_base64":base64.StdEncoding.EncodeToString(log)},
				"action":1,"pending":map[string]any{"kind":1,"line":1,"ordinal":0},
				"expected":map[string]any{"next_focus":map[string]any{"kind":"value","value":"Next"}},
			}
			if invalid { wire["predecessor"] = map[string]any{"kind":1,"line":1,"ordinal":0} }
			input, err := json.Marshal(wire); require.NoError(t, err)
			db, err := sql.Open("sqlite", filepath.Join(dir,"ledger.db")); require.NoError(t, err)
			_, err = db.Exec(`PRAGMA user_version=1;
CREATE TABLE ledger_meta(id INTEGER PRIMARY KEY, session_id TEXT, database_id TEXT, generation INTEGER, ledger_present INTEGER, forecasts_present INTEGER);
CREATE TABLE sections(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE records(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE forecasts(id TEXT PRIMARY KEY, value TEXT);
CREATE TABLE report(id INTEGER PRIMARY KEY, document_json TEXT, revision INTEGER);
INSERT INTO ledger_meta VALUES(1, ?, '0123456789abcdef0123456789abcdef', 3, 1, 0)`, filepath.Base(dir)); require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO report VALUES(1, ?, 5)`, string(prepared)); require.NoError(t, err)
			var stored string
			require.NoError(t, db.QueryRow(`SELECT document_json FROM report`).Scan(&stored))
			require.Equal(t, string(prepared), stored)
			require.NoError(t, db.Close())
			var out, diagnostics bytes.Buffer
			code := run([]string{"report-reconcile-locked",dir,"--stdin"},bytes.NewReader(input),&out,&diagnostics)
			db, err = sql.Open("sqlite", filepath.Join(dir,"ledger.db")); require.NoError(t, err); defer db.Close()
			var generation, revision int; var actual string
			require.NoError(t, db.QueryRow(`SELECT generation FROM ledger_meta`).Scan(&generation))
			require.NoError(t, db.QueryRow(`SELECT revision, document_json FROM report`).Scan(&revision,&actual))
			if invalid {
				require.Equal(t, 1, code); require.Empty(t, out.String()); require.Contains(t, diagnostics.String(), "selected operation did not project")
				require.Equal(t, 3, generation); require.Equal(t, 5, revision); require.Equal(t, string(prepared), actual)
			} else {
				require.Equal(t, 0, code, diagnostics.String()); require.Equal(t, 4, generation); require.Equal(t, 6, revision)
				parsed, err := parseDBReport([]byte(actual)); require.NoError(t, err); require.Equal(t,"applied",parsed.NextFocus)
			}
		})
	}
}
