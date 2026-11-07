package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunReportsFixedOperationAndValidationFailure checks the actual command boundary.
//
// Example: a successful stage returns zero, while a malformed native-style request returns nonzero.
func TestRunReportsFixedOperationAndValidationFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := fixtureRepository(t)
	var output bytes.Buffer
	var diagnostic bytes.Buffer
	streams := Streams{Output: &output, Error: &diagnostic}
	if code := Run([]string{"--repo", root, "stage-content", "--", "file.txt"}, streams); code != 0 || !strings.Contains(output.String(), "completed fixed operation") {
		t.Fatalf("code/output/error=%d/%s/%s", code, output.String(), diagnostic.String())
	}
	for _, identity := range []string{"worktree=", "gitdir=", "index="} {
		if !strings.Contains(output.String(), identity) {
			t.Fatalf("missing resolved identity field %q", identity)
		}
	}
	if code := Run([]string{"--repo", root, "commit", "--amend"}, streams); code == 0 || !strings.Contains(diagnostic.String(), "--amend") {
		t.Fatal("native flag did not return diagnostic")
	}
	if code := Run([]string{"--repo", root, "stage-content", "--", "missing.txt"}, streams); code == 0 {
		t.Fatal("missing content admitted")
	}
	if code := Run([]string{"--repo", root, "stage-content", "--", "hooks"}, streams); code == 0 {
		t.Fatal("directory content admitted without exact leaf selection")
	}
}

// TestPatchErrorsPreserveSemanticIndex rejects incomplete inputs before real mutation.
//
// Example: malformed or empty patch input cannot become a successful empty selection.
func TestPatchErrorsPreserveSemanticIndex(t *testing.T) {
	root := fixtureRepository(t)
	index := fixtureGit(t, root, "write-tree")
	for _, content := range []string{"", "not a patch\n"} {
		patch := filepath.Join(t.TempDir(), "invalid.patch")
		if err := os.WriteFile(patch, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := executeFixtureOperation(t, root, "stage-hunks", "--patch-file", patch, "--", "file.txt"); err == nil {
			t.Fatal("invalid patch admitted")
		}
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.patch"), t.TempDir()} {
		if err := executeFixtureOperation(t, root, "stage-hunks", "--patch-file", path, "--", "file.txt"); err == nil {
			t.Fatal("invalid patch source admitted")
		}
	}
	if fixtureGit(t, root, "write-tree") != index {
		t.Fatal("invalid patch changed actual index")
	}
}
