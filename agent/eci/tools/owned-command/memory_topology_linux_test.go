package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	candidate := filepath.Join(root, "system.slice", "owned-command-new.scope", commandLeaf)
	peer := filepath.Join(root, "custom.slice", "owned-command-peer.scope", commandLeaf)
	uncapped := filepath.Join(root, "custom.slice", "owned-command-uncapped.scope", commandLeaf)
	foreign := filepath.Join(root, "custom.slice", "unrelated.scope", commandLeaf)
	for path, value := range map[string]string{candidate: "4096", peer: "4096", uncapped: "max", foreign: "4096"} {
		writeMemoryTopologyFixture(t, path, "memory.max", value)
	}
	got, err := discoverMemoryTopology(context.Background(), root, candidate)
	require.NoError(t, err)
	require.Equal(t, []memoryClaim{{Path: peer, Cap: 4096}}, got.Claims)
	require.Equal(t, []string{filepath.Dir(candidate), filepath.Dir(filepath.Dir(candidate))}, got.Ancestors)
	require.NoError(t, os.RemoveAll(peer))
	got, err = discoverMemoryTopology(context.Background(), root, candidate)
	require.NoError(t, err)
	require.Empty(t, got.Claims)
	writeMemoryTopologyFixture(t, peer, "memory.max", "invalid")
	got, err = discoverMemoryTopology(context.Background(), root, candidate)
	require.Error(t, err)
	require.Empty(t, got.Claims)
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
	got, err := discoverMemoryTopology(context.Background(), root, candidate)
	require.ErrorIs(t, err, os.ErrPermission)
	require.Empty(t, got.Claims)
}

// TestMemoryTopologyProtection selects maximal explicit protections separately
// for the host and each memory boundary, excluding the boundary itself.
//
// Example: P is protected for R but only Q is protected for P.
func TestMemoryTopologyProtection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	r := filepath.Join(root, "R")
	p := filepath.Join(r, "P")
	q := filepath.Join(p, "Q")
	nested := filepath.Join(q, "nested")
	candidate := filepath.Join(p, "owned-command-pending.scope", commandLeaf)
	writeMemoryTopologyFixture(t, r, "memory.min", "0")
	writeMemoryTopologyFixture(t, p, "memory.low", "1")
	writeMemoryTopologyFixture(t, q, "memory.min", "1")
	writeMemoryTopologyFixture(t, q, "memory.max", "max")
	writeMemoryTopologyFixture(t, q, "memory.high", "max")
	writeMemoryTopologyFixture(t, nested, "memory.low", "1")
	got, err := discoverMemoryTopology(context.Background(), root, candidate)
	require.NoError(t, err)
	require.Equal(t, []string{p}, got.Protected[""])
	require.Equal(t, []string{p}, got.Protected[r])
	require.Equal(t, []string{q}, got.Protected[p])
	require.Empty(t, got.Protected[filepath.Dir(candidate)])
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unexpected, err := lockHostMemoryAdmission(ctx, root)
	if unexpected != nil {
		unexpected.Close()
	}
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, lock.Close())
	require.NoError(t, unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB))
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
	root, proc := t.TempDir(), t.TempDir()
	read := // Read the floor from fixture paths in the real reader.
		func() (MemoryBytes, error) {
			return readEarlyoomMemoryFloor(context.Background(), root, proc)
		}
	got, err := read()
	require.NoError(t, err)
	require.Zero(t, got)
	writeMemoryTopologyFixture(t, proc, "meminfo", "MemTotal: 1000 kB")
	writeMemoryTopologyFixture(t, filepath.Join(root, "system.slice", "earlyoom.service"), "cgroup.procs", "11\n12\n13\n14")
	writeMemoryTopologyFixture(t, filepath.Join(proc, "11"), "cmdline", "/usr/bin/earlyoom\x00-m10\x00")
	writeMemoryTopologyFixture(t, filepath.Join(proc, "11"), "cgroup", "0::/system.slice/earlyoom.service")
	writeMemoryTopologyFixture(t, filepath.Join(proc, "12"), "cmdline", "/usr/bin/earlyoom\x00-m18,13\x00")
	writeMemoryTopologyFixture(t, filepath.Join(proc, "12"), "cgroup", "0::/system.slice/earlyoom.service")
	writeMemoryTopologyFixture(t, filepath.Join(proc, "13"), "cmdline", "/usr/bin/earlyoom\x00--unknown\x00")
	writeMemoryTopologyFixture(t, filepath.Join(proc, "13"), "cgroup", "0::/system.slice/elsewhere.service")
	got, err = read()
	require.NoError(t, err)
	require.Equal(t, MemoryBytes(184320), got)
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
