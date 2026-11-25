package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// zoneinfoFile names the source for per-zone reclaim watermarks.
	//
	// Example: low watermarks are read in pages before converting to bytes.
	zoneinfoFile = "/proc/zoneinfo"
	// memoryHighFile names the memory reclaim threshold control.
	//
	// Example: a lower memory.high is more restrictive than memory.max for admission.
	memoryHighFile = "memory.high"
	// memoryCurrentFile names the charged memory counter.
	//
	// Example: a claim's charges reduce how much more it can grow at an ancestor.
	memoryCurrentFile = "memory.current"
	// memoryStatFile names the hierarchical memory statistics file.
	//
	// Example: active_file and inactive_file track the file LRU used for reclaim estimates.
	memoryStatFile = "memory.stat"
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
		File: s.File.saturatingAdd(other.File),
		Slab: s.Slab.saturatingAdd(other.Slab),
	}
}

// subtract removes overlapping cache without allowing a category to become negative.
//
// Example: excluding 30 file bytes from a 20-byte sample leaves zero file bytes.
func (s memoryCacheSample) subtract(other memoryCacheSample) memoryCacheSample {
	return memoryCacheSample{
		File: s.File - min(s.File, other.File),
		Slab: s.Slab - min(s.Slab, other.Slab),
	}
}

// memoryClaimSample separates LRU occupancy, used for held growth, from releasable anon.
//
// Example: a vanished peer has a zero sample but retains its published cap.
type memoryClaimSample struct {
	Current     MemoryBytes
	ResidentLRU MemoryBytes
	Release     MemoryBytes
	Cache       memoryCacheSample
}

// memoryClaimBracket holds remaining growth, cache bounds, and anonymous release.
//
// Example: a vanished peer holds its full cap and its cache range starts at zero.
type memoryClaimBracket struct {
	Release        MemoryBytes
	HostGrowth     MemoryBytes
	AncestorGrowth MemoryBytes
	CacheMin       memoryCacheSample
	CacheMax       memoryCacheSample
}

// bracket bounds a claim's growth and cache across both samples.
//
// Example: a growing cache uses its larger sample for exclusion and its smaller sample for overlap.
func (s memoryClaimSample) bracket(
	other memoryClaimSample,
	limit MemoryBytes,
) memoryClaimBracket {
	return memoryClaimBracket{
		Release:        min(s.Release, other.Release),
		HostGrowth:     limit - min(s.ResidentLRU, other.ResidentLRU),
		AncestorGrowth: limit - min(limit, s.Current, other.Current),
		CacheMin: memoryCacheSample{
			File: min(s.Cache.File, other.Cache.File),
			Slab: min(s.Cache.Slab, other.Cache.Slab),
		},
		CacheMax: memoryCacheSample{
			File: max(s.Cache.File, other.Cache.File),
			Slab: max(s.Cache.Slab, other.Cache.Slab),
		},
	}
}

// memoryAncestorSample keeps an ancestor's scratch sample until peer bracketing completes.
//
// Example: memory.stat reclaim candidates are discounted after subtracting peer cache.
type memoryAncestorSample struct {
	Bound cgroupMemoryBound
	Stat  memoryAccountingStat
}

// checkCommandMemory reads a bracketed estimate and admits only when every bound fits.
//
// Example: callers hold the admission lock and pass the host view plus discovered topology.
func checkCommandMemory(
	ctx context.Context,
	view memoryHostView,
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
		first, err := sampleMemoryClaim(view, claim)
		if err != nil {
			return err
		}
		peers[i] = first
	}

	host, low, err := sampleHostMemory(view, floor, page)
	if err != nil {
		return err
	}
	ancestors := make([]memoryAncestorSample, 0, len(topology.Ancestors))
	for _, path := range topology.Ancestors {
		if err := ctx.Err(); err != nil {
			return err
		}
		ancestor, finite, err := sampleMemoryAncestor(view, path)
		if err != nil {
			return err
		}
		if finite {
			ancestors = append(ancestors, ancestor)
		}
	}
	protected, err := sampleProtectedMemory(ctx, view, topology.Protected, ancestors)
	if err != nil {
		return err
	}
	claims := make(map[string]memoryClaimBracket, len(topology.Claims))
	for i, claim := range topology.Claims {
		if err := ctx.Err(); err != nil {
			return err
		}
		second, err := sampleMemoryClaim(view, claim)
		if err != nil {
			return err
		}
		claims[claim.Path] = peers[i].bracket(second, claim.Cap)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var excluded memoryCacheSample
	host.Held, excluded = accountBoundClaims(hostMemoryBoundID, topology.Protected[hostMemoryBoundID], claims, protected)
	host.Protected = excluded.File.saturatingAdd(excluded.Slab)
	var refusal *memoryAdmissionRefusal
	if !host.fits(requested) {
		refusal = &memoryAdmissionRefusal{Bound: host, Requested: requested}
	}
	for i := range ancestors {
		ancestor := &ancestors[i]
		bound := ancestor.Bound
		id := cgroupMemoryBoundID(bound.Path)
		bound.Held, excluded = accountBoundClaims(id, topology.Protected[id], claims, protected)
		bound.Credit = ancestor.Stat.reclaimCredit(excluded, low)
		ancestor.Bound = bound
		if refusal == nil && !bound.fits(requested) {
			refusal = &memoryAdmissionRefusal{Bound: bound, Requested: requested}
		}
	}
	if refusal == nil {
		return nil
	}
	for _, peer := range topology.Claims {
		if err := ctx.Err(); err != nil {
			return err
		}
		if claimDepartureFits(requested, host, ancestors, topology.Protected, claims, protected, low, peer) {
			refusal.SufficientClaims = append(refusal.SufficientClaims, peer)
		}
	}
	return refusal
}

