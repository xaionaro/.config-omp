package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// accountingMeminfo names the host's memory availability source.
	//
	// Example: MemAvailable in this file includes discounted reclaimable host cache.
	accountingMeminfo = "/proc/meminfo"
	// accountingZoneinfo names the source for per-zone reclaim watermarks.
	//
	// Example: low watermarks are read in pages before converting to bytes.
	accountingZoneinfo = "/proc/zoneinfo"
	// accountingMemoryHigh names the memory reclaim threshold control.
	//
	// Example: a lower memory.high is more restrictive than memory.max for admission.
	accountingMemoryHigh = "memory.high"
	// accountingMemoryCurrent names the charged memory counter.
	//
	// Example: a claim's charges reduce how much more it can grow at an ancestor.
	accountingMemoryCurrent = "memory.current"
	// accountingMemoryStat names the hierarchical memory statistics file.
	//
	// Example: active_file and inactive_file track the file LRU used for reclaim estimates.
	accountingMemoryStat = "memory.stat"
)

// memoryCacheSample holds reclaim candidates in two disjoint cache categories.
//
// Example: a subtree's file LRU and reclaimable slab may overlap other subtree samples.
type memoryCacheSample struct {
	File MemoryBytes
	Slab MemoryBytes
}

// add combines cache from disjoint subtrees without unsigned wraparound.
//
// Example: two sibling protected cgroups both exclude cache at the host.
func (s memoryCacheSample) add(other memoryCacheSample) memoryCacheSample {
	return memoryCacheSample{
		File: addMemoryClaims(s.File, other.File),
		Slab: addMemoryClaims(s.Slab, other.Slab),
	}
}

// memoryClaimSample holds one peer's measured occupancy and reclaimable cache.
//
// Example: a vanished peer has a zero sample but retains its published cap.
type memoryClaimSample struct {
	Current MemoryBytes
	Anon    MemoryBytes
	Cache   memoryCacheSample
}

// bracket conservatively merges observations on either side of aggregate sampling.
//
// Example: a shrinking peer only gets resident credit up to its second sample.
func (s memoryClaimSample) bracket(other memoryClaimSample) memoryClaimSample {
	return memoryClaimSample{
		Current: min(s.Current, other.Current),
		Anon:    min(s.Anon, other.Anon),
		Cache: memoryCacheSample{
			File: max(s.Cache.File, other.Cache.File),
			Slab: max(s.Cache.Slab, other.Cache.Slab),
		},
	}
}

// memoryAncestorSample keeps an ancestor's scratch sample until peer bracketing completes.
//
// Example: memory.stat reclaim candidates are discounted after subtracting peer cache.
type memoryAncestorSample struct {
	Bound memoryBound
	Stat  memoryAccountingStat
}

