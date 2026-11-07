package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStageRemovalsRequiresExactTrackedEntries rejects recursive directory selection.
//
// Example: naming a removed directory cannot stage both descendant removals.
func TestStageRemovalsRequiresExactTrackedEntries(
	t *testing.T,
) {
	root := fixtureRepository(t)
	if err := os.Mkdir(filepath.Join(root, "removed-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, "removed-dir", name), []byte("base\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, root, "add", "--", "removed-dir")
	index := fixtureGit(t, root, "write-tree")
	if err := os.RemoveAll(filepath.Join(root, "removed-dir")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "stage-removals", "--", "removed-dir"); err == nil {
		t.Fatal("directory removal swept unnamed descendants")
	}
	if fixtureGit(t, root, "write-tree") != index {
		t.Fatal("directory rejection changed index")
	}
	if err := executeFixtureOperation(t, root, "stage-removals", "--", "removed-dir/a"); err != nil {
		t.Fatal(err)
	}
	if fixtureGit(t, root, "show", ":removed-dir/b") != "base" {
		t.Fatal("exact removal changed sibling")
	}
}

// TestFixedMutationOperationsPreserveUnselectedState exercises every remaining named effect.
//
// Example: index-only removal cannot delete the corresponding live file.
func TestFixedMutationOperationsPreserveUnselectedState(
	t *testing.T,
) {
	for _, kind := range []string{"stage-removals", "restore-index", "restore-worktree", "restore-both", "remove-index", "remove-worktree", "remove-both", "move", "commit"} {
		t.Run(kind, func(t *testing.T) {
			root := fixtureRepository(t)
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			unrelated := fixtureGit(t, root, "show", ":unrelated.txt")
			var arguments []string
			switch kind {
			case "stage-removals":
				if err := os.Remove(filepath.Join(root, "file.txt")); err != nil {
					t.Fatal(err)
				}
				arguments = []string{"stage-removals", "--", "file.txt"}
			case "restore-index", "restore-worktree", "restore-both":
				if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("changed\n"), 0644); err != nil {
					t.Fatal(err)
				}
				fixtureGit(t, root, "add", "--", "file.txt")
				destination := kind[len("restore-"):]
				arguments = []string{"restore", "--source", "head", "--destination", destination, "--", "file.txt"}
			case "remove-index", "remove-worktree", "remove-both":
				arguments = []string{"remove", "--destination", kind[len("remove-"):], "--", "file.txt"}
			case "move":
				arguments = []string{"move", "--", "file.txt", "moved.txt"}
			case "commit":
				if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("changed\n"), 0644); err != nil {
					t.Fatal(err)
				}
				fixtureGit(t, root, "add", "--", "file.txt")
				arguments = []string{"commit", "--message", "prepared index checkpoint"}
			}
			if err := executeFixtureOperation(t, root, arguments...); err != nil {
				t.Fatal(err)
			}
			if fixtureGit(t, root, "show", ":unrelated.txt") != unrelated {
				t.Fatal("unselected index changed")
			}
			content, fileErr := os.ReadFile(filepath.Join(root, "file.txt"))
			switch kind {
			case "remove-index":
				if fileErr != nil || string(content) != "base\n" {
					t.Fatal("index removal changed live file")
				}
				if fixtureGit(t, root, "ls-files", "--", "file.txt") != "" {
					t.Fatal("index removal kept entry")
				}
			case "stage-removals", "remove-worktree", "remove-both", "move":
				if !os.IsNotExist(fileErr) {
					t.Fatalf("expected absent original: %v", fileErr)
				}
			case "restore-index":
				if fileErr != nil || string(content) != "changed\n" || fixtureGit(t, root, "show", ":file.txt") != "base" {
					t.Fatal("index restore changed worktree or missed index")
				}
			case "restore-worktree", "restore-both":
				if fileErr != nil || string(content) != "base\n" {
					t.Fatal("worktree restoration missed content")
				}
			case "commit":
				if fixtureGit(t, root, "show", "HEAD:file.txt") != "changed" || fixtureGit(t, root, "rev-parse", "HEAD^") != head {
					t.Fatal("prepared commit did not append expected content")
				}
			}
			if kind != "commit" && fixtureGit(t, root, "rev-parse", "HEAD") != head {
				t.Fatal("unexpected HEAD mutation")
			}
			if kind == "move" && fixtureGit(t, root, "show", ":moved.txt") != "base" {
				t.Fatal("move missed destination")
			}
			if kind == "remove-worktree" && fixtureGit(t, root, "show", ":file.txt") != "base" {
				t.Fatal("worktree removal changed index")
			}
		})
	}
}

