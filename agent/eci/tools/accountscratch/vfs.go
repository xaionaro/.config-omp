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
	vfs     uintptr
	parent  uintptr
	name    uintptr
	dir     string
	dirInfo os.FileInfo
	// createFilename is set before registration and never changed while in use.
	//
	// Example: one handle's test allocator cannot affect another handle.
	createFilename filenameAllocator
	tail           uintptr
	closeMu        sync.Mutex
	mu             sync.Mutex
	diagnostics    []error
	active         int
	opens          int
	closed         bool
	released       bool
	dbErr          error
	finalErr       error
}

// filenameAllocator creates a SQLite-owned auxiliary filename for one handle.
//
// Example: independent handles may exercise distinct allocation outcomes.
type filenameAllocator interface {
	// Create returns a SQLite filename pointer or zero on allocation failure.
	//
	// Example: the caller releases a nonzero pointer using SQLite's allocator.
	Create(
		*libc.TLS,
		uintptr,
		uintptr,
		uintptr,
		int32,
		uintptr,
	) uintptr
}

// sqliteFilenameAllocator delegates filename ownership to the pinned SQLite.
//
// Example: production auxiliary filenames are freed by SQLite.
type sqliteFilenameAllocator struct{}

// Create allocates an actual SQLite filename.
//
// Example: SQLite preserves URI metadata associated with the filename.
func (sqliteFilenameAllocator) Create(
	tls *libc.TLS,
	database uintptr,
	journal uintptr,
	wal uintptr,
	parameters int32,
	values uintptr,
) uintptr {
	return sqlite3.Xsqlite3_create_filename(tls, database, journal, wal, parameters, values)
}

// fileState owns the delegated close method and allocated SQLite filename.
//
// Example: its filename remains valid until the parent VFS closes the file.
type fileState struct {
	original uintptr
	filename uintptr
	owner    uintptr
	methods  sqlite3.Tsqlite3_io_methods
}

// registrations associates VFS and file handles with their owning database.
//
// Example: concurrent SQLite callbacks resolve their own handle.
var registrations = struct {
	sync.Mutex
	items map[uintptr]*Handle
	files map[uintptr]*Handle
}{items: make(map[uintptr]*Handle), files: make(map[uintptr]*Handle)}

// nextName provides process-unique VFS names.
//
// Example: two simultaneously opened databases use different registrations.
var nextName atomic.Uint64

// Open creates a database handle using only the resolved OS account's tmp.
//
// Example: HOME and TMPDIR cannot redirect SQLite auxiliary files.
func Open(dsn string) (*Handle, error) {
	account, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("resolve account home: %w", err)
	}
	if account.HomeDir == "" || !filepath.IsAbs(account.HomeDir) {
		return nil, fmt.Errorf("account home is unavailable")
	}
	return openAt(dsn, filepath.Join(account.HomeDir, "tmp"))
}

// openAt binds one VFS instance to an existing private scratch root.
//
// Example: a missing root fails without a default temporary-directory fallback.
func openAt(
	dsn string,
	root string,
) (*Handle, error) {
	return openAtWithAllocator(dsn, root, sqliteFilenameAllocator{})
}

