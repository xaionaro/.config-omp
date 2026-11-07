package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// b3Fixture is a fixture HOME with an omp gate, an optional configured Kimi
// root, and the omp receipt written as build-native-binaries.sh writes it.
type b3Fixture struct {
	home       string
	eciGate    string
	eciReceipt string
	kimiRoot   string
	kimiGate   string
}

func newB3Fixture(t *testing.T) b3Fixture {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve fixture directory: %v", err)
	}
	fixture := b3Fixture{
		home:     filepath.Join(directory, "home"),
		kimiRoot: filepath.Join(directory, "configured-kimi"),
	}
	eciRoot := filepath.Join(fixture.home, ".omp", "agent", "eci")
	if err := os.MkdirAll(filepath.Join(eciRoot, "bin"), 0o755); err != nil {
		t.Fatalf("create omp bin: %v", err)
	}
	fixture.eciGate = filepath.Join(eciRoot, "bin", "eci-command-gate-mode")
	fixture.eciReceipt = fixture.eciGate + ".sha256"
	fixture.kimiGate = filepath.Join(fixture.kimiRoot, "bin", "eci-command-gate-mode")
	if err := os.WriteFile(fixture.eciGate, []byte("gate-v1\n"), 0o755); err != nil {
		t.Fatalf("write omp gate: %v", err)
	}
	fixture.writeReceipt(t, []byte("gate-v1\n"), "eci-command-gate-mode")
	t.Setenv("HOME", fixture.home)
	t.Setenv("KIMI_CODE_HOME", fixture.kimiRoot)
	return fixture
}

// writeReceipt writes the "<sha256>  <name>" line for contents.
func (f b3Fixture) writeReceipt(t *testing.T, contents []byte, name string) {
	t.Helper()
	sum := sha256.Sum256(contents)
	line := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	if err := os.WriteFile(f.eciReceipt, []byte(line), 0o644); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
}

func (f b3Fixture) hardlinkKimi(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.kimiRoot, "bin"), 0o755); err != nil {
		t.Fatalf("create kimi bin: %v", err)
	}
	if err := os.Link(f.eciGate, f.kimiGate); err != nil {
		t.Fatalf("hardlink kimi gate: %v", err)
	}
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = writer
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(reader)
		done <- string(data)
	}()
	fn()
	os.Stderr = saved
	if err := writer.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	return <-done
}

