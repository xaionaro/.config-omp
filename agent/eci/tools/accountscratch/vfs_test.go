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

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// TestScratchDatabaseSpill exercises actual SQLite temporary storage.
//
// Example: a disk-backed TEMP table is readable and its scratch is removed.
func TestScratchDatabaseSpill(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "source.db"), root)
	if err != nil { t.Fatal(err) }
	for _, statement := range []string{
		"PRAGMA temp_store=FILE", "PRAGMA cache_size=5", "PRAGMA temp.cache_size=5",
		"CREATE TEMP TABLE spill(x BLOB)", "INSERT INTO spill VALUES(zeroblob(5000000))",
	} {
		if _, err := h.Exec(statement); err != nil { t.Fatal(err) }
	}
	var size int
	if err := h.QueryRow("SELECT length(x) FROM spill").Scan(&size); err != nil || size != 5000000 { t.Fatalf("size=%d err=%v", size, err) }
	if h.scratchOpens() == 0 { t.Fatal("SQLite did not open a scratch file") }
	if err := h.Close(); err != nil { t.Fatal(err) }
	entries, err := os.ReadDir(root)
	if err != nil { t.Fatal(err) }
	for _, entry := range entries { if strings.HasPrefix(entry.Name(), "eci-sqlite-") { t.Fatalf("retained scratch %s", entry.Name()) } }
}

// TestMissingScratchNeverFallsBack rejects missing account temporary roots.
//
// Example: a usable ambient TMPDIR is not used as an alternative.
func TestMissingScratchNeverFallsBack(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	_, err := openAt("file:"+filepath.Join(root, "db"), filepath.Join(root, "missing"))
	if err == nil { t.Fatal("missing scratch root accepted") }
	entries, err := os.ReadDir(root)
	if err != nil { t.Fatal(err) }
	if len(entries) != 0 { t.Fatalf("unexpected fallback files: %v", entries) }
}

// TestIndependentHandles creates separate VFS registrations and lifetimes.
//
// Example: closing one handle cannot invalidate the other.
func TestIndependentHandles(t *testing.T) {
	root := t.TempDir()
	first, err := openAt("file:"+filepath.Join(root, "a.db"), root)
	if err != nil { t.Fatal(err) }
	second, err := openAt("file:"+filepath.Join(root, "b.db"), root)
	if err != nil { t.Fatal(err) }
	if err := first.Close(); err != nil { t.Fatal(err) }
	if _, err := second.Exec("CREATE TABLE test(x)"); err != nil { t.Fatal(err) }
	if err := second.Close(); err != nil { t.Fatal(err) }
}

// TestAuxiliaryClassesUseOwnedScratch covers named and unnamed SQLite opens.
//
// Example: a named subjournal is redirected before its original name is used.
func TestAuxiliaryClassesUseOwnedScratch(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	if err != nil { t.Fatal(err) }
	tls := libc.NewTLS()
	defer tls.Close()
	ignored, err := libc.CString(filepath.Join(root, "must-not-exist"))
	if err != nil { t.Fatal(err) }
	defer libc.Xfree(tls, ignored)
	for _, class := range []int32{sqlite3.SQLITE_OPEN_TEMP_DB, sqlite3.SQLITE_OPEN_TEMP_JOURNAL, sqlite3.SQLITE_OPEN_TRANSIENT_DB, sqlite3.SQLITE_OPEN_SUBJOURNAL} {
		for _, name := range []uintptr{0, ignored} {
			file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
			if file == 0 { t.Fatal("allocate SQLite file") }
			output := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(int32(0))))
			if output == 0 { t.Fatal("allocate SQLite output flags") }
			flags := class | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE
			rc := openFile(tls, h.vfs, name, file, flags, output)
			if rc != sqlite3.SQLITE_OK { t.Fatalf("class %#x name %d: code %d", class, name, rc) }
			if *(*int32)(unsafe.Pointer(output))&sqlite3.SQLITE_OPEN_READWRITE == 0 { t.Fatal("auxiliary file is not writable") }
			if rc := closeFile(tls, file); rc != sqlite3.SQLITE_OK { t.Fatalf("close code %d", rc) }
			sqlite3.Xsqlite3_free(tls, output)
			sqlite3.Xsqlite3_free(tls, file)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "must-not-exist")); !os.IsNotExist(err) { t.Fatalf("original name used: %v", err) }
	if err := h.Close(); err != nil { t.Fatal(err) }
}