// checkCommandMemory reads a bracketed estimate and admits only when every bound fits.
//
// Example: callers hold the admission lock and pass os.DirFS("/") plus discovered topology.
func checkCommandMemory(
	ctx context.Context,
	source fs.FS,
	topology memoryTopology,
	floor MemoryBytes,
	requested MemoryBytes,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	page := MemoryBytes(os.Getpagesize())
	peers := make([]memoryClaimSample, len(topology.Claims))
	for i, claim := range topology.Claims {
		if err := ctx.Err(); err != nil {
			return err
		}
		first, err := sampleMemoryClaim(source, claim, page)
		if err != nil {
			return err
		}
		peers[i] = first
	}

	host, low, err := sampleHostMemory(source, floor, page)
	if err != nil {
		return err
	}
	ancestors := make([]memoryAncestorSample, 0, len(topology.Ancestors))
	for _, path := range topology.Ancestors {
		if err := ctx.Err(); err != nil {
			return err
		}
		ancestor, err := sampleMemoryAncestor(source, path)
		if err != nil {
			return err
		}
		if ancestor.Bound.Path != "" {
			ancestors = append(ancestors, ancestor)
		}
	}
	protected, err := sampleProtectedMemory(ctx, source, topology.Protected, ancestors)
	if err != nil {
		return err
	}
	for _, path := range topology.Protected[hostMemoryProtectionRoot] {
		sample := protected[path]
		host.Protected = addMemoryClaims(host.Protected, addMemoryClaims(sample.File, sample.Slab))
	}

	for i, claim := range topology.Claims {
		if err := ctx.Err(); err != nil {
			return err
		}
		second, err := sampleMemoryClaim(source, claim, page)
		if err != nil {
			return err
		}
		peers[i] = peers[i].bracket(second)
		host.Held = addMemoryClaims(host.Held, claim.Cap-peers[i].Anon)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := host.admit(requested); err != nil {
		return err
	}
	for _, ancestor := range ancestors {
		bound := ancestorMemoryBound(ancestor, topology, peers, protected, low)
		if err := bound.admit(requested); err != nil {
			return err
		}
	}
	return nil
}

// ancestorMemoryBound takes the maximal union of claim and protected cache subtrees.
//
// Example: a claim under a protected child contributes growth but its cache is excluded only through the child.
func ancestorMemoryBound(
	ancestor memoryAncestorSample,
	topology memoryTopology,
	peers []memoryClaimSample,
	protected map[string]memoryCacheSample,
	low MemoryBytes,
) memoryBound {
	bound := ancestor.Bound
	roots := topology.Protected[bound.Path]
	exclusions := make(map[string]memoryCacheSample, len(roots))
	for i, claim := range topology.Claims {
		if !isStrictMemoryDescendant(claim.Path, bound.Path) {
			continue
		}
		peer := peers[i]
		bound.Held = addMemoryClaims(bound.Held, claim.Cap-min(claim.Cap, peer.Current))
		exclusions[claim.Path] = peer.Cache
	}
	for _, path := range roots {
		claimCache := exclusions[path]
		protectedCache := protected[path]
		exclusions[path] = memoryCacheSample{
			File: max(claimCache.File, protectedCache.File),
			Slab: max(claimCache.Slab, protectedCache.Slab),
		}
	}

	var excluded memoryCacheSample
	for path, cache := range exclusions {
		covered := false
		for parent := filepath.Dir(path); parent != bound.Path; parent = filepath.Dir(parent) {
			if _, covered = exclusions[parent]; covered {
				break
			}
		}
		if !covered {
			excluded = excluded.add(cache)
		}
	}
	bound.Credit = ancestor.Stat.reclaimCredit(memoryAccountingStat{File: excluded.File, Slab: excluded.Slab}, low)
	return bound
}

// sampleProtectedMemory reads each protected cgroup at most once, before the second peer pass.
//
// Example: a child protected for both the host and a finite ancestor is read once.
func sampleProtectedMemory(
	ctx context.Context,
	source fs.FS,
	protected map[string][]string,
	ancestors []memoryAncestorSample,
) (map[string]memoryCacheSample, error) {
	cache := make(map[string]memoryCacheSample)
	if err := sampleProtectedCache(ctx, source, cache, protected[hostMemoryProtectionRoot]); err != nil {
		return nil, err
	}
	for _, ancestor := range ancestors {
		if err := sampleProtectedCache(ctx, source, cache, protected[ancestor.Bound.Path]); err != nil {
			return nil, err
		}
	}
	return cache, nil
}

// sampleProtectedCache fills missing subtree samples in a shared cache.
//
// Example: an unreadable subtree must not expand claimable capacity.
func sampleProtectedCache(
	ctx context.Context,
	source fs.FS,
	cache map[string]memoryCacheSample,
	paths []string,
) error {
	for _, path := range paths {
		if _, exists := cache[path]; exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		stat, err := sampleMemoryStat(source, path)
		if err != nil {
			return err
		}
		cache[path] = memoryCacheSample{File: stat.File, Slab: stat.Slab}
	}
	return nil
}

// sampleMemoryClaim reads current charges and stats from one peer.
//
// Example: ENOENT during either read yields no resident credit for this pass.
func sampleMemoryClaim(
	source fs.FS,
	claim memoryClaim,
	page MemoryBytes,
) (memoryClaimSample, error) {
	current, _, err := sampleMemoryControl(source, claim.Path, accountingMemoryCurrent)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return memoryClaimSample{}, nil
	case err != nil:
		return memoryClaimSample{}, err
	}
	stat, err := sampleMemoryStat(source, claim.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return memoryClaimSample{}, nil
	case err != nil:
		return memoryClaimSample{}, err
	}
	return memoryClaimSample{
		Current: current,
		Anon:    stat.residentAnon(current, claim.Cap, page),
		Cache:   memoryCacheSample{File: stat.File, Slab: stat.Slab},
	}, nil
}

