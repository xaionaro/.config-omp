package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLedgerDBLifecycle creates a session store, applies batches and reads the result through the built binary.
//
// Example: init, put a section and a record, read both back with generation 1.
func TestLedgerDBLifecycle(t *testing.T) {
	t.Parallel()
	fx := newWrapperFixture(t)

	stdout, stderr, code := fx.run(t, 0, "ledger-init")
	require.Equal(t, 0, code, stderr)
	path := filepath.Join(fx.contextDir(), ledgerDBName)
	require.Equal(t, path, strings.TrimSpace(stdout))

	_, stderr, code = fx.run(t, 0, "ledger-init")
	require.Equal(t, 0, code, "re-init of an existing store must succeed: %s", stderr)

	ops := `[{"op":"put","table":"sections","id":"work","value":{"title":"Work"}},` +
		`{"op":"put","table":"records","id":"leaf:1.1","value":{"section":"work","type":"leaf","data":{"work":"design"},"evidence":[]}},` +
		`{"op":"put","table":"forecasts","id":"tree","value":{"kind":"tree","status":"active"}}]`
	stdout, stderr, code = fx.run(t, 0, "ledger-apply", "--json", ops)
	require.Equal(t, 0, code, stderr)
	require.Equal(t, "ledger generation 1", strings.TrimSpace(stdout))

	_, stderr, code = fx.run(t, 0, "ledger-apply", "--json",
		`[{"op":"put","table":"records","id":"dangling","value":{"section":"work","type":"leaf","data":{},"evidence":[],"links":{"to":["missing"]}}}]`)
	require.Equal(t, 1, code, "a batch with a dangling link must be refused")
	require.Contains(t, stderr, "no change was committed")

	stdout, stderr, code = fx.run(t, 0, "ledger-read")
	require.Equal(t, 0, code, stderr)
	var doc ledgerDocument
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc))
	require.EqualValues(t, 1, doc.Generation)
	require.Contains(t, doc.Sections, "work")
	require.Contains(t, doc.Records, "leaf:1.1")
	require.NotContains(t, doc.Records, "dangling")
	require.Contains(t, doc.Forecasts, "tree")

	stdout, stderr, code = fx.run(t, 0, "ledger-apply", "--json",
		`[{"op":"patch","table":"records","id":"leaf:1.1","expect":{"type":"leaf"},"value":{"data":{"work":"design","closed":true}}}]`)
	require.Equal(t, 0, code, stderr)
	require.Equal(t, "ledger generation 2", strings.TrimSpace(stdout))
}

// TestLedgerApplyRequiresStore refuses to mutate a session that has no store, and creates none.
//
// Example: ledger-apply before ledger-init exits 1.
func TestLedgerApplyRequiresStore(t *testing.T) {
	t.Parallel()
	fx := newWrapperFixture(t)
	_, _, code := fx.run(t, 0, "ledger-apply", "--json", `[]`)
	require.Equal(t, 1, code)
	_, err := os.Lstat(filepath.Join(fx.contextDir(), ledgerDBName))
	require.ErrorIs(t, err, os.ErrNotExist)
}