// TestRetainedScratchIsReported preserves unexpected contents on close.
//
// Example: an unexpected file causes a diagnostic, not recursive deletion.
func TestRetainedScratchIsReported(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	if err != nil { t.Fatal(err) }
	retained := filepath.Join(h.dir, "unexpected")
	if err := os.WriteFile(retained, []byte("keep"), 0600); err != nil { t.Fatal(err) }
	if err := h.Close(); err == nil || !strings.Contains(err.Error(), h.dir) { t.Fatalf("missing retained-path diagnostic: %v", err) }
	data, err := os.ReadFile(retained)
	if err != nil || string(data) != "keep" { t.Fatalf("retained content: %q %v", data, err) }
}

// TestOwnedCleanupPreservesReplacement avoids removing another file.
//
// Example: a path replaced after exclusive creation is left untouched.
func TestOwnedCleanupPreservesReplacement(t *testing.T) {
	root := t.TempDir()
	f, err := os.CreateTemp(root, "owned-")
	if err != nil { t.Fatal(err) }
	info, err := f.Stat()
	if err != nil { t.Fatal(err) }
	path := f.Name()
	if err := os.Rename(path, path+".original"); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil { t.Fatal(err) }
	if err := closeOwned(f, info); err == nil || !strings.Contains(err.Error(), "replaced") { t.Fatalf("missing identity error: %v", err) }
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" { t.Fatalf("replacement changed: %q %v", data, err) }
}

// TestConcurrentHandles isolates independent SQLite VFS registrations.
//
// Example: four simultaneous TEMP spills close without crossing lifetimes.
func TestConcurrentHandles(t *testing.T) {
	root := t.TempDir()
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		// exerciseHandle opens and closes one independent database.
		//
		// Example: another handle's VFS remains registered throughout.
		go func(n int) {
			h, err := openAt("file:"+filepath.Join(root, fmt.Sprintf("db-%d", n)), root)
			if err == nil {
				for _, statement := range []string{"PRAGMA temp_store=FILE", "PRAGMA temp.cache_size=5", "CREATE TEMP TABLE spill(x)", "INSERT INTO spill VALUES(zeroblob(5000000))"} {
					if _, err = h.Exec(statement); err != nil { break }
				}
				err = errors.Join(err, h.Close())
			}
			results <- err
		}(i)
	}
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil { t.Fatal(err) }
	}
}

// TestCloseRetainsVFSForCheckedOutConnection prevents use-after-free.
//
// Example: a connection outliving DB.Close can still open temporary storage.
func TestCloseRetainsVFSForCheckedOutConnection(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	if err != nil { t.Fatal(err) }
	conn, err := h.Conn(context.Background())
	if err != nil { t.Fatal(err) }
	if h.active != 0 { t.Fatalf("unexpected auxiliary files: %d", h.active) }
	if err := h.Close(); err == nil || !strings.Contains(err.Error(), "open files") { t.Fatalf("missing live connection error: %v", err) }
	for _, statement := range []string{"PRAGMA temp_store=FILE", "PRAGMA temp.cache_size=5", "CREATE TEMP TABLE spill(x)", "INSERT INTO spill VALUES(zeroblob(5000000))"} {
		if _, err := conn.ExecContext(context.Background(), statement); err != nil { t.Fatal(err) }
	}
	if err := conn.Close(); err != nil { t.Fatal(err) }
	if err := h.Close(); err != nil { t.Fatal(err) }
	if _, err := os.Lstat(h.dir); !os.IsNotExist(err) { t.Fatalf("scratch not removed: %v", err) }
}

