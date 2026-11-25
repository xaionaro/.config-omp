package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xaionaro-go/observability"
	"golang.org/x/sys/unix"
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
			require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 10*4096, tc.request*4096), &refusal)
			if tc.eligible {
				require.Equal(t, topology.Claims, refusal.SufficientClaims)
				return
			}
			require.Empty(t, refusal.SufficientClaims)
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
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 0, 30*4096), &refusal)
	require.ElementsMatch(t, topology.Claims, refusal.SufficientClaims)
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 0, 31*4096), &refusal)
	require.Equal(t, topology.Claims[:1], refusal.SufficientClaims)
}

func TestMemoryQueueExcludesOriginClaim(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const enclosing = "/enclosing.scope/command"
	const sibling = "/sibling.scope/command"
	const page = 4096
	topology := memoryTopology{Claims: []memoryClaim{{Path: enclosing, Cap: 60 * page}, {Path: sibling, Cap: 60 * page}}}
	writeMemorySample(t, root, "/proc/meminfo", "MemAvailable: 520 kB\n")
	writeMemorySample(t, root, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	for _, claim := range topology.Claims {
		writeMemorySample(t, root, claim.Path+"/memory.current", "0\n")
		writeMemorySample(t, root, claim.Path+"/memory.stat", memoryFixtureStat(0, 0))
	}
	view := memoryHostView{Source: os.DirFS(root)}
	for _, origin := range []string{enclosing, enclosing + "/worker", enclosing + "-other"} {
		var refusal *memoryAdmissionRefusal
		require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, origin, 0, 70*page), &refusal)
		if origin == enclosing+"-other" {
			require.Equal(t, topology.Claims, refusal.SufficientClaims, origin)
		} else {
			require.Equal(t, topology.Claims[1:], refusal.SufficientClaims, origin)
		}
		require.Equal(t, MemoryBytes(120*page), refusal.Bound.(hostMemoryBound).Held)
	}
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
	watch, err := armMemoryClaimWatch(ctx, []memoryClaim{{Path: first}, {Path: later}})
	require.NoError(t, err)
	require.NotNil(t, watch)
	defer watch.Close()
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
		require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 0, 50*page), &refusal)
		if tc.eligible {
			require.Equal(t, topology.Claims, refusal.SufficientClaims)
		} else {
			require.Empty(t, refusal.SufficientClaims)
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
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 0, 25*page), &refusal)
	require.ElementsMatch(t, topology.Claims, refusal.SufficientClaims)
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 0, 30*page), &refusal)
	require.Equal(t, topology.Claims[:1], refusal.SufficientClaims)
}

func TestMemoryQueueErrorClassification(t *testing.T) {
	t.Parallel()
	refusal := &memoryAdmissionRefusal{Bound: hostMemoryBound{}, Requested: 1}
	require.ErrorIs(t, memoryClaimRollbackError("command", refusal, nil), refusal)
	broken := memoryClaimRollbackError("command", refusal, os.ErrPermission)
	require.ErrorIs(t, broken, os.ErrPermission)
	require.NotErrorIs(t, broken, errMemoryCapacity)
	expired := memoryClaimWaitError(refusal, context.DeadlineExceeded, false)
	require.ErrorIs(t, expired, refusal)
	require.ErrorIs(t, expired, context.DeadlineExceeded)
	interrupted := memoryClaimWaitError(refusal, context.DeadlineExceeded, true)
	require.ErrorIs(t, interrupted, context.DeadlineExceeded)
	require.NotErrorIs(t, interrupted, errMemoryCapacity)
	for _, err := range []error{context.Canceled, os.ErrPermission} {
		failed := memoryClaimWaitError(refusal, err, false)
		require.ErrorIs(t, failed, err)
		require.NotErrorIs(t, failed, errMemoryCapacity)
	}
}

func TestMemoryQueueProtectedDeparture(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const protected = "/protected.slice"
	const peer = protected + "/peer.scope/command"
	const page = 4096
	writeMemorySample(t, root, "/proc/meminfo", "MemAvailable: 400 kB\n")
	writeMemorySample(t, root, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, root, protected+"/memory.stat", memoryFixtureStat(10, 30))
	writeMemorySample(t, root, peer+"/memory.current", strconv.Itoa(30*page))
	writeMemorySample(t, root, peer+"/memory.stat", memoryFixtureStat(10, 20))
	view := memoryHostView{Source: os.DirFS(root)}
	topology := memoryTopology{
		Claims:    []memoryClaim{{Path: peer, Cap: 40 * page}},
		Protected: map[memoryBoundID][]string{hostMemoryBoundID: {protected}},
	}
	var refusal *memoryAdmissionRefusal
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 0, 80*page), &refusal)
	require.Equal(t, topology.Claims, refusal.SufficientClaims)
	require.ErrorAs(t, checkCommandMemory(t.Context(), view, topology, "", 0, 85*page), &refusal)
	require.Empty(t, refusal.SufficientClaims)
}

func TestMemoryQueueIgnoresSiblingAndCancelsArmedWait(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	leaf := filepath.Join(parent, "command")
	sibling := filepath.Join(parent, "sibling")
	require.NoError(t, os.Mkdir(leaf, 0o700))
	require.NoError(t, os.Mkdir(sibling, 0o700))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	watch, err := armMemoryClaimWatch(ctx, []memoryClaim{{Path: leaf}})
	require.NoError(t, err)
	require.NotNil(t, watch)
	defer watch.Close()
	require.NoError(t, os.Remove(sibling))
	pending, err := unix.IoctlGetInt(watch.fd, unix.TIOCINQ)
	require.NoError(t, err)
	require.Positive(t, pending)
	done := make(chan error, 1)
	observability.Go(ctx, func(ctx context.Context) { done <- watch.Wait(ctx) })
	defer func() {
		if done == nil {
			return
		}
		// Release a broken wait in RED before closing its descriptor.
		_ = os.Remove(leaf)
		<-done
	}()
	require.Eventually(t, func() bool {
		left, err := unix.IoctlGetInt(watch.fd, unix.TIOCINQ)
		return err == nil && left == 0
	}, time.Second, time.Millisecond)
	select {
	case err := <-done:
		done = nil
		t.Fatalf("sibling deletion completed the wait: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		done = nil
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release the armed wait")
	}
	require.DirExists(t, leaf)
}
