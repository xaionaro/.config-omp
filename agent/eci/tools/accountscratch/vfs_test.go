package accountscratch

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/xaionaro-go/observability"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// TestScratchDatabaseSpill exercises actual SQLite temporary storage.
//
// Example: a disk-backed TEMP table is readable and its scratch is removed.
func TestScratchDatabaseSpill(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "source.db"), root)
	require.NoError(t, err)
	for _, statement := range []string{
		"PRAGMA temp_store=FILE", "PRAGMA cache_size=5", "PRAGMA temp.cache_size=5",
		"CREATE TEMP TABLE spill(x BLOB)", "INSERT INTO spill VALUES(zeroblob(5000000))",
	} {
		_, err := h.Exec(statement)
		require.NoError(t, err)
	}
	var size int
	require.NoError(t, h.QueryRow("SELECT length(x) FROM spill").Scan(&size))
	require.Equal(t, 5000000, size)
	require.Positive(t, h.scratchOpens())
	require.NoError(t, h.Close())
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), "eci-sqlite-"), "retained scratch %s", entry.Name())
	}
}

// TestMissingScratchNeverFallsBack rejects missing account temporary roots.
//
// Example: a usable ambient TMPDIR is not used as an alternative.
func TestMissingScratchNeverFallsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	_, err := openAt("file:"+filepath.Join(root, "db"), filepath.Join(root, "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// TestIndependentHandles creates separate VFS registrations and lifetimes.
//
// Example: closing one handle cannot invalidate the other.
func TestIndependentHandles(t *testing.T) {
	root := t.TempDir()
	first, err := openAt("file:"+filepath.Join(root, "a.db"), root)
	require.NoError(t, err)
	second, err := openAt("file:"+filepath.Join(root, "b.db"), root)
	require.NoError(t, err)
	require.NoError(t, first.Close())
	_, err = second.Exec("CREATE TABLE test(x)")
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

// TestAuxiliaryClassesUseOwnedScratch covers named and unnamed SQLite opens.
//
// Example: a named subjournal is redirected before its original name is used.
func TestAuxiliaryClassesUseOwnedScratch(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	require.NoError(t, err)
	tls := libc.NewTLS()
	defer tls.Close()
	ignored, err := libc.CString(filepath.Join(root, "must-not-exist"))
	require.NoError(t, err)
	defer libc.Xfree(tls, ignored)
	for _, class := range []int32{sqlite3.SQLITE_OPEN_TEMP_DB, sqlite3.SQLITE_OPEN_TEMP_JOURNAL, sqlite3.SQLITE_OPEN_TRANSIENT_DB, sqlite3.SQLITE_OPEN_SUBJOURNAL} {
		for _, name := range []uintptr{0, ignored} {
			file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
			require.NotZero(t, file)
			output := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(int32(0))))
			require.NotZero(t, output)
			flags := class | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE
			rc := openFile(tls, h.vfs, name, file, flags, output)
			require.Equal(t, int32(sqlite3.SQLITE_OK), rc, "class %#x name %d", class, name)
			require.NotZero(t, *(*int32)(unsafe.Pointer(output))&sqlite3.SQLITE_OPEN_READWRITE)
			require.Equal(t, int32(sqlite3.SQLITE_OK), closeFile(tls, file))
			sqlite3.Xsqlite3_free(tls, output)
			sqlite3.Xsqlite3_free(tls, file)
		}
	}
	_, err = os.Lstat(filepath.Join(root, "must-not-exist"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, h.Close())
}

// TestRetainedScratchIsReported preserves unexpected contents on close.
//
// Example: an unexpected file causes a diagnostic, not recursive deletion.
func TestRetainedScratchIsReported(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	require.NoError(t, err)
	retained := filepath.Join(h.dir, "unexpected")
	require.NoError(t, os.WriteFile(retained, []byte("keep"), 0600))
	err = h.Close()
	require.Error(t, err)
	require.Contains(t, err.Error(), h.dir)
	data, err := os.ReadFile(retained)
	require.NoError(t, err)
	require.Equal(t, "keep", string(data))
}

// TestOwnedCleanupPreservesReplacement avoids removing another file.
//
// Example: a path replaced after exclusive creation is left untouched.
func TestOwnedCleanupPreservesReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	f, err := os.CreateTemp(root, "owned-")
	require.NoError(t, err)
	info, err := f.Stat()
	require.NoError(t, err)
	path := f.Name()
	require.NoError(t, os.Rename(path, path+".original"))
	require.NoError(t, os.WriteFile(path, []byte("replacement"), 0600))
	require.Error(t, closeOwned(f, info))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "replacement", string(data))
}