// claimDepartureFits projects one departure against every sampled bound.
// Re-account cache overlap without the claim; never add newly reclaimable cache.
//
// Example: a peer in a different slice cannot repair a shortage in this slice.
func claimDepartureFits(
	requested MemoryBytes,
	host hostMemoryBound,
	ancestors []memoryAncestorSample,
	protectedRoots map[memoryBoundID][]string,
	claims map[string]memoryClaimBracket,
	protected map[string]memoryCacheSample,
	low MemoryBytes,
	peer memoryClaim,
) bool {
	sample := claims[peer.Path]
	release := sample.Release
	if strictMemoryAncestor(claims, peer.Path, "/") != "" {
		// The remaining claim's hierarchical accounting absorbs resident release.
		release = 0
	}
	delete(claims, peer.Path)
	// Restore the original sample before testing the next independent departure.
	//
	// Example: projecting one peer never affects the next peer's eligibility.
	defer func() { claims[peer.Path] = sample }()
	var excluded memoryCacheSample
	host.Held, excluded = accountBoundClaims(hostMemoryBoundID, protectedRoots[hostMemoryBoundID], claims, protected)
	host.Protected = excluded.File.saturatingAdd(excluded.Slab)
	host.Available = host.Available.saturatingAdd(release)
	if !host.fits(requested) {
		return false
	}
	for _, ancestor := range ancestors {
		bound := ancestor.Bound
		if isStrictMemoryDescendant(peer.Path, bound.Path) {
			id := cgroupMemoryBoundID(bound.Path)
			bound.Held, excluded = accountBoundClaims(id, protectedRoots[id], claims, protected)
			bound.Credit = min(bound.Credit, ancestor.Stat.reclaimCredit(excluded, low))
			bound.Current -= min(bound.Current, release)
		}
		if !bound.fits(requested) {
			return false
		}
	}
	return true
}

// accountBoundClaims calculates held growth and excluded cache for one typed bound.
//
// Example: count nested cap growth independently, but exclude cache only through an outer claim.
func accountBoundClaims(
	bound memoryBoundID,
	protectedRoots []string,
	claims map[string]memoryClaimBracket,
	protected map[string]memoryCacheSample,
) (MemoryBytes, memoryCacheSample) {
	atHost := bound.kind == hostMemoryKind
	stop := bound.path
	if atHost {
		stop = "/"
	}
	remainders := make(map[string]memoryCacheSample, len(protectedRoots))
	for _, root := range protectedRoots {
		if strictMemoryAncestor(claims, root, stop) == "" {
			remainders[root] = protected[root]
		}
	}

	var held MemoryBytes
	var excluded memoryCacheSample
	for path, claim := range claims {
		switch {
		case atHost:
			held = held.saturatingAdd(claim.HostGrowth)
		case isStrictMemoryDescendant(path, bound.path):
			held = held.saturatingAdd(claim.AncestorGrowth)
		default:
			continue
		}
		if atHost && len(remainders) == 0 {
			continue
		}
		if strictMemoryAncestor(claims, path, stop) != "" {
			continue
		}
		if owner := strictMemoryAncestor(remainders, path, stop); owner != "" {
			remainders[owner] = remainders[owner].subtract(claim.CacheMin)
		}
		if atHost {
			delete(remainders, path)
			continue
		}
		if cache, exact := remainders[path]; exact {
			// Exact overlap contributes the larger of the two samples.
			remainders[path] = cache.subtract(claim.CacheMax)
		}
		excluded = excluded.add(claim.CacheMax)
	}
	for _, cache := range remainders {
		excluded = excluded.add(cache)
	}
	return held, excluded
}

// strictMemoryAncestor finds the nearest indexed parent strictly below the reclaim root.
//
// Example: look up a claim's containing protected root without counting the cgroup itself.
func strictMemoryAncestor[T any](
	index map[string]T,
	path string,
	stop string,
) string {
	if len(index) == 0 {
		return ""
	}
	for dir := filepath.Dir(path); dir != stop && dir != "/"; dir = filepath.Dir(dir) {
		if _, found := index[dir]; found {
			return dir
		}
	}
	return ""
}