// TestStageHunksChecksEntireFrozenPatch preserves unnamed staged entries and worktree content.
//
// Example: a patch that includes an unlisted file is rejected before index mutation.
func TestStageHunksChecksEntireFrozenPatch(
	t *testing.T,
) {
	root := fixtureRepository(t)
	patch := filepath.Join(t.TempDir(), "selected.patch")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	diff := fixtureGit(t, root, "diff", "--", "file.txt") + "\n"
	if err := os.WriteFile(patch, []byte(diff), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "stage-hunks", "--patch-file", patch, "--", "file.txt"); err != nil {
		t.Fatal(err)
	}
	if fixtureGit(t, root, "show", ":file.txt") != "changed" {
		t.Fatal("patch missed index")
	}
	index := fixtureGit(t, root, "write-tree")
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("unlisted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	diff = fixtureGit(t, root, "diff", "--", "unrelated.txt") + "\n"
	if err := os.WriteFile(patch, []byte(diff), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "stage-hunks", "--patch-file", patch, "--", "file.txt"); err == nil {
		t.Fatal("unlisted patch admitted")
	}
	if fixtureGit(t, root, "write-tree") != index {
		t.Fatal("rejected patch changed index")
	}
}

// TestStageHunksRejectsUnlistedRenameSource includes both endpoints in patch ownership.
//
// Example: naming only the destination cannot authorize removal of the old index entry.
func TestStageHunksRejectsUnlistedRenameSource(
	t *testing.T,
) {
	root := fixtureRepository(t)
	fixtureGit(t, root, "mv", "--", "file.txt", "renamed.txt")
	diff := fixtureGit(t, root, "diff", "--cached", "--find-renames", "HEAD") + "\n"
	fixtureGit(t, root, "restore", "--source=HEAD", "--staged", "--", "file.txt", "renamed.txt")
	index := fixtureGit(t, root, "write-tree")
	patch := filepath.Join(t.TempDir(), "rename.patch")
	if err := os.WriteFile(patch, []byte(diff), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "stage-hunks", "--patch-file", patch, "--", "renamed.txt"); err == nil {
		t.Fatal("unlisted rename source admitted")
	}
	if fixtureGit(t, root, "write-tree") != index {
		t.Fatal("rejected rename patch changed index")
	}
}

// TestExactLeavesRejectDirectoriesBeforeAnyWrite checks every operand before mutation.
//
// Example: an invalid second operand preserves the first selected file.
func TestExactLeavesRejectDirectoriesBeforeAnyWrite(
	t *testing.T,
) {
	for _, args := range [][]string{
		{"remove", "--destination", "worktree", "--", "file.txt", "dir"},
		{"remove", "--destination", "index", "--", "dir"},
		{"restore", "--source", "head", "--destination", "index", "--", "dir"},
		{"unstage", "--", "dir"},
		{"move", "--", "dir", "moved"},
	} {
		// Each subtest exercises an independent native operation.
		t.Run(args[0]+args[1], func(t *testing.T) {
			root := fixtureRepository(t)
			if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "dir/a"), []byte("child\n"), 0644); err != nil {
				t.Fatal(err)
			}
			fixtureGit(t, root, "add", "--", "dir/a")
			fixtureGit(t, root, "commit", "-qm", "directory fixture")
			before := fixtureGit(t, root, "ls-files", "--stage", "-z")
			if err := executeFixtureOperation(t, root, args...); err == nil {
				t.Fatal("directory selection admitted")
			}
			if after := fixtureGit(t, root, "ls-files", "--stage", "-z"); after != before {
				t.Fatal("index changed")
			}
			if content, err := os.ReadFile(filepath.Join(root, "file.txt")); err != nil || string(content) != "base\n" {
				t.Fatal("earlier operand changed")
			}
			if content, err := os.ReadFile(filepath.Join(root, "dir/a")); err != nil || string(content) != "child\n" {
				t.Fatal("unnamed descendant changed")
			}
		})
	}
}