// TestConcurrentHandles isolates independent SQLite VFS registrations.
//
// Example: four simultaneous TEMP spills close without crossing lifetimes.
func TestConcurrentHandles(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	ready := make(chan struct{}, 4)
	start := make(chan struct{})
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		n := i
		observability.Go(ctx,
			// exerciseHandle opens and closes one independent database.
			//
			// Example: another handle's VFS remains registered throughout.
			func(context.Context) {
				ready <- struct{}{}
				<-start
				h, err := openAt("file:"+filepath.Join(root, fmt.Sprintf("db-%d", n)), root)
				if err == nil {
					for _, statement := range []string{"PRAGMA temp_store=FILE", "PRAGMA temp.cache_size=5", "CREATE TEMP TABLE spill(x)", "INSERT INTO spill VALUES(zeroblob(5000000))"} {
						if _, err = h.Exec(statement); err != nil {
							break
						}
					}
					err = errors.Join(err, h.Close())
				}
				results <- err
			},
		)
	}
	for i := 0; i < 4; i++ {
		<-ready
	}
	close(start)
	var errs []error
	for i := 0; i < 4; i++ {
		errs = append(errs, <-results)
	}
	require.NoError(t, errors.Join(errs...))
}

// TestCloseRetainsVFSForCheckedOutConnection prevents use-after-free.
//
// Example: a connection outliving DB.Close can still open temporary storage.
func TestCloseRetainsVFSForCheckedOutConnection(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	require.NoError(t, err)
	conn, err := h.Conn(context.Background())
	require.NoError(t, err)
	require.Zero(t, h.active)
	require.Error(t, h.Close())
	_, err = os.Lstat(h.dir)
	require.NoError(t, err)
	for _, statement := range []string{"PRAGMA temp_store=FILE", "PRAGMA temp.cache_size=5", "CREATE TEMP TABLE spill(x)", "INSERT INTO spill VALUES(zeroblob(5000000))"} {
		_, err := conn.ExecContext(context.Background(), statement)
		require.NoError(t, err)
	}
	require.NoError(t, conn.Close())
	require.NoError(t, h.Close())
	_, err = os.Lstat(h.dir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestConcurrentCloseSerializesVFSRetirement prevents double unregister and free.
//
// Example: concurrent callers receive the same completed close result.
func TestConcurrentCloseSerializesVFSRetirement(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	require.NoError(t, err)
	_, err = h.Exec("CREATE TABLE x(v)")
	require.NoError(t, err)
	ctx := context.Background()
	ready := make(chan struct{}, 8)
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		observability.Go(ctx,
			// closeHandle races other callers on the same database handle.
			//
			// Example: only one caller performs VFS retirement.
			func(context.Context) {
				ready <- struct{}{}
				<-start
				results <- h.Close()
			},
		)
	}
	for i := 0; i < 8; i++ {
		<-ready
	}
	close(start)
	var errs []error
	for i := 0; i < 8; i++ {
		errs = append(errs, <-results)
	}
	require.NoError(t, errors.Join(errs...))
}

// TestCloseRetainsVFSForRowsAndTransaction covers checked-out SQL resources.
//
// Example: rows and transactions outlive DB.Close until explicitly released.
func TestCloseRetainsVFSForRowsAndTransaction(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "rows.db"), root)
	require.NoError(t, err)
	rows, err := h.Query("SELECT 1 UNION ALL SELECT 2")
	require.NoError(t, err)
	require.Zero(t, h.active)
	require.Error(t, h.Close())
	require.True(t, rows.Next(), "rows invalid after close: %v", rows.Err())
	require.NoError(t, rows.Close())
	require.NoError(t, h.Close())

	h, err = openAt("file:"+filepath.Join(root, "tx.db"), root)
	require.NoError(t, err)
	tx, err := h.Begin()
	require.NoError(t, err)
	require.Zero(t, h.active)
	require.Error(t, h.Close())
	_, err = tx.Exec("CREATE TEMP TABLE x(v)")
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	require.NoError(t, h.Close())
}

// TestCloseRetainsVFSForMixedIdleConnections verifies deterministic retirement.
//
// Example: DB.Close retires idle connections but retains a checked-out one.
func TestCloseRetainsVFSForMixedIdleConnections(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "multi.db"), root)
	require.NoError(t, err)
	h.SetMaxOpenConns(2)
	first, err := h.Conn(context.Background())
	require.NoError(t, err)
	second, err := h.Conn(context.Background())
	require.NoError(t, err)
	require.NoError(t, second.Close())
	require.Error(t, h.Close())
	_, err = first.ExecContext(context.Background(), "SELECT 1")
	require.NoError(t, err)
	require.NoError(t, first.Close())
	require.NoError(t, h.Close())
}

// TestBadConnectionDiscardRetiresVFS exercises database/sql's real discard path.
//
// Example: ErrBadConn closes the real modernc connection before VFS retirement.
func TestBadConnectionDiscardRetiresVFS(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "bad.db"), root)
	require.NoError(t, err)
	conn, err := h.Conn(context.Background())
	require.NoError(t, err)
	err = conn.Raw(
		// discardBadConnection requests database/sql's actual bad-connection path.
		//
		// Example: the driver connection is closed before the handle retires.
		func(any) error { return driver.ErrBadConn },
	)
	require.ErrorIs(t, err, driver.ErrBadConn)
	require.NoError(t, h.Close())
	_, err = os.Lstat(h.dir)
	require.ErrorIs(t, err, os.ErrNotExist)
}
