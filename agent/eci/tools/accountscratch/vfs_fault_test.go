package accountscratch

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	// unlinkFailureChildEnv selects the process-global syscall fault subprocess.
	//
	// Example: the child performs the syscall override only once.
	unlinkFailureChildEnv = "ECI_TEST_AUX_UNLINK_FAILURE"
)

// faultMode identifies one simulated parent VFS failure.
//
// Example: a null-method failure never delegates a close.
type faultMode int

const (
	// faultNull makes the parent open fail without close methods.
	//
	// Example: the shim must not call a missing close method.
	faultNull faultMode = iota

	// faultNonNull makes an open fail after installing close methods.
	//
	// Example: the shim closes the partially opened file.
	faultNonNull

	// faultRetained preserves the file after a failed delegated open.
	//
	// Example: the retained path appears in the returned diagnostic.
	faultRetained

	// faultCloseFailure makes a delegated close report failure.
	//
	// Example: the handle reports the parent close result.
	faultCloseFailure
)

// faultParent records a delegated open and injects one parent VFS failure.
//
// Example: an open may fail after installing a usable close method.
type faultParent struct {
	parent        uintptr
	mode          faultMode
	methods       sqlite3.Tsqlite3_io_methods
	originalClose uintptr
	path          string
	opens         int
	closes        int
}

// nullFilenameAllocator records a per-handle injected allocation failure.
//
// Example: SQLite reports a null filename without changing another handle.
type nullFilenameAllocator struct {
	calls int
}

// Create records a call and returns the injected null result.
//
// Example: allocation failure prevents delegation to the parent VFS.
func (a *nullFilenameAllocator) Create(
	_ *libc.TLS,
	_ uintptr,
	_ uintptr,
	_ uintptr,
	_ int32,
	_ uintptr,
) uintptr {
	a.calls++
	return 0
}

// currentFaultParent is used only by serial tests while their parent clone lives.
//
// Example: serial callback tests release it before the next case.
var currentFaultParent *faultParent

