package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFinalizeHookDenialPassesDiagnosticContext checks the gate-mode finalizer receives command, cwd, and session.
//
// Example: a denied `git add -A` reaches finalize with ECI_GATE_DENIAL_COMMAND set.
func TestFinalizeHookDenialPassesDiagnosticContext(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".omp", "agent", "eci", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(home, "env.txt")
	script := "#!/bin/sh\nprintf '%s\\n%s\\n%s\\n' \"$ECI_GATE_DENIAL_COMMAND\" \"$ECI_GATE_DENIAL_CWD\" \"$ECI_GATE_DENIAL_SESSION_ID\" > " + dump + "\n"
	if err := os.WriteFile(filepath.Join(bin, "eci-command-gate-mode"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	request := Request{
		Provider:      ProviderOmp,
		Role:          RoleWorker,
		Marker:        MarkerInactive,
		Command:       "git add -A\x00tail" + strings.Repeat("x", 20_000),
		CWD:           "/tmp/repo",
		HookSessionID: "sess-7",
	}
	if _, err := finalizeHookDenial(request, []byte(`{}`)); err != nil {
		t.Fatalf("finalizeHookDenial(): %v", err)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("finalizer did not run: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "git add -A") || lines[1] != "/tmp/repo" || lines[2] != "sess-7" {
		t.Fatalf("finalizer env = %q", lines)
	}
	if len(lines[0]) > maxFinalizeEnvBytes {
		t.Fatalf("command env length = %d, want <= %d", len(lines[0]), maxFinalizeEnvBytes)
	}
}
