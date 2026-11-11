//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newCLIStateRoot(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	base, err := filepath.EvalSymlinks(filepath.Join(home, "tmp"))
	if err != nil {
		t.Fatalf("resolve home temporary directory: %v", err)
	}
	root, err := os.MkdirTemp(base, "eci-command-gate-cli.")
	if err != nil {
		t.Fatalf("create CLI fixture: %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("chmod CLI fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove CLI fixture: %v", err)
		}
	})
	return root
}

func TestRunGetMatchesCompatibilityBytes(t *testing.T) {
	root := newCLIStateRoot(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))

	var stdout, stderr bytes.Buffer
	if status := run([]string{"get"}, strings.NewReader(""), &stdout, &stderr); status != 0 {
		t.Fatalf("get status = %d, want 0; stderr=%q", status, stderr.String())
	}
	if got, want := stdout.String(), "{\"mode\":\"permissive\",\"config_state\":\"missing\"}\n"; got != want {
		t.Fatalf("get output = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("get stderr = %q, want empty", stderr.String())
	}
}

func TestRunSetMatchesCompatibilityAndPersistsExactBytes(t *testing.T) {
	root := newCLIStateRoot(t)
	configRoot := filepath.Join(root, "config")
	t.Setenv("XDG_CONFIG_HOME", configRoot)

	for _, mode := range []Mode{ModePermissive, ModeEnforcing} {
		var stdout, stderr bytes.Buffer
		if status := run([]string{"set", string(mode)}, strings.NewReader(""), &stdout, &stderr); status != 0 {
			t.Fatalf("set %q status = %d; stderr=%q", mode, status, stderr.String())
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("set %q output = stdout %q stderr %q, want both empty", mode, stdout.String(), stderr.String())
		}
		configPath := filepath.Join(configRoot, ConfigDirectoryName, ConfigFileName)
		contents, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read set %q config: %v", mode, err)
		}
		if got, want := string(contents), string(mode)+"\n"; got != want {
			t.Fatalf("set %q bytes = %q, want %q", mode, got, want)
		}
	}

	var stdout, stderr bytes.Buffer
	if status := run([]string{"set", "invalid"}, strings.NewReader(""), &stdout, &stderr); status != 2 {
		t.Fatalf("invalid set status = %d, want 2", status)
	}
	if got, want := stderr.String(), "usage: eci-command-gate-mode get | set permissive | set enforcing\n"; got != want {
		t.Fatalf("invalid set stderr = %q, want %q", got, want)
	}
}