// faultOpen forwards to the real parent except for a null-method open failure.
//
// Example: a successful parent open can be reported as failed to the shim.
func faultOpen(
	tls *libc.TLS,
	_ uintptr,
	name uintptr,
	file uintptr,
	flags int32,
	out uintptr,
) int32 {
	f := currentFaultParent
	switch f.mode {
	case faultNull, faultNonNull, faultRetained, faultCloseFailure:
	default:
		*(*uintptr)(unsafe.Pointer(file)) = 0
		return sqlite3.SQLITE_CANTOPEN
	}
	f.opens++
	f.path = libc.GoString(name)
	if f.mode == faultNull {
		*(*uintptr)(unsafe.Pointer(file)) = 0
		return sqlite3.SQLITE_CANTOPEN
	}
	if f.mode == faultRetained {
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
	f.methods.FxClose = *(*uintptr)(unsafe.Pointer(&struct {
		F func(*libc.TLS, uintptr) int32
	}{faultClose}))
	*(*uintptr)(unsafe.Pointer(file)) = uintptr(unsafe.Pointer(&f.methods))
	if f.mode == faultNonNull {
		return sqlite3.SQLITE_CANTOPEN
	}
	return rc
}

// faultClose closes the real parent file and can expose a close error.
//
// Example: the shim retains an error returned by the delegated close.
func faultClose(
	tls *libc.TLS,
	file uintptr,
) int32 {
	f := currentFaultParent
	switch f.mode {
	case faultNull, faultNonNull, faultRetained, faultCloseFailure:
	default:
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	f.closes++
	method := f.originalClose
	rc := (*(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&method)))(tls, file)
	if rc == sqlite3.SQLITE_OK && f.mode == faultCloseFailure {
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	return rc
}

// TestUnknownFaultModeRejectsDelegation prevents an invalid test mode falling through.
//
// Example: an unrecognized mode cannot call the real parent VFS.
func TestUnknownFaultModeRejectsDelegation(t *testing.T) {
	f := &faultParent{mode: faultMode(99)}
	currentFaultParent = f
	defer
	// clearUnknownFaultParent releases the process-global callback state.
	//
	// Example: later fault tests begin without an invalid mode.
	func() { currentFaultParent = nil }()

	methods := uintptr(1)
	rc := faultOpen(nil, 0, 0, uintptr(unsafe.Pointer(&methods)), 0, 0)
	require.Equal(t, int32(sqlite3.SQLITE_CANTOPEN), rc)
	require.Zero(t, methods)
	require.Zero(t, f.opens)
	require.Equal(t, int32(sqlite3.SQLITE_IOERR_CLOSE), faultClose(nil, 0))
	require.Zero(t, f.closes)
}

// TestAuxiliaryParentFailures exercises shim cleanup and diagnostic ownership.
//
// Example: a failed open with methods is closed before SQLite sees null methods.
func TestAuxiliaryParentFailures(t *testing.T) {
	account, err := user.Current()
	require.NoError(t, err)
	root, err := os.MkdirTemp(filepath.Join(account.HomeDir, "tmp"), "vfs-fault-test-")
	require.NoError(t, err)
	t.Cleanup(
		// cleanupFaultRoot removes only this test's isolated temporary root.
		//
		// Example: a retained diagnostic file is cleaned after its assertion.
		func() {
			assert.NoError(t, os.RemoveAll(root))
		},
	)

	for _, test := range []struct {
		name string
		mode faultMode
	}{
		{"null", faultNull},
		{"nonnull", faultNonNull},
		{"retained", faultRetained},
		{"close", faultCloseFailure},
	} {
		t.Run(test.name,
			// exerciseParentFailure checks one delegated-open outcome.
			//
			// Example: non-null failed opens are synchronously closed.
			func(t *testing.T) {
				h, err := openAt("file:"+filepath.Join(root, test.name+".db"), root)
				require.NoError(t, err)
				tls := libc.NewTLS()
				defer tls.Close()
				clone := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
				require.NotZero(t, clone, "allocate parent VFS")
				defer sqlite3.Xsqlite3_free(tls, clone)
				*(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)) = *(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
				f := &faultParent{parent: h.parent, mode: test.mode}
				currentFaultParent = f
				defer
				// clearFaultParent removes the serial callback's test state.
				//
				// Example: a later subtest starts without the prior parent.
				func() { currentFaultParent = nil }()
				(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)).FxOpen = *(*uintptr)(unsafe.Pointer(&struct {
					F func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32
				}{faultOpen}))
				h.parent = clone
				file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
				require.NotZero(t, file, "allocate SQLite file")
				defer sqlite3.Xsqlite3_free(tls, file)
				output := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(int32(0))))
				require.NotZero(t, output, "allocate SQLite output flags")
				defer sqlite3.Xsqlite3_free(tls, output)
				flags := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE)
				rc := openFile(tls, h.vfs, 0, file, flags, output)
				require.Equal(t, 1, f.opens, "parent opens")
				if test.mode == faultCloseFailure {
					require.Equal(t, int32(sqlite3.SQLITE_OK), rc, "open")
					rc = closeFile(tls, file)
					require.Equal(t, int32(sqlite3.SQLITE_IOERR_CLOSE), rc, "close")
				} else {
					require.Equal(t, int32(sqlite3.SQLITE_CANTOPEN), rc, "open")
					require.Zero(t, *(*uintptr)(unsafe.Pointer(file)), "failed open retained methods")
				}
				wantCloses := 1
				if test.mode == faultNull {
					wantCloses = 0
				}
				require.Equal(t, wantCloses, f.closes, "parent closes")
				if test.mode == faultRetained {
					_, err := os.Lstat(f.path)
					require.NoError(t, err, "retained file missing")
				} else {
					_, err := os.Lstat(f.path)
					require.True(t, os.IsNotExist(err), "unexpected auxiliary file: %v", err)
				}
				h.parent = f.parent
				err = h.Close()
				require.Error(t, err, "missing callback diagnostic")
				if test.mode == faultRetained {
					require.Contains(t, err.Error(), f.path)
				}
				if test.mode == faultCloseFailure {
					require.Contains(t, err.Error(), fmt.Sprint(sqlite3.SQLITE_IOERR_CLOSE))
				}
			},
		)
	}
}

