package plan

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

// TestRunHookPythonDenialIgnoresGateMode checks that permissive gate mode
// silences mode-dependent denials but never a direct Python interpreter call.
//
// Example: /usr/bin/python3 -c 1 stays denied while uncapped make passes.
func TestRunHookPythonDenialIgnoresGateMode(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ECI_PROOF_ROOT", filepath.Join(home, "proof"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("ECI_HOOK_IS_SUBAGENT", "true")
	contextMarker(t, filepath.Join(home, "proof"), "python-owner", cwd)
	bin := filepath.Join(home, ".omp", "agent", "eci", "bin")
	requireHookDirectory(t, bin)
	// A finalizer that consumes the denial and prints nothing behaves like permissive mode.
	require.NoError(t, os.WriteFile(filepath.Join(bin, "eci-command-gate-mode"), []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700))

	// runHook returns the callback output for command in the prepared session.
	//
	// Example: runHook("make test") is empty under permissive mode on Linux.
	runHook := func(command string) string {
		input := HookInput{SessionID: "python-owner", CWD: cwd, ToolName: "Bash"}
		input.ToolInput.Command = command
		callback, err := json.Marshal(input)
		require.NoError(t, err)
		var output bytes.Buffer
		require.Zero(t, RunHook(bytes.NewReader(callback), &output, io.Discard))
		return output.String()
	}

	require.Contains(t, runHook("/usr/bin/python3 -c 1"), "[ECI_PYTHON_SCRIPT_REQUIRED]")
	if runtime.GOOS != "darwin" {
		require.Empty(t, runHook("make test"), "permissive mode must silence the make cap")
	}
}