func TestRunFinalizePermissivePersistsReducedEvent(t *testing.T) {
	root := newCLIStateRoot(t)
	configRoot := filepath.Join(root, "config")
	stateRoot := filepath.Join(root, "state")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv(EnvDenialCommand, "git add -A")
	t.Setenv(EnvDenialCWD, "/tmp/repo")
	t.Setenv(EnvDenialSessionID, "sess-9")

	var setOut, setErr bytes.Buffer
	if status := run([]string{"set", "permissive"}, strings.NewReader(""), &setOut, &setErr); status != 0 {
		t.Fatalf("seed permissive status = %d; stderr=%q", status, setErr.String())
	}
	denial := `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"[ECI_COMMAND_SYNTAX_DENIED] ECI gate denied (phase=PreToolUse, operation=plan-segment)"}}`
	var stdout, stderr bytes.Buffer
	if status := run(
		[]string{"finalize", "omp", "worker", "active", "parser"},
		strings.NewReader(denial),
		&stdout,
		&stderr,
	); status != 0 {
		t.Fatalf("permissive finalize status = %d; stderr=%q", status, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("permissive finalize output = stdout %q stderr %q, want both empty", stdout.String(), stderr.String())
	}
	logPath := filepath.Join(stateRoot, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName)
	record, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read permissive telemetry: %v", err)
	}
	var event struct {
		Schema      string `json:"schema"`
		Event       string `json:"event"`
		Provider    string `json:"provider"`
		Role        string `json:"role"`
		Marker      string `json:"marker"`
		Source      string `json:"source"`
		Code        string `json:"code"`
		Operation   string `json:"operation"`
		ConfigState string `json:"config_state"`
		Command     string `json:"command"`
		CWD         string `json:"cwd"`
		SessionID   string `json:"session_id"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(record), &event); err != nil {
		t.Fatalf("decode permissive telemetry: %v; record=%q", err, record)
	}
	if event.Schema != EventSchema || event.Event != "would-deny" || event.Provider != "omp" ||
		event.Role != "worker" || event.Marker != "active" || event.Source != "parser" ||
		event.Code != string(TelemetryCodeCommandSyntaxDenied) || event.Operation != string(TelemetryOperationPlanSegment) ||
		event.ConfigState != string(ConfigStateConfiguredPermissive) ||
		event.Command != "git add -A" || event.CWD != "/tmp/repo" || event.SessionID != "sess-9" ||
		!strings.HasPrefix(event.Reason, "[ECI_COMMAND_SYNTAX_DENIED]") {
		t.Fatalf("telemetry event = %#v", event)
	}
}

func TestRunFinalizeEnforcingForwardsBytesWithoutTelemetry(t *testing.T) {
	root := newCLIStateRoot(t)
	configRoot := filepath.Join(root, "config")
	stateRoot := filepath.Join(root, "state")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)

	var setOut, setErr bytes.Buffer
	if status := run([]string{"set", "enforcing"}, strings.NewReader(""), &setOut, &setErr); status != 0 {
		t.Fatalf("seed enforcing status = %d; stderr=%q", status, setErr.String())
	}
	raw := "not-json\x00still-forwarded"
	var stdout, stderr bytes.Buffer
	if status := run(
		[]string{"finalize", "kimi", "coordinator", "inactive", "legacy"},
		strings.NewReader(raw),
		&stdout,
		&stderr,
	); status != 0 {
		t.Fatalf("enforcing finalize status = %d; stderr=%q", status, stderr.String())
	}
	if stdout.String() != raw || stderr.Len() != 0 {
		t.Fatalf("enforcing finalize output = stdout %q stderr %q, want raw stdout and empty stderr", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(stateRoot, TelemetryParentDirectoryName)); !os.IsNotExist(err) {
		t.Fatalf("enforcing finalize created telemetry state: %v", err)
	}
}

// TestRunFinalizeInvalidModeConfigDoesNotForwardWouldDeny checks the mode fallback at the admission boundary.
//
// Example: a corrupted user preference file must not turn a harmless command's would-deny result into a visible denial.
func TestRunFinalizeInvalidModeConfigDoesNotForwardWouldDeny(t *testing.T) {
	root := newCLIStateRoot(t)
	configRoot := filepath.Join(root, "config")
	stateRoot := filepath.Join(root, "state")
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)

	configDirectory := filepath.Join(configRoot, ConfigDirectoryName)
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	configPath := filepath.Join(configDirectory, ConfigFileName)
	if err := os.WriteFile(configPath, []byte("permissive\r\n"), 0o600); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}

	denial := `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"[ECI_COMMAND_SYNTAX_DENIED] ECI gate denied (phase=PreToolUse, operation=plan-segment)"}}`
	var stdout, stderr bytes.Buffer
	if status := run(
		[]string{"finalize", "omp", "coordinator", "active", "parser"},
		strings.NewReader(denial),
		&stdout,
		&stderr,
	); status != 0 {
		t.Fatalf("finalize status = %d; stderr=%q", status, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("invalid config finalized output = stdout %q stderr %q, want both empty", stdout.String(), stderr.String())
	}

	logPath := filepath.Join(stateRoot, TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName)
	record, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fallback telemetry: %v", err)
	}
	var event struct {
		ConfigState string `json:"config_state"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(record), &event); err != nil {
		t.Fatalf("decode fallback telemetry: %v; record=%q", err, record)
	}
	if event.ConfigState != string(ConfigStateInvalidBytes) {
		t.Fatalf("fallback telemetry config state = %q, want %q", event.ConfigState, ConfigStateInvalidBytes)
	}
}