// TestGateModeIdentityIsProviderScoped verifies that the omp identity never
// depends on the Kimi peer or on the omp receipt's denial, while the Kimi
// identity keeps its two-root checks. Receipt problems are report-only.
//
// Example: a missing Kimi root leaves the omp identity clean, and a receipt
// mismatch writes an advisory without a failure.
func TestGateModeIdentityIsProviderScoped(t *testing.T) {
	kimiStates := []struct {
		name  string
		setup func(*testing.T, b3Fixture)
	}{
		{name: "kimi absent", setup: func(*testing.T, b3Fixture) {}},
		{name: "kimi hardlinked", setup: func(t *testing.T, f b3Fixture) { f.hardlinkKimi(t) }},
		{name: "kimi split same content", setup: func(t *testing.T, f b3Fixture) {
			if err := os.MkdirAll(filepath.Join(f.kimiRoot, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.kimiGate, []byte("gate-v1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "kimi split different content", setup: func(t *testing.T, f b3Fixture) {
			if err := os.MkdirAll(filepath.Join(f.kimiRoot, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.kimiGate, []byte("other\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "kimi root present, gate missing", setup: func(t *testing.T, f b3Fixture) {
			if err := os.MkdirAll(filepath.Join(f.kimiRoot, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, kimi := range kimiStates {
		t.Run("omp unaffected/"+kimi.name, func(t *testing.T) {
			fixture := newB3Fixture(t)
			kimi.setup(t, fixture)
			identity := gateModeIdentityForEnvironment(ProviderOmp)
			if identity.failure != "" {
				t.Fatalf("omp failure=%q, want none", identity.failure)
			}
			if len(identity.canonicalPaths) != 1 || identity.canonicalPaths[0] != fixture.eciGate {
				t.Fatalf("omp paths=%q, want [%q]", identity.canonicalPaths, fixture.eciGate)
			}
		})
	}

	t.Run("omp receipt states are advisory only", func(t *testing.T) {
		cases := []struct {
			name     string
			mutate   func(*testing.T, b3Fixture)
			advisory string
		}{
			{name: "receipt matches", mutate: func(*testing.T, b3Fixture) {}, advisory: ""},
			{name: "receipt missing", mutate: func(t *testing.T, f b3Fixture) {
				if err := os.Remove(f.eciReceipt); err != nil {
					t.Fatal(err)
				}
			}, advisory: "canonical-receipt-missing"},
			{name: "receipt mismatch", mutate: func(t *testing.T, f b3Fixture) {
				f.writeReceipt(t, []byte("other\n"), "eci-command-gate-mode")
			}, advisory: "canonical-receipt-mismatch"},
			{name: "receipt wrong name", mutate: func(t *testing.T, f b3Fixture) {
				f.writeReceipt(t, []byte("gate-v1\n"), "eci-active")
			}, advisory: "canonical-receipt-mismatch"},
			{name: "receipt malformed", mutate: func(t *testing.T, f b3Fixture) {
				if err := os.WriteFile(f.eciReceipt, []byte(strings.Repeat("x", 2048)), 0o644); err != nil {
					t.Fatal(err)
				}
			}, advisory: "canonical-receipt-invalid"},
			{name: "receipt symlink", mutate: func(t *testing.T, f b3Fixture) {
				target := filepath.Join(filepath.Dir(f.eciReceipt), "target.sha256")
				f.writeReceipt(t, []byte("gate-v1\n"), "eci-command-gate-mode")
				if err := os.Rename(f.eciReceipt, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, f.eciReceipt); err != nil {
					t.Fatal(err)
				}
			}, advisory: "canonical-receipt-invalid"},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				fixture := newB3Fixture(t)
				fixture.hardlinkKimi(t)
				testCase.mutate(t, fixture)
				var identity gateModeIdentity
				advisory := captureStderr(t, func() {
					identity = gateModeIdentityForEnvironment(ProviderOmp)
				})
				if identity.failure != "" {
					t.Fatalf("omp failure=%q, want none (receipt is report-only)", identity.failure)
				}
				if testCase.advisory == "" {
					if advisory != "" {
						t.Fatalf("advisory=%q, want none", advisory)
					}
					return
				}
				if !strings.Contains(advisory, testCase.advisory) {
					t.Fatalf("advisory=%q, want %q", advisory, testCase.advisory)
				}
			})
		}
	})

	t.Run("omp own checks still deny", func(t *testing.T) {
		fixture := newB3Fixture(t)
		if err := os.Remove(fixture.eciGate); err != nil {
			t.Fatal(err)
		}
		if identity := gateModeIdentityForEnvironment(ProviderOmp); identity.failure != "canonical-path-missing" {
			t.Fatalf("missing omp gate failure=%q, want canonical-path-missing", identity.failure)
		}
		fixture = newB3Fixture(t)
		if err := os.Chmod(fixture.eciGate, 0o644); err != nil {
			t.Fatal(err)
		}
		if identity := gateModeIdentityForEnvironment(ProviderOmp); identity.failure != "canonical-metadata-invalid" {
			t.Fatalf("non-executable omp gate failure=%q, want canonical-metadata-invalid", identity.failure)
		}
	})

	t.Run("kimi keeps two-root checks", func(t *testing.T) {
		fixture := newB3Fixture(t)
		if identity := gateModeIdentityForEnvironment(ProviderKimi); identity.failure != "canonical-path-invalid" {
			t.Fatalf("kimi absent failure=%q, want canonical-path-invalid", identity.failure)
		}
		fixture = newB3Fixture(t)
		fixture.hardlinkKimi(t)
		identity := gateModeIdentityForEnvironment(ProviderKimi)
		if identity.failure != "" || len(identity.canonicalPaths) != 2 {
			t.Fatalf("kimi hardlinked: failure=%q paths=%q, want none and two paths", identity.failure, identity.canonicalPaths)
		}
		fixture = newB3Fixture(t)
		if err := os.MkdirAll(filepath.Join(fixture.kimiRoot, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.kimiGate, []byte("gate-v1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if identity := gateModeIdentityForEnvironment(ProviderKimi); identity.failure != "canonical-hardlink-split" {
			t.Fatalf("kimi split failure=%q, want canonical-hardlink-split", identity.failure)
		}
	})
}

// TestGateModeEmptyExpectedDenies is a new fixture-HOME test. Control rows
// K1-K3 and X1 must deny at HEAD and after B3: with the Kimi root absent,
// expected is "" and no gateModeTarget value (basename eci-command-gate-mode)
// can equal it. Expected-change rows E1-E2 fail at HEAD (omp denied by the
// Kimi peer) and pass after B3.
//
// Example: E1 is the omp coordinator set of its own canonical gate with the
// Kimi root absent, which must now be allowed.
func TestGateModeEmptyExpectedDenies(t *testing.T) {
	fixture := newB3Fixture(t)
	kimiIdentity := gateModeIdentityForEnvironment(ProviderKimi)
	if expected, known := gateModeProviderPath(ProviderKimi, kimiIdentity); known || expected != "" {
		t.Fatalf("kimi path: expected=%q known=%t, want empty/false", expected, known)
	}
	rows := []struct {
		name     string
		provider Provider
		role     Role
		command  string
		decision DecisionKind
		code     DiagnosticCode
	}{
		{name: "K1 kimi bare target", provider: ProviderKimi, role: RoleCoordinator, command: "eci-command-gate-mode set enforcing", decision: DecisionDeny},
		{name: "K2 kimi absolute target", provider: ProviderKimi, role: RoleCoordinator, command: "/abs/eci-command-gate-mode set enforcing", decision: DecisionDeny},
		{name: "K3 kimi interpreter target", provider: ProviderKimi, role: RoleCoordinator, command: "bash eci-command-gate-mode set enforcing", decision: DecisionDeny},
		{name: "X1 kimi on omp canonical path", provider: ProviderKimi, role: RoleCoordinator, command: fixture.eciGate + " set enforcing", decision: DecisionDeny},
		{name: "E1 omp coordinator set canonical", provider: ProviderOmp, role: RoleCoordinator, command: fixture.eciGate + " set enforcing", decision: DecisionAllow},
		{name: "E2 omp worker set canonical", provider: ProviderOmp, role: RoleWorker, command: fixture.eciGate + " set enforcing", decision: DecisionDeny, code: CodeControlOwnerRequired},
	}
	for _, row := range rows {
		result := Classify(Request{
			Provider:       row.provider,
			Role:           row.role,
			CWD:            fixture.home,
			CommandPath:    "/usr/bin/timeout",
			CommandPathSet: true,
			Marker:         MarkerActive,
			ActiveSession:  "test-session",
			Command:        row.command,
		})
		if result.Decision != row.decision {
			t.Errorf("%s: decision=%q, want %q; diagnostic=%#v", row.name, result.Decision, row.decision, result.Diagnostic)
		}
		if row.code != "" && (result.Diagnostic == nil || result.Diagnostic.Code != row.code) {
			t.Errorf("%s: diagnostic=%#v, want code %q", row.name, result.Diagnostic, row.code)
		}
	}
}
