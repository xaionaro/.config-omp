package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCleanupRouteForTest(t *testing.T, command, allow string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCleanupRoute([]string{command, allow}, nil, &stdout, &stderr)
	return code, stdout.String()
}

func TestCleanupRouteAcceptsApprovedRootFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", filepath.Join(home, "tmp"))
	eciDir := filepath.Join(home, ".omp", "agent", "eci")
	if err := os.MkdirAll(eciDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(eciDir, "config-new.toml")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out := runCleanupRouteForTest(t, "rm -f -- "+target, "false")
	if code != statusOK || out != "ok\n" {
		t.Fatalf("got code=%d out=%q, want ok", code, out)
	}
}

func TestCleanupRouteRejectsWithReasonPrefix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())

	cases := []string{
		"",
		"rm -x -- /etc/passwd",
		"rm -f -- /etc/passwd",
		"rm -f -- 'quoted'",
		"rm -f -- $HOME/x",
		"rm -f -- a;rm",
		"mv",
		"mv -- /etc/passwd /tmp/eci-generated-cleanup-q extra",
		"rm -f \\",
	}
	for _, command := range cases {
		code, out := runCleanupRouteForTest(t, command, "false")
		if code != 1 {
			t.Errorf("%q: code=%d, want 1 (out=%q)", command, code, out)
		}
		if !strings.HasPrefix(out, "coordinator-cleanup-route command=") {
			t.Errorf("%q: out=%q lacks reject prefix", command, out)
		}
	}
}

func TestCleanupRouteMoveRejectsProviderHomeEscape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", filepath.Join(home, "tmp"))
	eciDir := filepath.Join(home, ".omp", "agent", "eci")
	if err := os.MkdirAll(eciDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "artifact.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(eciDir, "escape")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(eciDir, "escape", "artifact.txt")
	destination := filepath.Join(home, "tmp", "eci-generated-cleanup-q")

	code, out := runCleanupRouteForTest(t, "mv -- "+source+" "+destination, "false")
	if code != 1 || !strings.Contains(out, "resolved target escapes canonical provider home") {
		t.Fatalf("got code=%d out=%q, want escape rejection", code, out)
	}
}

func TestCleanupRouteUsageErrorReturnsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCleanupRoute([]string{"rm -f -- x"}, nil, &stdout, &stderr)
	if code != statusRejected {
		t.Fatalf("code=%d, want %d", code, statusRejected)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q, want empty on usage error", stdout.String())
	}
}