func TestRunFinalizePermissiveMalformedAndOversizeWarns(t *testing.T) {
	root := newCLIStateRoot(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))

	var setOut, setErr bytes.Buffer
	if status := run([]string{"set", "permissive"}, strings.NewReader(""), &setOut, &setErr); status != 0 {
		t.Fatalf("seed permissive status = %d; stderr=%q", status, setErr.String())
	}
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "malformed", raw: "not-json"},
		{name: "oversize", raw: strings.Repeat("x", MaxDenialBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := run(
				[]string{"finalize", "omp", "coordinator", "active", "legacy"},
				strings.NewReader(test.raw),
				&stdout,
				&stderr,
			); status != 0 {
				t.Fatalf("%s finalize status = %d", test.name, status)
			}
			if stdout.Len() != 0 || stderr.String() != TelemetryUnavailableMessage {
				t.Fatalf("%s finalize output = stdout %q stderr %q, want empty stdout and fixed warning", test.name, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunFinalizeUnknownCodeAndOperationUseClosedFallbacks(t *testing.T) {
	root := newCLIStateRoot(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))

	var setOut, setErr bytes.Buffer
	if status := run([]string{"set", "permissive"}, strings.NewReader(""), &setOut, &setErr); status != 0 {
		t.Fatalf("seed permissive status = %d; stderr=%q", status, setErr.String())
	}
	denial := `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"[ECI_FUTURE_DENIAL] ECI gate denied (phase=PreToolUse, operation=future-boundary)"}}`
	var stdout, stderr bytes.Buffer
	if status := run(
		[]string{"finalize", "kimi", "worker", "inactive", "legacy"},
		strings.NewReader(denial),
		&stdout,
		&stderr,
	); status != 0 {
		t.Fatalf("fallback finalize status = %d; stderr=%q", status, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("fallback finalize output = stdout %q stderr %q, want both empty", stdout.String(), stderr.String())
	}
	logPath := filepath.Join(root, "state", TelemetryParentDirectoryName, TelemetryDirectoryName, TelemetryFileName)
	record, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fallback telemetry: %v", err)
	}
	var event struct {
		Code      string `json:"code"`
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(record), &event); err != nil {
		t.Fatalf("decode fallback telemetry: %v; record=%q", err, record)
	}
	if event.Code != string(TelemetryCodeOther) || event.Operation != string(TelemetryOperationOther) {
		t.Fatalf("fallback telemetry = %#v, want code=%q operation=%q", event, TelemetryCodeOther, TelemetryOperationOther)
	}
}

func TestRunUsageHasExactExitCodeAndBytes(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		nil,
		{},
		{"get", "extra"},
		{"finalize", "other", "coordinator", "active", "parser"},
	}
	for index, arguments := range tests {
		arguments := arguments
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := run(arguments, strings.NewReader(""), &stdout, &stderr); status != 2 {
				t.Fatalf("arguments %#v status = %d, want 2", arguments, status)
			}
			if stdout.Len() != 0 {
				t.Fatalf("arguments %#v stdout = %q, want empty", arguments, stdout.String())
			}
			if got, want := stderr.String(), "usage: eci-command-gate-mode get | set permissive | set enforcing\n"; got != want {
				t.Fatalf("arguments %#v stderr = %q, want %q", arguments, got, want)
			}
		})
	}
}

func TestReadBoundedDenialRejectsOversizeAndDrainsStdin(t *testing.T) {
	raw := strings.Repeat("x", MaxDenialBytes+17)
	reader := strings.NewReader(raw)
	if _, err := readBoundedDenial(reader); err == nil {
		t.Fatal("readBoundedDenial accepted oversized input")
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read remainder: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("oversized input left %d stdin bytes unread", len(remaining))
	}
}

func TestProductionBinaryIsCompiledAndDoesNotEmbedPythonRuntime(t *testing.T) {
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", "..", ".."))
	binaryPath := filepath.Join(moduleRoot, "bin", "eci-command-gate-mode")
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read production binary: %v", err)
	}
	if len(binary) < 4 || !bytes.Equal(binary[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		t.Fatalf("production gate-mode path is not an ELF executable: %s", binaryPath)
	}
	if bytes.Contains(bytes.ToLower(binary), []byte("python")) {
		t.Fatalf("production binary contains a Python runtime reference: %s", binaryPath)
	}
}
