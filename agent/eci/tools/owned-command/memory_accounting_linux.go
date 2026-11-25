package main

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

// errMemoryCapacity identifies a sampled capacity shortfall rather than a read failure.
//
// Example: an otherwise valid cap cannot fit under the current host floor.
var errMemoryCapacity = errors.New("memory admission refused")

// memoryBoundKind distinguishes a host budget from a cgroup budget.
//
// Example: a cgroup bound has a path independent of its kind.
type memoryBoundKind uint8

const (
	// hostMemoryKind identifies the MemAvailable budget.
	//
	// Example: all peer claims contribute to host growth.
	hostMemoryKind memoryBoundKind = iota
	// cgroupMemoryKind identifies a finite cgroup budget.
	//
	// Example: only descendants contribute growth to a cgroup bound.
	cgroupMemoryKind
)

// memoryBoundID identifies where protection applies.
//
// Example: protected host roots never share a map entry with a cgroup.
type memoryBoundID struct {
	kind memoryBoundKind
	path string
}

// hostMemoryBoundID identifies protection relative to the host reclaim root.
//
// Example: topology.Protected[hostMemoryBoundID] lists host exclusions.
var hostMemoryBoundID = memoryBoundID{kind: hostMemoryKind}

// cgroupMemoryBoundID identifies protection relative to an enclosing cgroup.
//
// Example: /user.slice and the host always have distinct identities.
func cgroupMemoryBoundID(path string) memoryBoundID {
	return memoryBoundID{kind: cgroupMemoryKind, path: path}
}

// memoryBound checks and reports capacity in one sampled memory domain.
//
// Example: both the host and each finite cgroup must independently admit the new cap.
type memoryBound interface {
	// fits checks whether the request can fit alongside the bound's held growth.
	//
	// Example: an existing claim leaves only part of the sampled headroom usable.
	fits(requested MemoryBytes) bool
	// describe formats sampled terms for the refusal receipt.
	//
	// Example: host descriptions include MemAvailable and the earlyoom floor.
	describe(requested MemoryBytes) string
}

// hostMemoryBound holds the sampled host budget and total peer growth.
//
// Example: protected cache and the earlyoom floor are deductions from MemAvailable.
type hostMemoryBound struct {
	Available MemoryBytes
	Floor     MemoryBytes
	Protected MemoryBytes
	Held      MemoryBytes
}

// fits checks host growth against MemAvailable after deductions.
//
// Example: earlyoom's floor cannot become negative usable capacity.
func (b hostMemoryBound) fits(requested MemoryBytes) bool {
	usable := b.Available - min(b.Available, b.Floor)
	usable -= min(usable, b.Protected)
	return requested <= usable-min(usable, b.Held)
}

// describe formats the sampled host terms and request.
//
// Example: a host refusal names its protected-cache deduction.
func (b hostMemoryBound) describe(requested MemoryBytes) string {
	return fmt.Sprintf("at host: %d requested bytes, %d MemAvailable, %d floor, %d protected cache, %d bytes held for existing commands", requested, b.Available, b.Floor, b.Protected, b.Held)
}

// cgroupMemoryBound holds the finite limit, charges, reclaim credit and descendant growth.
//
// Example: memory.high can bind a workload before memory.max is reached.
type cgroupMemoryBound struct {
	Path    string
	Limit   MemoryBytes
	Current MemoryBytes
	Credit  MemoryBytes
	Held    MemoryBytes
}

// fits checks cgroup growth after applying reclaim to any current overage.
//
// Example: reclaim first pays off charges over the limit; only the remainder funds growth.
func (b cgroupMemoryBound) fits(requested MemoryBytes) bool {
	chargedWithinLimit := min(b.Limit, b.Current)
	base := b.Limit - chargedWithinLimit
	debt := b.Current - chargedWithinLimit
	extra := b.Credit - min(b.Credit, debt)
	needed, neededCarry := bits.Add64(uint64(requested), uint64(b.Held), 0)
	usable, usableCarry := bits.Add64(uint64(base), uint64(extra), 0)
	return neededCarry < usableCarry || (neededCarry == usableCarry && needed <= usable)
}

// describe formats the sampled cgroup terms and request.
//
// Example: an ancestor refusal names the binding cgroup path.
func (b cgroupMemoryBound) describe(requested MemoryBytes) string {
	return fmt.Sprintf("at %s: %d requested bytes, %d limit, %d current, %d reclaim credit, %d bytes held for existing commands", b.Path, requested, b.Limit, b.Current, b.Credit, b.Held)
}

// admitMemoryBound reports a structured refusal only when a sampled bound fails.
//
// Example: the host and each finite ancestor must independently fit the request.
func admitMemoryBound(
	bound memoryBound,
	requested MemoryBytes,
) *memoryAdmissionRefusal {
	if bound.fits(requested) {
		return nil
	}
	return &memoryAdmissionRefusal{Bound: bound, Requested: requested}
}

