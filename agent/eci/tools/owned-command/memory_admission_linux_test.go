package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryAdmissionHost charges a peer's full cap across user managers,
// ignoring changes in its residency until the peer's cgroup actually leaves.
func TestMemoryAdmissionHost(t *testing.T) {
	t.Parallel()
	root, meminfo, command := memoryAdmissionTree(t, 128)
	peer := memoryAdmissionPeer(t, root, "1002", "owned-command-2-2.scope", 100)
	putAdmissionFile(t, filepath.Join(peer, "memory.current"), "102400\n")

	require.NoError(t, admitCommandMemory(root, meminfo, command, 28*1024))
	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 29*1024), "host MemAvailable")

	putAdmissionFile(t, filepath.Join(peer, "memory.current"), "0\n")
	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 29*1024), "host MemAvailable")

	require.NoError(t, os.RemoveAll(peer))
	require.NoError(t, admitCommandMemory(root, meminfo, command, 128*1024))
}

// TestMemoryAdmissionAncestors charges related peers and ignores disjoint
// peers when checking the tighter user limit.
func TestMemoryAdmissionAncestors(t *testing.T) {
	t.Parallel()
	root, meminfo, command := memoryAdmissionTree(t, 4096)
	memoryAdmissionPeer(t, root, "1001", "owned-command-2-2.scope", 20)
	memoryAdmissionPeer(t, root, "1002", "owned-command-3-3.scope", 300)
	user := filepath.Join(root, "user.slice", "user-1001.slice")
	putAdmissionFile(t, filepath.Join(user, "memory.max"), "245760\n")
	putAdmissionFile(t, filepath.Join(user, "memory.high"), "204800\n")
	putAdmissionFile(t, filepath.Join(user, "memory.current"), "122880\n")

	require.NoError(t, admitCommandMemory(root, meminfo, command, 60*1024))
	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 61*1024), user)
}

// TestMemoryAdmissionObservedReclaim admits newly usable RAM only after the
// constraining ancestor's current charge decreases.
func TestMemoryAdmissionObservedReclaim(t *testing.T) {
	t.Parallel()
	root, meminfo, command := memoryAdmissionTree(t, 128)
	user := filepath.Join(root, "user.slice", "user-1001.slice")
	putAdmissionFile(t, filepath.Join(user, "memory.max"), "102400\n")
	putAdmissionFile(t, filepath.Join(user, "memory.current"), "81920\n")
	putAdmissionFile(t, filepath.Join(user, "memory.stat"), "file 81920\nslab_reclaimable 10240\n")

	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 40*1024), user)
	putAdmissionFile(t, filepath.Join(user, "memory.current"), "61440\n")
	require.NoError(t, admitCommandMemory(root, meminfo, command, 40*1024))
}

// TestMemoryAdmissionLock proves independent opens contend and filesystem custody
// is available again as soon as the holding descriptor closes.
func TestMemoryAdmissionLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first, err := lockMemoryAdmission(context.Background(), root)
	require.NoError(t, err)
	defer func() {
		if first != nil {
			assert.NoError(t, first.Close())
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = lockMemoryAdmission(ctx, root)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, first.Close())
	first = nil
	ctx, cancelAgain := context.WithTimeout(context.Background(), time.Second)
	defer cancelAgain()
	second, err := lockMemoryAdmission(ctx, root)
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

// memoryAdmissionTree creates a synthetic hierarchy and an unlimited candidate's ancestors.
func memoryAdmissionTree(t *testing.T, available int) (root, meminfo, command string) {
	t.Helper()
	root = t.TempDir()
	meminfo = filepath.Join(root, "meminfo")
	putAdmissionFile(t, meminfo, fmt.Sprintf("MemFree: 1 kB\nMemAvailable: %d kB\n", available))
	command = filepath.Join(userManagerAppSlice(filepath.Join(root, "user.slice"), "1001"), "owned-command-1-1.scope", commandLeaf)
	for ancestor := filepath.Dir(command); ancestor != root; ancestor = filepath.Dir(ancestor) {
		putAdmissionFile(t, filepath.Join(ancestor, "memory.max"), "max\n")
		putAdmissionFile(t, filepath.Join(ancestor, "memory.high"), "max\n")
	}
	return root, meminfo, command
}

// memoryAdmissionPeer publishes a capped peer using the same controls as a live kernel.
func memoryAdmissionPeer(t *testing.T, root, uid, scope string, cap int) string {
	t.Helper()
	command := filepath.Join(userManagerAppSlice(filepath.Join(root, "user.slice"), uid), scope, commandLeaf)
	putAdmissionFile(t, filepath.Join(command, "memory.max"), fmt.Sprintln(cap*1024))
	return command
}

// putAdmissionFile populates a synthetic kernel memory control.
func putAdmissionFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
}