// TestLiveHookReferentAndLinkedPointerProtection protects both hook identities and administration.
//
// Example: the live symlink's referent cannot be removed through its ordinary source name.
func TestLiveHookReferentAndLinkedPointerProtection(
	t *testing.T,
) {
	root := fixtureRepository(t)
	if err := os.Remove(filepath.Join(root, "hooks/validate-bash.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../file.txt", filepath.Join(root, "hooks/validate-bash.sh")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "file.txt"); err == nil {
		t.Fatal("live referent removal admitted")
	}
	if content, err := os.ReadFile(filepath.Join(root, "file.txt")); err != nil || string(content) != "base\n" {
		t.Fatal("referent changed")
	}
	linked := filepath.Join(t.TempDir(), "linked")
	fixtureGit(t, root, "worktree", "add", "-q", "-b", "linked", linked)
	pointer, err := os.ReadFile(filepath.Join(linked, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, linked, "remove", "--destination", "worktree", "--", ".git"); err == nil {
		t.Fatal("linked pointer removal admitted")
	}
	if after, err := os.ReadFile(filepath.Join(linked, ".git")); err != nil || string(after) != string(pointer) {
		t.Fatal("pointer changed")
	}
}

// TestGitlinkWorktreeEffectsAreRejected preserves submodule metadata.
//
// Example: native mv of a gitlink is rejected even when its working directory is absent.
func TestGitlinkWorktreeEffectsAreRejected(
	t *testing.T,
) {
	for _, args := range [][]string{{"remove", "--destination", "both", "--", "sub"}, {"move", "--", "sub", "destination"}, {"restore", "--source", "head", "--destination", "worktree", "--", "sub"}} {
		// Each fixture starts with the same exact gitlink record.
		t.Run(args[0], func(t *testing.T) {
			root := fixtureRepository(t)
			oid := fixtureGit(t, root, "rev-parse", "HEAD")
			fixtureGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+oid+",sub")
			fixtureGit(t, root, "commit", "-qm", "gitlink")
			before := fixtureGit(t, root, "ls-files", "--stage", "-z")
			if err := executeFixtureOperation(t, root, args...); err == nil {
				t.Fatal("worktree gitlink admitted")
			}
			if fixtureGit(t, root, "ls-files", "--stage", "-z") != before {
				t.Fatal("gitlink index changed")
			}
		})
	}
}

// TestHunkPreviewPreservesUnrelatedConflictStages supports patches beside unmerged entries.
//
// Example: selected regular content can be staged without resolving an unrelated conflict.
func TestHunkPreviewPreservesUnrelatedConflictStages(
	t *testing.T,
) {
	root := fixtureRepository(t)
	oid := fixtureGit(t, root, "rev-parse", ":unrelated.txt")
	fixtureGit(t, root, "update-index", "--force-remove", "--", "unrelated.txt")
	repository, err := ResolveRepository(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	command := repository.Command(context.Background(), "update-index", "--index-info")
	command.Stdin = strings.NewReader("100644 " + oid + " 1\tunrelated.txt\n100644 " + oid + " 2\tunrelated.txt\n100644 " + oid + " 3\tunrelated.txt\n")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("conflict fixture: %v %s", err, output)
	}
	before := fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "unrelated.txt")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(t.TempDir(), "patch")
	if err := os.WriteFile(patch, []byte(fixtureGit(t, root, "diff", "--", "file.txt")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "stage-hunks", "--patch-file", patch, "--", "file.txt"); err != nil {
		t.Fatal(err)
	}
	if fixtureGit(t, root, "show", ":file.txt") != "changed" {
		t.Fatal("selected content missing")
	}
	if fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "unrelated.txt") != before {
		t.Fatal("unrelated stages changed")
	}
}

// TestAllOperandsRejectMissingLeafBeforeRemoval prevents partial manual operations.
//
// Example: an absent second leaf cannot allow deletion of the first leaf.
func TestAllOperandsRejectMissingLeafBeforeRemoval(
	t *testing.T,
) {
	root := fixtureRepository(t)
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "file.txt", "missing.txt"); err == nil {
		t.Fatal("missing operand admitted")
	}
	if content, err := os.ReadFile(filepath.Join(root, "file.txt")); err != nil || string(content) != "base\n" {
		t.Fatal("first leaf removed before validation")
	}
}

