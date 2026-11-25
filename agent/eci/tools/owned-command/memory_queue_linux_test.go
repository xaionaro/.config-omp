package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryQueueProjectsAllBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, slice                  string
		request                      MemoryBytes
		hostPages, ancestorFilePages int
		eligible                     bool
	}{
		{"sufficient across both bounds", "/target.slice", 60, 100, 20, true},
		{"fails the ancestor after anonymous release", "/target.slice", 61, 100, 20, false},
		{"fails the host after anonymous release", "/target.slice", 60, 40, 20, false},
		{"capacity is in another slice", "/other.slice", 60, 100, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			const ancestor = "/target.slice"
			peer := tc.slice + "/owned-command-1.scope/command"
			for path, contents := range map[string]string{
				"/proc/meminfo":              "MemAvailable: " + strconv.Itoa(tc.hostPages*4) + " kB\n",
				"/proc/zoneinfo":             "Node 0, zone Normal\n        low 0\n",
				ancestor + "/memory.max":     strconv.Itoa(100 * 4096),
				ancestor + "/memory.high":    "max\n",
				ancestor + "/memory.current": strconv.Itoa(60 * 4096),
				ancestor + "/memory.stat":    memoryFixtureStat(20, tc.ancestorFilePages),
				peer + "/memory.current":     strconv.Itoa(40 * 4096),
				peer + "/memory.stat":        memoryFixtureStat(20, 20),
			} {
				writeMemorySample(t, root, path, contents)
			}
			view := memoryHostView{Source: os.DirFS(root)}
			topology := memoryTopology{
				Claims:    []memoryClaim{{Path: peer, Cap: 60 * 4096}},
				Ancestors: []string{ancestor},
			}
			var refusal *memoryAdmissionRefusal
			require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 10*4096, tc.request*4096), &refusal)
			if tc.eligible {
				require.Equal(t, topology.Claims, refusal.Peers)
				return
			}
			require.Empty(t, refusal.Peers)
		})
	}
}

func TestMemoryQueueArmsEveryIndividuallySufficientClaim(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	large := "/large.scope/command"
	small := "/small.scope/command"
	for _, path := range []string{large, small} {
		writeMemorySample(t, root, path+"/memory.current", "0\n")
		writeMemorySample(t, root, path+"/memory.stat", memoryFixtureStat(0, 0))
	}
	writeMemorySample(t, root, "/proc/meminfo", "MemAvailable: 520 kB\n")
	writeMemorySample(t, root, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	view := memoryHostView{Source: os.DirFS(root)}
	topology := memoryTopology{Claims: []memoryClaim{
		{Path: large, Cap: 100 * 4096},
		{Path: small, Cap: 60 * 4096},
	}}
	var refusal *memoryAdmissionRefusal
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 0, 30*4096), &refusal)
	require.ElementsMatch(t, topology.Claims, refusal.Peers)
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 0, 31*4096), &refusal)
	require.Equal(t, topology.Claims[:1], refusal.Peers)
}

func TestMemoryQueueDoesNotWaitForRemovedClaim(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "command")
	claims := []memoryClaim{{Path: path}}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.NoError(t, waitForMemoryClaims(ctx, claims))
	cancel()
	require.ErrorIs(t, waitForMemoryClaims(ctx, claims), context.Canceled)
}

func TestMemoryQueueWakesOnLaterClaimRemoval(t *testing.T) {
	t.Parallel()
	first := filepath.Join(t.TempDir(), "command")
	later := filepath.Join(t.TempDir(), "command")
	require.NoError(t, os.Mkdir(first, 0o700))
	require.NoError(t, os.Mkdir(later, 0o700))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	watch, gone, err := armMemoryClaimWatch(ctx, []memoryClaim{{Path: first}, {Path: later}})
	require.NoError(t, err)
	defer watch.Close()
	require.False(t, gone)
	require.DirExists(t, later)
	require.NoError(t, os.Remove(later))
	require.NoError(t, watch.Wait(ctx))
	require.DirExists(t, first)
}

func TestMemoryQueueAnonymousRelease(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const peer = "/peer.scope/command"
	const page = 4096
	writeMemorySample(t, root, "/proc/meminfo", "MemAvailable: 100 kB\n")
	writeMemorySample(t, root, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, root, peer+"/memory.current", strconv.Itoa(40*page))
	view := memoryHostView{Source: os.DirFS(root)}
	topology := memoryTopology{Claims: []memoryClaim{{Path: peer, Cap: 40 * page}}}
	for _, tc := range []struct {
		anon, lru int
		eligible  bool
	}{
		{10, 35, false}, // Shmem on the LRU can outlive the claim.
		{35, 10, true},  // anon, rather than the LRU, supplies the missing release.
	} {
		writeMemorySample(t, root, peer+"/memory.stat", memoryFixtureStatWithAnon(tc.anon, tc.lru, 0))
		var refusal *memoryAdmissionRefusal
		require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 0, 50*page), &refusal)
		if tc.eligible {
			require.Equal(t, topology.Claims, refusal.Peers)
		} else {
			require.Empty(t, refusal.Peers)
		}
	}
}

func TestMemoryQueueNestedRelease(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const outer = "/outer.scope/command"
	const inner = outer + "/inner.scope/command"
	const page = 4096
	writeMemorySample(t, root, "/proc/meminfo", "MemAvailable: 220 kB\n")
	writeMemorySample(t, root, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	for _, path := range []string{outer, inner} {
		writeMemorySample(t, root, path+"/memory.current", strconv.Itoa(10*page))
		writeMemorySample(t, root, path+"/memory.stat", memoryFixtureStat(10, 0))
	}
	view := memoryHostView{Source: os.DirFS(root)}
	topology := memoryTopology{Claims: []memoryClaim{{Path: outer, Cap: 40 * page}, {Path: inner, Cap: 20 * page}}}
	var refusal *memoryAdmissionRefusal
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 0, 25*page), &refusal)
	require.ElementsMatch(t, topology.Claims, refusal.Peers)
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 0, 30*page), &refusal)
	require.Equal(t, topology.Claims[:1], refusal.Peers)
}

func TestMemoryQueueErrorClassification(t *testing.T) {
	t.Parallel()
	refusal := &memoryAdmissionRefusal{Bound: hostMemoryBound{}, Requested: 1}
	require.ErrorIs(t, memoryClaimRollbackError("command", refusal, nil), refusal)
	broken := memoryClaimRollbackError("command", refusal, os.ErrPermission)
	require.ErrorIs(t, broken, os.ErrPermission)
	require.NotErrorIs(t, broken, errMemoryCapacity)
	expired := memoryClaimWaitError(refusal, context.DeadlineExceeded)
	require.ErrorIs(t, expired, refusal)
	require.ErrorIs(t, expired, context.DeadlineExceeded)
	for _, err := range []error{context.Canceled, os.ErrPermission} {
		failed := memoryClaimWaitError(refusal, err)
		require.ErrorIs(t, failed, err)
		require.NotErrorIs(t, failed, errMemoryCapacity)
	}
}
