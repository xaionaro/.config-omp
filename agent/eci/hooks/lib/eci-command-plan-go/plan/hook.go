package plan

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
	diagnostics io.Writer,
) int {
	decoder := json.NewDecoder(input)
	var envelope HookInput
	if err := decoder.Decode(&envelope); err != nil {
		return hookUnavailable(diagnostics, "decode callback", hookCallbackRepair)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return hookUnavailable(diagnostics, "decode trailing callback", hookCallbackRepair)
	}
	if envelope.ToolName != "" && envelope.ToolName != "Bash" {
		return 0
	}
	if hookIdentityMalformed(envelope) {
		hookAdvisory("invalid callback identity", errors.New("missing or malformed identity"))
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
			return hookUnavailable(diagnostics, "encode callback denial", "retry the call; if it fails again, rebuild the planner with \"$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" eci-command-plan and retry")
		}
		encoded, err = finalizeHookDenial(request, encoded)
		if err != nil {
			hookAdvisory("finalize inactive denial", err)
		}
		if len(encoded) == 0 {
			return 0
		}
		if _, err := fmt.Fprintln(output, string(encoded)); err != nil {
			return hookUnavailable(diagnostics, "write callback denial", "run the hook with stdout connected to a pipe or file the caller reads, then retry")
		}
		return 0
	}
	if err := recordHookActivity(request, result); err != nil {
		return hookAdvisory("record callback activity", err)
	}
	return 0
}

// hookIdentityMalformed reports an envelope whose identity fields cannot name a scope.
// Absent cwd resolves from the process unless cwd_unknown was explicitly set.
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

// finalizeHookDenial applies the user's gate mode to a callback denial.
//
// Example: permissive mode records telemetry and emits silence.
func finalizeHookDenial(
	request Request,
	denial []byte,
) ([]byte, error) {
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

// hookCallbackRepair names the same-actor repair for a callback the planner cannot decode.
//
// Example: a truncated JSON envelope from a provider adapter.
const hookCallbackRepair = "send exactly one JSON object with tool_name and tool_input.command on stdin; on omp repair the adapter that builds it ($HOME/.omp/agent/hooks-disabled/pre/eci-bash-gate.ts and $HOME/.omp/agent/hooks-disabled/lib/eci-hook-runner.ts, loaded by $HOME/.omp/agent/extensions/eci-bash-gate.ts) with Edit, restart the session and retry"

// hookUnavailable reports a callback that could not reach a decision, with its repair step.
//
// Example: malformed input blocks the tool call instead of silently allowing it.
func hookUnavailable(
	diagnostics io.Writer,
	operation string,
	repair string,
) int {
	// The call is blocked by the exit status even when the diagnostic is unwritable.
	_, _ = fmt.Fprintf(diagnostics, "command hook: %s failed; could not decide; the tool call is blocked; repair: %s\n", operation, repair)
	return 2
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