// TestOrdinarySymlinkRemovalPreservesProtectedReferent keeps lexical leaf semantics.
//
// Example: deleting a disposable alias to a live hook leaves the live hook intact.
func TestOrdinarySymlinkRemovalPreservesProtectedReferent(
	t *testing.T,
) {
	root := fixtureRepository(t)
	if err := os.Symlink("hooks/validate-bash.sh", filepath.Join(root, "ordinary-link")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "ordinary-link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "ordinary-link")); !os.IsNotExist(err) {
		t.Fatal("lexical symlink retained")
	}
	if content, err := os.ReadFile(filepath.Join(root, "hooks/validate-bash.sh")); err != nil || string(content) != "base\n" {
		t.Fatal("referent changed")
	}
}

// TestSourceOnlyDirectorySelectionIsRejected checks the source and index union.
//
// Example: a removed index directory cannot hide descendant selection from HEAD.
func TestSourceOnlyDirectorySelectionIsRejected(
	t *testing.T,
) {
	root := fixtureRepository(t)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir/a"), []byte("child\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "add", "--", "dir/a")
	fixtureGit(t, root, "commit", "-qm", "source directory")
	fixtureGit(t, root, "rm", "-r", "--", "dir")
	before := fixtureGit(t, root, "ls-files", "--stage", "-z")
	if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "both", "--", "dir"); err == nil {
		t.Fatal("source-only directory admitted")
	}
	if fixtureGit(t, root, "ls-files", "--stage", "-z") != before {
		t.Fatal("source-only rejection changed index")
	}
	if _, err := os.Lstat(filepath.Join(root, "dir")); !os.IsNotExist(err) {
		t.Fatal("unnamed descendants restored")
	}
}

// TestStageContentRejectsDirectoryToFileSweep checks existing index descendants.
//
// Example: replacing a tracked directory with a file cannot stage unnamed removals.
func TestStageContentRejectsDirectoryToFileSweep(
	t *testing.T,
) {
	root := fixtureRepository(t)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir/a"), []byte("child\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "add", "--", "dir/a")
	before := fixtureGit(t, root, "ls-files", "--stage", "-z")
	if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir"), []byte("replacement\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "stage-content", "--", "dir"); err == nil {
		t.Fatal("unnamed descendant sweep admitted")
	}
	if fixtureGit(t, root, "ls-files", "--stage", "-z") != before {
		t.Fatal("descendant index records changed")
	}
}

// TestIndexOnlyGitlinkRemovalPreservesWorktreeMetadata proves the bounded gitlink domain.
//
// Example: an absent submodule's cached removal changes only its exact index entry.
func TestIndexOnlyGitlinkRemovalPreservesWorktreeMetadata(
	t *testing.T,
) {
	root := fixtureRepository(t)
	oid := fixtureGit(t, root, "rev-parse", "HEAD")
	fixtureGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+oid+",sub")
	if err := os.WriteFile(filepath.Join(root, ".gitmodules"), []byte("metadata\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "file.txt", "unrelated.txt")
	head := fixtureGit(t, root, "rev-parse", "HEAD")
	if err := executeFixtureOperation(t, root, "remove", "--destination", "index", "--", "sub"); err != nil {
		t.Fatal(err)
	}
	if fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "sub") != "" {
		t.Fatal("gitlink retained")
	}
	if fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "file.txt", "unrelated.txt") != before || fixtureGit(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("unselected state changed")
	}
	if content, err := os.ReadFile(filepath.Join(root, ".gitmodules")); err != nil || string(content) != "metadata\n" {
		t.Fatal("worktree metadata changed")
	}
}

