package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestMemoryTopologyClaims checks discovery outside the launcher's slice, dead
// guard independence, and exclusion of uncapped and prospective commands.
//
// Example: a finite orphan under another UID remains a claim.
func TestMemoryTopologyClaims(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	view := memoryHostView{Source: os.DirFS("/")}
	candidate := filepath.Join(root, "system.slice", "owned-command-new.scope", commandLeaf)
	peer := filepath.Join(root, "custom.slice", ownedCommandScopeName(123, 456), commandLeaf)
	uncapped := filepath.Join(root, "custom.slice", "owned-command-uncapped.scope", commandLeaf)
	foreign := filepath.Join(root, "custom.slice", "unrelated.scope", commandLeaf)
	for path, value := range map[string]string{candidate: "4096", peer: "4096", uncapped: "max", foreign: "4096"} {
		writeMemoryTopologyFixture(t, path, "memory.max", value)
	}
	noController := filepath.Join(root, "custom.slice", ownedCommandScopeName(456, 789), commandLeaf)
	writeMemoryTopologyFixture(t, noController, cgroupProcsFile, "1234")
	got, err := discoverMemoryTopology(context.Background(), view, root, candidate)
	require.NoError(t, err)
	require.Equal(t, []memoryClaim{{Path: peer, Cap: 4096}}, got.Claims)
	require.Equal(t, []string{filepath.Dir(candidate), filepath.Dir(filepath.Dir(candidate))}, got.Ancestors)
	require.NoError(t, os.RemoveAll(peer))
	got, err = discoverMemoryTopology(context.Background(), view, root, candidate)
	require.NoError(t, err)
	require.Empty(t, got.Claims)
	writeMemoryTopologyFixture(t, peer, "memory.max", "invalid")
	got, err = discoverMemoryTopology(context.Background(), view, root, candidate)
	require.Error(t, err)
	require.Empty(t, got.Claims)
}

// TestMemoryTopologyGone skips deactivated kernel directories and their claims.
//
// Example: a scope removed while the walk opens its command leaf is not discovered.
func TestMemoryTopologyGone(t *testing.T) {
	t.Parallel()
	source := &memoryTopologyFixtureFS{
		MapFS: fstest.MapFS{
			"cg/owned-command-stale.scope/command/memory.max": {Data: []byte("4096")},
			"cg/owned-command-live.scope/command/memory.max":  {Data: []byte("8192")},
		},
		gone: "cg/owned-command-stale.scope",
	}
	got, err := discoverMemoryTopology(context.Background(), memoryHostView{Source: source}, "/cg", "/cg/new/command")
	require.NoError(t, err)
	require.Equal(t, []memoryClaim{{Path: "/cg/owned-command-live.scope/command", Cap: 8192}}, got.Claims)
}

// memoryTopologyFixtureFS simulates a disappearing kernfs directory or PID.
//
// Example: a PID vanishes from cgroup.procs between sequential membership reads.
type memoryTopologyFixtureFS struct {
	fstest.MapFS
	gone          string
	exitedOnRetry bool
}

// Open injects ENODEV when a kernfs directory is deactivated.
//
// Example: fs.WalkDir attempts to read a directory that no longer exists.
func (f *memoryTopologyFixtureFS) Open(path string) (fs.File, error) {
	if path == f.gone {
		return nil, &fs.PathError{Op: "open", Path: path, Err: unix.ENODEV}
	}
	return f.MapFS.Open(path)
}

// ReadDir makes the simulated directory failure visible to fs.WalkDir.
//
// Example: a live sibling is still read after the deactivated path fails.
func (f *memoryTopologyFixtureFS) ReadDir(path string) ([]fs.DirEntry, error) {
	if path == f.gone {
		return nil, &fs.PathError{Op: "readdir", Path: path, Err: unix.ENODEV}
	}
	return f.MapFS.ReadDir(path)
}

// ReadFile simulates process exit while opening a missing command line.
//
// Example: a reread of cgroup.procs no longer contains the missing PID.
func (f *memoryTopologyFixtureFS) ReadFile(path string) ([]byte, error) {
	if f.exitedOnRetry && path == "proc/14/cmdline" {
		f.MapFS["cg/system.slice/earlyoom.service/cgroup.procs"] = &fstest.MapFile{}
	}
	return f.MapFS.ReadFile(path)
}

