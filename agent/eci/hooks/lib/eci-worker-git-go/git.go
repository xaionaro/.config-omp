package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Streams provides operation IO without storing process-global streams in repository state.
//
// Example: a private fixture captures output while native Git retains its diagnostics.
type Streams struct {
	Input  io.Reader
	Output io.Writer
	Error  io.Writer
}

// ExecuteOperation validates literal targets before running a fixed Git operation.
//
// Example: unstage changes named index entries without touching worktree content.
func ExecuteOperation(
	ctx context.Context,
	operation Operation,
	streams Streams,
) error {
	repository, err := ResolveRepository(ctx, operation.Repo)
	if err != nil {
		return err
	}
	access, err := operationPathAccess(operation)
	if err != nil {
		return err
	}
	diagnostics, denial := repository.CheckPaths(operation, access)
	if diagnostics != nil {
		if _, err := fmt.Fprintf(streams.Error, "eci-worker-git: live protection discovery diagnostics: %v\n", diagnostics); err != nil {
			return fmt.Errorf("report live protection diagnostics before operation: %w", err)
		}
	}
	if denial != nil {
		return denial
	}
	if err := repository.CheckExactLeaves(ctx, operation, access); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(streams.Output, "eci-worker-git: worktree=%q gitdir=%q index=%q\n", repository.Worktree, repository.GitDir, repository.Index); err != nil {
		return fmt.Errorf("report repository identity before operation: %w", err)
	}
	arguments := []string{}
	switch operation.Kind {
	case StageContent:
		for _, path := range operation.Paths {
			info, err := os.Lstat(repository.Worktree + "/" + path)
			if err != nil {
				return fmt.Errorf("stage-content requires existing leaf %q: %w", path, err)
			}
			if info.IsDir() {
				return fmt.Errorf("stage-content requires exact file or symlink names, not directory %q", path)
			}
		}
		arguments = []string{"add", "--"}
	case Unstage:
		head := repository.Command(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
		if err := head.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				return fmt.Errorf("unstage HEAD lookup: %w", err)
			}
			branch, branchErr := repository.Command(ctx, "symbolic-ref", "--quiet", "HEAD").Output()
			if branchErr != nil {
				return fmt.Errorf("unstage unborn branch lookup: %w", branchErr)
			}
			branchName := string(bytes.TrimSuffix(branch, []byte("\n")))
			if branchName == "" {
				return fmt.Errorf("unstage has no known unborn branch")
			}
			refErr := repository.Command(ctx, "show-ref", "--verify", "--quiet", branchName).Run()
			if !errors.As(refErr, &exit) || exit.ExitCode() != 1 {
				return fmt.Errorf("unstage requires an existing HEAD or proven unborn branch")
			}
			arguments = []string{"rm", "--cached", "--ignore-unmatch", "--"}
			break
		}
		arguments = []string{"restore", "--source=HEAD", "--staged", "--"}
	case StageRemovals:
		for _, path := range operation.Paths {
			_, err := os.Lstat(filepath.Join(repository.Worktree, path))
			switch {
			case err == nil:
				return fmt.Errorf("stage-removals requires an absent worktree path: %q", path)
			case errors.Is(err, os.ErrNotExist):
			default:
				return fmt.Errorf("inspect stage-removals path %q: %w", path, err)
			}
			selected, err := repository.Command(ctx, "ls-files", "-z", "--error-unmatch", "--", path).Output()
			if err != nil {
				return fmt.Errorf("stage-removals requires an exact tracked entry %q: %w", path, err)
			}
			for _, name := range bytes.Split(bytes.TrimSuffix(selected, []byte{0}), []byte{0}) {
				if string(name) != path {
					return fmt.Errorf("stage-removals rejects directory selection %q; name each removed entry", path)
				}
			}
		}
		arguments = []string{"add", "-u", "--"}
	case StageHunks:
		return repository.ApplySelectedPatch(ctx, operation, streams)
	case Restore:
		arguments = []string{"restore", "--no-recurse-submodules"}
		if operation.Source != IndexSource {
			source := operation.TreeOID
			if operation.Source == HeadSource {
				source = "HEAD"
			}
			arguments = append(arguments, "--source="+source)
		}
		switch operation.Destination {
		case IndexDestination:
			arguments = append(arguments, "--staged")
		case WorktreeDestination:
			arguments = append(arguments, "--worktree")
		case BothDestination:
			arguments = append(arguments, "--staged", "--worktree")
		}
		arguments = append(arguments, "--")
	case Remove:
		switch operation.Destination {
		case IndexDestination:
			arguments = []string{"rm", "--cached", "--force", "--"}
		case BothDestination:
			arguments = []string{"rm", "--force", "--"}
		case WorktreeDestination:
			for _, path := range operation.Paths {
				if err := os.Remove(filepath.Join(repository.Worktree, path)); err != nil {
					return fmt.Errorf("remove worktree path %q: %w", path, err)
				}
			}
			return nil
		}
	case Move:
		_, err := os.Lstat(filepath.Join(repository.Worktree, operation.Paths[1]))
		switch {
		case err == nil:
			return fmt.Errorf("move requires an absent exact destination: %q", operation.Paths[1])
		case errors.Is(err, os.ErrNotExist):
		default:
			return fmt.Errorf("inspect move destination %q: %w", operation.Paths[1], err)
		}
		arguments = []string{"mv", "--"}
	case Commit:
		arguments = []string{"commit", "--message", operation.Message}
	default:
		return fmt.Errorf("unsupported operation kind")
	}
	arguments = append(arguments, operation.Paths...)
	command := repository.Command(ctx, arguments...)
	command.Stdin, command.Stdout, command.Stderr = streams.Input, streams.Output, streams.Error
	if err := command.Run(); err != nil {
		return fmt.Errorf("fixed Git operation failed: %w", err)
	}
	return nil
}