// TestLinkedWorktreePointerIsProtected preserves the administrative entry itself.
//
// Example: removing .git cannot disconnect a linked worktree from its Git directory.
func TestLinkedWorktreePointerIsProtected(
	t *testing.T,
) {
	root := fixtureRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	fixtureGit(t, root, "worktree", "add", "-q", "-b", "linked", linked)
	pointer, err := os.ReadFile(filepath.Join(linked, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, linked, "remove", "--destination", "worktree", "--", ".git"); err == nil {
		t.Fatal("linked pointer removal admitted")
	}
	if after, err := os.ReadFile(filepath.Join(linked, ".git")); err != nil || string(after) != string(pointer) {
		t.Fatal("pointer changed")
	}
}

// TestLiveHookEntryUnderAliasedParentIsProtected preserves the physical symlink entry.
//
// Example: an aliased hooks directory and a symlink hook require separate entry and referent identities.
func TestLiveHookEntryUnderAliasedParentIsProtected(
	t *testing.T,
) {
	root := fixtureRepository(t)
	if err := os.Rename(filepath.Join(root, "hooks"), filepath.Join(root, "physical-hooks")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("physical-hooks", filepath.Join(root, "hooks")); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "physical-hooks/validate-bash.sh")
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../file.txt", hook); err != nil {
		t.Fatal(err)
	}
	before := fixtureGit(t, root, "ls-files", "--stage", "-z")
	head := fixtureGit(t, root, "rev-parse", "HEAD")
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "physical-hooks/validate-bash.sh"); err == nil {
		t.Fatal("physical live hook entry removal admitted")
	}
	if target, err := os.Readlink(hook); err != nil || target != "../file.txt" {
		t.Fatal("live hook entry changed")
	}
	if err := os.Symlink("file.txt", filepath.Join(root, "ordinary-link")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "ordinary-link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "ordinary-link")); !os.IsNotExist(err) {
		t.Fatal("ordinary symlink removal failed")
	}
	if content, err := os.ReadFile(filepath.Join(root, "file.txt")); err != nil || string(content) != "base\n" {
		t.Fatal("hook referent changed")
	}
	if fixtureGit(t, root, "ls-files", "--stage", "-z") != before || fixtureGit(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("index or HEAD changed")
	}
}

// TestIndexOnlyOperationsPreserveUntouchedDirectory ignores physical leaf shape when only the index changes.
//
// Example: restoring an exact index file leaves an unrelated directory at that worktree path intact.
func TestIndexOnlyOperationsPreserveUntouchedDirectory(
	t *testing.T,
) {
	for _, kind := range []string{"restore-index", "remove-index", "unstage", "stage-hunks"} {
		// Each public operation starts with the same exact index leaf and unrelated physical directory.
		t.Run(kind, func(t *testing.T) {
			root := fixtureRepository(t)
			if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("changed\n"), 0644); err != nil {
				t.Fatal(err)
			}
			patch := filepath.Join(t.TempDir(), "selected.patch")
			if err := os.WriteFile(patch, []byte(fixtureGit(t, root, "diff", "--", "file.txt")+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind != "stage-hunks" {
				fixtureGit(t, root, "add", "--", "file.txt")
			}
			if err := os.Remove(filepath.Join(root, "file.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "file.txt"), 0755); err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(root, "file.txt/untouched")
			if err := os.WriteFile(child, []byte("unrelated child\n"), 0640); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("unrelated staged\n"), 0644); err != nil {
				t.Fatal(err)
			}
			fixtureGit(t, root, "add", "--", "unrelated.txt")
			unrelated := fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "unrelated.txt")
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			args := []string{"unstage", "--", "file.txt"}
			switch kind {
			case "restore-index":
				args = []string{"restore", "--source", "head", "--destination", "index", "--", "file.txt"}
			case "remove-index":
				args = []string{"remove", "--destination", "index", "--", "file.txt"}
			case "stage-hunks":
				args = []string{"stage-hunks", "--patch-file", patch, "--", "file.txt"}
			}
			if err := executeFixtureOperation(t, root, args...); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "remove-index":
				if fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "file.txt") != "" {
					t.Fatal("selected entry retained")
				}
			case "stage-hunks":
				if fixtureGit(t, root, "show", ":file.txt") != "changed" {
					t.Fatal("selected patch missing")
				}
			default:
				if fixtureGit(t, root, "show", ":file.txt") != "base" {
					t.Fatal("selected entry not restored")
				}
			}
			if content, err := os.ReadFile(child); err != nil || string(content) != "unrelated child\n" {
				t.Fatal("directory child changed")
			}
			if info, err := os.Stat(child); err != nil || info.Mode().Perm() != 0640 {
				t.Fatal("directory child mode changed")
			}
			if fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "unrelated.txt") != unrelated || fixtureGit(t, root, "rev-parse", "HEAD") != head {
				t.Fatal("unrelated index or HEAD changed")
			}
		})
	}
}