// TestMemoryTopologyUnreadableSubtree refuses to admit from a partial scan.
//
// Example: a cgroup hierarchy with a permissions-restricted slice fails closed.
func TestMemoryTopologyUnreadableSubtree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	candidate := filepath.Join(root, "owned-command-pending.scope", commandLeaf)
	hidden := filepath.Join(root, "restricted.slice")
	require.NoError(t, os.Mkdir(hidden, 0000))
	defer os.Chmod(hidden, 0700)
	if os.Geteuid() == 0 {
		t.Skip("root can read the permission-denied fixture")
	}
	got, err := discoverMemoryTopology(context.Background(), memoryHostView{Source: os.DirFS("/")}, root, candidate)
	require.ErrorIs(t, err, os.ErrPermission)
	require.Empty(t, got.Claims)
}

// TestMemoryTopologyProtection selects maximal explicit protections separately
// for the host and each memory boundary, excluding the boundary itself.
//
// Example: P is protected for R but only Q is protected for P.
func TestMemoryTopologyProtection(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	root := "/cg"
	r := root + "/R"
	p := r + "/P"
	q := p + "/Q"
	nested := q + "/nested"
	candidate := p + "/command"
	for _, path := range []string{r, p} {
		writeMemorySample(t, tree, path+"/memory.max", "4194304")
		writeMemorySample(t, tree, path+"/memory.high", "max")
		writeMemorySample(t, tree, path+"/memory.current", "262144")
		writeMemorySample(t, tree, path+"/memory.stat", memoryFixtureStat(0, 32))
	}
	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 4096 kB")
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, p+"/memory.low", "max")
	writeMemorySample(t, tree, q+"/memory.min", "max")
	writeMemorySample(t, tree, q+"/memory.max", "max")
	writeMemorySample(t, tree, q+"/memory.high", "max")
	writeMemorySample(t, tree, q+"/memory.stat", memoryFixtureStat(0, 32))
	writeMemorySample(t, tree, nested+"/memory.low", "1")
	view := memoryHostView{Source: os.DirFS(tree)}
	ctx := context.Background()
	got, err := discoverMemoryTopology(ctx, view, root, candidate)
	require.NoError(t, err)
	require.Equal(t, []string{p}, got.Protected[hostMemoryBoundID])
	require.Equal(t, []string{p}, got.Protected[cgroupMemoryBoundID(r)])
	require.Equal(t, []string{q}, got.Protected[cgroupMemoryBoundID(p)])
	require.NoError(t, checkCommandMemory(ctx, view, got, 0, 3932160, false))
	var refusal *memoryAdmissionRefusal
	require.ErrorAs(t, checkCommandMemory(ctx, view, got, 0, 3932160+4096, false), &refusal)
	writeMemorySample(t, tree, q+"/memory.min", "invalid")
	_, err = discoverMemoryTopology(context.Background(), view, root, candidate)
	require.ErrorContains(t, err, "memory.min")
}

// TestMemoryAdmissionRoot verifies that only a hierarchy root can be locked.
//
// Example: a delegated subtree exposes cgroup.type and must be refused.
func TestMemoryAdmissionRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lock, err := lockHostMemoryAdmission(context.Background(), root)
	require.NoError(t, err)
	require.NoError(t, lock.Close())
	writeMemoryTopologyFixture(t, root, "cgroup.type", "domain")
	lock, err = lockHostMemoryAdmission(context.Background(), root)
	require.Error(t, err)
	require.Nil(t, lock)
}

// TestMemoryTopologyLock checks contention, cancellation, and reacquisition.
//
// Example: two independent opens of the same directory cannot publish at once.
func TestMemoryTopologyLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lock, err := lockHostMemoryAdmission(context.Background(), root)
	require.NoError(t, err)
	flags, err := unix.FcntlInt(lock.Fd(), unix.F_GETFD, 0)
	require.NoError(t, err)
	require.NotZero(t, flags&unix.FD_CLOEXEC)
	defer lock.Close()
	other, err := os.Open(root)
	require.NoError(t, err)
	defer other.Close()
	require.ErrorIs(t, unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB), unix.EWOULDBLOCK)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	unexpected, err := lockHostMemoryAdmission(ctx, root)
	if unexpected != nil {
		unexpected.Close()
	}
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, lock.Close())
	require.NoError(t, unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB))
}

