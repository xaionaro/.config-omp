package main

import (
	"fmt"
	"os"
	"strconv"
)

// MemoryBytes identifies a workload memory quantity in bytes.
//
// Example: a limit of 67108864 bounds the workload to 64 MiB.
type MemoryBytes uint64

// saturatingAdd adds bytes without wrapping beyond the representable range.
//
// Example: summing extreme peer caps cannot turn occupied capacity into zero.
func (b MemoryBytes) saturatingAdd(other MemoryBytes) MemoryBytes {
	return b + min(other, ^MemoryBytes(0)-b)
}

// pageFloor rounds a byte quantity down to the kernel page size.
//
// Example: a non-page-aligned limit never exceeds its requested value.
func (b MemoryBytes) pageFloor() MemoryBytes {
	page := MemoryBytes(os.Getpagesize())
	return b / page * page
}

// String formats the configured byte limit in decimal.
//
// Example: a limit of 64 bytes is formatted as "64".
func (b *MemoryBytes) String() string {
	return strconv.FormatUint(uint64(*b), 10)
}

// Set parses a positive byte limit using the same syntax as flag.Uint64.
//
// Example: "0x40" selects a limit of 64 bytes; explicit zero is invalid.
func (b *MemoryBytes) Set(value string) error {
	parsed, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return err
	}
	if parsed == 0 {
		return fmt.Errorf("memory limit must be positive; omit the flag for an uncapped launch")
	}
	if MemoryBytes(parsed).pageFloor() == 0 {
		return fmt.Errorf("memory limit must be at least one page (%d bytes)", os.Getpagesize())
	}
	*b = MemoryBytes(parsed)
	return nil
}
