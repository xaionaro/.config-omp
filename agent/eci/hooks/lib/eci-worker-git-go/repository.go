package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repository identifies actual state independently of the invocation spelling.
//
// Example: a persisted core.worktree may locate the index outside the worktree.
type Repository struct {
	Worktree string
	GitDir   string
	Index    string
}

// gitEnvironment removes pathspec expansion and fixes the selected index identity.
//
// Example: GIT_ICASE_PATHSPECS cannot expand a typed literal filename.
func gitEnvironment(index string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS":
			continue
		case "GIT_INDEX_FILE":
			if index != "" {
				continue
			}
		}
		environment = append(environment, entry)
	}
	if index != "" {
		environment = append(environment, "GIT_INDEX_FILE="+index)
	}
	return environment
}

// ResolveRepository asks native Git for actual worktree, Git directory and index.
//
// Example: --repo may point at a directory whose core.worktree is persisted elsewhere.
func ResolveRepository(
	ctx context.Context,
	location string,
) (Repository, error) {
	repository := Repository{}
	if os.Getenv("GIT_DIR") != "" || os.Getenv("GIT_WORK_TREE") != "" {
		return repository, fmt.Errorf("use --repo without GIT_DIR/GIT_WORK_TREE overrides")
	}
	values := make([]string, 0, 3)
	for _, arguments := range [][]string{
		{"rev-parse", "--show-toplevel"},
		{"rev-parse", "--absolute-git-dir"},
		{"rev-parse", "--path-format=absolute", "--git-path", "index"},
	} {
		command := exec.CommandContext(ctx, "/usr/bin/git", append([]string{"-C", location}, arguments...)...)
		command.Env = gitEnvironment("")
		output, err := command.Output()
		if err != nil {
			return repository, fmt.Errorf("resolve repository identity: %w", err)
		}
		value := strings.TrimSuffix(string(output), "\n")
		if !filepath.IsAbs(value) {
			return repository, fmt.Errorf("Git returned nonabsolute identity")
		}
		values = append(values, filepath.Clean(value))
	}
	worktree, err := filepath.EvalSymlinks(values[0])
	if err != nil {
		return repository, fmt.Errorf("resolve worktree: %w", err)
	}
	gitDir, err := filepath.EvalSymlinks(values[1])
	if err != nil {
		return repository, fmt.Errorf("resolve Git directory: %w", err)
	}
	return Repository{Worktree: worktree, GitDir: gitDir, Index: values[2]}, nil
}

// Command constructs a fixed repository invocation using literal path semantics.
//
// Example: add -- literal[star]* selects that one name.
func (r Repository) Command(
	ctx context.Context,
	arguments ...string,
) *exec.Cmd {
	prefix := []string{"--literal-pathspecs", "--no-pager", "-c", "submodule.recurse=false", "-C", r.Worktree,
		"--git-dir=" + r.GitDir, "--work-tree=" + r.Worktree}
	command := exec.CommandContext(ctx, "/usr/bin/git", append(prefix, arguments...)...)
	command.Env = gitEnvironment(r.Index)
	return command
}
