package main

// TODO: Remove retired Stop/ECI marker control entries after hook ownership rules are migrated.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// liveDependencySymlinkLimit bounds expansions consistently with filepath.EvalSymlinks.
//
// Example: a looping live-hook target retains known entries after 255 expansions.
const liveDependencySymlinkLimit = 255

// PathAccess describes how an operation uses its selected worktree names.
//
// Example: cached hunks select logical index names while staging content reads physical leaves.
type PathAccess int

const (
	// LogicalOnly selects index entries without reading selected worktree leaves.
	//
	// Example: unstaging a file preserves any worktree ancestor alias.
	LogicalOnly PathAccess = iota
	// PhysicalReadProbe reads content or probes absence under selected worktree names.
	//
	// Example: stage-content reads the selected file before updating the index.
	PhysicalReadProbe
	// PhysicalWrite changes selected worktree entries and protects live targets.
	//
	// Example: a move must protect both physical endpoints.
	PhysicalWrite
)

// containsPath compares filesystem components rather than textual prefixes.
//
// Example: hooks-old does not contain hooks/validate-bash.sh.
func containsPath(
	parent string,
	child string,
) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// resolvedParent resolves existing ancestors while keeping the selected leaf lexical.
//
// Example: removing a symlink changes its entry rather than its referent.
func resolvedParent(path string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err == nil {
		return filepath.Join(parent, filepath.Base(path)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if filepath.Dir(path) == path {
		return "", err
	}
	parent, err = resolvedParent(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

// LiveFrontier identifies an unresolved configured lookup that can hide another alias.
//
// Example: unreadable hooks can hide a target naming an otherwise independent worktree alias.
type LiveFrontier struct {
	Hook                 string
	Path                 string
	Directory            string
	OwnedVisibility      bool
	VisibilityDiagnostic error
}

// LiveProtection retains concrete identities, opaque lookup frontiers, and advisory diagnostics.
//
// Example: a malformed absent suffix remains advisory while an unreadable hook parent requires recheck.
type LiveProtection struct {
	Paths       []string
	Frontiers   []LiveFrontier
	Diagnostics error
}

// LiveHookTrace carries accessible lookup evidence without inventing a cleaned hook spelling.
//
// Example: raw portal/../runtime retains portal while an unreadable hook keeps its accessible prefixes.
type LiveHookTrace struct {
	Dependencies []string
	Entry        string
	Resolved     string
	Absent       bool
	Frontier     *LiveFrontier
	Diagnostic   error
}

// liveProtectedPaths discovers named hooks before deciding whether their lookup is complete.
//
// Example: hooks -> dir/private retains dir even when the final hook entry cannot be inspected.
func liveProtectedPaths() (LiveProtection, error) {
	roots := []string{os.Getenv("ECI_CONFIGURED_HOME"), os.Getenv("ECI_HOME"), os.Getenv("KIMI_CODE_HOME"),
		filepath.Join(os.Getenv("HOME"), ".omp", "agent", "eci"), filepath.Join(os.Getenv("HOME"), ".kimi-code")}
	var paths []string
	var frontiers []LiveFrontier
	var diagnostics []error
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		for _, name := range []string{"validate-bash.sh", "pretooluse-edit-dispatch.sh", "stop-gate.sh", "eci-active-gate.sh",
			"eci-review-gate.sh", "ate-orchestrator-gate.sh", "validate-edit-write.sh", "validate-apply-patch.sh"} {
			// Preserve raw root traversal; Join would invent a different configured hook when dotdot follows a link.
			hook := root + string(filepath.Separator) + "hooks" + string(filepath.Separator) + name
			trace := traceLiveHook(hook)
			if trace.Absent && trace.Entry == "" {
				if trace.Diagnostic != nil && !errors.Is(trace.Diagnostic, os.ErrNotExist) {
					diagnostics = append(diagnostics, fmt.Errorf("trace absent configured hook %q: %w", hook, trace.Diagnostic))
				}
				continue
			}
			paths = append(paths, trace.Dependencies...)
			if trace.Entry != "" {
				paths = append(paths, trace.Entry)
			}
			if trace.Resolved != "" {
				paths = append(paths, trace.Resolved)
			}
			if trace.Frontier != nil {
				trace.Frontier.Hook = hook
				frontiers = append(frontiers, *trace.Frontier)
				if trace.Frontier.VisibilityDiagnostic != nil {
					diagnostics = append(diagnostics, trace.Frontier.VisibilityDiagnostic)
				}
			}
			if trace.Diagnostic != nil {
				diagnostics = append(diagnostics, fmt.Errorf("trace configured hook %q: %w", hook, trace.Diagnostic))
			}
		}
	}
	for _, name := range []string{"ECI_PROOF_ROOT", "KIMI_PROOF_ROOT"} {
		root := os.Getenv(name)
		if filepath.IsAbs(root) {
			paths = append(paths, filepath.Clean(root))
			entry, err := resolvedParent(root)
			if err != nil {
				return LiveProtection{Paths: paths, Diagnostics: errors.Join(diagnostics...)}, fmt.Errorf("resolve proof root parent %q: %w", root, err)
			}
			paths = append(paths, entry)
			referent, err := filepath.EvalSymlinks(root)
			switch {
			case err == nil:
				paths = append(paths, referent)
			case errors.Is(err, os.ErrNotExist):
				// A not-yet-created proof root still protects its lexical entry.
			default:
				return LiveProtection{Paths: paths, Diagnostics: errors.Join(diagnostics...)}, fmt.Errorf("resolve proof root %q: %w", root, err)
			}
		}
	}

	return LiveProtection{Paths: paths, Frontiers: frontiers, Diagnostics: errors.Join(diagnostics...)}, nil
}

// livePathDependencies exposes the consumed entries and contextual trace error.
//
// Example: an invalid suffix keeps an earlier consumed alias alongside its PathError.
func livePathDependencies(path string) ([]string, error) {
	trace := traceLiveHook(path)
	return trace.Dependencies, trace.Diagnostic
}

// traceLiveHook retains dependencies until lookup completion or an observable frontier.
//
// Example: an absent overlong component is closed by a complete parent listing, without an errno allowlist.
func traceLiveHook(path string) LiveHookTrace {
	trace := LiveHookTrace{}
	if !filepath.IsAbs(path) {
		trace.Diagnostic = fmt.Errorf("live dependency path must be absolute: %q", path)
		return trace
	}
	separator := string(filepath.Separator)
	volume := filepath.VolumeName(path)
	resolved := volume + separator
	pending := strings.Split(path[len(volume):], separator)
	expansions := 0
	for len(pending) != 0 {
		component := pending[0]
		pending = pending[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		entry := filepath.Join(resolved, component)
		info, err := os.Lstat(entry)
		if err != nil {
			trace.Diagnostic = fmt.Errorf("inspect live dependency %q: %w", entry, err)
			entries, listingErr := os.ReadDir(resolved)
			if listingErr == nil {
				present := false
				for _, listed := range entries {
					if listed.Name() == component {
						present = true
					}
				}
				if !present {
					trace.Absent = true
					return trace
				}
			}
			if listingErr != nil {
				trace.Diagnostic = errors.Join(trace.Diagnostic, fmt.Errorf("inspect lookup frontier directory %q: %w", resolved, listingErr))
			}
			trace.Frontier = liveLookupFrontier(entry, resolved)
			return trace
		}
		if trace.Entry == "" && len(pending) == 0 {
			trace.Entry = entry
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if !info.IsDir() && len(pending) != 0 {
				trace.Absent = true
				return trace
			}
			resolved = entry
			continue
		}
		trace.Dependencies = append(trace.Dependencies, entry)
		if expansions == liveDependencySymlinkLimit {
			trace.Diagnostic = fmt.Errorf("live dependency expansion stopped at %q after %d links", entry, expansions)
			trace.Frontier = liveLookupFrontier(entry, resolved)
			return trace
		}
		target, err := os.Readlink(entry)
		if err != nil {
			trace.Diagnostic = fmt.Errorf("read live dependency %q: %w", entry, err)
			trace.Frontier = liveLookupFrontier(entry, resolved)
			return trace
		}
		expansions++
		if filepath.IsAbs(target) {
			volume = filepath.VolumeName(target)
			resolved = volume + separator
			target = target[len(volume):]
		}
		pending = append(strings.Split(target, separator), pending...)
	}
	trace.Resolved = resolved
	return trace
}

// liveLookupFrontier names the exact unresolved lookup and an owned visibility correction when proven.
//
// Example: a mode-000 directory owned by this actor supports chmod u+rx before rechecking.
func liveLookupFrontier(
	entry string,
	parent string,
) *LiveFrontier {
	frontier := &LiveFrontier{Path: entry, Directory: parent}
	info, err := os.Lstat(parent)
	if err != nil {
		frontier.VisibilityDiagnostic = fmt.Errorf("inspect owned visibility correction directory %q: %w", parent, err)
		return frontier
	}
	if !info.IsDir() {
		return frontier
	}
	status, ok := info.Sys().(*syscall.Stat_t)
	frontier.OwnedVisibility = ok && status.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0500 != 0500
	return frontier
}

// shellLiteral quotes a single exact native path for an executable Unix recovery command.
//
// Example: a path containing a quote remains one literal chmod operand.
func shellLiteral(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

// ownedVisibilityRefusal reports an unresolved lookup frontier whose owned visibility correction is proven.
//
// Example: a mode-000 hooks directory owned by this actor yields one literal chmod operand.
type ownedVisibilityRefusal struct {
	Leaf     string
	Ancestor string
	Frontier LiveFrontier
}

// Correction returns the exact owned lookup correction and the directory it changes.
//
// Example: a directory path containing a quote remains one literal chmod operand.
func (r ownedVisibilityRefusal) Correction() (command string, operand string) {
	return "chmod -- u+rx " + shellLiteral(r.Frontier.Directory), r.Frontier.Directory
}

// Error names the frontier and embeds the exact correction returned by Correction.
//
// Example: the rendered chmod command is the one a caller executes before rerunning restore.
func (r ownedVisibilityRefusal) Error() string {
	command, _ := r.Correction()
	return fmt.Sprintf("restore leaf %q has ancestor alias %q whose relation to configured hook %q remains unresolved at %q; restore exact owned lookup visibility (%s), then rerun this exact restore; use an owned move route only if the fresh check offers it; index-only restore remains available", r.Leaf, r.Ancestor, r.Frontier.Hook, r.Frontier.Path, command)
}

// protectedAncestorRefusal reports a restore ancestor that a live hook lookup consumes.
//
// Example: dir is protected when hooks/validate-bash.sh resolves through dir/nested/file.
type protectedAncestorRefusal struct {
	Leaf     string
	Ancestor string
}

// Error names the protected ancestor and the in-place recovery.
//
// Example: index-only restore stays available while the worktree leaf is refused.
func (r protectedAncestorRefusal) Error() string {
	return fmt.Sprintf("protected live target or ancestor %q for restore leaf %q; edit source in place and stage exact content; index-only restore remains available", r.Ancestor, r.Leaf)
}

// CheckPaths returns advisory discovery diagnostics separately from a selected-target denial.
//
// Example: index-only hook staging is allowed while worktree restoration is denied.
func (r Repository) CheckPaths(
	operation Operation,
	access PathAccess,
) (diagnostics error, denial error) {
	var protection LiveProtection
	if access == PhysicalWrite {
		var err error
		protection, err = liveProtectedPaths()
		if err != nil {
			return protection.Diagnostics, err
		}
	}
	return protection.Diagnostics, r.checkProtectedPaths(operation, access, protection)
}

// checkProtectedPaths rejects concrete logical and physical effects using discovered identities.
//
// Example: incomplete discovery elsewhere does not prevent restoration of an ordinary exact file.
func (r Repository) checkProtectedPaths(
	operation Operation,
	access PathAccess,
	protection LiveProtection,
) error {
	for _, name := range operation.Paths {
		candidate := filepath.Join(r.Worktree, name)
		if !containsPath(r.Worktree, candidate) {
			return fmt.Errorf("logical path leaves worktree: %q", name)
		}
		pointer := filepath.Join(r.Worktree, ".git")
		if containsPath(r.GitDir, candidate) || containsPath(candidate, r.GitDir) || candidate == r.Index || containsPath(pointer, candidate) || containsPath(candidate, pointer) {
			return fmt.Errorf("operation targets logical Git control state: %q", name)
		}
		if access == LogicalOnly {
			continue
		}
		if operation.Kind == Restore && access == PhysicalWrite {
			if err := r.checkRestoreAncestors(name, protection); err != nil {
				return err
			}
		}
		resolved, err := resolvedParent(candidate)
		if err != nil {
			return fmt.Errorf("resolve path %q: %w", name, err)
		}
		if !containsPath(r.Worktree, resolved) {
			return fmt.Errorf("path leaves worktree through an ancestor alias: %q", name)
		}
		if containsPath(r.GitDir, resolved) || containsPath(resolved, r.GitDir) || resolved == r.Index || containsPath(pointer, resolved) || containsPath(resolved, pointer) {
			return fmt.Errorf("operation targets Git control state: %q", name)
		}
		if access != PhysicalWrite {
			continue
		}
		for _, control := range protection.Paths {
			if resolved == control || containsPath(resolved, control) || containsPath(control, resolved) {
				return fmt.Errorf("protected live target or ancestor: %q; edit source in place and stage exact content", name)
			}
		}
	}
	return nil
}

// checkRestoreAncestors diagnoses the first lexical alias before resolving deeper parents.
//
// Example: dir pointing to a regular file is named before inspecting dir/nested/file.
func (r Repository) checkRestoreAncestors(
	name string,
	protection LiveProtection,
) error {
	ancestor := r.Worktree
	components := strings.Split(name, string(filepath.Separator))
	for _, component := range components[:len(components)-1] {
		ancestor = filepath.Join(ancestor, component)
		info, err := os.Lstat(ancestor)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil
		case err != nil:
			return fmt.Errorf("inspect restore ancestor %q for leaf %q: %w", ancestor, name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		for _, control := range protection.Paths {
			if containsPath(ancestor, control) || containsPath(control, ancestor) {
				return protectedAncestorRefusal{Leaf: name, Ancestor: ancestor}
			}
		}
		if len(protection.Frontiers) != 0 {
			frontier := protection.Frontiers[0]
			if frontier.OwnedVisibility {
				return ownedVisibilityRefusal{Leaf: name, Ancestor: ancestor, Frontier: frontier}
			}
			return fmt.Errorf("restore leaf %q has ancestor alias %q whose relation to configured hook %q remains unresolved at %q; resolve that exact lookup frontier in place, then rerun this exact restore before moving the alias; index-only restore remains available", name, ancestor, frontier.Hook, frontier.Path)
		}
		// Native restore can replace this unnamed entry, so advise only its owned lexical move.
		relative, err := filepath.Rel(r.Worktree, ancestor)
		if err != nil {
			return fmt.Errorf("name restore ancestor %q for leaf %q: %w", ancestor, name, err)
		}
		return fmt.Errorf("restore leaf %q has existing ancestor symlink %q; move that exact owned ancestor entry to an unused owned name (mv -- <ancestor> <saved-alias>), then retry restore; index-only restore remains available", name, relative)
	}
	return nil
}