// TestWorktreeLeafOperationsStillRejectDirectory keeps physical restrictions on reads and writes.
//
// Example: stage-content cannot read a directory merely because the index has an exact file there.
func TestWorktreeLeafOperationsStillRejectDirectory(
	t *testing.T,
) {
	for _, args := range [][]string{
		{"stage-content", "--", "file.txt"},
		{"stage-removals", "--", "file.txt"},
		{"restore", "--source", "head", "--destination", "worktree", "--", "file.txt"},
		{"restore", "--source", "head", "--destination", "both", "--", "file.txt"},
		{"remove", "--destination", "worktree", "--", "file.txt"},
		{"remove", "--destination", "both", "--", "file.txt"},
		{"move", "--", "file.txt", "moved.txt"},
	} {
		// Each operation must refuse the same physical directory before any mutation.
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := fixtureRepository(t)
			if err := os.Remove(filepath.Join(root, "file.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "file.txt"), 0755); err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(root, "file.txt/untouched")
			if err := os.WriteFile(child, []byte("unrelated child\n"), 0644); err != nil {
				t.Fatal(err)
			}
			index := fixtureGit(t, root, "ls-files", "--stage", "-z")
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			if err := executeFixtureOperation(t, root, args...); err == nil {
				t.Fatal("physical directory admitted")
			}
			if content, err := os.ReadFile(child); err != nil || string(content) != "unrelated child\n" {
				t.Fatal("directory child changed")
			}
			if fixtureGit(t, root, "ls-files", "--stage", "-z") != index || fixtureGit(t, root, "rev-parse", "HEAD") != head {
				t.Fatal("index or HEAD changed")
			}
		})
	}
}

// TestLogicalIndexOperationsPreserveAncestorAlias pairs native and typed exact index effects.
//
// Example: a staged restore of dir/file preserves an outside sentinel reached by dir's worktree symlink.
func TestLogicalIndexOperationsPreserveAncestorAlias(
	t *testing.T,
) {
	for _, kind := range []string{"restore-index", "remove-index", "unstage", "stage-hunks"} {
		for _, runner := range []string{"native", "typed"} {
			// Independent fixtures compare native Git with the public typed operation.
			t.Run(kind+"/"+runner, func(t *testing.T) {
				root := fixtureRepository(t)
				if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
					t.Fatal(err)
				}
				selected := filepath.Join(root, "dir/file")
				if err := os.WriteFile(selected, []byte("base\n"), 0644); err != nil {
					t.Fatal(err)
				}
				fixtureGit(t, root, "add", "--", "dir/file")
				fixtureGit(t, root, "commit", "-qm", "selected leaf")
				if err := os.WriteFile(selected, []byte("changed\n"), 0644); err != nil {
					t.Fatal(err)
				}
				patch := filepath.Join(t.TempDir(), "selected.patch")
				if err := os.WriteFile(patch, []byte(fixtureGit(t, root, "diff", "--", "dir/file")+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind != "stage-hunks" {
					fixtureGit(t, root, "add", "--", "dir/file")
				}
				oid := fixtureGit(t, root, "rev-parse", ":unrelated.txt")
				fixtureGit(t, root, "update-index", "--force-remove", "--", "unrelated.txt")
				repository, err := ResolveRepository(context.Background(), root)
				if err != nil {
					t.Fatal(err)
				}
				command := repository.Command(context.Background(), "update-index", "--index-info")
				command.Stdin = strings.NewReader("100644 " + oid + " 1\tunrelated.txt\n100644 " + oid + " 2\tunrelated.txt\n100644 " + oid + " 3\tunrelated.txt\n")
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("conflict fixture: %v %s", err, output)
				}
				before := fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "unrelated.txt", "file.txt")
				head := fixtureGit(t, root, "rev-parse", "HEAD")
				outside := t.TempDir()
				sentinel := filepath.Join(outside, "file")
				if err := os.WriteFile(sentinel, []byte("outside sentinel\n"), 0640); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "dir")); err != nil {
					t.Fatal(err)
				}
				typed := []string{"unstage", "--", "dir/file"}
				native := []string{"restore", "--source=HEAD", "--staged", "--", "dir/file"}
				switch kind {
				case "restore-index":
					typed = []string{"restore", "--source", "head", "--destination", "index", "--", "dir/file"}
				case "remove-index":
					typed = []string{"remove", "--destination", "index", "--", "dir/file"}
					native = []string{"rm", "--cached", "--force", "--", "dir/file"}
				case "stage-hunks":
					typed = []string{"stage-hunks", "--patch-file", patch, "--", "dir/file"}
					native = []string{"apply", "--cached", patch}
				}
				if runner == "native" {
					fixtureGit(t, root, native...)
				} else {
					if err := executeFixtureOperation(t, root, typed...); err != nil {
						t.Fatal(err)
					}
				}
				switch kind {
				case "remove-index":
					if fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "dir/file") != "" {
						t.Fatal("selected index entry retained")
					}
				case "stage-hunks":
					if fixtureGit(t, root, "show", ":dir/file") != "changed" {
						t.Fatal("selected patch missing")
					}
				default:
					if fixtureGit(t, root, "show", ":dir/file") != "base" {
						t.Fatal("selected index entry not restored")
					}
				}
				if fixtureGit(t, root, "ls-files", "--stage", "-z", "--", "unrelated.txt", "file.txt") != before || fixtureGit(t, root, "rev-parse", "HEAD") != head {
					t.Fatal("unrelated records or HEAD changed")
				}
				if content, err := os.ReadFile(sentinel); err != nil || string(content) != "outside sentinel\n" {
					t.Fatal("outside sentinel changed")
				}
				if info, err := os.Stat(sentinel); err != nil || info.Mode().Perm() != 0640 {
					t.Fatal("outside mode changed")
				}
				if target, err := os.Readlink(filepath.Join(root, "dir")); err != nil || target != outside {
					t.Fatal("ancestor alias changed")
				}
				if names, err := os.ReadDir(outside); err != nil || len(names) != 1 || names[0].Name() != "file" {
					t.Fatal("outside directory changed")
				}
			})
		}
	}
}

