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
	"golang.org/x/sys/unix"
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
	bound := hostMemoryBound{Available: 200 * page, Floor: 20 * page, Held: 60 * page}
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
	bound := cgroupMemoryBound{Path: "/user.slice", Limit: 100, Current: 130, Credit: 50, Held: 10}
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
	require.Equal(t, MemoryBytes(128), stat.reclaimCredit(memoryCacheSample{File: 30, Slab: 4}, 20))
	require.Zero(t, stat.reclaimCredit(memoryCacheSample{File: 201, Slab: 21}, 20))
}

// TestMemoryResidentSample checks the anon LRU credit is capped by current and the claim limit.
//
// Example: 120 resident bytes against a 50-byte cap credit at most 50.
func TestMemoryResidentSample(t *testing.T) {
	t.Parallel()
	stat := memoryAccountingStat{AnonLRU: 120}
	require.Equal(t, MemoryBytes(50), stat.residentAnon(90, 50))
	require.Equal(t, MemoryBytes(70), stat.residentAnon(70, 100))
	require.Zero(t, stat.residentAnon(0, 100))
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
	input := "active_anon 10\ninactive_anon 20\nfile 600\nactive_file 7\ninactive_file 13\nslab_reclaimable 9\nfile_dirty 3\nfile_writeback 4\n"
	stat, err := parseMemoryAccountingStat(input)
	require.NoError(t, err)
	require.Equal(t, memoryAccountingStat{AnonLRU: 30, File: 20, Slab: 9, Dirty: 3, Writeback: 4}, stat)

	for _, malformed := range []string{input + "active_anon 0\n", input + "slab_reclaimable bad\n", "active_anon 30\n"} {
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
		Protected: map[memoryBoundID][]string{
			hostMemoryBoundID:           {parent},
			cgroupMemoryBoundID(root):   {parent},
			cgroupMemoryBoundID(parent): {protected},
		},
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

	source := memoryHostView{Source: os.DirFS(tree)}
	ctx := context.Background()
	var refusal *memoryAdmissionRefusal
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 100*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 101*4096), &refusal)
	require.IsType(t, hostMemoryBound{}, refusal.Bound)

	writeMemorySample(t, tree, parent+"/memory.max", strconv.Itoa(120*4096))
	writeMemorySample(t, tree, parent+"/memory.high", strconv.Itoa(110*4096))
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 50*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 51*4096), &refusal)
	bound, ok := refusal.Bound.(cgroupMemoryBound)
	require.True(t, ok)
	require.Equal(t, parent, bound.Path)
	require.Equal(t, MemoryBytes(40*4096), bound.Credit)

	topology.Protected[cgroupMemoryBoundID(parent)] = []string{inside}
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 70*4096))

	writeMemorySample(t, tree, root+"/memory.max", strconv.Itoa(120*4096))
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 20*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 21*4096), &refusal)
	bound, ok = refusal.Bound.(cgroupMemoryBound)
	require.True(t, ok)
	require.Equal(t, root, bound.Path)
	require.Zero(t, bound.Credit)
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

	ctx, source := context.Background(), memoryHostView{Source: os.DirFS(tree)}
	var refusal *memoryAdmissionRefusal
	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 800 kB\n")
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 70*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 71*4096), &refusal)
	bound, ok := refusal.Bound.(cgroupMemoryBound)
	require.True(t, ok)
	require.Equal(t, parent, bound.Path)
	require.Equal(t, MemoryBytes(10*4096), bound.Held)

	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 640 kB\n")
	require.NoError(t, checkCommandMemory(ctx, source, topology, 0, 55*4096))
	require.ErrorAs(t, checkCommandMemory(ctx, source, topology, 0, 56*4096), &refusal)
	host, ok := refusal.Bound.(hostMemoryBound)
	require.True(t, ok)
	require.Equal(t, MemoryBytes(105*4096), host.Held)
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
		Protected: map[memoryBoundID][]string{cgroupMemoryBoundID(parent): {protected}},
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
		view := memoryHostView{Source: &source}
		require.NoError(t, checkCommandMemory(ctx, view, topology, 0, 10*4096), tc.name)
		source.Pass = 0
		var refusal *memoryAdmissionRefusal
		require.ErrorAs(t, checkCommandMemory(ctx, view, topology, 0, 11*4096), &refusal, tc.name)
		bound, ok := refusal.Bound.(cgroupMemoryBound)
		require.True(t, ok, tc.name)
		require.Equal(t, parent, bound.Path, tc.name)
		require.Zero(t, bound.Credit, tc.name)
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

	for i, tc := range []struct {
		first, second int
		want          MemoryBytes
		gone          [2]string
	}{
		{20, 40, 40, [2]string{}},
		{40, 20, 40, [2]string{}},
		{40, -1, 60, [2]string{}},
		{-1, 40, 60, [2]string{}},
		{40, 40, 60, [2]string{"memory.current", ""}},
		{40, 40, 60, [2]string{"memory.stat", ""}},
		{40, 40, 60, [2]string{"", "memory.current"}},
		{40, 40, 60, [2]string{"", "memory.stat"}},
	} {
		t.Run(strconv.Itoa(i),
			// Check each scripted observation independently, including both possible failure sites.
			//
			// Example: a claim's second memory.stat read can disappear after memory.current succeeded.
			func(t *testing.T) {
				headroom := 120
				if min(tc.first, tc.second) < 0 || tc.gone != [2]string{} {
					headroom = 160
				}
				writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: "+strconv.Itoa(headroom*4)+" kB\n")
				writeMemorySample(t, tree, parent+"/memory.current", strconv.Itoa((200-headroom)*4096))
				source := memorySampleTestFS{FS: os.DirFS(tree), Claim: claim, Before: tc.first, After: tc.second, Gone: tc.gone}
				view := memoryHostView{Source: &source}
				require.NoError(t, checkCommandMemory(ctx, view, topology, 0, tc.want*4096))
				source.Pass = 0
				var refusal *memoryAdmissionRefusal
				require.ErrorAs(t, checkCommandMemory(ctx, view, topology, 0, (tc.want+1)*4096), &refusal)
				require.IsType(t, hostMemoryBound{}, refusal.Bound)
				source.Pass = 0
				// Only the ancestor binds after adding one page to the host's aggregate.
				writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: "+strconv.Itoa((headroom+1)*4)+" kB\n")
				require.ErrorAs(t, checkCommandMemory(ctx, view, topology, 0, (tc.want+1)*4096), &refusal)
				bound, ok := refusal.Bound.(cgroupMemoryBound)
				require.True(t, ok)
				require.Equal(t, parent, bound.Path)
				require.Zero(t, bound.Credit)
			})
	}
}

