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
	// memoryCgroupTypeFile exists in every non-root cgroup v2 directory.
	//
	// Example: a delegated mount root must not contain this file.
	memoryCgroupTypeFile = "cgroup.type"

	// memoryProtectionMinFile names the explicit hard reclaim protection.
	//
	// Example: a child with memory.min>0 is protected from its parent's reclaim.
	memoryProtectionMinFile = "memory.min"

	// memoryProtectionLowFile names the explicit best-effort reclaim protection.
	//
	// Example: a child with memory.low>0 is excluded from claimable cache credit.
	memoryProtectionLowFile = "memory.low"

	// hostMemoryProtectionRoot is the key for protections below the host root.
	//
	// Example: Protected[hostMemoryProtectionRoot] excludes host cache credit.
	hostMemoryProtectionRoot = ""

	// memoryClaimCapFile records the finite command limit that establishes a claim.
	//
	// Example: a command directory with memory.max=4096 claims one 4 KiB page.
	memoryClaimCapFile = "memory.max"
)

// memoryTopology identifies peer claims, enclosing boundaries, and the maximal
// protected subtrees relevant to ancestor reclaim credit.
//
// Example: a claim in a different user slice affects host admission only.
type memoryTopology struct {
	Claims    []memoryClaim
	Ancestors []string
	Protected map[string][]string
}

// memoryClaim fixes the finite cap observed while the command cgroup existed.
//
// Example: keep holding Cap if Path disappears during a later sampling pass.
type memoryClaim struct {
	Path string
	Cap  MemoryBytes
}

// lockHostMemoryAdmission rejects delegated hierarchy views before taking the
// shared root inode lock. Close the returned file to release the lock.
//
// Example: hold the lock across claim discovery and cap publication.
func lockHostMemoryAdmission(
	ctx context.Context,
	root string,
) (*os.File, error) {
	_, err := os.Lstat(filepath.Join(root, memoryCgroupTypeFile))
	if err == nil {
		return nil, fmt.Errorf("memory admission requires the host cgroup root; %s contains %s", root, memoryCgroupTypeFile)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("verify host cgroup root: %w", err)
	}
	return lockMemoryAdmission(ctx, root)
}

// discoverMemoryTopology inventories live peers and the candidate's enclosing
// cgroups below root. The caller holds the host admission lock.
//
// Example: include a finite peer even if its guard is no longer in the scope.
func discoverMemoryTopology(
	ctx context.Context,
	root string,
	command string,
) (memoryTopology, error) {
	root, command = filepath.Clean(root), filepath.Clean(command)
	if !filepath.IsAbs(root) || !isStrictMemoryDescendant(command, root) {
		return memoryTopology{}, fmt.Errorf("memory admission command %s is not below %s", command, root)
	}

	topology := memoryTopology{Protected: make(map[string][]string)}
	for dir := filepath.Dir(command); dir != root; dir = filepath.Dir(dir) {
		topology.Ancestors = append(topology.Ancestors, dir)
	}

	// Visit cgroups in preorder so each ancestor is considered before its descendants.
	err := filepath.WalkDir(root,
		// Skip vanished cgroups, but fail closed when a live subtree cannot be read.
		func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if path != root && errors.Is(walkErr, fs.ErrNotExist) {
					return nil
				}
				return walkErr
			}
			if path == root || path == command || !entry.IsDir() {
				return nil
			}
			if err := topology.discoverProtection(path); err != nil {
				return err
			}
			if entry.Name() != commandLeaf {
				return nil
			}
			scopeName := filepath.Base(filepath.Dir(path))
			if !strings.HasPrefix(scopeName, "owned-command-") || !strings.HasSuffix(scopeName, ".scope") {
				return nil
			}
			limit, finite, err := readTopologyMemoryControl(path, memoryClaimCapFile)
			if finite {
				topology.Claims = append(topology.Claims, memoryClaim{Path: path, Cap: limit})
			}
			return err
		})
	if err != nil {
		return memoryTopology{}, fmt.Errorf("discover memory admission topology: %w", err)
	}
	return topology, nil
}

// discoverProtection selects the largest explicitly protected subtrees for
// each reclaim root independently.
//
// Example: a protected ancestor contributes for the host but not itself.
func (t *memoryTopology) discoverProtection(path string) error {
	for _, name := range [...]string{memoryProtectionMinFile, memoryProtectionLowFile} {
		value, _, err := readTopologyMemoryControl(path, name)
		if err != nil {
			return err
		}
		if value != 0 {
			t.addProtection(hostMemoryProtectionRoot, path)
			for _, ancestor := range t.Ancestors {
				if isStrictMemoryDescendant(path, ancestor) {
					t.addProtection(ancestor, path)
				}
			}
			return nil
		}
	}
	return nil
}

// addProtection records a maximal protected subtree in preorder.
//
// Example: a protected descendant of the last recorded subtree is already covered.
func (t *memoryTopology) addProtection(
	bound string,
	path string,
) {
	protected := t.Protected[bound]
	if len(protected) == 0 || !isStrictMemoryDescendant(path, protected[len(protected)-1]) {
		t.Protected[bound] = append(protected, path)
	}
}

// isStrictMemoryDescendant tests path containment at a directory boundary.
//
// Example: /root/a/b is below /root/a; /root/ab is not.
func isStrictMemoryDescendant(
	path string,
	root string,
) bool {
	suffix, ok := strings.CutPrefix(path, strings.TrimSuffix(root, "/"))
	return ok && len(suffix) > 1 && suffix[0] == '/'
}

// readTopologyMemoryControl reads a numeric topology control, distinguishing an
// uncapped command from a finite command, and tolerating missing controls.
//
// Example: the unlimited kernel marker max does not make a claim.
func readTopologyMemoryControl(
	path string,
	name string,
) (MemoryBytes, bool, error) {
	control := filepath.Join(path, name)
	data, err := os.ReadFile(control)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	value := strings.TrimSpace(string(data))
	if name == memoryClaimCapFile && value == "max" {
		return 0, false, nil
	}
	bytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse %s: %w", control, err)
	}
	return MemoryBytes(bytes), true, nil
}