// openAtWithAllocator fixes the filename allocator before VFS registration.
//
// Example: tests can exercise a null allocation result without a global hook.
func openAtWithAllocator(
	dsn string,
	root string,
	createFilename filenameAllocator,
) (_ *Handle, _err error) {
	if createFilename == nil {
		return nil, fmt.Errorf("SQLite filename allocator is unavailable")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "file" {
		return nil, fmt.Errorf("SQLite DSN must be a file URI")
	}
	q := u.Query()
	if q.Has("vfs") {
		return nil, fmt.Errorf("SQLite DSN already specifies a VFS")
	}
	dir, err := os.MkdirTemp(root, "eci-sqlite-")
	if err != nil {
		return nil, fmt.Errorf("create SQLite scratch directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect SQLite scratch; retained %s: %w", dir, err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("SQLite scratch directory is not private; retained %s", dir)
	}
	h := &Handle{dir: dir, dirInfo: info, createFilename: createFilename}
	tls := libc.NewTLS()
	defer tls.Close()
	h.parent = sqlite3.Xsqlite3_vfs_find(tls, 0)
	if h.parent == 0 {
		return nil, errors.Join(fmt.Errorf("default SQLite VFS unavailable"), h.removeDirectory())
	}
	parent := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
	h.tail = (uintptr(parent.FszOsFile) + 7) &^ 7
	h.vfs = sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
	if h.vfs == 0 {
		return nil, errors.Join(fmt.Errorf("allocate SQLite VFS"), h.removeDirectory())
	}
	h.name, err = libc.CString(fmt.Sprintf("eci-account-scratch-%d", nextName.Add(1)))
	if err != nil {
		sqlite3.Xsqlite3_free(tls, h.vfs)
		return nil, errors.Join(err, h.removeDirectory())
	}
	vfs := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.vfs))
	*vfs = *parent
	vfs.FszOsFile = int32(h.tail + unsafe.Sizeof(fileState{}))
	vfs.FpNext = 0
	vfs.FzName = h.name
	vfs.FxOpen = *(*uintptr)(unsafe.Pointer(&struct {
		F func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32
	}{openFile}))
	registerVFS(h)
	if rc := sqlite3.Xsqlite3_vfs_register(tls, h.vfs, 0); rc != sqlite3.SQLITE_OK {
		unregisterVFS(h.vfs)
		libc.Xfree(tls, h.name)
		sqlite3.Xsqlite3_free(tls, h.vfs)
		return nil, errors.Join(fmt.Errorf("register SQLite VFS: %d", rc), h.removeDirectory())
	}
	q.Set("vfs", libc.GoString(h.name))
	u.RawQuery = q.Encode()
	h.DB, err = sql.Open("sqlite", u.String())
	if err != nil {
		return nil, errors.Join(err, h.Close())
	}
	return h, nil
}

// Close closes the database before unregistering its VFS and removing scratch.
// Retirement is valid for the pinned modernc driver and database/sql-managed
// connections, rows and transactions, not untracked native resources.
//
// Example: retained names or callback errors are returned to the caller.
func (h *Handle) Close() error {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.released {
		return h.finalErr
	}
	if !h.closed {
		h.closed = true
		if h.DB != nil {
			h.dbErr = h.DB.Close()
		}
	}
	openConnections := 0
	if h.DB != nil {
		openConnections = h.DB.Stats().OpenConnections
	}
	active, diagnostics := h.snapshotDiagnostics()
	if h.dbErr != nil {
		return errors.Join(h.dbErr, diagnostics, fmt.Errorf("SQLite close ownership uncertain; retained VFS and %s", h.dir))
	}
	if openConnections != 0 || active != 0 {
		return errors.Join(h.dbErr, diagnostics, fmt.Errorf("SQLite VFS still owns %d open connections and %d open files in %s", openConnections, active, h.dir))
	}
	tls := libc.NewTLS()
	defer tls.Close()
	rc := sqlite3.Xsqlite3_vfs_unregister(tls, h.vfs)
	if rc != sqlite3.SQLITE_OK {
		return errors.Join(h.dbErr, diagnostics, fmt.Errorf("unregister SQLite VFS: %d; retained %s", rc, h.dir))
	}
	unregisterVFS(h.vfs)
	libc.Xfree(tls, h.name)
	sqlite3.Xsqlite3_free(tls, h.vfs)
	h.released = true
	h.finalErr = errors.Join(h.dbErr, diagnostics, h.removeDirectory())
	return h.finalErr
}

// removeDirectory removes only the empty directory created for this handle.
//
// Example: a replaced or nonempty directory is retained and reported.
func (h *Handle) removeDirectory() error {
	info, err := os.Lstat(h.dir)
	if err != nil {
		return fmt.Errorf("inspect SQLite scratch %s: %w", h.dir, err)
	}
	if !os.SameFile(info, h.dirInfo) {
		return fmt.Errorf("SQLite scratch directory replaced: %s", h.dir)
	}
	if err := os.Remove(h.dir); err != nil {
		return fmt.Errorf("remove SQLite scratch %s: %w", h.dir, err)
	}
	return nil
}

// record retains callback failures until the owning handle closes.
//
// Example: an auxiliary unlink failure remains visible to the caller.
func (h *Handle) record(err error) {
	if err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.diagnostics = append(h.diagnostics, err)
}

// scratchOpens reports successful auxiliary opens for focused tests.
//
// Example: a disk-backed temporary table increments the count.
func (h *Handle) scratchOpens() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opens
}

// findHandle resolves a VFS registration to its owning handle.
//
// Example: SQLite callbacks cannot use another database's scratch directory.
func findHandle(vfs uintptr) *Handle {
	registrations.Lock()
	defer registrations.Unlock()
	return registrations.items[vfs]
}

