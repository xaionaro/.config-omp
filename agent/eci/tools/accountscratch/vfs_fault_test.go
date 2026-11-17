package accountscratch

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// faultParent records a delegated open and injects one parent VFS failure.
//
// Example: an open may fail after installing a usable close method.
type faultParent struct {
	parent uintptr
	mode string
	methods sqlite3.Tsqlite3_io_methods
	originalClose uintptr
	path string
	opens int
	closes int
}

// currentFaultParent is used only by serial tests while their parent clone lives.
var currentFaultParent *faultParent

// faultOpen forwards to the real parent except for a null-method open failure.
//
// Example: a successful parent open can be reported as failed to the shim.
func faultOpen(tls *libc.TLS, _ uintptr, name, file uintptr, flags int32, out uintptr) int32 {
	f := currentFaultParent
	f.opens++
	f.path = libc.GoString(name)
	if f.mode == "null" {
		*(*uintptr)(unsafe.Pointer(file)) = 0
		return sqlite3.SQLITE_CANTOPEN
	}
	if f.mode == "retained" {
		flags &^= sqlite3.SQLITE_OPEN_DELETEONCLOSE
	}
	method := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(f.parent)).FxOpen
	rc := (*(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&method)))(tls, f.parent, name, file, flags, out)
	if rc != sqlite3.SQLITE_OK {
		return rc
	}
	methods := *(**sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(file))
	f.methods = *methods
	f.originalClose = methods.FxClose
	f.methods.FxClose = *(*uintptr)(unsafe.Pointer(&struct{ F func(*libc.TLS, uintptr) int32 }{faultClose}))
	*(*uintptr)(unsafe.Pointer(file)) = uintptr(unsafe.Pointer(&f.methods))
	if f.mode == "nonnull" {
		return sqlite3.SQLITE_CANTOPEN
	}
	return rc
}

// faultClose closes the real parent file and can expose a close error.
//
// Example: the shim retains an error returned by the delegated close.
func faultClose(tls *libc.TLS, file uintptr) int32 {
	f := currentFaultParent
	f.closes++
	method := f.originalClose
	rc := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file)
	if rc == sqlite3.SQLITE_OK && f.mode == "close" {
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	return rc
}

// TestAuxiliaryParentFailures exercises shim cleanup and diagnostic ownership.
//
// Example: a failed open with methods is closed before SQLite sees null methods.
func TestAuxiliaryParentFailures(t *testing.T) {
	account, err := user.Current()
	if err != nil { t.Fatal(err) }
	root, err := os.MkdirTemp(filepath.Join(account.HomeDir, "tmp"), "vfs-fault-test-")
	if err != nil { t.Fatal(err) }
	t.Cleanup(
		// cleanupFaultRoot removes only this test's isolated temporary root.
		//
		// Example: a retained diagnostic file is cleaned after its assertion.
		func() { if err := os.RemoveAll(root); err != nil { t.Error(err) } },
	)

	for _, mode := range []string{"null", "nonnull", "retained", "close"} {
		t.Run(mode,
			// exerciseParentFailure checks one delegated-open outcome.
			//
			// Example: non-null failed opens are synchronously closed.
			func(t *testing.T) {
			h, err := openAt("file:"+filepath.Join(root, mode+".db"), root)
			if err != nil { t.Fatal(err) }
			tls := libc.NewTLS()
			defer tls.Close()
			clone := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
			if clone == 0 { t.Fatal("allocate parent VFS") }
			defer sqlite3.Xsqlite3_free(tls, clone)
			*(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)) = *(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
			f := &faultParent{parent: h.parent, mode: mode}
			currentFaultParent = f
			defer
			// clearFaultParent removes the serial callback's test state.
			//
			// Example: a later subtest starts without the prior parent.
			func() { currentFaultParent = nil }()
			(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)).FxOpen = *(*uintptr)(unsafe.Pointer(&struct{ F func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32 }{faultOpen}))
			h.parent = clone
			file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
			if file == 0 { t.Fatal("allocate SQLite file") }
			defer sqlite3.Xsqlite3_free(tls, file)
			output := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(int32(0))))
			if output == 0 { t.Fatal("allocate SQLite output flags") }
			defer sqlite3.Xsqlite3_free(tls, output)
			flags := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE)
			rc := openFile(tls, h.vfs, 0, file, flags, output)
			if f.opens != 1 { t.Fatalf("parent opens: %d", f.opens) }
			if mode == "close" {
				if rc != sqlite3.SQLITE_OK { t.Fatalf("open: %d", rc) }
				if rc = closeFile(tls, file); rc != sqlite3.SQLITE_IOERR_CLOSE { t.Fatalf("close: %d", rc) }
			} else {
				if rc != sqlite3.SQLITE_CANTOPEN { t.Fatalf("open: %d", rc) }
				if *(*uintptr)(unsafe.Pointer(file)) != 0 { t.Fatal("failed open retained methods") }
			}
			wantCloses := 1
			if mode == "null" { wantCloses = 0 }
			if f.closes != wantCloses { t.Fatalf("parent closes: got %d, want %d", f.closes, wantCloses) }
			if mode == "retained" {
				if _, err := os.Lstat(f.path); err != nil { t.Fatalf("retained file missing: %v", err) }
			} else if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
				t.Fatalf("unexpected auxiliary file: %v", err)
			}
			h.parent = f.parent
			err = h.Close()
			if err == nil { t.Fatal("missing callback diagnostic") }
			if mode == "retained" && !strings.Contains(err.Error(), f.path) { t.Fatalf("missing retained path: %v", err) }
			if mode == "close" && !strings.Contains(err.Error(), fmt.Sprint(sqlite3.SQLITE_IOERR_CLOSE)) { t.Fatalf("missing close error: %v", err) }
			},
		)
	}
}

