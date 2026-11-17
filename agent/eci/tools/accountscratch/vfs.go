// Package accountscratch confines SQLite auxiliary files to the account's tmp.
package accountscratch

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Handle owns a database, its VFS registration, and its auxiliary-file directory.
//
// Example: Close reports retained scratch artifacts instead of hiding them.
type Handle struct {
	*sql.DB
	vfs uintptr
	parent uintptr
	name uintptr
	dir string
	dirInfo os.FileInfo
	// createFilename is set before registration and never changed while in use.
	//
	// Example: one handle's test allocator cannot affect another handle.
	createFilename func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) uintptr
	tail uintptr
	closeMu sync.Mutex
	mu sync.Mutex
	diagnostics []error
	active int
	opens int
	closed bool
	released bool
	dbErr error
	finalErr error
}

type fileState struct {
	original uintptr
	filename uintptr
	owner uintptr
	methods sqlite3.Tsqlite3_io_methods
}

var registrations = struct {
	sync.Mutex
	items map[uintptr]*Handle
	files map[uintptr]*Handle
}{items: make(map[uintptr]*Handle), files: make(map[uintptr]*Handle)}
var nextName atomic.Uint64

// Open creates a database handle using only the resolved OS account's tmp.
//
// Example: HOME and TMPDIR cannot redirect SQLite auxiliary files.
func Open(dsn string) (*Handle, error) {
	account, err := user.Current()
	if err != nil { return nil, fmt.Errorf("resolve account home: %w", err) }
	if account.HomeDir == "" || !filepath.IsAbs(account.HomeDir) { return nil, fmt.Errorf("account home is unavailable") }
	return openAt(dsn, filepath.Join(account.HomeDir, "tmp"))
}

// openAt binds one VFS instance to an existing private scratch root.
//
// Example: a missing root fails without a default temporary-directory fallback.
func openAt(dsn, root string) (*Handle, error) {
	return openAtWithAllocator(dsn, root, sqlite3.Xsqlite3_create_filename)
}

// openAtWithAllocator fixes the filename allocator before VFS registration.
//
// Example: tests can exercise a null allocation result without a global hook.
func openAtWithAllocator(dsn, root string, createFilename func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) uintptr) (_ *Handle, _err error) {
	if createFilename == nil { return nil, fmt.Errorf("SQLite filename allocator is unavailable") }
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "file" { return nil, fmt.Errorf("SQLite DSN must be a file URI") }
	q := u.Query()
	if q.Has("vfs") { return nil, fmt.Errorf("SQLite DSN already specifies a VFS") }
	dir, err := os.MkdirTemp(root, "eci-sqlite-")
	if err != nil { return nil, fmt.Errorf("create SQLite scratch directory: %w", err) }
	info, err := os.Lstat(dir)
	if err != nil { return nil, fmt.Errorf("inspect SQLite scratch; retained %s: %w", dir, err) }
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("SQLite scratch directory is not private; retained %s", dir)
	}
	h := &Handle{dir: dir, dirInfo: info, createFilename: createFilename}
	tls := libc.NewTLS()
	defer tls.Close()
	h.parent = sqlite3.Xsqlite3_vfs_find(tls, 0)
	if h.parent == 0 { return nil, errors.Join(fmt.Errorf("default SQLite VFS unavailable"), h.removeDirectory()) }
	parent := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
	h.tail = (uintptr(parent.FszOsFile) + 7) &^ 7
	h.vfs = sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
	if h.vfs == 0 { return nil, errors.Join(fmt.Errorf("allocate SQLite VFS"), h.removeDirectory()) }
	h.name, err = libc.CString(fmt.Sprintf("eci-account-scratch-%d", nextName.Add(1)))
	if err != nil { sqlite3.Xsqlite3_free(tls, h.vfs); return nil, errors.Join(err, h.removeDirectory()) }
	vfs := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.vfs))
	*vfs = *parent
	vfs.FszOsFile = int32(h.tail + unsafe.Sizeof(fileState{}))
	vfs.FpNext = 0
	vfs.FzName = h.name
	vfs.FxOpen = *(*uintptr)(unsafe.Pointer(&struct{ F func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32 }{openFile}))
	registrations.Lock(); registrations.items[h.vfs] = h; registrations.Unlock()
	if rc := sqlite3.Xsqlite3_vfs_register(tls, h.vfs, 0); rc != sqlite3.SQLITE_OK {
		registrations.Lock(); delete(registrations.items, h.vfs); registrations.Unlock()
		libc.Xfree(tls, h.name); sqlite3.Xsqlite3_free(tls, h.vfs)
		return nil, errors.Join(fmt.Errorf("register SQLite VFS: %d", rc), h.removeDirectory())
	}
	q.Set("vfs", libc.GoString(h.name)); u.RawQuery = q.Encode()
	h.DB, err = sql.Open("sqlite", u.String())
	if err != nil { return nil, errors.Join(err, h.Close()) }
	return h, nil
}