// TestConcurrentCloseSerializesVFSRetirement prevents double unregister and free.
//
// Example: concurrent callers receive the same completed close result.
func TestConcurrentCloseSerializesVFSRetirement(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	if err != nil { t.Fatal(err) }
	if _, err := h.Exec("CREATE TABLE x(v)"); err != nil { t.Fatal(err) }
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		// closeHandle races other callers without sharing their result channel slot.
		//
		// Example: only one caller performs VFS retirement.
		go func() { results <- h.Close() }()
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil { t.Fatal(err) }
	}
}

// TestCloseRetainsVFSForRowsAndTransaction covers checked-out SQL resources.
//
// Example: rows and transactions outlive DB.Close until explicitly released.
func TestCloseRetainsVFSForRowsAndTransaction(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "rows.db"), root)
	if err != nil { t.Fatal(err) }
	rows, err := h.Query("SELECT 1 UNION ALL SELECT 2")
	if err != nil { t.Fatal(err) }
	if h.active != 0 { t.Fatalf("unexpected auxiliary files: %d", h.active) }
	if err := h.Close(); err == nil || !strings.Contains(err.Error(), "open connections") { t.Fatalf("missing rows lifetime error: %v", err) }
	if !rows.Next() { t.Fatalf("rows invalid after close: %v", rows.Err()) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	if err := h.Close(); err != nil { t.Fatal(err) }

	h, err = openAt("file:"+filepath.Join(root, "tx.db"), root)
	if err != nil { t.Fatal(err) }
	tx, err := h.Begin()
	if err != nil { t.Fatal(err) }
	if h.active != 0 { t.Fatalf("unexpected auxiliary files: %d", h.active) }
	if err := h.Close(); err == nil || !strings.Contains(err.Error(), "open connections") { t.Fatalf("missing transaction lifetime error: %v", err) }
	if _, err := tx.Exec("CREATE TEMP TABLE x(v)"); err != nil { t.Fatal(err) }
	if err := tx.Rollback(); err != nil { t.Fatal(err) }
	if err := h.Close(); err != nil { t.Fatal(err) }
}

// TestCloseRetainsVFSForMixedIdleConnections verifies deterministic retirement.
//
// Example: DB.Close retires idle connections but retains a checked-out one.
func TestCloseRetainsVFSForMixedIdleConnections(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "multi.db"), root)
	if err != nil { t.Fatal(err) }
	h.SetMaxOpenConns(2)
	first, err := h.Conn(context.Background())
	if err != nil { t.Fatal(err) }
	second, err := h.Conn(context.Background())
	if err != nil { t.Fatal(err) }
	if err := second.Close(); err != nil { t.Fatal(err) }
	if err := h.Close(); err == nil || !strings.Contains(err.Error(), "open connections") { t.Fatalf("missing checked-out connection error: %v", err) }
	if _, err := first.ExecContext(context.Background(), "SELECT 1"); err != nil { t.Fatal(err) }
	if err := first.Close(); err != nil { t.Fatal(err) }
	if err := h.Close(); err != nil { t.Fatal(err) }
}

// TestBadConnectionDiscardRetiresVFS exercises database/sql's real discard path.
//
// Example: ErrBadConn closes the real modernc connection before VFS retirement.
func TestBadConnectionDiscardRetiresVFS(t *testing.T) {
	root := t.TempDir()
	h, err := openAt("file:"+filepath.Join(root, "bad.db"), root)
	if err != nil { t.Fatal(err) }
	conn, err := h.Conn(context.Background())
	if err != nil { t.Fatal(err) }
	err = conn.Raw(
		// discardBadConnection requests database/sql's actual bad-connection path.
		//
		// Example: the driver connection is closed before the handle retires.
		func(any) error { return driver.ErrBadConn },
	)
	if !errors.Is(err, driver.ErrBadConn) { t.Fatalf("unexpected discard result: %v", err) }
	if err := h.Close(); err != nil { t.Fatal(err) }
	if _, err := os.Lstat(h.dir); !os.IsNotExist(err) { t.Fatalf("scratch not removed: %v", err) }
}