// TestPhysicalOperationsRejectAncestorAlias retains physical read and write boundaries.
//
// Example: staging aliased worktree content cannot read outside the selected repository.
func TestPhysicalOperationsRejectAncestorAlias(
	t *testing.T,
) {
	for _, args := range [][]string{{"stage-content", "--", "alias/file"}, {"stage-removals", "--", "alias/file"}, {"restore", "--source", "head", "--destination", "worktree", "--", "alias/file"}, {"remove", "--destination", "worktree", "--", "alias/file"}, {"move", "--", "file.txt", "alias/moved"}} {
		// Each operation refuses a real outside alias before touching any selected state.
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := fixtureRepository(t)
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
			before := fixtureGit(t, root, "ls-files", "--stage", "-z")
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			if err := executeFixtureOperation(t, root, args...); err == nil {
				t.Fatal("physical ancestor alias admitted")
			}
			if fixtureGit(t, root, "ls-files", "--stage", "-z") != before || fixtureGit(t, root, "rev-parse", "HEAD") != head {
				t.Fatal("index or HEAD changed")
			}
			if content, err := os.ReadFile(filepath.Join(outside, "file")); err != nil || string(content) != "outside\n" {
				t.Fatal("outside state changed")
			}
			if _, err := os.Lstat(filepath.Join(outside, "moved")); !os.IsNotExist(err) {
				t.Fatal("outside destination created")
			}
		})
	}
}

// TestLogicalIndexOperationsRejectControlNames retains lexical administrative protection.
//
// Example: index-only operations cannot select the worktree's .git pointer or index pathname.
func TestLogicalIndexOperationsRejectControlNames(
	t *testing.T,
) {
	for _, kind := range []string{"restore-index", "remove-index", "unstage", "stage-hunks"} {
		// Every logical-only peer retains its Git control check before native execution.
		t.Run(kind, func(t *testing.T) {
			root := fixtureRepository(t)
			patch := filepath.Join(t.TempDir(), "patch")
			if err := os.WriteFile(patch, []byte("unused"), 0600); err != nil {
				t.Fatal(err)
			}
			before := fixtureGit(t, root, "ls-files", "--stage", "-z")
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			for _, name := range []string{".git", ".git/index"} {
				args := []string{"unstage", "--", name}
				switch kind {
				case "restore-index":
					args = []string{"restore", "--source", "head", "--destination", "index", "--", name}
				case "remove-index":
					args = []string{"remove", "--destination", "index", "--", name}
				case "stage-hunks":
					args = []string{"stage-hunks", "--patch-file", patch, "--", name}
				}
				err := executeFixtureOperation(t, root, args...)
				if err == nil || !strings.Contains(err.Error(), "logical Git control state") {
					t.Fatalf("logical control selection %q: %v", name, err)
				}
			}
			if fixtureGit(t, root, "ls-files", "--stage", "-z") != before || fixtureGit(t, root, "rev-parse", "HEAD") != head {
				t.Fatal("administrative rejection changed state")
			}
		})
	}
}
