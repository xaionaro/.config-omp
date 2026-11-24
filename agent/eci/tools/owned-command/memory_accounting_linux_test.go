package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHostMemoryBound checks that the floor and unfilled claims reduce host capacity.
//
// Example: a claim holding 40 of 100 bytes leaves 60 more to reserve.
func TestHostMemoryBound(t *testing.T) {
	t.Parallel()
	const (
		// page is the fixture's unit of usable memory.
		//
		// Example: requesting one extra page crosses the capacity boundary.
		page MemoryBytes = 4096
	)
	bound := memoryBound{Available: 200 * page, Floor: 20 * page, Held: 60 * page}
	require.NoError(t, bound.admit(120*page))
	var refusal *memoryAdmissionRefusal
	require.ErrorAs(t, bound.admit(121*page), &refusal)
	require.Equal(t, 121*page, refusal.Requested)
	require.Equal(t, bound, refusal.Bound)

	bound.Floor = 201 * page
	require.ErrorAs(t, bound.admit(page), &refusal)
}

// TestAncestorMemoryBound checks reclaim pays down above-limit charges first.
//
// Example: 50 bytes of claimable cache with current 30 over limit leaves 20.
func TestAncestorMemoryBound(t *testing.T) {
	t.Parallel()
	bound := memoryBound{Path: "/user.slice", Limit: 100, Current: 130, Credit: 50, Held: 10}
	require.NoError(t, bound.admit(10))
	var refusal *memoryAdmissionRefusal
	require.ErrorAs(t, bound.admit(11), &refusal)
	require.Equal(t, bound, refusal.Bound)

	bound.Credit = 29
	require.ErrorAs(t, bound.admit(1), &refusal)
}

// TestMemoryReclaimCredit checks discounting and the exclusions independently.
//
// Example: dirty pages do not count toward claimable file cache.
func TestMemoryReclaimCredit(t *testing.T) {
	t.Parallel()
	stat := memoryAccountingStat{File: 200, Slab: 20, Dirty: 10, Writeback: 20}
	// File: (200 − 30 − 10 − 20) − min(70, 20); slab: (20 − 4) − min(8, 20).
	require.Equal(t, MemoryBytes(128), stat.reclaimCredit(memoryAccountingStat{File: 30, Slab: 4}, 20))
	require.Zero(t, stat.reclaimCredit(memoryAccountingStat{File: 201, Slab: 21}, 20))
}

// TestMemoryResidentSample checks an anonymous sample cannot credit lazyfree or exceed the cap.
//
// Example: two pages marked lazy-free reduce anon credit by two pages.
func TestMemoryResidentSample(t *testing.T) {
	t.Parallel()
	stat := memoryAccountingStat{Anon: 120, LazyFree: 2}
	require.Equal(t, MemoryBytes(50), stat.residentAnon(90, 50, 20))
	require.Equal(t, MemoryBytes(70), stat.residentAnon(70, 100, 20))
	require.Equal(t, MemoryBytes(80), stat.residentAnon(100, 100, 20))
	stat.LazyFree = ^MemoryBytes(0)
	require.Zero(t, stat.residentAnon(100, 100, 20))
}

// TestMemoryZoneLow checks summing every zone's low watermark in bytes.
//
// Example: a watermark of seven 4096-byte pages contributes 28672 bytes.
func TestMemoryZoneLow(t *testing.T) {
	t.Parallel()
	value, err := parseMemoryZoneLow("Node 0, zone DMA\n  pages free 1\n        low 7\n        lowmem_reserve[]: 4\nNode 1, zone Normal\n        low 9\n", 4096)
	require.NoError(t, err)
	require.Equal(t, MemoryBytes(65536), value)

	for _, zoneinfo := range []string{"Node 0, zone DMA\n", "        low foo\n", "        low 18446744073709551615\n", "        low 1\n        low \n"} {
		_, err := parseMemoryZoneLow(zoneinfo, 4096)
		require.Error(t, err)
	}
}

// TestMemoryAccountingStat checks hierarchical memory.stat counters and rejects malformed required fields.
//
// Example: unknown valid kernel counters do not contribute to file LRU credit.
func TestMemoryAccountingStat(t *testing.T) {
	t.Parallel()
	input := "anon 30\npglazyfree 2\nfile 600\nactive_file 7\ninactive_file 13\nslab_reclaimable 9\nfile_dirty 3\nfile_writeback 4\n"
	stat, err := parseMemoryAccountingStat(input)
	require.NoError(t, err)
	require.Equal(t, memoryAccountingStat{Anon: 30, LazyFree: 2, File: 20, Slab: 9, Dirty: 3, Writeback: 4}, stat)

	for _, malformed := range []string{input + "anon 0\n", input + "slab_reclaimable bad\n", "anon 30\n"} {
		_, err := parseMemoryAccountingStat(malformed)
		require.Error(t, err)
	}
}