// unlinkFailurePath records the pathname passed to the failing parent syscall.
var unlinkFailurePath string

// failUnlink simulates the Unix parent's unlink syscall failing.
//
// Example: a DELETEONCLOSE open leaves its name behind.
func failUnlink(_ *libc.TLS, path uintptr) int32 {
	unlinkFailurePath = libc.GoString(path)
	return -1
}

// TestAuxiliaryUnlinkFailure isolates SQLite's process-global syscall override.
//
// Example: an ignored parent unlink error becomes a retained-path diagnostic.
func TestAuxiliaryUnlinkFailure(t *testing.T) {
	const childEnv = "ECI_TEST_AUX_UNLINK_FAILURE"
	if os.Getenv(childEnv) != "1" {
		executable, err := os.Executable()
		if err != nil { t.Fatal(err) }
		cmd := exec.Command(executable, "-test.run=^TestAuxiliaryUnlinkFailure$")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("unlink fault subprocess: %v\n%s", err, output)
		}
		return
	}

	account, err := user.Current()
	if err != nil { t.Fatal(err) }
	root, err := os.MkdirTemp(filepath.Join(account.HomeDir, "tmp"), "vfs-unlink-test-")
	if err != nil { t.Fatal(err) }
	defer
	// cleanupUnlinkRoot removes the subprocess-owned scratch after assertions.
	//
	// Example: a deliberately retained auxiliary name is test-local.
	func() { if err := os.RemoveAll(root); err != nil { t.Error(err) } }()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	if err != nil { t.Fatal(err) }
	tls := libc.NewTLS()
	defer tls.Close()
	parent := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
	if parent.FxGetSystemCall == 0 || parent.FxSetSystemCall == 0 { t.Fatal("parent VFS cannot override unlink") }
	name, err := libc.CString("unlink")
	if err != nil { t.Fatal(err) }
	defer libc.Xfree(tls, name)
	get := parent.FxGetSystemCall
	set := parent.FxSetSystemCall
	original := (*(*func(*libc.TLS, uintptr, uintptr) uintptr)(unsafe.Pointer(&get)))(tls, h.parent, name)
	if original == 0 { t.Fatal("parent VFS has no unlink syscall") }
	setCall := *(*func(*libc.TLS, uintptr, uintptr, uintptr) int32)(unsafe.Pointer(&set))
	failing := *(*uintptr)(unsafe.Pointer(&struct{ F func(*libc.TLS, uintptr) int32 }{failUnlink}))
	if rc := setCall(tls, h.parent, name, failing); rc != sqlite3.SQLITE_OK { t.Fatalf("override unlink: %d", rc) }
	defer
	// restoreUnlink reinstates the parent syscall before subprocess exit.
	//
	// Example: no global hook survives the fault exercise.
	func() {
		if rc := setCall(tls, h.parent, name, original); rc != sqlite3.SQLITE_OK { t.Errorf("restore unlink: %d", rc) }
	}()

	file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
	if file == 0 { t.Fatal("allocate SQLite file") }
	defer sqlite3.Xsqlite3_free(tls, file)
	out := sqlite3.Xsqlite3_malloc64(tls, 4)
	if out == 0 { t.Fatal("allocate SQLite output flags") }
	defer sqlite3.Xsqlite3_free(tls, out)
	flags := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE)
	rc := openFile(tls, h.vfs, 0, file, flags, out)
	if rc != sqlite3.SQLITE_CANTOPEN { t.Fatalf("open with failed unlink: %d", rc) }
	if *(*uintptr)(unsafe.Pointer(file)) != 0 { t.Fatal("failed open retained methods") }
	if unlinkFailurePath == "" || filepath.Dir(unlinkFailurePath) != h.dir { t.Fatalf("unlink not called on owned scratch: %q", unlinkFailurePath) }
	if _, err := os.Lstat(unlinkFailurePath); err != nil { t.Fatalf("unlinked file was not retained: %v", err) }
	if err := h.Close(); err == nil || !strings.Contains(err.Error(), unlinkFailurePath) { t.Fatalf("missing retained-path diagnostic: %v", err) }
}