// unlinkFailurePath records the pathname passed to the failing parent syscall.
//
// Example: the subprocess verifies SQLite attempted the owned filename.
var unlinkFailurePath string

// failUnlink simulates the Unix parent's unlink syscall failing.
//
// Example: a DELETEONCLOSE open leaves its name behind.
func failUnlink(
	_ *libc.TLS,
	path uintptr,
) int32 {
	unlinkFailurePath = libc.GoString(path)
	return -1
}

// TestAuxiliaryUnlinkFailure isolates SQLite's process-global syscall override.
//
// Example: an ignored parent unlink error becomes a retained-path diagnostic.
func TestAuxiliaryUnlinkFailure(t *testing.T) {
	if os.Getenv(unlinkFailureChildEnv) != "1" {
		executable, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(executable, "-test.run=^TestAuxiliaryUnlinkFailure$")
		cmd.Env = append(os.Environ(), unlinkFailureChildEnv+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "unlink fault subprocess: %s", output)
		return
	}

	account, err := user.Current()
	require.NoError(t, err)
	root, err := os.MkdirTemp(filepath.Join(account.HomeDir, "tmp"), "vfs-unlink-test-")
	require.NoError(t, err)
	defer
	// cleanupUnlinkRoot removes the subprocess-owned scratch after assertions.
	//
	// Example: a deliberately retained auxiliary name is test-local.
	func() {
		assert.NoError(t, os.RemoveAll(root))
	}()
	h, err := openAt("file:"+filepath.Join(root, "main.db"), root)
	require.NoError(t, err)
	tls := libc.NewTLS()
	defer tls.Close()
	parent := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
	require.NotZero(t, parent.FxGetSystemCall, "parent VFS cannot get unlink syscall")
	require.NotZero(t, parent.FxSetSystemCall, "parent VFS cannot override unlink")
	name, err := libc.CString("unlink")
	require.NoError(t, err)
	defer libc.Xfree(tls, name)
	get := parent.FxGetSystemCall
	set := parent.FxSetSystemCall
	original := (*(*func(*libc.TLS, uintptr, uintptr) uintptr)(unsafe.Pointer(&get)))(tls, h.parent, name)
	require.NotZero(t, original, "parent VFS has no unlink syscall")
	setCall := *(*func(*libc.TLS, uintptr, uintptr, uintptr) int32)(unsafe.Pointer(&set))
	failing := *(*uintptr)(unsafe.Pointer(&struct {
		F func(*libc.TLS, uintptr) int32
	}{failUnlink}))
	require.Equal(t, int32(sqlite3.SQLITE_OK), setCall(tls, h.parent, name, failing), "override unlink")
	defer
	// restoreUnlink reinstates the parent syscall before subprocess exit.
	//
	// Example: no global hook survives the fault exercise.
	func() {
		assert.Equal(t, int32(sqlite3.SQLITE_OK), setCall(tls, h.parent, name, original), "restore unlink")
	}()

	file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
	require.NotZero(t, file, "allocate SQLite file")
	defer sqlite3.Xsqlite3_free(tls, file)
	out := sqlite3.Xsqlite3_malloc64(tls, 4)
	require.NotZero(t, out, "allocate SQLite output flags")
	defer sqlite3.Xsqlite3_free(tls, out)
	flags := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE)
	rc := openFile(tls, h.vfs, 0, file, flags, out)
	require.Equal(t, int32(sqlite3.SQLITE_CANTOPEN), rc, "open with failed unlink")
	require.Zero(t, *(*uintptr)(unsafe.Pointer(file)), "failed open retained methods")
	require.NotEmpty(t, unlinkFailurePath, "unlink was not called")
	require.Equal(t, h.dir, filepath.Dir(unlinkFailurePath), "unlink did not target owned scratch")
	_, err = os.Lstat(unlinkFailurePath)
	require.NoError(t, err, "unlinked file was not retained")
	err = h.Close()
	require.Error(t, err, "missing retained-path diagnostic")
	require.Contains(t, err.Error(), unlinkFailurePath)
}

