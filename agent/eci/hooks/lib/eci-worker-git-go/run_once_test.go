package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunOnceRequiresAuthorizationAndReason checks rejection before Git runs.
//
// Example: a whitespace reason never executes the supplied Git alias.
func TestRunOnceRequiresAuthorizationAndReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := fixtureRepository(t)
	marker := filepath.Join(t.TempDir(), "runs")
	t.Setenv("GIT_ONCE_MARKER", marker)
	valid := []string{"--repo", root, "run-once", "--reason", "user approved this alias", "--user-authorized", "--", "-c", `alias.once=!printf x >> "$GIT_ONCE_MARKER"`, "once"}
	var output, diagnostic bytes.Buffer
	streams := Streams{Output: &output, Error: &diagnostic}
	blank := append([]string(nil), valid...)
	blank[4] = " \t\n"
	missingAuthorization := append(append([]string(nil), valid[:5]...), valid[6:]...)
	for _, arguments := range [][]string{blank, missingAuthorization, valid[:7]} {
		if code := Run(arguments, streams); code == 0 {
			t.Fatalf("invalid request succeeded: %q", arguments)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("invalid request executed Git: %v", err)
		}
	}
	if code := Run(valid, streams); code != 0 {
		t.Fatalf("valid request failed: %d: %s", code, diagnostic.String())
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "x" {
		t.Fatalf("expected exactly one invocation: %q: %v", content, err)
	}
}

// TestRunOncePreservesNativeContextAndStreams checks native repository selection and process I/O.
//
// Example: native -C selects another owned fixture while an alias reads inherited environment and stdin.
func TestRunOncePreservesNativeContextAndStreams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	initial := fixtureRepository(t)
	selected := fixtureRepository(t)
	t.Setenv("GIT_ONCE_VALUE", "inherited value")
	var output, diagnostic bytes.Buffer
	arguments := []string{"--repo", initial, "run-once", "--reason", "approved fixture context and streams", "--user-authorized", "--", "-C", selected, "-c", `alias.fidelity=!git rev-parse --show-toplevel && printf '%s\n' "$GIT_ONCE_VALUE" && cat && printf 'diagnostic\n' >&2`, "fidelity"}
	if code := Run(arguments, Streams{Input: strings.NewReader("input value\n"), Output: &output, Error: &diagnostic}); code != 0 {
		t.Fatalf("native context failed: %d: %s", code, diagnostic.String())
	}
	if got, want := output.String(), selected+"\ninherited value\ninput value\n"; got != want {
		t.Fatalf("native context or stdout changed: got %q, want %q", got, want)
	}
	if got := diagnostic.String(); got != "diagnostic\n" {
		t.Fatalf("stderr changed: %q", got)
	}
}

// TestRunOncePreservesNativeExit checks that Git's exit status survives the CLI.
//
// Example: a Git alias exiting 37 makes the escape invocation exit 37.
func TestRunOncePreservesNativeExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := fixtureRepository(t)
	var output, diagnostic bytes.Buffer
	if code := Run([]string{"--repo", root, "run-once", "--reason", "approved status check", "--user-authorized", "--", "-c", "alias.failure=!exit 37", "failure"}, Streams{Output: &output, Error: &diagnostic}); code != 37 {
		t.Fatalf("native exit changed: %d: %s", code, diagnostic.String())
	}
}