// TestMemoryAnonLRUAdmission checks host capacity against resident swap-backed pages.
//
// Example: old cumulative lazyfree events do not cancel out present anonymous-LRU credit.
func TestMemoryAnonLRUAdmission(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	claim := "/owned-command-1.scope/command"
	topology := memoryTopology{Claims: []memoryClaim{{Path: claim, Cap: 120 * 4096}}}
	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 800 kB\n")
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, claim+"/memory.current", strconv.Itoa(110*4096))
	view := memoryHostView{Source: os.DirFS(tree)}
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		legacy string
		lru    int
	}{
		{"cumulative_lazyfree", "anon 409600\npglazyfree 1000\n", 80},
		{"lazyfree_on_file_lru", "anon 409600\npglazyfree 0\n", 60},
		{"shmem_on_anon_lru", "anon 163840\npglazyfree 0\nshmem 204800\n", 90},
	} {
		t.Run(tc.name,
			// Measure the host fit against the current LRU rather than the legacy counters.
			//
			// Example: shmem grows the anon LRU without growing the anon counter.
			func(t *testing.T) {
				writeMemorySample(t, tree, claim+"/memory.stat", tc.legacy+memoryFixtureStat(tc.lru, 0))
				fit := MemoryBytes(80+tc.lru) * 4096
				require.NoError(t, checkCommandMemory(ctx, view, topology, 0, fit))
				var refusal *memoryAdmissionRefusal
				require.ErrorAs(t, checkCommandMemory(ctx, view, topology, 0, fit+4096), &refusal)
				host, ok := refusal.Bound.(hostMemoryBound)
				require.True(t, ok)
				require.Equal(t, MemoryBytes(120-tc.lru)*4096, host.Held)
			})
	}
}

