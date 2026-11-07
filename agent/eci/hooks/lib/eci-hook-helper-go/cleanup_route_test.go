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

// newKimiCleanupFixture builds a HOME with an ECI home, a Kimi home reached
// through KIMI_CODE_HOME, an artifact outside both, and two escape vectors.
func newKimiCleanupFixture(t *testing.T) (home, kimi, elsewhere string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", filepath.Join(home, "tmp"))
	kimi = filepath.Join(home, ".kimi-code")
	t.Setenv("KIMI_CODE_HOME", kimi)
	elsewhere = filepath.Join(home, "elsewhere")
	for _, dir := range []string{filepath.Join(home, ".omp", "agent", "eci"), kimi, filepath.Join(kimi, "cron"), filepath.Join(home, "tmp"), elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(kimi, "config-new.toml"), filepath.Join(kimi, "artifact.txt"), filepath.Join(elsewhere, "config-new.toml")} {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(elsewhere, filepath.Join(kimi, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(kimi, "config-new.toml"), filepath.Join(kimi, "link")); err != nil {
		t.Fatal(err)
	}
	return home, kimi, elsewhere
}

// TestCleanupRouteKimiHomeApprovedAndRefused checks the Kimi home as a cleanup root.
//
// Source anchors (pre-migration source 575bb81 hooks/lib/eci-hook-helper-go/cleanup_route.go):
// KIMI_CODE_HOME pair 343-348; rm checks 115-134; mv checks 425-437; approved
// tables 32-35; escape 401; outside-home 421. The 575bb81 cleanup_route_test.go
// has no Kimi-home case (its four tests are at lines 18, 37, 63, 87), so the
// Kimi cases here are derived from the cleanup_route.go lines above.
func TestCleanupRouteKimiHomeApprovedAndRefused(t *testing.T) {
	cases := []struct {
		name       string
		command    func(home, kimi, elsewhere string) string
		wantReason string // "" means accepted
	}{
		{"rm -f approved root file", func(_, kimi, _ string) string {
			return "rm -f -- " + filepath.Join(kimi, "config-new.toml")
		}, ""},
		{"rm -rf approved root directory", func(_, kimi, _ string) string {
			return "rm -rf -- " + filepath.Join(kimi, "cron")
		}, ""},
		{"mv approved root file to quarantine", func(home, kimi, _ string) string {
			return "mv -- " + filepath.Join(kimi, "config-new.toml") + " " + filepath.Join(home, "tmp", "eci-generated-cleanup-q")
		}, ""},
		{"rm -f unapproved name in kimi home", func(_, kimi, _ string) string {
			return "rm -f -- " + filepath.Join(kimi, "artifact.txt")
		}, "path is not an approved generated artifact"},
		{"rm -rf kimi home root itself", func(_, kimi, _ string) string {
			return "rm -rf -- " + kimi
		}, "path is not an approved generated artifact"},
		// rm does not report the escape text: 575bb81 cleanup_route.go 115-127 passes escape kinds to the -f kind check.
		{"rm -f symlink escaping kimi home", func(_, kimi, _ string) string {
			return "rm -f -- " + filepath.Join(kimi, "escape", "config-new.toml")
		}, "-f requires an approved regular file"},
		{"mv symlink escaping kimi home", func(home, kimi, _ string) string {
			return "mv -- " + filepath.Join(kimi, "escape", "config-new.toml") + " " + filepath.Join(home, "tmp", "eci-generated-cleanup-q")
		}, "resolved target escapes canonical provider home"},
		{"rm -f symlink inside kimi home", func(_, kimi, _ string) string {
			return "rm -f -- " + filepath.Join(kimi, "link")
		}, "symlink targets are not cleanup-eligible"},
		{"rm -f file outside kimi home", func(_, _, elsewhere string) string {
			return "rm -f -- " + filepath.Join(elsewhere, "config-new.toml")
		}, "target is outside canonical ECI/Kimi homes"},
		{"mv file outside kimi home", func(home, _, elsewhere string) string {
			return "mv -- " + filepath.Join(elsewhere, "config-new.toml") + " " + filepath.Join(home, "tmp", "eci-generated-cleanup-q")
		}, "target is outside canonical ECI/Kimi homes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, kimi, elsewhere := newKimiCleanupFixture(t)
			code, out := runCleanupRouteForTest(t, tc.command(home, kimi, elsewhere), "false")
			if tc.wantReason == "" {
				if code != statusOK || out != "ok\n" {
					t.Fatalf("got code=%d out=%q, want ok", code, out)
				}
				return
			}
			if code != 1 || !strings.Contains(out, "reason="+tc.wantReason) {
				t.Fatalf("got code=%d out=%q, want reason %q", code, out, tc.wantReason)
			}
		})
	}
}