// TestAuxiliaryFilenameNullResult verifies per-handle allocation failure cleanup.
//
// Example: a null filename allocation never delegates or retains the owned inode.
func TestAuxiliaryFilenameNullResult(t *testing.T) {
	account, err := user.Current()
	require.NoError(t, err)
	root, err := os.MkdirTemp(filepath.Join(account.HomeDir, "tmp"), "vfs-allocation-test-")
	require.NoError(t, err)
	t.Cleanup(
		// cleanupAllocationRoot removes this test's isolated temporary root.
		//
		// Example: unrelated account files are never traversed.
		func() {
			assert.NoError(t, os.RemoveAll(root))
		},
	)
	allocator := &nullFilenameAllocator{}
	h, err := openAtWithAllocator("file:"+filepath.Join(root, "main.db"), root, allocator)
	require.NoError(t, err)
	tls := libc.NewTLS()
	defer tls.Close()
	clone := sqlite3.Xsqlite3_malloc64(tls, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_vfs{})))
	require.NotZero(t, clone, "allocate parent VFS")
	defer sqlite3.Xsqlite3_free(tls, clone)
	*(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)) = *(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(h.parent))
	fault := &faultParent{parent: h.parent, mode: faultNull}
	currentFaultParent = fault
	defer
	// clearAllocationFaultParent releases the serial test callback state.
	//
	// Example: later fault cases do not see this cloned parent.
	func() { currentFaultParent = nil }()
	(*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(clone)).FxOpen = *(*uintptr)(unsafe.Pointer(&struct {
		F func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32
	}{faultOpen}))
	h.parent = clone
	file := sqlite3.Xsqlite3_malloc64(tls, uint64(h.tail+unsafe.Sizeof(fileState{})))
	require.NotZero(t, file, "allocate SQLite file")
	defer sqlite3.Xsqlite3_free(tls, file)
	flags := int32(sqlite3.SQLITE_OPEN_TEMP_DB | sqlite3.SQLITE_OPEN_READWRITE | sqlite3.SQLITE_OPEN_CREATE | sqlite3.SQLITE_OPEN_EXCLUSIVE | sqlite3.SQLITE_OPEN_DELETEONCLOSE)
	rc := openFile(tls, h.vfs, 0, file, flags, 0)
	require.Equal(t, int32(sqlite3.SQLITE_CANTOPEN), rc)
	require.Zero(t, *(*uintptr)(unsafe.Pointer(file)), "failed allocation retained methods")
	require.Equal(t, 1, allocator.calls)
	require.Zero(t, fault.opens, "delegated open after allocation failure")
	require.Zero(t, fault.closes, "delegated close after allocation failure")
	require.Zero(t, h.active, "changed active count")
	require.Zero(t, h.opens, "changed open count")
	entries, err := os.ReadDir(h.dir)
	require.NoError(t, err)
	require.Empty(t, entries, "owned scratch not cleaned")
	h.parent = fault.parent
	err = h.Close()
	require.Error(t, err, "missing allocation diagnostic")
	require.Contains(t, err.Error(), h.dir)
	require.True(t, h.released, "failed allocation retained VFS registration")
	_, err = os.Lstat(h.dir)
	require.True(t, os.IsNotExist(err), "scratch directory not removed: %v", err)
}