// memoryAdmissionRefusal records the binding constraint and its measured terms.
//
// Example: errors.As distinguishes a capacity refusal from a failed kernel read.
type memoryAdmissionRefusal struct {
	Bound     memoryBound
	Requested MemoryBytes
	Peers     []string
}

// Error formats the observed bound and demand for an admission receipt.
//
// Example: a slice refusal includes its finite limit, charges, reclaim credit, and held growth.
func (r *memoryAdmissionRefusal) Error() string {
	// TODO: List each claim's path and held growth if the total is not enough to attribute a refusal.
	return errMemoryCapacity.Error() + " " + r.Bound.describe(r.Requested)
}

// Unwrap exposes the admission capacity error.
//
// Example: errors.Is(err, errMemoryCapacity) matches host and ancestor shortfalls.
func (r *memoryAdmissionRefusal) Unwrap() error {
	return errMemoryCapacity
}

// memoryAccountingStat holds the hierarchical memory.stat counters used in admission.
//
// Example: File is active_file plus inactive_file, not all cached filesystem data.
type memoryAccountingStat struct {
	AnonLRU   MemoryBytes
	File      MemoryBytes
	Slab      MemoryBytes
	Dirty     MemoryBytes
	Writeback MemoryBytes
}

// residentAnon credits swap-backed LRU pages, including shmem, already charged to the claim.
//
// Example: MADV_FREE pages on the file LRU are excluded without consulting cumulative events.
func (s memoryAccountingStat) residentAnon(
	current MemoryBytes,
	limit MemoryBytes,
) MemoryBytes {
	return min(s.AnonLRU, current, limit)
}

// reclaimCredit discounts file LRU and reclaimable slab separately after exclusions.
//
// Example: exclude claim cache before applying the kernel estimate's low-watermark haircut.
func (s memoryAccountingStat) reclaimCredit(
	excluded memoryCacheSample,
	watermark MemoryBytes,
) MemoryBytes {
	file := s.File - min(s.File, excluded.File)
	file -= min(file, s.Dirty)
	file -= min(file, s.Writeback)
	slab := s.Slab - min(s.Slab, excluded.Slab)
	return (file - min(file/2, watermark)).saturatingAdd(slab - min(slab/2, watermark))
}

// parseMemoryAccountingStat extracts the complete set of counters needed by admission.
//
// Example: unknown kernel counters are skipped, but missing or duplicate required counters are errors.
func parseMemoryAccountingStat(input string) (memoryAccountingStat, error) {
	var stat memoryAccountingStat
	var inactiveAnon, inactiveFile MemoryBytes
	fields := [...]struct {
		name  string
		value *MemoryBytes
	}{
		{"active_anon", &stat.AnonLRU},
		{"inactive_anon", &inactiveAnon},
		{"active_file", &stat.File},
		{"inactive_file", &inactiveFile},
		{"slab_reclaimable", &stat.Slab},
		{"file_dirty", &stat.Dirty},
		{"file_writeback", &stat.Writeback},
	}
	var seen uint8
	for line := range strings.Lines(input) {
		key, raw, _ := strings.Cut(line, " ")
		i := memoryStatFieldIndex(fields[:], key)
		if i < 0 {
			continue
		}
		if seen&(1<<i) != 0 {
			return memoryAccountingStat{}, fmt.Errorf("duplicate %s counter", key)
		}
		value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return memoryAccountingStat{}, fmt.Errorf("invalid %s counter: %w", key, err)
		}
		*fields[i].value = MemoryBytes(value)
		seen |= 1 << i
	}
	if seen != (1<<len(fields))-1 {
		return memoryAccountingStat{}, fmt.Errorf("incomplete memory.stat counters")
	}
	stat.AnonLRU = stat.AnonLRU.saturatingAdd(inactiveAnon)
	stat.File = stat.File.saturatingAdd(inactiveFile)
	return stat, nil
}

// memoryStatFieldIndex locates a counter used for admission.
//
// Example: unrelated kernel counters are ignored without parsing their values.
func memoryStatFieldIndex(
	fields []struct {
		name  string
		value *MemoryBytes
	},
	key string,
) int {
	for i, field := range fields {
		if field.name == key {
			return i
		}
	}
	return -1
}

// parseMemoryZoneLow sums the per-zone low watermarks and converts pages to bytes.
//
// Example: unrelated counters such as lowmem_reserve[] do not contribute.
func parseMemoryZoneLow(
	input string,
	page MemoryBytes,
) (MemoryBytes, error) {
	var bytes MemoryBytes
	found := false
	for line := range strings.Lines(input) {
		key, raw, _ := strings.Cut(strings.TrimLeft(line, " \t"), " ")
		if strings.TrimSpace(key) != "low" {
			continue
		}
		pages, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid zone low watermark: %w", err)
		}
		if MemoryBytes(pages) > (^MemoryBytes(0)-bytes)/page {
			return 0, fmt.Errorf("zone low watermarks overflow byte count")
		}
		bytes += MemoryBytes(pages) * page
		found = true
	}
	if !found {
		return 0, fmt.Errorf("missing zone low watermarks")
	}
	return bytes, nil
}