// TestMemoryCgroupVisibilityLock checks leaf creation waits for an admission on the same view.
//
// Example: a private cgroup view cannot expose a new leaf before the lock is released.
func TestMemoryCgroupVisibilityLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMemoryTopologyFixture(t, root, "cgroup.type", "domain")
	path := filepath.Join(root, "command")
	lock, err := lockMemoryAdmission(context.Background(), root)
	require.NoError(t, err)
	defer lock.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = createVisibleCgroup(ctx, root, path)
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, os.ErrNotExist)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, lock.Close())
	require.NoError(t, createVisibleCgroup(context.Background(), root, path))
	require.DirExists(t, path)
}

// TestEarlyoomHostFloor covers default, live split thresholds, and a malformed
// threshold that must not silently allow admission.
//
// Example: -m 18,13 reserves at the SIGTERM threshold, not SIGKILL.
func TestEarlyoomHostFloor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want MemoryBytes
		ok   bool
	}{
		{[]string{"-m10", "-m18,13"}, 180000, true},
		{[]string{"--unrecognized"}, 0, false},
		{nil, 100000, true},
		{[]string{"-m", "18,13", "-s", "100,100", "--avoid", "-m"}, 180000, true},
		{[]string{"-m12.5,10"}, 125000, true},
		{[]string{"-M100"}, 102400, true},
		{[]string{"-m12,18"}, 180000, true},
		{[]string{"--pref=earlyoom", "-dm18,13"}, 180000, true},
		{[]string{"-m100"}, 0, false},
		{[]string{"-m", "18", "-M", "100"}, 102400, true},
		{[]string{"-m", "18,invalid"}, 0, false},
		{[]string{"-M"}, 0, false},
	} {
		got, err := earlyoomHostFloor(1000000, tc.args)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("args %q: got %d, %v; want %d, success %v", tc.args, got, err, tc.want, tc.ok)
		}
	}
}

// TestReadHostMemoryFloor verifies membership revalidation and the maximum floor.
//
// Example: a reused PID outside the service must not contribute a floor.
func TestReadHostMemoryFloor(t *testing.T) {
	t.Parallel()
	source := &memoryTopologyFixtureFS{MapFS: fstest.MapFS{
		"proc/meminfo":    {Data: []byte("MemTotal: 1000 kB")},
		"proc/11/cmdline": {Data: []byte("/usr/bin/earlyoom\x00-m10\x00")},
		"proc/11/cgroup":  {Data: []byte("0::/system.slice/earlyoom.service")},
		"proc/12/cmdline": {Data: []byte("/usr/bin/earlyoom\x00-m18,13\x00")},
		"proc/12/cgroup":  {Data: []byte("0::/system.slice/earlyoom.service")},
		"proc/13/cmdline": {Data: []byte("/usr/bin/earlyoom\x00--unknown\x00")},
		"proc/13/cgroup":  {Data: []byte("0::/system.slice/elsewhere.service")},
	}}
	view := memoryHostView{Source: source}
	ctx := context.Background()
	got, err := readEarlyoomMemoryFloor(ctx, view, "/cg")
	require.NoError(t, err)
	require.Zero(t, got)

	unit := "cg/system.slice/earlyoom.service/cgroup.procs"
	source.MapFS[unit] = &fstest.MapFile{Data: []byte("11\n12\n13")}
	got, err = readEarlyoomMemoryFloor(ctx, view, "/cg")
	require.NoError(t, err)
	require.Equal(t, MemoryBytes(184320), got)

	source.MapFS[unit] = &fstest.MapFile{Data: []byte("14")}
	got, err = readEarlyoomMemoryFloor(ctx, view, "/cg")
	require.ErrorContains(t, err, "PID 14")
	require.ErrorContains(t, err, "hidepid")
	require.Zero(t, got)

	source.exitedOnRetry = true
	got, err = readEarlyoomMemoryFloor(ctx, view, "/cg")
	require.NoError(t, err)
	require.Zero(t, got)

	source.exitedOnRetry = false
	source.MapFS[unit] = &fstest.MapFile{Data: []byte("14")}
	source.MapFS["proc/14/cmdline"] = &fstest.MapFile{Data: []byte("/usr/bin/earlyoom\x00")}
	_, err = readEarlyoomMemoryFloor(ctx, view, "/cg")
	require.ErrorContains(t, err, "PID 14")
}

// writeMemoryTopologyFixture writes a controller sample into a fixture cgroup.
//
// Example: create a peer with memory.max=4096.
func writeMemoryTopologyFixture(
	t *testing.T,
	path string,
	name string,
	value string,
) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(path, name), []byte(value), 0600))
}