// ApplySelectedPatch freezes patch bytes and verifies every native-selected index target.
//
// Example: an extra unnamed patch file is rejected before the index is touched.
func (r Repository) ApplySelectedPatch(
	ctx context.Context,
	operation Operation,
	streams Streams,
) (_err error) {
	info, err := os.Stat(operation.PatchFile)
	if err != nil {
		return fmt.Errorf("inspect selected patch input %q: %w", operation.PatchFile, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("patch input must be an existing regular file")
	}
	patch, err := os.ReadFile(operation.PatchFile)
	if err != nil {
		return fmt.Errorf("read selected patch: %w", err)
	}
	temporary, err := os.MkdirTemp(r.GitDir, ".eci-worker-git-index-")
	if err != nil {
		return fmt.Errorf("create patch index preview: %w", err)
	}
	defer func() { _err = errors.Join(_err, os.RemoveAll(temporary)) }()
	preview := r
	preview.Index = filepath.Join(temporary, "index")
	index, err := os.ReadFile(r.Index)
	switch {
	case err == nil:
		if err := os.WriteFile(preview.Index, index, 0600); err != nil {
			return fmt.Errorf("copy patch preview index: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := preview.Command(ctx, "read-tree", "--empty").Run(); err != nil {
			return fmt.Errorf("initialize empty patch preview index: %w", err)
		}
	default:
		return fmt.Errorf("read patch starting index: %w", err)
	}
	baseline, err := preview.StageRecords(ctx)
	if err != nil {
		return fmt.Errorf("read preview starting stage records: %w", err)
	}
	apply := preview.Command(ctx, "apply", "--cached", "-")
	apply.Stdin = bytes.NewReader(patch)
	apply.Stdout, apply.Stderr = streams.Output, streams.Error
	if err := apply.Run(); err != nil {
		return fmt.Errorf("preview patch: %w", err)
	}
	after, err := preview.StageRecords(ctx)
	if err != nil {
		return fmt.Errorf("read complete preview selection: %w", err)
	}
	allowed := make(map[string]bool, len(operation.Paths))
	for _, name := range operation.Paths {
		allowed[name] = true
	}
	changed := false
	for name, records := range baseline {
		if after[name] == records {
			continue
		}
		changed = true
		if !allowed[name] {
			return fmt.Errorf("patch selects an unlisted target: %q", name)
		}
	}
	for name, records := range after {
		if baseline[name] == records {
			continue
		}
		changed = true
		if !allowed[name] {
			return fmt.Errorf("patch selects an unlisted target: %q", name)
		}
	}
	if !changed {
		return fmt.Errorf("patch has no changed target selection")
	}
	for _, arguments := range [][]string{{"apply", "--cached", "--check", "-"}, {"apply", "--cached", "-"}} {
		command := r.Command(ctx, arguments...)
		command.Stdin = bytes.NewReader(patch)
		command.Stdout, command.Stderr = streams.Output, streams.Error
		if err := command.Run(); err != nil {
			return fmt.Errorf("apply selected index patch: %w", err)
		}
	}
	return nil
}

// StageRecords reads every native stage record without requiring a resolved tree.
//
// Example: an unrelated conflict retains its stage, mode and object identity.
func (r Repository) StageRecords(ctx context.Context) (map[string]string, error) {
	output, err := r.Command(ctx, "ls-files", "--stage", "--sparse", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("read complete index stage records: %w", err)
	}
	return parseStageRecords(output)
}

// parseStageRecords groups complete NUL records by their literal path.
//
// Example: three conflict stages remain distinct records under one path.
func parseStageRecords(output []byte) (map[string]string, error) {
	records := make(map[string]string)
	if len(output) == 0 {
		return records, nil
	}
	if output[len(output)-1] != 0 {
		return nil, fmt.Errorf("incomplete native stage records")
	}
	for _, record := range bytes.Split(output[:len(output)-1], []byte{0}) {
		metadata, path, ok := strings.Cut(string(record), "\t")
		if !ok || len(strings.Fields(metadata)) != 3 || path == "" {
			return nil, fmt.Errorf("invalid native stage record %q", record)
		}
		records[path] += string(record) + "\x00"
	}
	return records, nil
}

// CheckExactLeaves rejects native directory selections and implicit gitlink worktree effects.
//
// Example: a deleted directory is rejected from source entries even when absent on disk.
func (r Repository) CheckExactLeaves(
	ctx context.Context,
	operation Operation,
	access PathAccess,
) error {
	if operation.Kind == Commit {
		return nil
	}
	records, err := r.StageRecords(ctx)
	if err != nil {
		return err
	}
	source := ""
	switch {
	case operation.Kind == Unstage || (operation.Kind == Restore && operation.Source == HeadSource):
		source = "HEAD"
	case operation.Kind == Restore && operation.Source == TreeSource:
		source = operation.TreeOID
	}
	if source != "" {
		output, err := r.Command(ctx, "ls-tree", "-r", "-t", "-z", source).Output()
		if err != nil {
			// An unborn branch has no source tree; the operation's later branch proof handles it.
			if operation.Kind != Unstage {
				return fmt.Errorf("read exact source selection: %w", err)
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 128 {
				return fmt.Errorf("read unstage source selection: %w", err)
			}
		} else {
			for _, record := range bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0}) {
				if len(record) == 0 {
					continue
				}
				metadata, path, ok := strings.Cut(string(record), "\t")
				fields := strings.Fields(metadata)
				if !ok || len(fields) != 3 {
					return fmt.Errorf("invalid source tree record %q", record)
				}
				records[path] += fields[0] + " " + fields[2] + " 0\t" + path + "\x00"
			}
		}
	}
	for _, name := range operation.Paths {
		if access != LogicalOnly {
			info, err := os.Lstat(filepath.Join(r.Worktree, name))
			switch {
			case err == nil:
				if info.IsDir() {
					return fmt.Errorf("operation requires an exact file or symlink leaf, not directory %q", name)
				}
				if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
					return fmt.Errorf("unsupported worktree leaf %q", name)
				}
			case errors.Is(err, os.ErrNotExist):
				if (operation.Kind == Remove && operation.Destination == WorktreeDestination) || (operation.Kind == Move && name == operation.Paths[0]) {
					return fmt.Errorf("operation requires existing worktree leaf %q: %w", name, err)
				}
			default:
				return fmt.Errorf("inspect exact worktree leaf %q: %w", name, err)
			}
		}
		for path, entries := range records {
			if strings.HasPrefix(path, name+"/") {
				return fmt.Errorf("operation selects unnamed descendant %q through %q", path, name)
			}
			if path != name {
				continue
			}
			for _, record := range strings.Split(strings.TrimSuffix(entries, "\x00"), "\x00") {
				mode, _, _ := strings.Cut(record, " ")
				if mode == "040000" {
					return fmt.Errorf("operation rejects directory or sparse-directory entry %q", name)
				}
				if access == PhysicalWrite && mode == "160000" {
					return fmt.Errorf("operation rejects implicit gitlink worktree metadata changes at %q", name)
				}
			}
		}
		requiresEntry := operation.Kind == Restore || operation.Kind == Unstage || operation.Kind == StageRemovals ||
			(operation.Kind == Remove && operation.Destination != WorktreeDestination) ||
			(operation.Kind == Move && name == operation.Paths[0])
		if requiresEntry && records[name] == "" {
			return fmt.Errorf("operation has no exact index or source entry %q", name)
		}
	}
	return nil
}

// operationPathAccess classifies logical selection, physical reads/probes and physical writes once.
//
// Example: cached hunk application reads the patch and index while leaving a selected worktree directory untouched.
func operationPathAccess(operation Operation) (PathAccess, error) {
	switch operation.Kind {
	case StageContent, StageRemovals:
		return PhysicalReadProbe, nil
	case Move:
		return PhysicalWrite, nil
	case Unstage, StageHunks, Commit:
		return LogicalOnly, nil
	case Restore, Remove:
		switch operation.Destination {
		case IndexDestination:
			return LogicalOnly, nil
		case WorktreeDestination, BothDestination:
			return PhysicalWrite, nil
		default:
			return LogicalOnly, fmt.Errorf("unsupported destination for path access")
		}
	default:
		return LogicalOnly, fmt.Errorf("unsupported operation for path access")
	}
}