// Close closes the database before unregistering its VFS and removing scratch.
//
// Example: retained names or callback errors are returned to the caller.
func (h *Handle) Close() error {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.released { return h.finalErr }
	if !h.closed {
		h.closed = true
		if h.DB != nil { h.dbErr = h.DB.Close() }
	}
	openConnections := 0
	if h.DB != nil { openConnections = h.DB.Stats().OpenConnections }
	h.mu.Lock(); active := h.active; diagnostics := errors.Join(h.diagnostics...); h.mu.Unlock()
	if h.dbErr != nil {
		return errors.Join(h.dbErr, diagnostics, fmt.Errorf("SQLite close ownership uncertain; retained VFS and %s", h.dir))
	}
	if openConnections != 0 || active != 0 {
		return errors.Join(h.dbErr, diagnostics, fmt.Errorf("SQLite VFS still owns %d open connections and %d open files in %s", openConnections, active, h.dir))
	}
	tls := libc.NewTLS(); defer tls.Close()
	rc := sqlite3.Xsqlite3_vfs_unregister(tls, h.vfs)
	if rc != sqlite3.SQLITE_OK { return errors.Join(h.dbErr, diagnostics, fmt.Errorf("unregister SQLite VFS: %d; retained %s", rc, h.dir)) }
	registrations.Lock(); delete(registrations.items, h.vfs); registrations.Unlock()
	libc.Xfree(tls, h.name); sqlite3.Xsqlite3_free(tls, h.vfs)
	h.released = true
	h.finalErr = errors.Join(h.dbErr, diagnostics, h.removeDirectory())
	return h.finalErr
}

// removeDirectory removes only the empty directory created for this handle.
//
// Example: a replaced or nonempty directory is retained and reported.
func (h *Handle) removeDirectory() error {
	info, err := os.Lstat(h.dir)
	if err != nil { return fmt.Errorf("inspect SQLite scratch %s: %w", h.dir, err) }
	if !os.SameFile(info, h.dirInfo) { return fmt.Errorf("SQLite scratch directory replaced: %s", h.dir) }
	if err := os.Remove(h.dir); err != nil { return fmt.Errorf("remove SQLite scratch %s: %w", h.dir, err) }
	return nil
}

func (h *Handle) record(err error) { if err != nil { h.mu.Lock(); h.diagnostics = append(h.diagnostics, err); h.mu.Unlock() } }
func (h *Handle) scratchOpens() int { h.mu.Lock(); defer h.mu.Unlock(); return h.opens }
func findHandle(vfs uintptr) *Handle { registrations.Lock(); defer registrations.Unlock(); return registrations.items[vfs] }
func delegate(tls *libc.TLS, h *Handle, name, file uintptr, flags int32, out uintptr) int32 {
	method := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent)).FxOpen
	return (*(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&method)))(tls, h.parent, name, file, flags, out)
}