// sampleProtectedMemory reads each protected cgroup at most once, before the second peer pass.
//
// Example: a child protected for both the host and a finite ancestor is read once.
func sampleProtectedMemory(
	ctx context.Context,
	view memoryHostView,
	protected map[memoryBoundID][]string,
	ancestors []memoryAncestorSample,
) (map[string]memoryCacheSample, error) {
	cache := make(map[string]memoryCacheSample)
	if err := sampleProtectedCache(ctx, view, cache, protected[hostMemoryBoundID]); err != nil {
		return nil, err
	}
	for _, ancestor := range ancestors {
		if err := sampleProtectedCache(ctx, view, cache, protected[cgroupMemoryBoundID(ancestor.Bound.Path)]); err != nil {
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
	view memoryHostView,
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
		stat, err := sampleMemoryStat(view, path)
		switch {
		case isGoneMemoryCgroup(err):
			cache[path] = memoryCacheSample{}
			continue
		case err != nil:
			return err
		}
		cache[path] = memoryCacheSample{File: stat.File, Slab: stat.Slab}
	}
	return nil
}

// sampleMemoryClaim reads current charges and stats from one peer.
//
// Example: a disappearing controller file yields no resident credit for this pass.
func sampleMemoryClaim(
	view memoryHostView,
	claim memoryClaim,
) (memoryClaimSample, error) {
	current, _, err := view.readMemoryControl(claim.Path, memoryCurrentFile, memoryCounterControl)
	switch {
	case isGoneMemoryCgroup(err):
		return memoryClaimSample{}, nil
	case err != nil:
		return memoryClaimSample{}, err
	}
	stat, err := sampleMemoryStat(view, claim.Path)
	switch {
	case isGoneMemoryCgroup(err):
		return memoryClaimSample{}, nil
	case err != nil:
		return memoryClaimSample{}, err
	}
	return memoryClaimSample{
		Current:     current,
		ResidentLRU: stat.residentAnon(current, claim.Cap),
		Release:     min(stat.Anon, current),
		Cache:       memoryCacheSample{File: stat.File, Slab: stat.Slab},
	}, nil
}

// sampleMemoryAncestor reads the effective finite limit, current charges and cache counters.
//
// Example: if both limit controls are unlimited, finite is false.
func sampleMemoryAncestor(
	view memoryHostView,
	path string,
) (sample memoryAncestorSample, finite bool, err error) {
	limit, finiteMax, err := view.readMemoryControl(path, memoryMaxFile, memoryLimitControl)
	if err != nil {
		return memoryAncestorSample{}, false, err
	}
	high, finiteHigh, err := view.readMemoryControl(path, memoryHighFile, memoryLimitControl)
	if err != nil {
		return memoryAncestorSample{}, false, err
	}
	switch {
	case !finiteMax && !finiteHigh:
		return memoryAncestorSample{}, false, nil
	case !finiteMax:
		limit = high
	case finiteHigh:
		limit = min(limit, high)
	}
	current, _, err := view.readMemoryControl(path, memoryCurrentFile, memoryCounterControl)
	if err != nil {
		return memoryAncestorSample{}, false, err
	}
	stat, err := sampleMemoryStat(view, path)
	if err != nil {
		return memoryAncestorSample{}, false, err
	}
	return memoryAncestorSample{Bound: cgroupMemoryBound{Path: path, Limit: limit, Current: current}, Stat: stat}, true, nil
}

// sampleHostMemory reads the host aggregate and the low watermark needed by ancestor estimates.
//
// Example: MemAvailable needs no extra host-wide reclaim credit.
func sampleHostMemory(
	view memoryHostView,
	floor MemoryBytes,
	page MemoryBytes,
) (hostMemoryBound, MemoryBytes, error) {
	available, err := view.meminfoBytes(meminfoAvailableKey)
	if err != nil {
		return hostMemoryBound{}, 0, err
	}
	data, err := view.readFile(zoneinfoFile)
	if err != nil {
		return hostMemoryBound{}, 0, err
	}
	low, err := parseMemoryZoneLow(string(data), page)
	if err != nil {
		return hostMemoryBound{}, 0, fmt.Errorf("%s: %w", zoneinfoFile, err)
	}
	return hostMemoryBound{Available: available, Floor: floor}, low, nil
}

// sampleMemoryStat reads and parses a cgroup sample with contextual errors.
//
// Example: errors identify the specific memory.stat file that could not be read.
func sampleMemoryStat(
	view memoryHostView,
	path string,
) (memoryAccountingStat, error) {
	file := filepath.Join(path, memoryStatFile)
	data, err := view.readFile(file)
	if err != nil {
		return memoryAccountingStat{}, err
	}
	stat, err := parseMemoryAccountingStat(string(data))
	if err != nil {
		return memoryAccountingStat{}, fmt.Errorf("%s: %w", file, err)
	}
	return stat, nil
}
