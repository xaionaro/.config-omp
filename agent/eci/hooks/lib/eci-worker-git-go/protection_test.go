package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestorePreservesUnnamedAncestorAliases refuses native replacement of an unnamed alias.
//
// Example: restoring dir/file leaves dir's symlink and its referent unchanged.
func TestRestorePreservesUnnamedAncestorAliases(t *testing.T) {
	for _, target := range []string{"ordinary", "missing"} {
		// Run each alias shape in an independent committed repository.
		t.Run(target, func(t *testing.T) {
			root := fixtureRepository(t)
			if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "dir/file"), []byte("base\n"), 0644); err != nil {
				t.Fatal(err)
			}
			fixtureGit(t, root, "add", "--", "dir/file")
			fixtureGit(t, root, "commit", "-qm", "nested fixture")
			if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
				t.Fatal(err)
			}
			if target == "ordinary" {
				if err := os.Mkdir(filepath.Join(root, target), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, target, "file"), []byte("sentinel\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, filepath.Join(root, "dir")); err != nil {
				t.Fatal(err)
			}
			tree := fixtureGit(t, root, "write-tree")
			err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/file")
			if err == nil || !strings.Contains(err.Error(), "dir/file") || !strings.Contains(err.Error(), "dir") || !strings.Contains(err.Error(), "move") {
				t.Fatalf("missing refusal and owned route: %v", err)
			}
			if got, err := os.Readlink(filepath.Join(root, "dir")); err != nil || got != target {
				t.Fatalf("ancestor changed: %q %v", got, err)
			}
			if fixtureGit(t, root, "write-tree") != tree {
				t.Fatal("refusal changed index")
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "index", "--", "dir/file"); err != nil {
				t.Fatal(err)
			}
			if target == "ordinary" {
				if got, err := os.ReadFile(filepath.Join(root, target, "file")); err != nil || string(got) != "sentinel\n" {
					t.Fatalf("referent changed: %q %v", got, err)
				}
			}
		})
	}
}

// TestProofRootIdentitiesProtectPhysicalWrites preserves configured roots through aliases.
//
// Example: deleting physical-proof/eci_active cannot bypass a proof-link root.
func TestProofRootIdentitiesProtectPhysicalWrites(t *testing.T) {
	for _, variable := range []string{"ECI_PROOF_ROOT"} {
		// Check the proof root with its runtime root.
		t.Run(variable, func(t *testing.T) {
			root := fixtureRepository(t)
			if err := os.Mkdir(filepath.Join(root, "physical-proof"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "physical-proof/eci_active"), []byte("live"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("physical-proof", filepath.Join(root, "proof-link")); err != nil {
				t.Fatal(err)
			}
			t.Setenv(variable, filepath.Join(root, "proof-link"))
			if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "physical-proof/eci_active"); err == nil {
				t.Fatal("physical proof deleted through configured alias")
			}
			if got, err := os.ReadFile(filepath.Join(root, "physical-proof/eci_active")); err != nil || string(got) != "live" {
				t.Fatalf("proof changed: %q %v", got, err)
			}
			fixtureGit(t, root, "add", "--", "physical-proof/eci_active")
			if err := executeFixtureOperation(t, root, "remove", "--destination", "index", "--", "physical-proof/eci_active"); err != nil {
				t.Fatal(err)
			}
			t.Setenv(variable, filepath.Join(root, "absent"))
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "file.txt"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("loop", filepath.Join(root, "loop")); err != nil {
				t.Fatal(err)
			}
			t.Setenv(variable, filepath.Join(root, "loop"))
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "file.txt"); err == nil || !strings.Contains(err.Error(), "loop") {
				t.Fatalf("unexpected resolution error suppressed: %v", err)
			}
			if err := executeFixtureOperation(t, root, "stage-content", "--", "file.txt"); err != nil {
				t.Fatalf("read operation blocked by proof resolution: %v", err)
			}
		})
	}
}

// TestRestoreAliasHatchAndSelectedSymlinkLeaves preserves owned explicit leaf restoration.
//
// Example: moving an unnamed ancestor aside allows exact restoration without changing its referent.
func TestRestoreAliasHatchAndSelectedSymlinkLeaves(t *testing.T) {
	root := fixtureRepository(t)
	if err := os.MkdirAll(filepath.Join(root, "nested/dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested/dir/file"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(root, "selected-link")); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "add", "--", "nested/dir/file", "selected-link")
	fixtureGit(t, root, "commit", "-qm", "alias fixtures")
	if err := os.Rename(filepath.Join(root, "nested/dir"), filepath.Join(root, "ordinary")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ordinary/file"), []byte("sentinel\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../ordinary", filepath.Join(root, "nested/dir")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "nested/dir/file"); err == nil {
		t.Fatal("nested ancestor alias admitted")
	}
	if err := os.Rename(filepath.Join(root, "nested/dir"), filepath.Join(root, "nested/saved-alias")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "nested/dir/file"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "ordinary/file")); err != nil || string(got) != "sentinel\n" {
		t.Fatalf("hatch changed referent: %q %v", got, err)
	}
	if got, err := os.Readlink(filepath.Join(root, "nested/saved-alias")); err != nil || got != "../ordinary" {
		t.Fatalf("hatch changed alias: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(root, "selected-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("unrelated.txt", filepath.Join(root, "selected-link")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "selected-link"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(root, "selected-link")); err != nil || got != "file.txt" {
		t.Fatalf("selected leaf restore failed: %q %v", got, err)
	}
}

// TestProofRootAncestorAliasesAndNearPrefixes compares actual root identities by components.
//
// Example: proof-old remains writable beside a protected proof root beneath an aliased parent.
func TestProofRootAncestorAliasesAndNearPrefixes(t *testing.T) {
	root := fixtureRepository(t)
	if err := os.MkdirAll(filepath.Join(root, "physical/proof"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("physical", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(root, "alias/proof"))
	for _, name := range []string{"physical/proof/live", "physical/proof-old"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("sentinel"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "physical/proof/live"); err == nil {
		t.Fatal("ancestor alias lost proof protection")
	}
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "physical/proof-old"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "physical/proof/live")); err != nil || string(got) != "sentinel" {
		t.Fatalf("proof changed: %q %v", got, err)
	}
	if err := executeFixtureOperation(t, root, "remove", "--destination", "worktree", "--", "alias/proof/live"); err == nil {
		t.Fatal("lexical proof alias admitted")
	}
}