// TestSampledMemoryBounds checks host protection, independent bounds, and overlapping exclusions.
//
// Example: a protected child's claim excludes cache once and holds only its remaining growth.
func TestSampledMemoryBounds(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	root := "/root.slice"
	parent := root + "/parent.slice"
	protected := parent + "/protected.scope"
	claim := protected + "/owned-command-1.scope/command"
	inside := claim + "/low"
	topology := memoryTopology{
		Claims:    []memoryClaim{{Path: claim, Cap: 60 * 4096}},
		Ancestors: []string{parent, root},
		Protected: map[string][]string{"": {parent}, root: {parent}, parent: {protected}},
	}
	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 800 kB\n")
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, claim+"/memory.current", strconv.Itoa(50*4096))
	writeMemorySample(t, tree, claim+"/memory.stat", memoryFixtureStat(20, 30))
	writeMemorySample(t, tree, protected+"/memory.stat", memoryFixtureStat(0, 50))
	writeMemorySample(t, tree, inside+"/memory.stat", memoryFixtureStat(0, 15))
	for _, path := range []string{root, parent} {
		writeMemorySample(t, tree, path+"/memory.max", "max\n")
		writeMemorySample(t, tree, path+"/memory.high", "max\n")
		writeMemorySample(t, tree, path+"/memory.current", strconv.Itoa(90*4096))
		writeMemorySample(t, tree, path+"/memory.stat", memoryFixtureStat(0, 90))
	}

	source := os.DirFS(tree)
	ctx := context.Background()
	var refusal *memoryAdmissionRefusal
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 100*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 101*4096), &refusal)
	require.Empty(t, refusal.Bound.Path)

	writeMemorySample(t, tree, parent+"/memory.max", strconv.Itoa(120*4096))
	writeMemorySample(t, tree, parent+"/memory.high", strconv.Itoa(110*4096))
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 50*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 51*4096), &refusal)
	require.Equal(t, parent, refusal.Bound.Path)
	require.Equal(t, MemoryBytes(40*4096), refusal.Bound.Credit)

	topology.Protected[parent] = []string{inside}
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 70*4096))

	writeMemorySample(t, tree, root+"/memory.max", strconv.Itoa(120*4096))
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 20*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 21*4096), &refusal)
	require.Equal(t, root, refusal.Bound.Path)
	require.Zero(t, refusal.Bound.Credit)
}

// TestDisjointMemoryClaim checks a peer counts at the host but not an unrelated finite ancestor.
//
// Example: similar directory names are not cgroup ancestry.
func TestDisjointMemoryClaim(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	parent := "/parent.slice"
	claim := parent + "/owned-command-1.scope/command"
	disjoint := parent + ".other/owned-command-2.scope/command"
	topology := memoryTopology{
		Claims: []memoryClaim{
			{Path: claim, Cap: 10 * 4096},
			{Path: disjoint, Cap: 95 * 4096},
		},
		Ancestors: []string{parent},
	}
	for _, path := range []string{claim, disjoint} {
		writeMemorySample(t, tree, path+"/memory.current", "0\n")
		writeMemorySample(t, tree, path+"/memory.stat", memoryFixtureStat(0, 0))
	}
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, parent+"/memory.max", strconv.Itoa(100*4096))
	writeMemorySample(t, tree, parent+"/memory.high", "max\n")
	writeMemorySample(t, tree, parent+"/memory.current", strconv.Itoa(20*4096))
	writeMemorySample(t, tree, parent+"/memory.stat", memoryFixtureStat(0, 0))

	ctx, source := context.Background(), os.DirFS(tree)
	var refusal *memoryAdmissionRefusal
	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 800 kB\n")
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 70*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 71*4096), &refusal)
	require.Equal(t, parent, refusal.Bound.Path)
	require.Equal(t, MemoryBytes(10*4096), refusal.Bound.Held)

	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 640 kB\n")
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 55*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 56*4096), &refusal)
	require.Empty(t, refusal.Bound.Path)
	require.Equal(t, MemoryBytes(105*4096), refusal.Bound.Held)
}

// TestProtectedMemoryClaimTurnover checks that a later protected root sample cannot hide earlier peer cache.
//
// Example: after a peer replaces file cache with anonymous memory, its earlier cache still cannot fund admission.
func TestProtectedMemoryClaimTurnover(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	parent := "/parent.slice"
	protected := parent + "/protected.slice"
	claim := protected + "/owned-command-1.scope/command"
	topology := memoryTopology{
		Claims:    []memoryClaim{{Path: claim, Cap: 100 * 4096}},
		Ancestors: []string{parent},
		Protected: map[string][]string{parent: {protected}},
	}
	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 800 kB\n")
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, parent+"/memory.max", strconv.Itoa(200*4096))
	writeMemorySample(t, tree, parent+"/memory.high", "max\n")
	writeMemorySample(t, tree, parent+"/memory.current", strconv.Itoa(170*4096))
	writeMemorySample(t, tree, parent+"/memory.stat", memoryFixtureStat(0, 110))

	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		protectedFile int
		after         string
	}{
		{"turnover", 30, memoryFixtureStat(80, 0)},
		{"stable", 110, memoryFixtureStat(0, 80)},
	} {
		writeMemorySample(t, tree, protected+"/memory.stat", memoryFixtureStat(0, tc.protectedFile))
		source := memorySampleTestFS{
			FS: os.DirFS(tree), Claim: claim, Before: 80, After: 80,
			Stat: [2]string{memoryFixtureStat(0, 80), tc.after},
		}
		require.NoError(t, checkCommandMemory(ctx, &source, topology, 0, 10*4096), tc.name)
		source.Pass = 0
		var refusal *memoryAdmissionRefusal
		require.ErrorAs(t, checkCommandMemory(ctx, &source, topology, 0, 11*4096), &refusal, tc.name)
		require.Equal(t, parent, refusal.Bound.Path, tc.name)
		require.Zero(t, refusal.Bound.Credit, tc.name)
	}
}

