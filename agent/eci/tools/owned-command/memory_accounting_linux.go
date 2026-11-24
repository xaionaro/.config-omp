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

// memoryBound holds the measured capacity and outstanding growth at one boundary.
//
// Example: an empty Path is the host bound; a cgroup path uses Limit, Current, and Credit.
type memoryBound struct {
	Path      string
	Available MemoryBytes
	Floor     MemoryBytes
	Protected MemoryBytes
	Limit     MemoryBytes
	Current   MemoryBytes
	Credit    MemoryBytes
	Held      MemoryBytes
}

// admit checks the new cap against capacity remaining after existing claims.
//
// Example: when an ancestor exceeds its limit, reclaim must pay off that debt before admitting more.
func (b memoryBound) admit(requested MemoryBytes) error {
	base, extra := b.Available-min(b.Available, b.Floor), MemoryBytes(0)
	base -= min(base, b.Protected)
	if b.Path != "" {
		base = b.Limit - min(b.Limit, b.Current)
		debt := b.Current - min(b.Limit, b.Current)
		extra = b.Credit - min(b.Credit, debt)
	}

	// Compare the two sums exactly, including when a uint64 would wrap.
	needed, neededCarry := bits.Add64(uint64(requested), uint64(b.Held), 0)
	usable, usableCarry := bits.Add64(uint64(base), uint64(extra), 0)
	if neededCarry > usableCarry || (neededCarry == usableCarry && needed > usable) {
		return &memoryAdmissionRefusal{Bound: b, Requested: requested}
	}
	return nil
}

// memoryAdmissionRefusal records the binding constraint and its measured terms.
//
// Example: errors.As distinguishes a capacity refusal from a failed kernel read.
type memoryAdmissionRefusal struct {
	Bound     memoryBound
	Requested MemoryBytes
}

// Error formats the observed bound and demand for an admission receipt.
//
// Example: a slice refusal includes its finite limit, charges, reclaim credit, and held growth.
func (r *memoryAdmissionRefusal) Error() string {
	b := r.Bound
	if b.Path == "" {
		return fmt.Sprintf("%s at host: %d requested bytes, %d MemAvailable, %d floor, %d protected cache, %d bytes held for existing commands", errMemoryCapacity, r.Requested, b.Available, b.Floor, b.Protected, b.Held)
	}
	return fmt.Sprintf("%s at %s: %d requested bytes, %d limit, %d current, %d reclaim credit, %d bytes held for existing commands", errMemoryCapacity, b.Path, r.Requested, b.Limit, b.Current, b.Credit, b.Held)
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
	Anon      MemoryBytes
	LazyFree  MemoryBytes // Kernel event count in pages, not bytes.
	File      MemoryBytes
	Slab      MemoryBytes
	Dirty     MemoryBytes
	Writeback MemoryBytes
}

// residentAnon removes possibly reclaimable anonymous pages from resident credit.
//
// Example: a claim with more lazyfree events than resident pages gets no anonymous credit.
func (s memoryAccountingStat) residentAnon(
	current MemoryBytes,
	limit MemoryBytes,
	page MemoryBytes,
) MemoryBytes {
	if s.LazyFree > s.Anon/page {
		return 0
	}
	return min(s.Anon-s.LazyFree*page, current, limit)
}

// reclaimCredit discounts file LRU and reclaimable slab separately after exclusions.
//
// Example: exclude claim cache before applying the kernel estimate's low-watermark haircut.
func (s memoryAccountingStat) reclaimCredit(
	excluded memoryAccountingStat,
	watermark MemoryBytes,
) MemoryBytes {
	file := s.File - min(s.File, excluded.File)
	file -= min(file, s.Dirty)
	file -= min(file, s.Writeback)
	slab := s.Slab - min(s.Slab, excluded.Slab)
	return addMemoryClaims(file-min(file/2, watermark), slab-min(slab/2, watermark))
}

// parseMemoryAccountingStat extracts the complete set of counters needed by admission.
//
// Example: unknown kernel counters are skipped, but missing or duplicate required counters are errors.
func parseMemoryAccountingStat(input string) (memoryAccountingStat, error) {
	var stat memoryAccountingStat
	var inactiveFile MemoryBytes
	fields := [...]struct {
		name  string
		value *MemoryBytes
	}{
		{"anon", &stat.Anon},
		{"pglazyfree", &stat.LazyFree},
		{"active_file", &stat.File},
		{"inactive_file", &inactiveFile},
		{"slab_reclaimable", &stat.Slab},
		{"file_dirty", &stat.Dirty},
		{"file_writeback", &stat.Writeback},
	}
	var seen uint8
	for line := range strings.Lines(input) {
		key, raw, _ := strings.Cut(line, " ")
		for i, field := range fields {
			if key != field.name {
				continue
			}
			if seen&(1<<i) != 0 {
				return memoryAccountingStat{}, fmt.Errorf("duplicate %s counter", key)
			}
			value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				return memoryAccountingStat{}, fmt.Errorf("invalid %s counter: %w", key, err)
			}
			*field.value = MemoryBytes(value)
			seen |= 1 << i
			break
		}
	}
	if seen != (1<<len(fields))-1 {
		return memoryAccountingStat{}, fmt.Errorf("incomplete memory.stat counters")
	}
	stat.File = addMemoryClaims(stat.File, inactiveFile)
	return stat, nil
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