// openFile delegates durable files and redirects every auxiliary file class.
//
// Example: named subjournals cannot escape to a default temporary directory.
func openFile(tls *libc.TLS, vfs, name, file uintptr, flags int32, out uintptr) int32 {
	h := findHandle(vfs)
	if h == nil { *(*uintptr)(unsafe.Pointer(file)) = 0; return sqlite3.SQLITE_CANTOPEN }
	aux := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_TEMP_JOURNAL | sqlite3.SQLITE_OPEN_TRANSIENT_DB | sqlite3.SQLITE_OPEN_SUBJOURNAL)
	if flags&aux == 0 {
		if name == 0 { h.record(fmt.Errorf("SQLite requested unnamed non-auxiliary file (flags %#x)", flags)); *(*uintptr)(unsafe.Pointer(file)) = 0; return sqlite3.SQLITE_CANTOPEN }
		return delegate(tls, h, name, file, flags, out)
	}
	*(*uintptr)(unsafe.Pointer(file)) = 0
	if flags&sqlite3.SQLITE_OPEN_DELETEONCLOSE == 0 || flags&sqlite3.SQLITE_OPEN_READWRITE == 0 {
		h.record(fmt.Errorf("unsupported SQLite auxiliary flags %#x", flags)); return sqlite3.SQLITE_CANTOPEN
	}
	owned, err := os.CreateTemp(h.dir, "aux-")
	if err != nil { h.record(fmt.Errorf("create SQLite auxiliary file in %s: %w", h.dir, err)); return sqlite3.SQLITE_CANTOPEN }
	path := owned.Name()
	identity, statErr := owned.Stat()
	if statErr != nil { h.record(errors.Join(fmt.Errorf("inspect SQLite auxiliary %s: %w", path, statErr), owned.Close())); return sqlite3.SQLITE_CANTOPEN }
	cpath, err := libc.CString(path)
	if err != nil { h.record(errors.Join(err, closeOwned(owned, identity))); return sqlite3.SQLITE_CANTOPEN }
	empty, err := libc.CString("")
	if err != nil { libc.Xfree(tls, cpath); h.record(errors.Join(err, closeOwned(owned, identity))); return sqlite3.SQLITE_CANTOPEN }
	filename := h.createFilename(tls, cpath, empty, empty, 0, 0)
	libc.Xfree(tls, cpath); libc.Xfree(tls, empty)
	if filename == 0 { h.record(errors.Join(fmt.Errorf("allocate SQLite auxiliary filename for %s", path), closeOwned(owned, identity))); return sqlite3.SQLITE_CANTOPEN }
	if out == 0 {
		out = sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(int32(0))))
		if out == 0 {
			sqlite3.Xsqlite3_free_filename(tls, filename)
			h.record(errors.Join(fmt.Errorf("allocate SQLite auxiliary output flags for %s", path), closeOwned(owned, identity)))
			return sqlite3.SQLITE_CANTOPEN
		}
		defer sqlite3.Xsqlite3_free(tls, out)
	}
	rc := delegate(tls, h, filename, file, flags &^ sqlite3.SQLITE_OPEN_EXCLUSIVE, out)
	methods := *(**sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(file))
	if rc != sqlite3.SQLITE_OK {
		failure := fmt.Errorf("open SQLite auxiliary %s: code %d", path, rc)
		if methods != nil {
			method := methods.FxClose
			closeRC := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file)
			if closeRC != sqlite3.SQLITE_OK { failure = errors.Join(failure, fmt.Errorf("close failed SQLite auxiliary %s: code %d", path, closeRC)) }
		}
		*(*uintptr)(unsafe.Pointer(file)) = 0
		sqlite3.Xsqlite3_free_filename(tls, filename)
		h.record(errors.Join(failure, closeOwned(owned, identity)))
		return sqlite3.SQLITE_CANTOPEN
	}
	var failure error
	if *(*int32)(unsafe.Pointer(out))&sqlite3.SQLITE_OPEN_READWRITE == 0 || methods == nil { failure = fmt.Errorf("SQLite auxiliary %s did not open writable", path) }
	if closeErr := owned.Close(); closeErr != nil { failure = errors.Join(failure, fmt.Errorf("close SQLite auxiliary creator %s: %w", path, closeErr)) }
	if _, statErr = os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) { failure = errors.Join(failure, fmt.Errorf("SQLite auxiliary remains or cannot be inspected %s: %v", path, statErr)) }
	if failure != nil {
		if methods != nil { method := methods.FxClose; closeRC := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file); if closeRC != sqlite3.SQLITE_OK { failure = errors.Join(failure, fmt.Errorf("close failed SQLite auxiliary %s: code %d", path, closeRC)) } }
		*(*uintptr)(unsafe.Pointer(file)) = 0
		sqlite3.Xsqlite3_free_filename(tls, filename)
		h.record(failure)
		return sqlite3.SQLITE_CANTOPEN
	}
	state := (*fileState)(unsafe.Pointer(file + h.tail))
	state.original, state.filename, state.owner, state.methods = methods.FxClose, filename, vfs, *methods
	state.methods.FxClose = *(*uintptr)(unsafe.Pointer(&struct{ F func(*libc.TLS, uintptr) int32 }{closeFile}))
	*(*uintptr)(unsafe.Pointer(file)) = uintptr(unsafe.Pointer(&state.methods))
	registrations.Lock(); registrations.files[file] = h; registrations.Unlock()
	h.mu.Lock(); h.active++; h.opens++; h.mu.Unlock()
	return sqlite3.SQLITE_OK
}

// closeOwned removes an exact still-owned predelegation file, never a replacement.
//
// Example: an allocation failure does not leave private content behind.
func closeOwned(f *os.File, original os.FileInfo) error {
	path := f.Name(); closeErr := f.Close(); info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) { return closeErr }
	if err != nil { return errors.Join(closeErr, fmt.Errorf("inspect SQLite auxiliary %s: %w", path, err)) }
	if !os.SameFile(info, original) { return errors.Join(closeErr, fmt.Errorf("SQLite auxiliary replaced: %s", path)) }
	if err := os.Remove(path); err != nil { return errors.Join(closeErr, fmt.Errorf("remove SQLite auxiliary %s: %w", path, err)) }
	return closeErr
}

// closeFile frees the invented name only after the parent has closed the file.
//
// Example: a failed delegated close is visible when the handle closes.
func closeFile(tls *libc.TLS, file uintptr) int32 {
	registrations.Lock()
	h := registrations.files[file]
	delete(registrations.files, file)
	registrations.Unlock()
	if h == nil { return sqlite3.SQLITE_IOERR_CLOSE }
	state := (*fileState)(unsafe.Pointer(file + h.tail))
	method, filename := state.original, state.filename
	rc := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file)
	sqlite3.Xsqlite3_free_filename(tls, filename)
	h.mu.Lock(); h.active--; if rc != sqlite3.SQLITE_OK { h.diagnostics = append(h.diagnostics, fmt.Errorf("close SQLite auxiliary: code %d", rc)) }; h.mu.Unlock()
	return rc
}
