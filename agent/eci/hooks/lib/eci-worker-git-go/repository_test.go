package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureGit executes fixture setup or reads native Git state.
//
// Example: write-tree captures semantic index identity independently of stat refresh.
func fixtureGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("/usr/bin/git", append([]string{"-C", root}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSuffix(string(output), "\n")
}

// fixtureRepository creates a committed private provider repository.
//
// Example: its live hook can be staged but cannot be overwritten by restore.
func fixtureRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "provider")
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file.txt", "unrelated.txt", "hooks/validate-bash.sh", "literal[star]*"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("base\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Worker Git Test")
	fixtureGit(t, root, "config", "user.email", "worker-git@example.invalid")
	fixtureGit(t, root, "add", "--", ".")
	fixtureGit(t, root, "commit", "-qm", "fixture")
	t.Setenv("ECI_HOME", root)
	t.Setenv("ECI_CONFIGURED_HOME", root)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return root
}

// executeFixtureOperation runs the typed public operation with captured streams.
//
// Example: its errors are checked before observing actual index blobs.
func executeFixtureOperation(t *testing.T, root string, arguments ...string) error {
	t.Helper()
	operation, err := ParseOperation(append([]string{"--repo", root}, arguments...))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	return ExecuteOperation(context.Background(), operation, Streams{Output: &output, Error: &output})
}

// TestStageUnstageChangesOnlySelectedIndexContent proves the first operation slice.
//
// Example: an unrelated staged change survives both operations.
func TestStageUnstageChangesOnlySelectedIndexContent(t *testing.T) {
	root := fixtureRepository(t)
	for _, name := range []string{"file.txt", "unrelated.txt", "literal[star]*"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("changed\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, root, "add", "--", "unrelated.txt")
	head := fixtureGit(t, root, "rev-parse", "HEAD")
	if err := executeFixtureOperation(t, root, "stage-content", "--", "file.txt", "literal[star]*"); err != nil {
		t.Fatal(err)
	}
	if got := fixtureGit(t, root, "show", ":file.txt"); got != "changed" {
		t.Fatalf("index=%q", got)
	}
	if got := fixtureGit(t, root, "show", ":literal[star]*"); got != "changed" {
		t.Fatalf("literal index=%q", got)
	}
	if err := executeFixtureOperation(t, root, "unstage", "--", "file.txt"); err != nil {
		t.Fatal(err)
	}
	if got := fixtureGit(t, root, "show", ":file.txt"); got != "base" {
		t.Fatalf("unstaged index=%q", got)
	}
	if got := fixtureGit(t, root, "show", ":unrelated.txt"); got != "changed" {
		t.Fatalf("unrelated index=%q", got)
	}
	if got := fixtureGit(t, root, "rev-parse", "HEAD"); got != head {
		t.Fatal("HEAD changed")
	}
	content, err := os.ReadFile(filepath.Join(root, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "changed\n" {
		t.Fatal("worktree changed")
	}
}

// TestProtectedWorktreeWritesPreserveState distinguishes staging from destructive writes.
//
// Example: a hooks-directory move cannot remove its protected descendants.
func TestProtectedWorktreeWritesPreserveState(t *testing.T) {
	root := fixtureRepository(t)
	protected := filepath.Join(root, "hooks/validate-bash.sh")
	if err := os.WriteFile(protected, []byte("dirty hook\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "stage-content", "--", "hooks/validate-bash.sh"); err != nil {
		t.Fatal(err)
	}
	index := fixtureGit(t, root, "write-tree")
	head := fixtureGit(t, root, "rev-parse", "HEAD")
	for _, arguments := range [][]string{
		{"restore", "--source", "head", "--destination", "worktree", "--", "hooks/validate-bash.sh"},
		{"remove", "--destination", "worktree", "--", "hooks"},
		{"move", "--", "hooks", "old-hooks"},
	} {
		if err := executeFixtureOperation(t, root, arguments...); err == nil {
			t.Fatalf("protected operation admitted: %v", arguments)
		}
	}
	content, err := os.ReadFile(protected)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "dirty hook\n" {
		t.Fatal("protected content changed")
	}
	if fixtureGit(t, root, "write-tree") != index || fixtureGit(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("repository state changed")
	}
}

// TestRepositoryIdentityUsesPersistedWorktree resolves the actual target instead of invocation directory.
//
// Example: a .git directory in another directory still identifies this worktree and index.
func TestRepositoryIdentityUsesPersistedWorktree(t *testing.T) {
	root := fixtureRepository(t)
	invocation := t.TempDir()
	invocation, err := filepath.EvalSymlinks(invocation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(invocation, ".git")); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, invocation, "config", "core.worktree", root)
	repository, err := ResolveRepository(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if repository.Worktree != root || repository.GitDir != filepath.Join(invocation, ".git") || repository.Index != filepath.Join(invocation, ".git/index") {
		t.Fatalf("identity=%+v", repository)
	}
}

// TestUnstageUnbornPreservesWorktree supports the first prepared index before any commit.
//
// Example: unstaging a new repository file removes its index entry only.
func TestUnstageUnbornPreservesWorktree(t *testing.T) {
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "add", "--", "new.txt")
	if err := executeFixtureOperation(t, root, "unstage", "--", "new.txt"); err != nil {
		t.Fatal(err)
	}
	if fixtureGit(t, root, "ls-files", "--", "new.txt") != "" {
		t.Fatal("unborn index entry retained")
	}
	if content, err := os.ReadFile(filepath.Join(root, "new.txt")); err != nil || string(content) != "new\n" {
		t.Fatal("unstage changed new worktree content")
	}
}

// TestAncestorAliasAndGitControlsAreRejected protects actual resolved targets.
//
// Example: a directory symlink cannot route removal to an external live hook.
func TestAncestorAliasAndGitControlsAreRejected(t *testing.T) {
	root := fixtureRepository(t)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "file.txt"), []byte("outside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"outside/file.txt", ".git/index", ".git"} {
		if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", path); err == nil {
			t.Fatalf("unsafe target admitted: %q", path)
		}
	}
	if content, err := os.ReadFile(filepath.Join(external, "file.txt")); err != nil || string(content) != "outside\n" {
		t.Fatal("external alias target changed")
	}
}

// TestAlternateIndexIdentityIsCanonical proves the actual shared-index lookup key.
//
// Example: a symlink spelling names the same index and must report its referent.
func TestAlternateIndexIdentityIsCanonical(t *testing.T) {
	root := fixtureRepository(t)
	actual := filepath.Join(root, ".git", "index")
	alias := filepath.Join(t.TempDir(), "index-alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_INDEX_FILE", alias)
	repository, err := ResolveRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if repository.Index != actual {
		t.Fatalf("index identity is not canonical: got %q want %q", repository.Index, actual)
	}
}