// registerVFS publishes one handle to SQLite callbacks.
//
// Example: the callback resolves the handle before its first open.
func registerVFS(h *Handle) {
	registrations.Lock()
	defer registrations.Unlock()
	registrations.items[h.vfs] = h
}

// unregisterVFS removes a retired handle from callback lookup.
//
// Example: a closed VFS no longer resolves to its prior owner.
func unregisterVFS(vfs uintptr) {
	registrations.Lock()
	defer registrations.Unlock()
	delete(registrations.items, vfs)
}

// registerFile records the owner of a successfully opened auxiliary file.
//
// Example: its close callback can find the right VFS handle.
func registerFile(
	file uintptr,
	h *Handle,
) {
	registrations.Lock()
	defer registrations.Unlock()
	registrations.files[file] = h
}

// takeFile removes and returns an auxiliary file's registered owner.
//
// Example: repeated closes cannot free the same filename twice.
func takeFile(file uintptr) *Handle {
	registrations.Lock()
	defer registrations.Unlock()
	h := registrations.files[file]
	delete(registrations.files, file)
	return h
}

// snapshotDiagnostics captures current callback ownership and failures.
//
// Example: close refuses retirement while an auxiliary file remains open.
func (h *Handle) snapshotDiagnostics() (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active, errors.Join(h.diagnostics...)
}

// openedFile records one successful auxiliary open.
//
// Example: the handle cannot retire until its close callback completes.
func (h *Handle) openedFile() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active++
	h.opens++
}

// closedFile records a delegated auxiliary close and any exposed error.
//
// Example: the handle reports a parent VFS close failure.
func (h *Handle) closedFile(rc int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active--
	if rc != sqlite3.SQLITE_OK {
		h.diagnostics = append(h.diagnostics, fmt.Errorf("close SQLite auxiliary: code %d", rc))
	}
}

// delegate opens a file through the original SQLite VFS.
//
// Example: durable database files retain the parent's normal semantics.
func delegate(
	tls *libc.TLS,
	h *Handle,
	name uintptr,
	file uintptr,
	flags int32,
	out uintptr,
) int32 {
	method := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent)).FxOpen
	return (*(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&method)))(tls, h.parent, name, file, flags, out)
}

