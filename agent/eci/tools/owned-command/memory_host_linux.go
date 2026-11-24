package main

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	// memoryLimitControl permits an unlimited value.
	//
	// Example: memory.max=max does not bound a command.
	memoryLimitControl memoryControlKind = iota

	// memoryProtectionControl treats max as full protection.
	//
	// Example: memory.low=max excludes the subtree from reclaim credit.
	memoryProtectionControl

	// memoryCounterControl accepts only a number.
	//
	// Example: memory.current=max is invalid.
	memoryCounterControl
)

const (
	// memoryControlMax is the kernel's maximum marker.
	//
	// Example: a memory limit of max means unlimited.
	memoryControlMax = "max"

	// meminfoFile is the host's memory information path.
	//
	// Example: read MemAvailable from /proc/meminfo.
	meminfoFile = "/proc/meminfo"

	// meminfoAvailableKey identifies claimable host RAM.
	//
	// Example: MemAvailable already includes an estimate of reclaimable cache.
	meminfoAvailableKey = "MemAvailable"

	// meminfoTotalKey identifies all physical host memory reported by procfs.
	//
	// Example: MemTotal is the base for percentage-based earlyoom floors.
	meminfoTotalKey = "MemTotal"

	// meminfoKiBUnit is the kernel's label for values in KiB.
	//
	// Example: a line ending in kB uses 1024-byte units.
	meminfoKiBUnit = "kB"

	// bytesPerKiB converts kernel or earlyoom KiB values to bytes.
	//
	// Example: 1024 KiB is one MiB.
	bytesPerKiB = 1024
)

// memoryControlKind distinguishes the semantics of a maximum marker.
//
// Example: limits and protection cannot interpret max the same way.
type memoryControlKind uint8

// memoryHostView reads the absolute procfs and cgroup paths in one host view.
//
// Example: production supplies os.DirFS("/"); tests can use fstest.MapFS.
type memoryHostView struct {
	Source fs.FS
}

// readFile reads the given absolute host path through the view.
//
// Example: /proc/meminfo becomes the FS path proc/meminfo.
func (v memoryHostView) readFile(absolute string) ([]byte, error) {
	return fs.ReadFile(v.Source, hostMemoryFSPath(absolute))
}

// hostMemoryFSPath maps an absolute host path to a valid io/fs name.
//
// Example: the host root is named dot in io/fs.
func hostMemoryFSPath(absolute string) string {
	relative := strings.TrimPrefix(absolute, "/")
	if relative == "" {
		return "."
	}
	return relative
}

// readMemoryControl reads a numeric control or its kind-specific maximum marker.
// Missing or deactivated files keep their read error for the caller to interpret.
// On success, the boolean is false only for an unlimited limit.
//
// Example: a missing protection file means no protection during discovery.
func (v memoryHostView) readMemoryControl(
	dir string,
	name string,
	kind memoryControlKind,
) (MemoryBytes, bool, error) {
	path := dir + "/" + name
	data, err := v.readFile(path)
	if err != nil {
		return 0, false, err
	}
	value := strings.TrimSpace(string(data))
	if value == memoryControlMax {
		switch kind {
		case memoryLimitControl:
			return 0, false, nil
		case memoryProtectionControl:
			return math.MaxUint64, true, nil
		}
	}
	count, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return MemoryBytes(count), true, nil
}

// meminfoBytes reads a named KiB field and converts it to bytes.
//
// Example: a MemAvailable field of 100 kB returns 102400 bytes.
func (v memoryHostView) meminfoBytes(key string) (MemoryBytes, error) {
	data, err := v.readFile(meminfoFile)
	if err != nil {
		return 0, err
	}
	field := key + ":"
	for line := range strings.Lines(string(data)) {
		rest, ok := strings.CutPrefix(line, field)
		if ok {
			return parseMemoryInfoBytes(key, rest)
		}
	}
	return 0, fmt.Errorf("missing %s in %s", key, meminfoFile)
}

// parseMemoryInfoBytes checks a matched field's value and kernel unit label.
//
// Example: invalid labels and overflowing values are read errors.
func parseMemoryInfoBytes(
	key string,
	value string,
) (MemoryBytes, error) {
	fields := strings.Fields(value)
	if len(fields) != 2 || fields[1] != meminfoKiBUnit {
		return 0, fmt.Errorf("invalid %s in %s", key, meminfoFile)
	}
	count, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || count > math.MaxUint64/bytesPerKiB {
		return 0, fmt.Errorf("invalid %s in %s", key, meminfoFile)
	}
	return MemoryBytes(count * bytesPerKiB), nil
}

// isGoneMemoryCgroup identifies a missing or deactivated kernfs node.
//
// Example: ENODEV during a peer read can signal concurrent scope collection.
func isGoneMemoryCgroup(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENODEV)
}