// TestMemoryClaimBracket checks a growing, shrinking, or disappearing peer against sampled aggregate headroom.
//
// Example: when the peer vanishes in the second pass, the full cap still holds capacity.
func TestMemoryClaimBracket(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	parent := "/parent.slice"
	claim := parent + "/owned-command-1.scope/command"
	topology := memoryTopology{
		Claims:    []memoryClaim{{Path: claim, Cap: 100 * 4096}},
		Ancestors: []string{parent},
	}
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, parent+"/memory.max", "819200\n")
	writeMemorySample(t, tree, parent+"/memory.high", "max\n")
	writeMemorySample(t, tree, parent+"/memory.stat", memoryFixtureStat(0, 40))
	ctx := context.Background()
	var refusal *memoryAdmissionRefusal

	for _, tc := range []struct {
		first, second int
		want          MemoryBytes
	}{
		{20, 40, 40},
		{40, 20, 40},
		{40, -1, 60},
		{-1, 40, 60},
	} {
		// In the missing cases, aggregate headroom includes released residency.
		headroom := 120
		if min(tc.first, tc.second) < 0 {
			headroom = 160
		}
		writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: "+strconv.Itoa(headroom*4)+" kB\n")
		writeMemorySample(t, tree, parent+"/memory.current", strconv.Itoa((200-headroom)*4096))
		source := memorySampleTestFS{FS: os.DirFS(tree), Claim: claim, Before: tc.first, After: tc.second}
		require.NoError(t, checkCommandMemory(ctx, &source, topology, 0, tc.want*4096))
		source.Pass = 0
		require.ErrorAs(t, checkCommandMemory(ctx, &source, topology, 0, (tc.want+1)*4096), &refusal)
		require.Empty(t, refusal.Bound.Path)
		source.Pass = 0
		// Only the ancestor binds after adding one page to the host's aggregate.
		writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: "+strconv.Itoa((headroom+1)*4)+" kB\n")
		require.ErrorAs(t, checkCommandMemory(ctx, &source, topology, 0, (tc.want+1)*4096), &refusal)
		require.Equal(t, parent, refusal.Bound.Path)
		require.Zero(t, refusal.Bound.Credit)
	}
}

// memorySampleTestFS varies only a peer's reads, leaving aggregates on a real temporary filesystem.
//
// Example: Before=40, After=-1 models a peer removed between passes.
type memorySampleTestFS struct {
	fs.FS
	Claim  string
	Before int
	After  int
	Pass   int
	Stat   [2]string
}

// ReadFile gives each peer pass its own effective current and stat.
//
// Example: a negative resident sample returns ENOENT instead of a stat record.
func (s *memorySampleTestFS) ReadFile(path string) ([]byte, error) {
	if !strings.HasPrefix("/"+path, s.Claim+"/") {
		if s.Pass != 1 {
			return nil, fs.ErrInvalid
		}
		return fs.ReadFile(s.FS, path)
	}
	resident := s.Before
	if s.Pass != 0 {
		resident = s.After
	}
	if resident < 0 {
		s.Pass++
		return nil, fs.ErrNotExist
	}
	switch filepath.Base(path) {
	case "memory.current":
		return []byte(strconv.Itoa(resident * 4096)), nil
	case "memory.stat":
		stat := s.Stat[s.Pass]
		s.Pass++
		if stat != "" {
			return []byte(stat), nil
		}
		return []byte(memoryFixtureStat(resident, resident)), nil
	default:
		return nil, fs.ErrInvalid
	}
}

// memoryFixtureStat formats a stat with simulated anonymous and file-LRU page counts.
//
// Example: file=20 means 80 KiB on the fixture's page size.
func memoryFixtureStat(
	anon int,
	file int,
) string {
	return "anon " + strconv.Itoa(anon*4096) + "\npglazyfree 0\nactive_file " + strconv.Itoa(file*4096) + "\ninactive_file 0\nslab_reclaimable 0\nfile_dirty 0\nfile_writeback 0\n"
}

// writeMemorySample publishes an input at its virtual filesystem path.
//
// Example: /proc/meminfo is written beneath the fixture's directory.
func writeMemorySample(
	t *testing.T,
	tree string,
	path string,
	contents string,
) {
	t.Helper()
	path = filepath.Join(tree, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
}
