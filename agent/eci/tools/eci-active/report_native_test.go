package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNativeReportRequiresExistingDatabase preserves explicit initialization.
//
// Example: report editing cannot silently create a native store.
func TestNativeReportRequiresExistingDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := openNativeLedgerRead(t.Context(), dir)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(dir, "ledger.db"))
	require.True(t, os.IsNotExist(err))
}
