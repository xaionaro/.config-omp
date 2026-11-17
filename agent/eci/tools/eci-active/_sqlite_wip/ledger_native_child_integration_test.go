package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeInitializationWithReportChild exercises the real report preparation child.
//
// Example: an explicitly observed absent log initializes a complete report without YAML.
func TestNativeInitializationWithReportChild(t *testing.T) {
	fx := newWrapperFixture(t)
	child := filepath.Join(filepath.Dir(fx.binary), "status-report")
	built := child + ".built"
	build := exec.Command("go", "build", "-mod=readonly", "-o", built, ".")
	build.Dir = "../status-report"
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.NoError(t, os.Rename(built, child))

	input := `{"expect":"uninitialized","ledger":{"present":true,"sections":{},"records":{}},"forecasts":{"present":false},"report":{"present":true,"document":{"timestamp":"2026-11-02T15:24:00Z","state":"Working","progress":[],"decisions":[],"blockers_risks":[],"verification":[],"next_focus":"Next","scope_complete":true,"tasks":[],"top_priorities":[],"completion_estimate":{"min_minutes":10,"max_minutes":20,"as_of":"2026-11-02T15:24:00Z","basis":"guess","assumptions":"none","dependencies":"none","critical_path":"work"}},"decisions":{},"log":{"present":false,"device":"","inode":"","bytes_base64":""}}}`
	stdout, stderr, code := fx.runInput(t, 0, input, "ledger-init", "--stdin")
	require.Zero(t, code, "%s", stderr)
	_, err = decodeNativeObservation([]byte(stdout))
	require.NoError(t, err)
	db, err := openNativeLedgerRead(context.Background(), fx.contextDir())
	require.NoError(t, err)
	defer db.Close()
	var document []byte
	require.NoError(t, db.QueryRow("SELECT document_json FROM report WHERE id=1").Scan(&document))
	require.True(t, json.Valid(document))
}
