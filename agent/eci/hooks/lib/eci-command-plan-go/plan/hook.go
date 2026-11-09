package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// RunHook handles one provider callback. Uncertain external metadata remains
// advisory; only a resolved command effect emits a PreToolUse denial.
//
// Example: a cat callback emits silence; a live-control write emits hook JSON.
func RunHook(
	input io.Reader,
	output io.Writer,
) int {
	decoder := json.NewDecoder(input)
	var envelope HookInput
	if err := decoder.Decode(&envelope); err != nil {
		if code, denied := hookMalformedIdentityDenied(output); denied {
			return code
		}
		return hookAdvisory("decode callback", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return hookAdvisory("decode trailing callback", err)
	}
	if envelope.ToolName != "" && envelope.ToolName != "Bash" {
		return 0
	}
	if hookIdentityMalformed(envelope) {
		if code, denied := hookMalformedIdentityDenied(output); denied {
			return code
		}
	}
	request, err := hookRequest(envelope)
	if err != nil {
		hookAdvisory("resolve callback context", err)
	}
	request.HookMode = true
	request.CollectShellCommands = true
	result := Classify(request)
	if result.Decision != DecisionDeny {
		if diagnostic := inspectHookEffects(request, result); diagnostic != nil {
			result = deniedResult(request, *diagnostic)
		}
	}
	if result.Decision == DecisionDeny && result.HookSpecificOutput != nil {
		// Emit only the provider's callback envelope, never planner protocol fields.
		denial := struct {
			HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput"`
		}{HookSpecificOutput: result.HookSpecificOutput}
		encoded, err := json.Marshal(denial)
		if err != nil {
			return hookAdvisory("encode callback denial", err)
		}
		encoded, err = finalizeHookDenial(request, encoded)
		if err != nil {
			hookAdvisory("finalize inactive denial", err)
		}
		if len(encoded) == 0 {
			return 0
		}
		if _, err := fmt.Fprintln(output, string(encoded)); err != nil {
			return hookAdvisory("write callback denial", err)
		}
		return 0
	}
	if err := recordHookActivity(request, result); err != nil {
		return hookAdvisory("record callback activity", err)
	}
	return 0
}

// hookCodeMalformedIdentity identifies a callback whose identity cannot be trusted.
const hookCodeMalformedIdentity DiagnosticCode = "ECI_MALFORMED_HOOK_IDENTITY"

// hookIdentityMalformed reports an envelope whose identity fields cannot name a scope.
// Absent cwd remains valid because the callback resolves it from the process.
//
// Example: session_id [] or an empty tool_name is malformed, while cwd "" is not.
func hookIdentityMalformed(envelope HookInput) bool {
	if envelope.SessionID == "" || envelope.ToolName == "" {
		return true
	}
	for _, r := range envelope.CWD {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// hookMalformedIdentityDenied emits a fail-closed denial when any active ECI scope exists.
// Unknown marker state is treated as active, so an unreadable proof root also denies.
//
// Example: session_id [] with an active marker emits malformed hook identity.
func hookMalformedIdentityDenied(output io.Writer) (int, bool) {
	active, err := hookActiveMarkerPresent()
	if err == nil && !active {
		return 0, false
	}
	request := Request{Provider: ProviderOmp, Role: RoleCoordinator, Marker: MarkerActive, HookMode: true}
	result := deniedResult(request, Diagnostic{
		Code:        hookCodeMalformedIdentity,
		Operation:   "hook-identity",
		Reason:      "malformed hook identity",
		Remediation: "repair session_id, tool_name, and cwd in the callback envelope",
	})
	encoded, err := json.Marshal(struct {
		HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput"`
	}{HookSpecificOutput: result.HookSpecificOutput})
	if err != nil {
		return hookAdvisory("encode identity denial", err), true
	}
	if _, err := fmt.Fprintln(output, string(encoded)); err != nil {
		return hookAdvisory("write identity denial", err), true
	}
	return 0, true
}

// hookActiveMarkerPresent reports whether any session directory holds an eci_active marker.
//
// Example: an empty proof root has no active scope; an unreadable entry returns an error.
func hookActiveMarkerPresent() (bool, error) {
	root, err := hookProofRoot()
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		_, err := os.Stat(filepath.Join(root, entry.Name(), "eci_active"))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// finalizeHookDenial preserves the optional compiled inactive gate-mode route.
// Active ECI denials never enter its permissive telemetry reduction.
//
// Example: an inactive permissive callback records telemetry and emits silence.
func finalizeHookDenial(
	request Request,
	denial []byte,
) ([]byte, error) {
	if request.Marker == MarkerActive {
		return denial, nil
	}
	tool := filepath.Join(os.Getenv("HOME"), ".omp", "agent", "eci", "bin", "eci-command-gate-mode")
	info, err := os.Stat(tool)
	if errors.Is(err, os.ErrNotExist) {
		return denial, nil
	}
	if err != nil {
		return denial, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return denial, nil
	}
	command := exec.Command(tool, "finalize", string(request.Provider), string(request.Role), string(request.Marker), "parser")
	command.Stdin = bytes.NewReader(denial)
	command.Stderr = os.Stderr
	finalized, err := command.Output()
	if err != nil {
		return denial, err
	}
	return finalized, nil
}

// hookAdvisory records an external callback failure without inventing an effect.
//
// Example: a malformed transcript is diagnostic context, so its command continues.
func hookAdvisory(
	operation string,
	err error,
) int {
	if err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "command hook advisory: %s: %v\n", operation, err); writeErr != nil {
			return 0
		}
	}
	return 0
}
