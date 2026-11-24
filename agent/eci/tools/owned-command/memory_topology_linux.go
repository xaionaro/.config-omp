package main

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

const (
	// memoryProtectionMinFile names the explicit hard reclaim protection.
	//
	// Example: a child with memory.min>0 is protected from its parent's reclaim.
	memoryProtectionMinFile = "memory.min"

	// memoryProtectionLowFile names the explicit best-effort reclaim protection.
	//
	// Example: a child with memory.low>0 is excluded from claimable cache credit.
	memoryProtectionLowFile = "memory.low"
)

// memoryTopology identifies peer claims, enclosing boundaries, and the maximal
// protected subtrees relevant to ancestor reclaim credit.
//
// Example: a claim in a different user slice affects host admission only.
type memoryTopology struct {
	Claims    []memoryClaim
	Ancestors []string
	Protected map[memoryBoundID][]string
}

// memoryClaim fixes the finite cap observed while the command cgroup existed.
//
// Example: keep holding Cap if Path disappears during a later sampling pass.
type memoryClaim struct {
	Path string
	Cap  MemoryBytes
}

// memoryTopologyWalker retains the traversal context and discovered topology.
//
// Example: pass visit to fs.WalkDir to inventory a host cgroup hierarchy.
type memoryTopologyWalker struct {
	ctx      context.Context
	view     memoryHostView
	root     string
	command  string
	topology memoryTopology
}

// discoverMemoryTopology inventories live peers and the candidate's enclosing
// cgroups below root. The caller holds the host admission lock.
//
// Example: include a finite peer even if its guard is no longer in the scope.
func discoverMemoryTopology(
	ctx context.Context,
	view memoryHostView,
	root string,
	command string,
) (memoryTopology, error) {
	root, command = filepath.Clean(root), filepath.Clean(command)
	if !filepath.IsAbs(root) || !isStrictMemoryDescendant(command, root) {
		return memoryTopology{}, fmt.Errorf("memory admission command %s is not below %s", command, root)
	}

	walker := memoryTopologyWalker{
		ctx:      ctx,
		view:     view,
		root:     root,
		command:  command,
		topology: memoryTopology{Protected: make(map[memoryBoundID][]string)},
	}
	for dir := filepath.Dir(command); dir != root; dir = filepath.Dir(dir) {
		walker.topology.Ancestors = append(walker.topology.Ancestors, dir)
	}

	// Preorder visits explicit protections before any of their descendants.
	if err := fs.WalkDir(view.source, hostMemoryFSPath(root), walker.visit); err != nil {
		return memoryTopology{}, fmt.Errorf("discover memory admission topology: %w", err)
	}
	return walker.topology, nil
}

// visit reads claims and protections for one live cgroup in walk order.
//
// Example: skip a peer directory deactivated since listing its parent.
func (w *memoryTopologyWalker) visit(
	relative string,
	entry fs.DirEntry,
	walkErr error,
) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	path := "/" + relative
	if relative == "." {
		path = "/"
	}
	if walkErr != nil {
		if path != w.root && isGoneMemoryCgroup(walkErr) {
			return nil
		}
		return walkErr
	}
	if path == w.root || path == w.command || !entry.IsDir() {
		return nil
	}
	protected, err := w.hasProtection(path)
	if err != nil {
		return err
	}
	if protected {
		w.topology.recordProtection(path)
	}
	return w.recordClaim(path, entry.Name())
}

// recordClaim records a finite cap, but not unrelated or unlimited groups.
//
// Example: old launchers' scopes use the same name as current scopes.
func (w *memoryTopologyWalker) recordClaim(
	path string,
	leaf string,
) error {
	if leaf != commandLeaf || !isOwnedCommandScope(filepath.Base(filepath.Dir(path))) {
		return nil
	}
	limit, finite, err := w.view.memoryControl(path, memoryMaxFile, memoryLimitControl)
	if isGoneMemoryCgroup(err) {
		return nil
	}
	if finite {
		w.topology.Claims = append(w.topology.Claims, memoryClaim{Path: path, Cap: limit})
	}
	return err
}

// hasProtection checks both explicit protection controls for a live cgroup.
//
// Example: neither a missing control nor a zero gives reclaim protection.
func (w *memoryTopologyWalker) hasProtection(path string) (bool, error) {
	for _, name := range [...]string{memoryProtectionMinFile, memoryProtectionLowFile} {
		value, _, err := w.view.memoryControl(path, name, memoryProtectionControl)
		if isGoneMemoryCgroup(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if value != 0 {
			return true, nil
		}
	}
	return false, nil
}

// recordProtection distributes a protected cgroup to the applicable bounds.
//
// Example: a protected ancestor contributes for the host but not itself.
func (t *memoryTopology) recordProtection(path string) {
	t.addProtection(hostMemoryBoundID, path)
	for _, ancestor := range t.Ancestors {
		if isStrictMemoryDescendant(path, ancestor) {
			t.addProtection(cgroupMemoryBoundID(ancestor), path)
		}
	}
}

// addProtection records a maximal protected subtree in preorder.
//
// Example: a protected descendant of the last recorded subtree is already covered.
func (t *memoryTopology) addProtection(
	bound memoryBoundID,
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