// TestAuxiliaryFilenameNullResult verifies per-handle allocation failure cleanup.
//
// Example: a null filename allocation never delegates or retains the owned inode.
func TestAuxiliaryFilenameNullResult(t *testing.T) {
	account, err := user.Current()
	if err != nil { t.Fatal(err) }
	root, err := os.MkdirTemp(filepath.Join(account.HomeDir, "tmp"), "vfs-allocation-test-")
	if err != nil { t.Fatal(err) }
	t.Cleanup(
		// cleanupAllocationRoot removes this test's isolated temporary root.
		//
		// Example: unrelated account files are never traversed.
		func() { if err := os.RemoveAll(root); err != nil { t.Error(err) } },
	)
	calls := 0
	// failFilename injects only the selected handle's filename allocation result.
	//
	// Example: zero models SQLite returning a null allocation pointer.
	failFilename := func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) uintptr { calls++; return 0 }
	h, err := openAtWithAllocator("file:"+filepath.Join(root, "main.db"), root, failFilename)
	if err != nil { t.Fatal(err) }
	tls := libc.NewTLS()
	defer tls.Close()
	clone := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
	if clone == 0 { t.Fatal("allocate parent VFS") }
	defer sqlite3.Xsqlite3_free(tls, clone)
	*(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)) = *(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
	fault := &faultParent{parent: h.parent, mode: "null"}
	currentFaultParent = fault
	defer
	// clearAllocationFaultParent releases the serial test callback state.
	//
	// Example: later fault cases do not see this cloned parent.
	func() { currentFaultParent = nil }()
	(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)).FxOpen = *(*uintptr)(unsafe.Pointer(&struct{ F func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32 }{faultOpen}))
	h.parent = clone
	file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
	if file == 0 { t.Fatal("allocate SQLite file") }
	defer sqlite3.Xsqlite3_free(tls, file)
	flags := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE)
	if rc := openFile(tls, h.vfs, 0, file, flags, 0); rc != sqlite3.SQLITE_CANTOPEN { t.Fatalf("unexpected return: %d", rc) }
	if *(*uintptr)(unsafe.Pointer(file)) != 0 { t.Fatal("failed allocation retained methods") }
	if calls != 1 { t.Fatalf("allocation calls: %d", calls) }
	if fault.opens != 0 || fault.closes != 0 { t.Fatalf("delegated after allocation failure: opens=%d closes=%d", fault.opens, fault.closes) }
	if h.active != 0 || h.opens != 0 { t.Fatalf("changed open counters: active=%d opens=%d", h.active, h.opens) }
	entries, err := os.ReadDir(h.dir)
	if err != nil || len(entries) != 0 { t.Fatalf("owned scratch not cleaned: %v %v", entries, err) }
	h.parent = fault.parent
	if err := h.Close(); err == nil || !strings.Contains(err.Error(), "allocate SQLite auxiliary filename") { t.Fatalf("missing allocation diagnostic: %v", err) }
	if !h.released { t.Fatal("failed allocation retained VFS registration") }
	if _, err := os.Lstat(h.dir); !os.IsNotExist(err) { t.Fatalf("scratch directory not removed: %v", err) }
}