// openFile delegates durable files and redirects every auxiliary file class.
//
// Example: named subjournals cannot escape to a default temporary directory.
func openFile(
	tls *libc.TLS,
	vfs uintptr,
	name uintptr,
	file uintptr,
	flags int32,
	out uintptr,
) int32 {
	h := findHandle(vfs)
	if h == nil {
		*(*uintptr)(unsafe.Pointer(file)) = 0
		return sqlite3.SQLITE_CANTOPEN
	}
	aux := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_TEMP_JOURNAL | sqlite3.SQLITE_OPEN_TRANSIENT_DB | sqlite3.SQLITE_OPEN_SUBJOURNAL)
	if flags&aux == 0 {
		if name == 0 {
			h.record(fmt.Errorf("SQLite requested unnamed non-auxiliary file (flags %#x)", flags))
			*(*uintptr)(unsafe.Pointer(file)) = 0
			return sqlite3.SQLITE_CANTOPEN
		}
		return delegate(tls, h, name, file, flags, out)
	}
	*(*uintptr)(unsafe.Pointer(file)) = 0
	if flags&sqlite3.SQLITE_OPEN_DELETEONCLOSE == 0 || flags&sqlite3.SQLITE_OPEN_READWRITE == 0 {
		h.record(fmt.Errorf("unsupported SQLite auxiliary flags %#x", flags))
		return sqlite3.SQLITE_CANTOPEN
	}
	owned, err := os.CreateTemp(h.dir, "aux-")
	if err != nil {
		h.record(fmt.Errorf("create SQLite auxiliary file in %s: %w", h.dir, err))
		return sqlite3.SQLITE_CANTOPEN
	}
	path := owned.Name()
	identity, statErr := owned.Stat()
	if statErr != nil {
		h.record(errors.Join(fmt.Errorf("inspect SQLite auxiliary %s: %w", path, statErr), owned.Close()))
		return sqlite3.SQLITE_CANTOPEN
	}
	cpath, err := libc.CString(path)
	if err != nil {
		h.record(errors.Join(err, closeOwned(owned, identity)))
		return sqlite3.SQLITE_CANTOPEN
	}
	empty, err := libc.CString("")
	if err != nil {
		libc.Xfree(tls, cpath)
		h.record(errors.Join(err, closeOwned(owned, identity)))
		return sqlite3.SQLITE_CANTOPEN
	}
	filename := h.createFilename.Create(tls, cpath, empty, empty, 0, 0)
	libc.Xfree(tls, cpath)
	libc.Xfree(tls, empty)
	if filename == 0 {
		h.record(errors.Join(fmt.Errorf("allocate SQLite auxiliary filename for %s", path), closeOwned(owned, identity)))
		return sqlite3.SQLITE_CANTOPEN
	}
	if out == 0 {
		out = sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(int32(0))))
		if out == 0 {
			sqlite3.Xsqlite3_free_filename(tls, filename)
			h.record(errors.Join(fmt.Errorf("allocate SQLite auxiliary output flags for %s", path), closeOwned(owned, identity)))
			return sqlite3.SQLITE_CANTOPEN
		}
		defer sqlite3.Xsqlite3_free(tls, out)
	}
	rc := delegate(tls, h, filename, file, flags&^sqlite3.SQLITE_OPEN_EXCLUSIVE, out)
	methods := *(**sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(file))
	if rc != sqlite3.SQLITE_OK {
		failure := fmt.Errorf("open SQLite auxiliary %s: code %d", path, rc)
		if methods != nil {
			method := methods.FxClose
			closeRC := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file)
			if closeRC != sqlite3.SQLITE_OK {
				failure = errors.Join(failure, fmt.Errorf("close failed SQLite auxiliary %s: code %d", path, closeRC))
			}
		}
		*(*uintptr)(unsafe.Pointer(file)) = 0
		sqlite3.Xsqlite3_free_filename(tls, filename)
		h.record(errors.Join(failure, closeOwned(owned, identity)))
		return sqlite3.SQLITE_CANTOPEN
	}
	var failure error
	if *(*int32)(unsafe.Pointer(out))&sqlite3.SQLITE_OPEN_READWRITE == 0 || methods == nil {
		failure = fmt.Errorf("SQLite auxiliary %s did not open writable", path)
	}
	if closeErr := owned.Close(); closeErr != nil {
		failure = errors.Join(failure, fmt.Errorf("close SQLite auxiliary creator %s: %w", path, closeErr))
	}
	if _, statErr = os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		failure = errors.Join(failure, fmt.Errorf("SQLite auxiliary remains or cannot be inspected %s: %v", path, statErr))
	}
	if failure != nil {
		if methods != nil {
			method := methods.FxClose
			closeRC := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file)
			if closeRC != sqlite3.SQLITE_OK {
				failure = errors.Join(failure, fmt.Errorf("close failed SQLite auxiliary %s: code %d", path, closeRC))
			}
		}
		*(*uintptr)(unsafe.Pointer(file)) = 0
		sqlite3.Xsqlite3_free_filename(tls, filename)
		h.record(failure)
		return sqlite3.SQLITE_CANTOPEN
	}
	state := (*fileState)(unsafe.Pointer(file + h.tail))
	state.original, state.filename, state.owner, state.methods = methods.FxClose, filename, vfs, *methods
	state.methods.FxClose = *(*uintptr)(unsafe.Pointer(&struct {
		F func(*libc.TLS, uintptr) int32
	}{closeFile}))
	*(*uintptr)(unsafe.Pointer(file)) = uintptr(unsafe.Pointer(&state.methods))
	registerFile(file, h)
	h.openedFile()
	return sqlite3.SQLITE_OK
}

// closeOwned removes an exact still-owned predelegation file, never a replacement.
//
// Example: an allocation failure does not leave private content behind.
func closeOwned(
	f *os.File,
	original os.FileInfo,
) error {
	path := f.Name()
	closeErr := f.Close()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return closeErr
	}
	if err != nil {
		return errors.Join(closeErr, fmt.Errorf("inspect SQLite auxiliary %s: %w", path, err))
	}
	if !os.SameFile(info, original) {
		return errors.Join(closeErr, fmt.Errorf("SQLite auxiliary replaced: %s", path))
	}
	if err := os.Remove(path); err != nil {
		return errors.Join(closeErr, fmt.Errorf("remove SQLite auxiliary %s: %w", path, err))
	}
	return closeErr
}

// closeFile frees the invented name only after the parent has closed the file.
//
// Example: a failed delegated close is visible when the handle closes.
func closeFile(
	tls *libc.TLS,
	file uintptr,
) int32 {
	h := takeFile(file)
	if h == nil {
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	state := (*fileState)(unsafe.Pointer(file + h.tail))
	method, filename := state.original, state.filename
	rc := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file)
	sqlite3.Xsqlite3_free_filename(tls, filename)
	h.closedFile(rc)
	return rc
}
