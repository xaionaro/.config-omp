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

// TestMemoryAdmissionHost keeps a pending peer claim across user managers without
// counting the peer's cache twice, and releases the claim when its cgroup leaves.
func TestMemoryAdmissionHost(t *testing.T) {
	t.Parallel()
	root, meminfo, command := memoryAdmissionTree(t, 128)
	peer := memoryAdmissionPeer(t, root, "1002", "owned-command-2-2.scope", 100, 80, 20, 0)

	require.NoError(t, admitCommandMemory(root, meminfo, command, 48*1024))
	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 49*1024), "host MemAvailable")

	putAdmissionFile(t, filepath.Join(peer, "memory.current"), "0\n")
	putAdmissionFile(t, filepath.Join(peer, "memory.stat"), "anon 0\npglazyfree 0\n")
	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 29*1024), "host MemAvailable")

	require.NoError(t, os.RemoveAll(peer))
	require.NoError(t, admitCommandMemory(root, meminfo, command, 128*1024))
}

// TestMemoryAdmissionAncestors uses local resident charges and ignores disjoint
// peers when checking the tighter user limit.
func TestMemoryAdmissionAncestors(t *testing.T) {
	t.Parallel()
	root, meminfo, command := memoryAdmissionTree(t, 4096)
	memoryAdmissionPeer(t, root, "1001", "owned-command-2-2.scope", 100, 80, 20, 0)
	memoryAdmissionPeer(t, root, "1002", "owned-command-3-3.scope", 300, 0, 0, 0)
	user := filepath.Join(root, "user.slice", "user-1001.slice")
	putAdmissionFile(t, filepath.Join(user, "memory.max"), "245760\n")
	putAdmissionFile(t, filepath.Join(user, "memory.high"), "204800\n")
	putAdmissionFile(t, filepath.Join(user, "memory.current"), "122880\n")

	require.NoError(t, admitCommandMemory(root, meminfo, command, 60*1024))
	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 61*1024), user)
}

// TestMemoryAdmissionLazyFree gives no extra resident credit to potentially
// file-LRU-reclaimable anonymous pages.
func TestMemoryAdmissionLazyFree(t *testing.T) {
	t.Parallel()
	root, meminfo, command := memoryAdmissionTree(t, 128)
	memoryAdmissionPeer(t, root, "1002", "owned-command-2-2.scope", 100, 80, 20, 20*1024/os.Getpagesize()+1)

	require.NoError(t, admitCommandMemory(root, meminfo, command, 28*1024))
	require.ErrorContains(t, admitCommandMemory(root, meminfo, command, 29*1024), "host MemAvailable")
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
func memoryAdmissionPeer(t *testing.T, root, uid, scope string, cap, current, anon, lazy int) string {
	t.Helper()
	command := filepath.Join(userManagerAppSlice(filepath.Join(root, "user.slice"), uid), scope, commandLeaf)
	putAdmissionFile(t, filepath.Join(command, "memory.max"), fmt.Sprintln(cap*1024))
	putAdmissionFile(t, filepath.Join(command, "memory.current"), fmt.Sprintln(current*1024))
	putAdmissionFile(t, filepath.Join(command, "memory.swap.max"), "0\n")
	putAdmissionFile(t, filepath.Join(command, "memory.stat"), fmt.Sprintf("anon %d\nfile %d\npglazyfree %d\n", anon*1024, (current-anon)*1024, lazy))
	return command
}

// putAdmissionFile populates a synthetic kernel memory control.
func putAdmissionFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
}
