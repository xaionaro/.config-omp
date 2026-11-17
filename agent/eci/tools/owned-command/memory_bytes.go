package main

import (
	"os"
	"strconv"
)

// MemoryBytes identifies a workload memory quantity in bytes.
//
// Example: a limit of 67108864 bounds the workload to 64 MiB.
type MemoryBytes uint64

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

// Set parses a byte limit using the same syntax as flag.Uint64.
//
// Example: "0x40" selects a limit of 64 bytes.
func (b *MemoryBytes) Set(value string) error {
	parsed, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return err
	}
	*b = MemoryBytes(parsed)
	return nil
}
