package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)


// TestGuardSQLiteFilesRejectsAliases protects SQLite's main and sidecar ownership.
//
// Example: a hardlinked WAL is rejected before any database open.
func TestGuardSQLiteFilesRejectsAliases(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			main := filepath.Join(dir, "ledger.db")
			require.NoError(t, os.WriteFile(main, []byte("main"), 0600))
			path := main + suffix
			if suffix != "" {
				require.NoError(t, os.WriteFile(path, []byte("sidecar"), 0600))
			}
			require.NoError(t, guardSQLiteFiles(main))
			require.NoError(t, os.Link(path, filepath.Join(dir, "alias")))
			require.Error(t, guardSQLiteFiles(main))
			require.NoError(t, os.Remove(filepath.Join(dir, "alias")))
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Symlink(filepath.Join(dir, "elsewhere"), path))
			require.Error(t, guardSQLiteFiles(main))
		})
	}
	require.Error(t, guardSQLiteFiles(filepath.Join(t.TempDir(), "missing")))
}

