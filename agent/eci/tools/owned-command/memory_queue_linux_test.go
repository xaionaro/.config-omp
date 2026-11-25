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
		name, slice, population string
		request                 MemoryBytes
		hostPages               int
		ancestorFilePages       int
		queue, eligible         bool
	}{
		{"sufficient across both bounds", "/target.slice", "1", 60, 100, 20, true, true},
		{"fails the ancestor after anonymous release", "/target.slice", "1", 61, 100, 20, true, false},
		{"fails the host after anonymous release", "/target.slice", "1", 60, 40, 20, true, false},
		{"capacity is in another slice", "/other.slice", "1", 60, 100, 0, true, false},
		{"claim is not populated", "/target.slice", "0", 60, 100, 20, true, false},
		{"wait is not enabled", "/target.slice", "1", 60, 100, 20, false, false},
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
				peer + "/cgroup.events":      "populated " + tc.population + "\n",
			} {
				writeMemorySample(t, root, path, contents)
			}
			view := memoryHostView{Source: os.DirFS(root)}
			topology := memoryTopology{
				Claims:    []memoryClaim{{Path: peer, Cap: 60 * 4096}},
				Ancestors: []string{ancestor},
			}
			var refusal *memoryAdmissionRefusal
			require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 10*4096, tc.request*4096, tc.queue), &refusal)
			if tc.eligible {
				require.Equal(t, []string{peer}, refusal.Peers)
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
		writeMemorySample(t, root, path+"/cgroup.events", "populated 1\n")
	}
	writeMemorySample(t, root, "/proc/meminfo", "MemAvailable: 520 kB\n")
	writeMemorySample(t, root, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	view := memoryHostView{Source: os.DirFS(root)}
	topology := memoryTopology{Claims: []memoryClaim{
		{Path: large, Cap: 100 * 4096},
		{Path: small, Cap: 60 * 4096},
	}}
	var refusal *memoryAdmissionRefusal
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 0, 30*4096, true), &refusal)
	require.ElementsMatch(t, []string{large, small}, refusal.Peers)
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, 0, 31*4096, true), &refusal)
	require.Equal(t, []string{large}, refusal.Peers)
}

func TestMemoryQueueDoesNotWaitForRemovedClaim(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "command")
	require.NoError(t, waitForMemoryClaims(t.Context(), []string{path}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, waitForMemoryClaims(ctx, []string{path}), context.Canceled)
}

func TestMemoryQueueWakesOnLaterClaimRemoval(t *testing.T) {
	t.Parallel()
	first := filepath.Join(t.TempDir(), "command")
	later := filepath.Join(t.TempDir(), "command")
	require.NoError(t, os.Mkdir(first, 0o700))
	require.NoError(t, os.Mkdir(later, 0o700))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitForMemoryClaims(ctx, []string{first, later}) }()
	require.NoError(t, os.Remove(later))
	require.NoError(t, <-done)
	require.DirExists(t, first)
}
