package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHookGateModeUnstatableTargetDenies covers --hook gate-mode sets whose
// target cannot be resolved or statted, or resolves to a statable
// non-canonical copy. D1-D5, D7, W1 and D7W must deny gate-mode-identity; B1
// is a get control that stays allowed.
//
// Example: D1 is an omp coordinator set on a dangling symlink, which denies.
func TestHookGateModeUnstatableTargetDenies(t *testing.T) {
	fixture := newB3Fixture(t)
	scratch, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve scratch: %v", err)
	}
	dangling := filepath.Join(scratch, "eci-command-gate-mode")
	if err := os.Symlink(filepath.Join(scratch, "missing-target"), dangling); err != nil {
		t.Fatalf("create dangling symlink: %v", err)
	}
	locked := filepath.Join(scratch, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("create locked dir: %v", err)
	}
	lockedGate := filepath.Join(locked, "eci-command-gate-mode")
	if err := os.WriteFile(lockedGate, []byte("gate-v1\n"), 0o755); err != nil {
		t.Fatalf("write locked gate: %v", err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("lock dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := os.MkdirAll(filepath.Join(fixture.kimiRoot, "bin"), 0o755); err != nil {
		t.Fatalf("create kimi bin: %v", err)
	}
	if err := os.Symlink(filepath.Join(scratch, "missing-kimi"), fixture.kimiGate); err != nil {
		t.Fatalf("create dangling kimi gate: %v", err)
	}
	copied := filepath.Join(scratch, "copy", "eci-command-gate-mode")
	if err := os.MkdirAll(filepath.Dir(copied), 0o755); err != nil {
		t.Fatalf("create copy dir: %v", err)
	}
	if err := os.WriteFile(copied, []byte("gate-v1\n"), 0o755); err != nil {
		t.Fatalf("write copied gate: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	rows := []struct {
		name     string
		role     Role
		command  string
		decision DecisionKind
		rootSkip bool
	}{
		{name: "D1 coordinator set enforcing dangling", role: RoleCoordinator, command: dangling + " set enforcing", decision: DecisionDeny},
		{name: "D2 coordinator set permissive absent", role: RoleCoordinator, command: filepath.Join(scratch, "absent", "eci-command-gate-mode") + " set permissive", decision: DecisionDeny},
		{name: "D3 coordinator set enforcing unreadable parent", role: RoleCoordinator, command: lockedGate + " set enforcing", decision: DecisionDeny, rootSkip: true},
		{name: "D4 coordinator set enforcing dangling kimi gate", role: RoleCoordinator, command: fixture.kimiGate + " set enforcing", decision: DecisionDeny},
		{name: "D5 coordinator bare set enforcing empty PATH", role: RoleCoordinator, command: "eci-command-gate-mode set enforcing", decision: DecisionDeny},
		{name: "W1 worker set enforcing dangling", role: RoleWorker, command: dangling + " set enforcing", decision: DecisionDeny},
		{name: "B1 coordinator get dangling", role: RoleCoordinator, command: dangling + " get", decision: DecisionAllow},
		{name: "C1 coordinator set enforcing canonical statable", role: RoleCoordinator, command: fixture.eciGate + " set enforcing", decision: DecisionAllow},
		{name: "D7 coordinator set enforcing statable copy", role: RoleCoordinator, command: copied + " set enforcing", decision: DecisionDeny},
		{name: "D7W worker set enforcing statable copy", role: RoleWorker, command: copied + " set enforcing", decision: DecisionDeny},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if row.rootSkip && os.Geteuid() == 0 {
				t.Skip("root bypasses directory permissions")
			}
			result := Classify(Request{
				Provider:       ProviderOmp,
				Role:           row.role,
				CWD:            fixture.home,
				CommandPath:    "/usr/bin/timeout",
				CommandPathSet: true,
				Marker:         MarkerActive,
				ActiveSession:  "test-session",
				HookMode:       true,
				Command:        row.command,
			})
			if result.Decision != row.decision {
				t.Fatalf("decision=%q, want %q; diagnostic=%#v", result.Decision, row.decision, result.Diagnostic)
			}
			if row.decision == DecisionAllow {
				if result.Diagnostic != nil {
					t.Fatalf("diagnostic=%#v, want nil", result.Diagnostic)
				}
				return
			}
			if result.Diagnostic == nil || result.Diagnostic.Code != CodeControlIdentityDenied || result.Diagnostic.Predicate != "gate-mode-identity" {
				t.Fatalf("diagnostic=%#v, want %q/gate-mode-identity", result.Diagnostic, CodeControlIdentityDenied)
			}
		})
	}
}
