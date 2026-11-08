package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// restoreDiagnostic runs an exact request with independently captured diagnostic output.
//
// Example: advisory discovery context is observable even when a direct restore succeeds.
func restoreDiagnostic(
	t *testing.T,
	root string,
	destination string,
	name string,
	writer io.Writer,
) (string, error) {
	t.Helper()
	operation, err := ParseOperation([]string{"--repo", root, "restore", "--source", "head", "--destination", destination, "--", name})
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostic bytes.Buffer
	owned := writer == nil
	if owned {
		writer = &diagnostic
	}
	err = ExecuteOperation(context.Background(), operation, Streams{Output: &output, Error: writer})
	// Discovery context must reach the error stream and never stdout, even when nothing reached the error stream.
	if owned && destination != "index" && restoreDiscovery(t).Diagnostics != nil && diagnostic.Len() == 0 {
		t.Fatal("discovery diagnostic missing from the error stream")
	}
	if diagnostic.Len() != 0 && bytes.Contains(output.Bytes(), diagnostic.Bytes()) {
		t.Fatal("discovery diagnostic leaked to stdout")
	}
	return diagnostic.String(), err
}

// restoreRead checks exact fixture bytes rather than trimmed output.
//
// Example: a sentinel referent stays distinct from the restored base content.
func restoreRead(
	t *testing.T,
	path string,
	want string,
) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("read %q: got %q, error %v; want %q", path, got, err, want)
	}
}

// restoreDiscovery returns the production live protection for the current fixture environment.
//
// Example: a consumed alias entry appears in Paths only when a hook traverses it.
func restoreDiscovery(t *testing.T) LiveProtection {
	t.Helper()
	protection, err := liveProtectedPaths()
	if err != nil {
		t.Fatal(err)
	}
	return protection
}

// TestRestoreOpaqueVisibilityRecheck executes the correction carried by the owned refusal before the fresh recheck.
//
// Example: a hidden consumed alias becomes protected while an independent sharing alias remains movable.
func TestRestoreOpaqueVisibilityRecheck(t *testing.T) {
	for _, mode := range []os.FileMode{0000, 0400} {
		for _, consumed := range []bool{false, true} {
			// Each relation and permission boundary has its own owned fixture.
			t.Run(strconv.FormatUint(uint64(mode), 8)+"/"+strconv.FormatBool(consumed), func(t *testing.T) {
				root, target, sentinel := fixtureRestoreAlias(t, "internal")
				runtime := filepath.Join(root, "runtime ' $(printf expanded)\nline")
				hooks := filepath.Join(runtime, "hooks")
				if err := os.MkdirAll(hooks, 0755); err != nil {
					t.Fatal(err)
				}
				prefix := "../../ordinary/"
				if consumed {
					prefix = "../../dir/"
				}
				hook := filepath.Join(hooks, "validate-bash.sh")
				if err := os.Symlink(prefix+"nested/file", hook); err != nil {
					t.Fatal(err)
				}
				t.Setenv("ECI_HOME", runtime)
				t.Setenv("ECI_CONFIGURED_HOME", runtime)
				index, err := os.ReadFile(filepath.Join(root, ".git/index"))
				if err != nil {
					t.Fatal(err)
				}
				head, branch := fixtureGit(t, root, "rev-parse", "HEAD"), fixtureGit(t, root, "symbolic-ref", "HEAD")
				if err := os.Chmod(hooks, mode); err != nil {
					t.Fatal(err)
				}
				// Restore only this owned fixture's permissions after any assertion.
				t.Cleanup(func() {
					if err := os.Chmod(hooks, 0755); err != nil {
						t.Error(err)
					}
				})
				if _, err := os.Lstat(hook); !os.IsPermission(err) {
					t.Fatalf("permission boundary unavailable: %v", err)
				}
				diagnostic, err := restoreDiagnostic(t, root, "both", "dir/nested/file", nil)
				// The refusal value carries the production frontier and the exact correction its message embeds.
				var refusal ownedVisibilityRefusal
				if !errors.As(err, &refusal) {
					t.Fatalf("restore did not take the owned visibility branch: %v", err)
				}
				if refusal.Frontier.Path != hook {
					t.Fatalf("refusal frontier %q is not this fixture's hook %q", refusal.Frontier.Path, hook)
				}
				if protection := restoreDiscovery(t); diagnostic == "" || !errors.Is(protection.Diagnostics, os.ErrPermission) {
					t.Errorf("missing advisory permission context: %v %s", protection.Diagnostics, diagnostic)
				}
				if got, err := os.ReadFile(filepath.Join(root, ".git/index")); err != nil || !bytes.Equal(got, index) {
					t.Fatal("frontier refusal changed raw index")
				}
				if got, err := os.Readlink(filepath.Join(root, "dir")); err != nil || got != target {
					t.Fatal("frontier refusal changed alias")
				}
				if _, err := restoreDiagnostic(t, root, "index", "dir/nested/file", nil); err != nil {
					t.Fatalf("index-only restore unavailable at the hidden frontier: %v", err)
				}
				command, operand := refusal.Correction()
				// Execute only a correction whose operand lies inside this fixture's resolved root.
				if !containsPath(root, operand) {
					t.Fatalf("refusing to execute correction outside fixture root %q: %q", root, operand)
				}
				// The owned branch applies only to a directory that lacks owner r-x.
				info, err := os.Stat(operand)
				if err != nil || info.Mode().Perm()&0500 == 0500 {
					t.Fatalf("owned correction precondition unmet: %v %v", info, err)
				}
				if output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
					t.Fatalf("refusal correction failed: %v %s", err, output)
				}
				info, err = os.Stat(operand)
				if err != nil || info.Mode().Perm() != 0500 {
					t.Fatalf("refusal correction left unexpected mode: %v %v", info, err)
				}
				_, err = restoreDiagnostic(t, root, "both", "dir/nested/file", nil)
				if err == nil || errors.As(err, new(ownedVisibilityRefusal)) {
					t.Fatalf("fresh recheck did not classify the visible relationship: %v", err)
				}
				if errors.As(err, new(protectedAncestorRefusal)) != consumed {
					t.Fatalf("fresh refusal protected=%v, want %v: %v", !consumed, consumed, err)
				}
				if !consumed {
					if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
						t.Fatal(err)
					}
					if _, err := restoreDiagnostic(t, root, "both", "dir/nested/file", nil); err != nil {
						t.Fatal(err)
					}
					restoreRead(t, filepath.Join(root, "dir/nested/file"), "base\n")
					if got, err := os.Readlink(filepath.Join(root, "saved-alias")); err != nil || got != target {
						t.Fatal("saved alias spelling changed")
					}
				}
				restoreRead(t, hook, "sentinel\n")
				restoreRead(t, sentinel, "sentinel\n")
				if fixtureGit(t, root, "rev-parse", "HEAD") != head || fixtureGit(t, root, "symbolic-ref", "HEAD") != branch || fixtureGit(t, root, "show", ":unrelated.txt") != "staged" {
					t.Fatal("recovery changed HEAD/branch/unrelated index")
				}
			})
		}
	}
}