// TestMemoryCurrentRejectsUnlimited checks that memory.current cannot be an unlimited limit.
//
// Example: memory.max may be "max", but current usage must always be a byte count.
func TestMemoryCurrentRejectsUnlimited(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	claim := "/owned-command-1.scope/command"
	topology := memoryTopology{Claims: []memoryClaim{{Path: claim, Cap: 8192}}}
	writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 16 kB\n")
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, claim+"/memory.stat", memoryFixtureStat(1, 0))
	view := memoryHostView{Source: os.DirFS(tree)}
	ctx := context.Background()
	writeMemorySample(t, tree, claim+"/memory.current", "4096\n")
	require.NoError(t, checkCommandMemory(ctx, view, topology, 0, 4096))
	writeMemorySample(t, tree, claim+"/memory.current", "max\n")
	require.ErrorContains(t, checkCommandMemory(ctx, view, topology, 0, 4096), "memory.current")
}

// memoryReadFaultFS injects one kernel-file read error without changing other fixture files.
//
// Example: an offline protected cgroup can return ENODEV from memory.stat.
type memoryReadFaultFS struct {
	fs.FS
	Path string
	Err  error
}

// ReadFile returns the configured kernel error for one path.
//
// Example: other paths still read from the fixture directory.
func (s memoryReadFaultFS) ReadFile(path string) ([]byte, error) {
	if path == strings.TrimPrefix(s.Path, "/") {
		return nil, s.Err
	}
	return fs.ReadFile(s.FS, path)
}

// TestMemoryGoneProtection checks only a disappearing protection root can be skipped.
//
// Example: a disappearing candidate ancestor still causes a guard error.
func TestMemoryGoneProtection(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	group := "/parent.slice"
	protected := group + "/exiting"
	topology := memoryTopology{
		Ancestors: []string{group},
		Protected: map[memoryBoundID][]string{hostMemoryBoundID: {protected}, cgroupMemoryBoundID(group): {protected}},
	}
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, group+"/memory.max", strconv.Itoa(100*4096))
	writeMemorySample(t, tree, group+"/memory.high", "max\n")
	writeMemorySample(t, tree, group+"/memory.current", strconv.Itoa(30*4096))
	writeMemorySample(t, tree, group+"/memory.stat", memoryFixtureStat(0, 20))
	source := os.DirFS(tree)
	ctx := context.Background()
	for _, gone := range []error{fs.ErrNotExist, unix.ENODEV} {
		view := memoryHostView{Source: memoryReadFaultFS{FS: source, Path: protected + "/memory.stat", Err: gone}}
		writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 360 kB\n")
		require.NoError(t, checkCommandMemory(ctx, view, topology, 0, 90*4096))
		var hostRefusal *memoryAdmissionRefusal
		require.ErrorAs(t, checkCommandMemory(ctx, view, topology, 0, 91*4096), &hostRefusal)
		require.IsType(t, hostMemoryBound{}, hostRefusal.Bound)
		writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 400 kB\n")
		require.NoError(t, checkCommandMemory(ctx, view, topology, 0, 90*4096))
		var refusal *memoryAdmissionRefusal
		require.ErrorAs(t, checkCommandMemory(ctx, view, topology, 0, 91*4096), &refusal)
		require.IsType(t, cgroupMemoryBound{}, refusal.Bound)

		view.Source = memoryReadFaultFS{FS: source, Path: group + "/memory.stat", Err: gone}
		require.ErrorIs(t, checkCommandMemory(ctx, view, topology, 0, 4096), gone)
	}
}