// sampleMemoryAncestor reads the effective finite limit, current charges and cache counters.
//
// Example: if both controls are unlimited, the zero sample has no bound path.
func sampleMemoryAncestor(
	source fs.FS,
	path string,
) (memoryAncestorSample, error) {
	limit, finiteMax, err := sampleMemoryControl(source, path, memoryClaimCapFile)
	if err != nil {
		return memoryAncestorSample{}, err
	}
	high, finiteHigh, err := sampleMemoryControl(source, path, accountingMemoryHigh)
	if err != nil {
		return memoryAncestorSample{}, err
	}
	if !finiteMax && !finiteHigh {
		return memoryAncestorSample{}, nil
	}
	current, _, err := sampleMemoryControl(source, path, accountingMemoryCurrent)
	if err != nil {
		return memoryAncestorSample{}, err
	}
	stat, err := sampleMemoryStat(source, path)
	if err != nil {
		return memoryAncestorSample{}, err
	}
	return memoryAncestorSample{Bound: memoryBound{Path: path, Limit: min(limit, high), Current: current}, Stat: stat}, nil
}

// sampleHostMemory reads the host aggregate and the low watermark needed by ancestor estimates.
//
// Example: MemAvailable needs no extra host-wide reclaim credit.
func sampleHostMemory(
	source fs.FS,
	floor MemoryBytes,
	page MemoryBytes,
) (memoryBound, MemoryBytes, error) {
	data, err := readMemorySample(source, accountingMeminfo)
	if err != nil {
		return memoryBound{}, 0, err
	}
	available, err := parseSampledMemAvailable(data)
	if err != nil {
		return memoryBound{}, 0, fmt.Errorf("%s: %w", accountingMeminfo, err)
	}
	data, err = readMemorySample(source, accountingZoneinfo)
	if err != nil {
		return memoryBound{}, 0, err
	}
	low, err := parseMemoryZoneLow(data, page)
	if err != nil {
		return memoryBound{}, 0, fmt.Errorf("%s: %w", accountingZoneinfo, err)
	}
	return memoryBound{Available: available, Floor: floor}, low, nil
}

// parseSampledMemAvailable extracts the kernel's estimate of available RAM.
//
// Example: MemAvailable: 1024 kB corresponds to 1048576 bytes.
func parseSampledMemAvailable(data string) (MemoryBytes, error) {
	for line := range strings.Lines(data) {
		key, value, _ := strings.Cut(line, ":")
		if key != "MemAvailable" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 2 && fields[1] == "kB" {
			kib, err := strconv.ParseUint(fields[0], 10, 64)
			if err == nil && kib <= ^uint64(0)/1024 {
				return MemoryBytes(kib * 1024), nil
			}
		}
		break
	}
	return 0, fmt.Errorf("invalid or missing MemAvailable")
}

// sampleMemoryStat reads and parses a cgroup sample with contextual errors.
//
// Example: errors identify the specific memory.stat file that could not be read.
func sampleMemoryStat(
	source fs.FS,
	path string,
) (memoryAccountingStat, error) {
	file := filepath.Join(path, accountingMemoryStat)
	data, err := readMemorySample(source, file)
	if err != nil {
		return memoryAccountingStat{}, err
	}
	stat, err := parseMemoryAccountingStat(data)
	if err != nil {
		return memoryAccountingStat{}, fmt.Errorf("%s: %w", file, err)
	}
	return stat, nil
}

// sampleMemoryControl reads a byte counter or limit, accepting max only for limits.
//
// Example: a finite memory.high of zero is a real bound rather than unlimited.
func sampleMemoryControl(
	source fs.FS,
	path string,
	name string,
) (MemoryBytes, bool, error) {
	file := filepath.Join(path, name)
	data, err := readMemorySample(source, file)
	if err != nil {
		return 0, false, err
	}
	value := strings.TrimSpace(data)
	if value == "max" && name != accountingMemoryCurrent {
		return ^MemoryBytes(0), false, nil
	}
	bytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", file, err)
	}
	return MemoryBytes(bytes), true, nil
}

// readMemorySample reads a virtual absolute path through the supplied system view.
//
// Example: an injected fs.FS can supply proc and cgroup samples without changing the host.
func readMemorySample(
	source fs.FS,
	path string,
) (string, error) {
	data, err := fs.ReadFile(source, strings.TrimPrefix(path, "/"))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}