// TestRestoreObservedPrefixSurvivesOpaqueLookup traces consumed aliases before full hook lookup.
//
// Example: hooks -> dir/private records dir before private prevents inspection of the hook.
func TestRestoreObservedPrefixSurvivesOpaqueLookup(t *testing.T) {
	root, _, sentinel := fixtureRestoreAlias(t, "internal")
	blocked := filepath.Join(root, "ordinary/private")
	if err := os.Mkdir(blocked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "validate-bash.sh"), []byte("live\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "hooks"), filepath.Join(root, "old-hooks")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir/private", filepath.Join(root, "hooks")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	// Restore the owned directory so fixture cleanup remains possible.
	t.Cleanup(func() {
		if err := os.Chmod(blocked, 0755); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Lstat(filepath.Join(root, "hooks/validate-bash.sh")); !os.IsPermission(err) {
		t.Fatalf("permission boundary unavailable: %v", err)
	}
	_, err := restoreDiagnostic(t, root, "both", "dir/nested/file", nil)
	if err == nil || !errors.As(err, new(protectedAncestorRefusal)) {
		t.Fatalf("observed alias not protected: %v", err)
	}
	if _, err := restoreDiagnostic(t, root, "index", "dir/nested/file", nil); err != nil {
		t.Fatal(err)
	}
	restoreRead(t, sentinel, "sentinel\n")
}

// TestRestoreMalformedHooksRemainAdvisory preserves actual entries without blanket discovery denial.
//
// Example: a malformed suffix follows either a consumed or sharing alias, and a later hook is still inspected.
func TestRestoreMalformedHooksRemainAdvisory(t *testing.T) {
	for _, consumed := range []bool{false, true} {
		// A distinct repository proves each dependency classification under the same failure.
		t.Run(strconv.FormatBool(consumed), func(t *testing.T) {
			root, target, sentinel := fixtureRestoreAlias(t, "internal")
			hook := filepath.Join(root, "hooks/validate-bash.sh")
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			prefix := "../ordinary/"
			if consumed {
				prefix = "../dir/"
			}
			link := prefix + strings.Repeat("x", 300)
			if err := os.Symlink(link, hook); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("dirty\n"), 0644); err != nil {
				t.Fatal(err)
			}
			diagnostic, err := restoreDiagnostic(t, root, "worktree", "file.txt", nil)
			if err != nil || diagnostic == "" || !errors.Is(restoreDiscovery(t).Diagnostics, syscall.ENAMETOOLONG) {
				t.Errorf("malformed lookup denied direct restore or lost context: %v %s", err, diagnostic)
			}
			restoreRead(t, filepath.Join(root, "file.txt"), "base\n")
			_, err = restoreDiagnostic(t, root, "worktree", "hooks/validate-bash.sh", nil)
			if err == nil {
				t.Errorf("observed hook entry not protected: %v", err)
			}
			if got, err := os.Readlink(hook); err != nil || got != link {
				t.Errorf("protected hook entry changed: %q %v", got, err)
			}
			_, err = restoreDiagnostic(t, root, "both", "dir/nested/file", nil)
			if err == nil || errors.As(err, new(protectedAncestorRefusal)) != consumed {
				t.Fatalf("malformed alias classification: %v", err)
			}
			if consumed {
				if err := os.Symlink("../dir/nested/file", filepath.Join(root, "hooks/stop-gate.sh")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(hook); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(strings.Repeat("x", 300), hook); err != nil {
					t.Fatal(err)
				}
				_, err := restoreDiagnostic(t, root, "both", "dir/nested/file", nil)
				if err == nil || !errors.As(err, new(protectedAncestorRefusal)) {
					t.Fatalf("later hook not discovered: %v", err)
				}
			}
			if !consumed {
				if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
					t.Fatal(err)
				}
				if _, err := restoreDiagnostic(t, root, "both", "dir/nested/file", nil); err != nil {
					t.Fatal(err)
				}
				if got, err := os.Readlink(filepath.Join(root, "saved-alias")); err != nil || got != target {
					t.Fatal("saved alias changed")
				}
			}
			restoreRead(t, sentinel, "sentinel\n")
			if fixtureGit(t, root, "show", ":unrelated.txt") != "staged" {
				t.Fatal("unrelated index changed")
			}
		})
	}
}

// TestRestoreForeignFrontierKeepsLiteralFileAndIndexAvailable avoids foreign permission advice.
//
// Example: inaccessible /root needs lookup correction in place, while an exact literal file remains usable.
func TestRestoreForeignFrontierKeepsLiteralFileAndIndexAvailable(t *testing.T) {
	root, _, sentinel := fixtureRestoreAlias(t, "internal")
	info, err := os.Lstat("/root")
	if err != nil {
		t.Fatal(err)
	}
	status, ok := info.Sys().(*syscall.Stat_t)
	if !ok || status.Uid == uint32(os.Geteuid()) {
		t.Skip("foreign-owned permission fixture unavailable")
	}
	if _, err := os.Lstat("/root/hooks/validate-bash.sh"); !os.IsPermission(err) {
		t.Skipf("foreign permission boundary unavailable: %v", err)
	}
	t.Setenv("ECI_HOME", "/root")
	t.Setenv("ECI_CONFIGURED_HOME", "/root")
	_, err = restoreDiagnostic(t, root, "both", "dir/nested/file", nil)
	protection := restoreDiscovery(t)
	if err == nil || errors.As(err, new(ownedVisibilityRefusal)) || errors.As(err, new(protectedAncestorRefusal)) || len(protection.Frontiers) == 0 || protection.Frontiers[0].OwnedVisibility {
		t.Fatalf("foreign correction advice: %v", err)
	}
	if diagnostic, err := restoreDiagnostic(t, root, "index", "dir/nested/file", nil); err != nil || diagnostic != "" {
		t.Fatalf("index-only performed discovery: %v %s", err, diagnostic)
	}
	for _, name := range []string{"literal[star]*", "file.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("dirty\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := restoreDiagnostic(t, root, "worktree", "literal[star]*", nil); err != nil {
		t.Fatal(err)
	}
	restoreRead(t, filepath.Join(root, "literal[star]*"), "base\n")
	restoreRead(t, filepath.Join(root, "file.txt"), "dirty\n")
	restoreRead(t, sentinel, "sentinel\n")
}

// TestRestoreDiagnosticWriterFailurePrecedesMutation propagates diagnostic IO errors.
//
// Example: /dev/full prevents an otherwise allowed direct-file restore before native Git runs.
func TestRestoreDiagnosticWriterFailurePrecedesMutation(t *testing.T) {
	root := fixtureRepository(t)
	hook := filepath.Join(root, "hooks/validate-bash.sh")
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(strings.Repeat("x", 300), hook); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(root, ".git/index"))
	if err != nil {
		t.Fatal(err)
	}
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Close the diagnostic file even when an assertion fails.
	t.Cleanup(func() {
		if err := full.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = restoreDiagnostic(t, root, "worktree", "file.txt", full)
	if err == nil || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("writer failure not propagated: %v", err)
	}
	restoreRead(t, filepath.Join(root, "file.txt"), "dirty\n")
	if got, err := os.ReadFile(filepath.Join(root, ".git/index")); err != nil || !bytes.Equal(got, index) {
		t.Fatal("writer failure changed raw index")
	}
}

// TestRestoreHiddenDirectRelationshipRemainsAdvisory records the intentional observation boundary.
//
// Example: hidden hook text cannot deny a direct file, but restoring visibility makes the same file protected.
func TestRestoreHiddenDirectRelationshipRemainsAdvisory(t *testing.T) {
	root := fixtureRepository(t)
	hook, hooks := filepath.Join(root, "hooks/validate-bash.sh"), filepath.Join(root, "hooks")
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../file.txt", hook); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("live\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(hooks, 0000); err != nil {
		t.Fatal(err)
	}
	// Ensure cleanup retains access to this owned fixture.
	t.Cleanup(func() {
		if err := os.Chmod(hooks, 0755); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Lstat(hook); !os.IsPermission(err) {
		t.Fatalf("permission boundary unavailable: %v", err)
	}
	diagnostic, err := restoreDiagnostic(t, root, "worktree", "file.txt", nil)
	if err != nil || diagnostic == "" || !errors.Is(restoreDiscovery(t).Diagnostics, os.ErrPermission) {
		t.Fatalf("hidden relationship not advisory with context: %v %s", err, diagnostic)
	}
	if err := os.Chmod(hooks, 0755); err != nil {
		t.Fatal(err)
	}
	restoreRead(t, hook, "base\n")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("live\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = restoreDiagnostic(t, root, "worktree", "file.txt", nil)
	if err == nil {
		t.Fatalf("visible referent not protected: %v", err)
	}
	restoreRead(t, hook, "live\n")
}

// TestRestoreRawRootDecoyFileRemainsRestorable protects only the actually traversed hook entry.
//
// Example: portal/../runtime reaches an external runtime while the cleaned local hook is ordinary.
func TestRestoreRawRootDecoyFileRemainsRestorable(t *testing.T) {
	root := fixtureRepository(t)
	external := t.TempDir()
	for _, path := range []string{filepath.Join(root, "runtime/hooks"), filepath.Join(external, "sub"), filepath.Join(external, "runtime/hooks")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	decoy := filepath.Join(root, "runtime/hooks/validate-bash.sh")
	if err := os.WriteFile(decoy, []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "add", "--", "runtime/hooks/validate-bash.sh")
	fixtureGit(t, root, "commit", "-qm", "decoy base")
	if err := os.WriteFile(decoy, []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(external, "sub"), filepath.Join(root, "portal")); err != nil {
		t.Fatal(err)
	}
	actual := filepath.Join(external, "runtime/hooks/validate-bash.sh")
	if err := os.WriteFile(actual, []byte("live\n"), 0644); err != nil {
		t.Fatal(err)
	}
	raw := root + "/portal/../runtime"
	t.Setenv("ECI_HOME", raw)
	t.Setenv("ECI_CONFIGURED_HOME", raw)
	if _, err := restoreDiagnostic(t, root, "worktree", "runtime/hooks/validate-bash.sh", nil); err != nil {
		t.Fatalf("ordinary cleaned decoy denied: %v", err)
	}
	restoreRead(t, decoy, "base\n")
	restoreRead(t, actual, "live\n")
	restoreRead(t, raw+"/hooks/validate-bash.sh", "live\n")
}

// TestRestoreObservedNoHookKeepsConfiguredAliasMovable distinguishes absent hooks from opacity.
//
// Example: an ordinary configured root alias remains movable when its hooks are observably absent.
func TestRestoreObservedNoHookKeepsConfiguredAliasMovable(t *testing.T) {
	for _, shape := range []string{"missing", "regular-file", "unnamed-only"} {
		// Each shape closes all named-hook lookups using accessible filesystem evidence.
		t.Run(shape, func(t *testing.T) {
			root, target, sentinel := fixtureRestoreAlias(t, "internal")
			hooks := filepath.Join(root, "ordinary/hooks")
			switch shape {
			case "regular-file":
				if err := os.WriteFile(hooks, []byte("ordinary\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "unnamed-only":
				if err := os.Mkdir(hooks, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(hooks, "ordinary.sh"), []byte("ordinary\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("ECI_HOME", filepath.Join(root, "dir"))
			t.Setenv("ECI_CONFIGURED_HOME", filepath.Join(root, "dir"))
			_, err := restoreDiagnostic(t, root, "both", "dir/nested/file", nil)
			protection := restoreDiscovery(t)
			if err == nil || len(protection.Frontiers) != 0 || errors.As(err, new(protectedAncestorRefusal)) {
				t.Fatalf("observable absence lost owned move route: %v", err)
			}
			if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
				t.Fatal(err)
			}
			if _, err := restoreDiagnostic(t, root, "both", "dir/nested/file", nil); err != nil {
				t.Fatal(err)
			}
			if got, err := os.Readlink(filepath.Join(root, "saved-alias")); err != nil || got != target {
				t.Fatal("saved alias spelling changed")
			}
			restoreRead(t, sentinel, "sentinel\n")
			restoreRead(t, filepath.Join(root, "dir/nested/file"), "base\n")
			if fixtureGit(t, root, "show", ":unrelated.txt") != "staged" {
				t.Fatal("unrelated staged content changed")
			}
		})
	}
}

// fixtureRestoreAlias creates an exact tracked leaf beneath one owned alias shape.
//
// Example: a regular-file alias blocks dir/nested/file without touching its sentinel.
func fixtureRestoreAlias(
	t *testing.T,
	kind string,
) (string, string, string) {
	t.Helper()
	root := fixtureRepository(t)
	if err := os.MkdirAll(filepath.Join(root, "dir/nested"), 0755); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(root, "dir/nested/file")
	if err := os.WriteFile(leaf, []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, root, "add", "--", "dir/nested/file")
	fixtureGit(t, root, "commit", "-qm", "nested exact leaf")
	for _, name := range []string{"dir/nested/file", "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("staged\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, root, "add", "--", "dir/nested/file", "unrelated.txt")
	if err := os.RemoveAll(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	target, sentinel := "", ""
	switch kind {
	case "file":
		target = "ordinary"
		sentinel = filepath.Join(root, target)
	case "internal", "nested-alias":
		target = "ordinary"
		sentinel = filepath.Join(root, target, "nested/file")
	case "external":
		target = t.TempDir()
		sentinel = filepath.Join(target, "nested/file")
	case "dangling":
		target = "missing"
	case "loop":
		target = "dir"
	default:
		t.Fatalf("unsupported fixture kind %q", kind)
	}
	if sentinel != "" {
		if err := os.MkdirAll(filepath.Dir(sentinel), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sentinel, []byte("sentinel\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "nested-alias" {
		if err := os.Rename(filepath.Join(root, "ordinary/nested"), filepath.Join(root, "ordinary/actual")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("actual", filepath.Join(root, "ordinary/nested")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	return root, target, sentinel
}

// TestRestoreAncestorDiagnosisAndRecovery checks the actual owned recovery route and preserved state.
//
// Example: an external-directory alias is named before resolution, moved aside, and restored exactly.
func TestRestoreAncestorDiagnosisAndRecovery(t *testing.T) {
	for _, kind := range []string{"file", "external", "internal", "nested-alias", "dangling", "loop"} {
		// Each shape uses independent worktree, index, HEAD, and sentinel state.
		t.Run(kind, func(t *testing.T) {
			root, target, sentinel := fixtureRestoreAlias(t, kind)
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			branch := fixtureGit(t, root, "symbolic-ref", "HEAD")
			tree := fixtureGit(t, root, "write-tree")
			index, err := os.ReadFile(filepath.Join(root, ".git/index"))
			if err != nil {
				t.Fatal(err)
			}
			err = executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "both", "--", "dir/nested/file")
			diagnostic := ""
			if err != nil {
				diagnostic = err.Error()
			}
			t.Logf("refusal diagnostic: %s", diagnostic)
			if err == nil {
				t.Errorf("missing selected-ancestor diagnostic and bounded route: %v", err)
			}
			if got, err := os.Readlink(filepath.Join(root, "dir")); err != nil || got != target {
				t.Fatalf("refusal changed alias: %q %v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(root, ".git/index")); err != nil || !bytes.Equal(got, index) {
				t.Fatalf("refusal changed raw index: %v", err)
			}
			if fixtureGit(t, root, "write-tree") != tree || fixtureGit(t, root, "symbolic-ref", "HEAD") != branch || fixtureGit(t, root, "rev-parse", "HEAD") != head {
				t.Fatal("refusal changed semantic index, HEAD, or branch")
			}
			if got, err := os.ReadFile(filepath.Join(root, "unrelated.txt")); err != nil || string(got) != "staged\n" {
				t.Fatalf("refusal changed unrelated worktree: %q %v", got, err)
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "index", "--", "dir/nested/file"); err != nil {
				t.Fatalf("index-only recovery failed: %v", err)
			}
			if got := fixtureGit(t, root, "show", ":dir/nested/file"); got != "base" {
				t.Fatalf("selected index leaf=%q", got)
			}
			if got := fixtureGit(t, root, "show", ":unrelated.txt"); got != "staged" {
				t.Fatalf("unrelated staged leaf=%q", got)
			}
			if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
				t.Fatal(err)
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/nested/file"); err != nil {
				t.Fatalf("owned move-aside recovery failed: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(root, "dir/nested/file")); err != nil || string(got) != "base\n" {
				t.Fatalf("restored exact leaf=%q %v", got, err)
			}
			if got, err := os.Readlink(filepath.Join(root, "saved-alias")); err != nil || got != target {
				t.Fatalf("saved alias changed: %q %v", got, err)
			}
			if sentinel != "" {
				if got, err := os.ReadFile(sentinel); err != nil || string(got) != "sentinel\n" {
					t.Fatalf("referent changed: %q %v", got, err)
				}
			}
			if fixtureGit(t, root, "rev-parse", "HEAD") != head || fixtureGit(t, root, "symbolic-ref", "HEAD") != branch || fixtureGit(t, root, "show", ":unrelated.txt") != "staged" {
				t.Fatal("recovery changed HEAD or unrelated staged content")
			}
			t.Log("index-only and owned move-aside recovery preserved selected alias spelling, sentinel, HEAD, and unrelated staged entry")
		})
	}
}

// TestRestoreProtectionPrecedesMoveAdvice checks protected lexical roots before recovery advice.
//
// Example: a symlink-spelled configured provider root must not be offered as a movable ancestor.
func TestRestoreProtectionPrecedesMoveAdvice(t *testing.T) {
	for _, kind := range []string{"proof-root-alias", "kimi-proof-root-alias", "provider-root-alias", "configured-provider-root-alias", "kimi-provider-root-alias", "default-kimi-root-alias", "hook-parent-alias"} {
		// Each configured control alias is owned by a private provider fixture.
		t.Run(kind, func(t *testing.T) {
			root := fixtureRepository(t)
			path, alias, target := "", "", ""
			switch kind {
			case "proof-root-alias", "kimi-proof-root-alias":
				var sentinel string
				root, target, sentinel = fixtureRestoreAlias(t, "external")
				if sentinel == "" {
					t.Fatal("missing proof sentinel")
				}
				path, alias = "dir/nested/file", "dir"
				variable := "ECI_PROOF_ROOT"
				if kind == "kimi-proof-root-alias" {
					variable = "KIMI_PROOF_ROOT"
				}
				t.Setenv(variable, filepath.Join(root, alias))
			case "provider-root-alias", "configured-provider-root-alias", "kimi-provider-root-alias":
				path, alias, target = "runtime-link/hooks/validate-bash.sh", "runtime-link", "physical-runtime"
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, path), []byte("live\n"), 0644); err != nil {
					t.Fatal(err)
				}
				fixtureGit(t, root, "add", "--", path)
				fixtureGit(t, root, "commit", "-qm", "provider alias control")
				if err := os.Rename(filepath.Join(root, alias), filepath.Join(root, target)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, alias)); err != nil {
					t.Fatal(err)
				}
				variable := "ECI_HOME"
				switch kind {
				case "configured-provider-root-alias":
					variable = "ECI_CONFIGURED_HOME"
				case "kimi-provider-root-alias":
					variable = "KIMI_CODE_HOME"
				}
				t.Setenv(variable, filepath.Join(root, alias))
			case "default-kimi-root-alias":
				path, alias, target = ".kimi-code/hooks/validate-bash.sh", ".kimi-code", "physical-kimi"
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, path), []byte("live\n"), 0644); err != nil {
					t.Fatal(err)
				}
				fixtureGit(t, root, "add", "--", path)
				fixtureGit(t, root, "commit", "-qm", "default kimi root alias control")
				if err := os.Rename(filepath.Join(root, alias), filepath.Join(root, target)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, alias)); err != nil {
					t.Fatal(err)
				}
				// With KIMI_CODE_HOME unset, the default Kimi root is $HOME/.kimi-code.
				t.Setenv("HOME", root)
				if err := os.Unsetenv("KIMI_CODE_HOME"); err != nil {
					t.Fatal(err)
				}
			case "hook-parent-alias":
				path, alias, target = "hooks/validate-bash.sh", "hooks", "physical-hooks"
				if err := os.Rename(filepath.Join(root, alias), filepath.Join(root, target)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(root, alias)); err != nil {
					t.Fatal(err)
				}
			}
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			branch := fixtureGit(t, root, "symbolic-ref", "HEAD")
			tree := fixtureGit(t, root, "write-tree")
			sentinel, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatal(err)
			}
			index, err := os.ReadFile(filepath.Join(root, ".git/index"))
			if err != nil {
				t.Fatal(err)
			}
			err = executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", path)
			diagnostic := ""
			if err != nil {
				diagnostic = err.Error()
			}
			t.Logf("protected diagnostic: %s", diagnostic)
			if err == nil || !errors.As(err, new(protectedAncestorRefusal)) {
				t.Errorf("protected alias received movable-entry advice: %v", err)
			}
			if got, err := os.Readlink(filepath.Join(root, alias)); err != nil || got != target {
				t.Fatalf("protected alias changed: %q %v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(root, ".git/index")); err != nil || !bytes.Equal(got, index) {
				t.Fatalf("protected refusal changed raw index: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(root, path)); err != nil || !bytes.Equal(got, sentinel) {
				t.Fatalf("protected refusal changed sentinel: %v", err)
			}
			if fixtureGit(t, root, "rev-parse", "HEAD") != head || fixtureGit(t, root, "symbolic-ref", "HEAD") != branch || fixtureGit(t, root, "write-tree") != tree {
				t.Fatal("protected refusal changed HEAD, branch, or semantic index")
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "index", "--", path); err != nil {
				t.Fatalf("protected index-only recovery failed: %v", err)
			}
		})
	}
}

// TestRestoreAliasSourcesPreserveTypedSelection checks index and non-HEAD tree recovery.
//
// Example: a staged tree supplies selected content after moving an ordinary file alias aside.
func TestRestoreAliasSourcesPreserveTypedSelection(t *testing.T) {
	for _, sourceKind := range []string{"index", "tree"} {
		// Each source is selected without changing its corresponding stored blobs.
		t.Run(sourceKind, func(t *testing.T) {
			root, target, sentinel := fixtureRestoreAlias(t, "file")
			source := sourceKind
			if sourceKind == "tree" {
				source = fixtureGit(t, root, "write-tree")
				if source == fixtureGit(t, root, "rev-parse", "HEAD^{tree}") {
					t.Fatal("source must differ from HEAD tree")
				}
			}
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			index, err := os.ReadFile(filepath.Join(root, ".git/index"))
			if err != nil {
				t.Fatal(err)
			}
			err = executeFixtureOperation(t, root, "restore", "--source", source, "--destination", "worktree", "--", "dir/nested/file")
			if err == nil {
				t.Fatalf("missing source-specific refusal: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(root, ".git/index")); err != nil || !bytes.Equal(got, index) {
				t.Fatalf("source refusal changed index: %v", err)
			}
			if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
				t.Fatal(err)
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", source, "--destination", "worktree", "--", "dir/nested/file"); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(root, "dir/nested/file")); err != nil || string(got) != "staged\n" {
				t.Fatalf("wrong source content: %q %v", got, err)
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "sentinel\n" {
				t.Fatalf("referent changed: %q %v", got, err)
			}
			if got, err := os.Readlink(filepath.Join(root, "saved-alias")); err != nil || got != target {
				t.Fatalf("saved alias changed: %q %v", got, err)
			}
			if fixtureGit(t, root, "rev-parse", "HEAD") != head || fixtureGit(t, root, "show", ":unrelated.txt") != "staged" {
				t.Fatal("source recovery changed unrelated state")
			}
		})
	}
}

// TestRestoreDisposableAliasesRetainOwnedRecovery protects lexical identities rather than alias referents.
//
// Example: moving a disposable alias beside a configured provider alias preserves the provider's live hook.
func TestRestoreDisposableAliasesRetainOwnedRecovery(t *testing.T) {
	for _, kind := range []string{"physical-proof", "provider-near-prefix", "provider-without-hook"} {
		// Each disposable entry can be moved without changing any protected referent.
		t.Run(kind, func(t *testing.T) {
			root, _, sentinel := fixtureRestoreAlias(t, "internal")
			switch kind {
			case "physical-proof":
				t.Setenv("ECI_PROOF_ROOT", filepath.Join(root, "ordinary"))
			case "provider-near-prefix":
				if err := os.MkdirAll(filepath.Join(root, "ordinary/hooks"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "ordinary/hooks/validate-bash.sh"), []byte("live\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("ordinary", filepath.Join(root, "dir-protected")); err != nil {
					t.Fatal(err)
				}
				t.Setenv("ECI_HOME", filepath.Join(root, "dir-protected"))
				t.Setenv("ECI_CONFIGURED_HOME", filepath.Join(root, "dir-protected"))
			case "provider-without-hook":
				t.Setenv("ECI_HOME", filepath.Join(root, "dir"))
				t.Setenv("ECI_CONFIGURED_HOME", filepath.Join(root, "dir"))
			}
			err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/nested/file")
			if err == nil || errors.As(err, new(protectedAncestorRefusal)) {
				t.Fatalf("disposable entry lost bounded route: %v", err)
			}
			if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
				t.Fatal(err)
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/nested/file"); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "sentinel\n" {
				t.Fatalf("disposable recovery changed referent: %q %v", got, err)
			}
			if kind == "provider-near-prefix" {
				if got, err := os.ReadFile(filepath.Join(root, "dir-protected/hooks/validate-bash.sh")); err != nil || string(got) != "live\n" {
					t.Fatalf("provider hook changed: %q %v", got, err)
				}
			}
		})
	}
}

// TestRestoreProtectsConsumedHookAliases distinguishes traversed entries from shared referents.
//
// Example: a hook using dir/.. consumes dir even when its final referent is outside dir.
func TestRestoreProtectsConsumedHookAliases(t *testing.T) {
	for _, shape := range []string{"relative", "absolute", "chain", "nested", "dotdot", "root-dotdot", "dangling", "loop", "file", "sharing", "near-prefix", "no-hook"} {
		// Each trace runs in an independent exact-leaf fixture.
		t.Run(shape, func(t *testing.T) {
			kind := "internal"
			switch shape {
			case "nested":
				kind = "nested-alias"
			case "dangling", "loop", "file":
				kind = shape
			}
			root, target, sentinel := fixtureRestoreAlias(t, kind)
			hook := filepath.Join(root, "hooks/validate-bash.sh")
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			link := "../dir/nested/file"
			consumed := true
			switch shape {
			case "absolute":
				link = root + "/dir/nested/file"
			case "chain":
				if err := os.Symlink("dir/nested/file", filepath.Join(root, "chain")); err != nil {
					t.Fatal(err)
				}
				link = "../chain"
			case "dotdot":
				if err := os.WriteFile(filepath.Join(root, "hook-source"), []byte("live\n"), 0644); err != nil {
					t.Fatal(err)
				}
				link = "../dir/../hook-source"
			case "root-dotdot":
				if err := os.MkdirAll(filepath.Join(root, "runtime/hooks"), 0755); err != nil {
					t.Fatal(err)
				}
				hook = root + "/dir/../runtime/hooks/validate-bash.sh"
				if err := os.WriteFile(hook, []byte("live\n"), 0644); err != nil {
					t.Fatal(err)
				}
				t.Setenv("ECI_HOME", root+"/dir/../runtime")
				t.Setenv("ECI_CONFIGURED_HOME", root+"/dir/../runtime")
			case "sharing":
				link = "../ordinary/nested/file"
				consumed = false
			case "near-prefix":
				if err := os.Symlink("ordinary", filepath.Join(root, "dir-longer")); err != nil {
					t.Fatal(err)
				}
				link = "../dir-longer/nested/file"
				consumed = false
			case "no-hook":
				t.Setenv("ECI_HOME", filepath.Join(root, "dir"))
				t.Setenv("ECI_CONFIGURED_HOME", filepath.Join(root, "dir"))
				consumed = false
			}
			if shape != "root-dotdot" && shape != "no-hook" {
				if err := os.Symlink(link, hook); err != nil {
					t.Fatal(err)
				}
			}
			var live []byte
			if shape != "dangling" && shape != "loop" && shape != "file" && shape != "no-hook" {
				var err error
				live, err = os.ReadFile(hook)
				if err != nil {
					t.Fatal(err)
				}
			}
			tree := fixtureGit(t, root, "write-tree")
			head := fixtureGit(t, root, "rev-parse", "HEAD")
			branch := fixtureGit(t, root, "symbolic-ref", "HEAD")
			index, err := os.ReadFile(filepath.Join(root, ".git/index"))
			if err != nil {
				t.Fatal(err)
			}
			err = executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "both", "--", "dir/nested/file")
			if err == nil {
				t.Fatal("missing alias refusal")
			}
			if errors.As(err, new(protectedAncestorRefusal)) != consumed {
				t.Errorf("wrong dependency advice: %v", err)
			}
			if got, err := os.Readlink(filepath.Join(root, "dir")); err != nil || got != target {
				t.Fatalf("alias changed: %q %v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(root, ".git/index")); err != nil || !bytes.Equal(got, index) {
				t.Fatalf("raw index changed: %v", err)
			}
			if fixtureGit(t, root, "write-tree") != tree || fixtureGit(t, root, "rev-parse", "HEAD") != head || fixtureGit(t, root, "symbolic-ref", "HEAD") != branch {
				t.Fatal("refusal changed repository state")
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "index", "--", "dir/nested/file"); err != nil {
				t.Fatal(err)
			}
			if fixtureGit(t, root, "show", ":dir/nested/file") != "base" || fixtureGit(t, root, "show", ":unrelated.txt") != "staged" {
				t.Fatal("wrong index-only selection")
			}
			if !consumed {
				if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
					t.Fatal(err)
				}
				if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/nested/file"); err != nil {
					t.Fatal(err)
				}
				if got, err := os.ReadFile(sentinel); err != nil || string(got) != "sentinel\n" {
					t.Fatalf("referent changed: %q %v", got, err)
				}
			}
			if live != nil {
				if got, err := os.ReadFile(hook); err != nil || !bytes.Equal(got, live) {
					t.Fatalf("hook disconnected or changed: %v", err)
				}
			}
		})
	}
}

// TestRestorePermissionLimitedHookTracePreservesKnownAliases retains a partial dependency prefix.
//
// Example: inaccessible content leaves an earlier hook alias protected without blocking file.txt.
func TestRestorePermissionLimitedHookTracePreservesKnownAliases(t *testing.T) {
	for _, visitedAlias := range []bool{false, true} {
		// Permissions belong only to this disposable fixture and are restored for cleanup.
		t.Run(strconv.FormatBool(visitedAlias), func(t *testing.T) {
			root, _, _ := fixtureRestoreAlias(t, "internal")
			blocked := filepath.Join(root, "ordinary/inaccessible")
			if err := os.Mkdir(blocked, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blocked, "source"), []byte("live\n"), 0644); err != nil {
				t.Fatal(err)
			}
			hook := filepath.Join(root, "hooks/validate-bash.sh")
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			target := "../ordinary/inaccessible/source"
			if visitedAlias {
				target = "../dir/inaccessible/source"
			}
			if err := os.Symlink(target, hook); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(blocked, 0000); err != nil {
				t.Fatal(err)
			}
			// Restore owned permissions even if the assertions fail.
			t.Cleanup(func() {
				if err := os.Chmod(blocked, 0755); err != nil {
					t.Error(err)
				}
			})
			if _, err := os.Lstat(filepath.Join(blocked, "source")); !errors.Is(err, os.ErrPermission) {
				t.Skipf("permission boundary unavailable: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("changed\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "file.txt"); err != nil {
				t.Fatalf("unrelated restore blocked: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(root, "file.txt")); err != nil || string(got) != "base\n" {
				t.Fatalf("unrelated content incorrect: %q %v", got, err)
			}
			if visitedAlias {
				err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/nested/file")
				if err == nil || !errors.As(err, new(protectedAncestorRefusal)) {
					t.Fatalf("known prefix lost protection: %v", err)
				}
				if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "index", "--", "dir/nested/file"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// TestRestoreIgnoresCleanedHookDecoy follows raw configured roots through symlink-sensitive dotdot.
//
// Example: portal/../runtime names an external live runtime while its cleaned spelling has a decoy.
func TestRestoreIgnoresCleanedHookDecoy(t *testing.T) {
	root, _, sentinel := fixtureRestoreAlias(t, "internal")
	external := t.TempDir()
	for _, directory := range []string{filepath.Join(external, "sub"), filepath.Join(external, "runtime/hooks"), filepath.Join(root, "runtime/hooks")} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(external, "sub"), filepath.Join(root, "portal")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "runtime/hooks/validate-bash.sh"), []byte("actual hook\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../dir/nested/file", filepath.Join(root, "runtime/hooks/validate-bash.sh")); err != nil {
		t.Fatal(err)
	}
	rawRoot := root + "/portal/../runtime"
	t.Setenv("ECI_HOME", rawRoot)
	t.Setenv("ECI_CONFIGURED_HOME", rawRoot)
	hook := rawRoot + "/hooks/validate-bash.sh"
	if got, err := os.ReadFile(hook); err != nil || string(got) != "actual hook\n" {
		t.Fatalf("wrong actual hook: %q %v", got, err)
	}
	err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/nested/file")
	if err == nil || errors.As(err, new(protectedAncestorRefusal)) {
		t.Fatalf("cleaned decoy protected disposable entry: %v", err)
	}
	if err := os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved-alias")); err != nil {
		t.Fatal(err)
	}
	if err := executeFixtureOperation(t, root, "restore", "--source", "head", "--destination", "worktree", "--", "dir/nested/file"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(hook); err != nil || string(got) != "actual hook\n" {
		t.Fatalf("actual hook changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "sentinel\n" {
		t.Fatalf("referent changed: %q %v", got, err)
	}
}

// TestRestoreNestedHookDependencyKeepsSharingAliasMovable protects a visited nested entry only.
//
// Example: ordinary/nested is consumed by a hook while ordinary/share names the same directory independently.
func TestRestoreNestedHookDependencyKeepsSharingAliasMovable(t *testing.T) {
	root, _, _ := fixtureRestoreAlias(t, "nested-alias")
	hook := filepath.Join(root, "hooks/validate-bash.sh")
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../dir/nested/file", hook); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("actual", filepath.Join(root, "ordinary/share")); err != nil {
		t.Fatal(err)
	}
	paths, err := liveProtectedPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct {
		name     string
		consumed bool
	}{{"ordinary/nested/file", true}, {"ordinary/share/file", false}} {
		err := (Repository{Worktree: root}).checkRestoreAncestors(probe.name, paths)
		if err == nil || errors.As(err, new(protectedAncestorRefusal)) != probe.consumed {
			t.Fatalf("nested identity classification %q: %v", probe.name, err)
		}
	}
}

// TestLiveDependenciesRetainUnexpectedErrorContext preserves unknown filesystem errors.
//
// Example: an invalid native component wraps its PathError rather than returning a successful partial trace.
func TestLiveDependenciesRetainUnexpectedErrorContext(t *testing.T) {
	root := t.TempDir()
	path := root + "/\x00"
	_, err := livePathDependencies(path)
	var pathError *os.PathError
	if err == nil || !errors.As(err, &pathError) {
		t.Fatalf("unexpected inspection error lost context: %v", err)
	}
	if _, err := livePathDependencies("relative/path"); err == nil {
		t.Fatal("relative path silently admitted")
	}
}