// TestMemoryCoincidentAndNestedClaims checks selected cache ownership and held-growth boundaries.
//
// Example: nested claims both hold growth while the outer claim excludes their shared cache once.
func TestMemoryCoincidentAndNestedClaims(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	group := "/parent.slice"
	outer := group + "/owned-command-1.scope/command"
	inner := outer + "/owned-command-2.scope/command"
	writeMemorySample(t, tree, "/proc/zoneinfo", "Node 0, zone Normal\n        low 0\n")
	writeMemorySample(t, tree, group+"/memory.max", strconv.Itoa(160*4096))
	writeMemorySample(t, tree, group+"/memory.high", "max\n")
	writeMemorySample(t, tree, group+"/memory.current", strconv.Itoa(100*4096))
	writeMemorySample(t, tree, group+"/memory.stat", memoryFixtureStat(0, 55))
	writeMemorySample(t, tree, outer+"/memory.current", strconv.Itoa(70*4096))
	writeMemorySample(t, tree, outer+"/memory.stat", memoryFixtureStat(20, 40))
	writeMemorySample(t, tree, inner+"/memory.current", strconv.Itoa(30*4096))
	writeMemorySample(t, tree, inner+"/memory.stat", memoryFixtureStat(10, 20))
	for _, nested := range []bool{false, true} {
		topology := memoryTopology{
			Claims:    []memoryClaim{{Path: outer, Cap: 80 * 4096}},
			Ancestors: []string{group},
			Protected: map[memoryBoundID][]string{hostMemoryBoundID: {outer}, cgroupMemoryBoundID(group): {outer}},
		}
		hostHeld, ancestorFit := 60, 50
		var source fs.FS = os.DirFS(tree)
		if nested {
			topology.Claims = append(topology.Claims, memoryClaim{Path: inner, Cap: 35 * 4096})
			hostHeld, ancestorFit = 85, 60
		} else {
			// The shared path reports 55 cache pages during the aggregate read,
			// more than the claim's bracketing observations.
			view := &memorySampleTestFS{
				FS: source, Claim: outer, Before: 70, After: 70,
				Stat:   [2]string{memoryFixtureStat(20, 40), memoryFixtureStat(20, 40)},
				Middle: memoryFixtureStat(20, 55),
			}
			source = view
		}
		ctx := context.Background()
		// Reset the scripted peer observations before each capacity check.
		//
		// Example: fit and fit+1 exercise the same sampled cgroup state.
		check := func(requested int) error {
			if scripted, ok := source.(*memorySampleTestFS); ok {
				scripted.Pass = 0
				scripted.middleRead = false
			}
			return checkCommandMemory(ctx, memoryHostView{Source: source}, topology, 0, MemoryBytes(requested)*4096)
		}
		writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: "+strconv.Itoa((hostHeld+40)*4)+" kB\n")
		require.NoError(t, check(40))
		var refusal *memoryAdmissionRefusal
		require.ErrorAs(t, check(41), &refusal)
		require.IsType(t, hostMemoryBound{}, refusal.Bound)
		writeMemorySample(t, tree, "/proc/meminfo", "MemAvailable: 800 kB\n")
		require.NoError(t, check(ancestorFit))
		require.ErrorAs(t, check(ancestorFit+1), &refusal)
		require.IsType(t, cgroupMemoryBound{}, refusal.Bound)
	}
}

// memorySampleTestFS varies only a peer's reads, leaving aggregates on a real temporary filesystem.
//
// Example: Before=40, After=-1 models a peer removed between passes.
type memorySampleTestFS struct {
	fs.FS
	Claim      string
	Before     int
	After      int
	Pass       int
	Stat       [2]string
	Gone       [2]string
	Middle     string
	middleRead bool
}

// ReadFile gives each peer pass its own effective current and stat.
//
// Example: a negative resident sample returns ENOENT instead of a stat record.
func (s *memorySampleTestFS) ReadFile(path string) ([]byte, error) {
	if s.Pass == 1 && s.Middle != "" && !s.middleRead && "/"+path == s.Claim+"/memory.stat" {
		s.middleRead = true
		return []byte(s.Middle), nil
	}
	if !strings.HasPrefix("/"+path, s.Claim+"/") {
		if s.Pass != 1 {
			return nil, fs.ErrInvalid
		}
		return fs.ReadFile(s.FS, path)
	}
	if filepath.Base(path) == s.Gone[s.Pass] {
		s.Pass++
		return nil, unix.ENODEV
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
	return "active_anon " + strconv.Itoa(anon*4096) + "\ninactive_anon 0\nactive_file " + strconv.Itoa(file*4096) + "\ninactive_file 0\nslab_reclaimable 0\nfile_dirty 0\nfile_writeback 0\n"
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
