// Package codingskill evaluates omp events against agent-local skill evidence.
//
// Example: the hook helper invokes Run with describe, reduce, or check.
package codingskill

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// disableFileName names the file under ~/.omp/agent whose presence makes check allow every call.
// The helper reads it per call, so creating it disables the gate in running sessions without a restart.
//
// Example: touch ~/.omp/agent/coding-skill-gate.disabled while the hook is being changed.
const disableFileName = "coding-skill-gate.disabled"

// cliEvent carries the host data required to evaluate a single tool event.
//
// Example: a read result contains the actual displayed content and error flag.
type cliEvent struct {
	ToolName    string         `json:"toolName"`
	Input       map[string]any `json:"input"`
	Details     map[string]any `json:"details"`
	IsError     bool           `json:"isError"`
	EditTargets []string       `json:"editTargets"`
	Cwd         string         `json:"cwd"`
	AgentName   AgentName      `json:"agentName"`
}

// cliInput provides one state transition or preflight request.
//
// Example: the first reduce includes the current agent's active transcript branch.
type cliInput struct {
	State   State            `json:"state"`
	Branch  []map[string]any `json:"branch"`
	Results []cliEvent       `json:"results"`
	Call    cliEvent         `json:"call"`
}

// checkVerdict is the check reply: whether the host blocks the call, and the denial text when it does.
//
// Example: {"block":true,"reason":"Coding skill gate: writing a.go requires ..."} denies an unread Go write.
type checkVerdict struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason,omitempty"`
}

// Run applies a single JSON request or describes the supported events.
//
// Example: reduce folds completed result events into an opaque agent state.
func Run(
	args []string,
	input io.Reader,
	output io.Writer,
) error {
	if len(args) != 1 {
		return fmt.Errorf("expected describe, reduce, or check")
	}
	switch args[0] {
	case "describe":
		extensions := slices.Clone(instructionExtensions)
		for ext := range codeExtensions {
			extensions = append(extensions, ext)
		}
		sort.Strings(extensions)
		return json.NewEncoder(output).Encode(map[string]any{
			"callTools":          callTools,
			"resultTools":        []string{"read"},
			"gatedExtensions":    extensions,
			"gatedAgentPrefixes": []string{eciPresetPrefix},
		})
	case "check":
		return checkCall(input, output)
	case "reduce":
		return reduceResults(input, output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// checkCall answers one check request with a checkVerdict.
// While the gate is disabled it allows the call without decoding the request, so a running session keeps working even if a changed helper cannot read its state.
//
// Example: an unread Go write gets {"block":true,"reason":"Coding skill gate: ..."}; with the disable file present it gets {"block":false}.
func checkCall(
	input io.Reader,
	output io.Writer,
) error {
	disabled, err := gateDisabled()
	if err != nil {
		return err
	}
	if disabled {
		// Read to EOF anyway: the bridge writes the whole request, and a helper that exits first breaks that pipe, which the bridge reports as a block.
		if _, err := io.Copy(io.Discard, input); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(checkVerdict{})
	}
	var in cliInput
	if err := json.NewDecoder(input).Decode(&in); err != nil {
		return err
	}
	response, err := decide(Request{
		Action:      callAction,
		Tool:        in.Call.ToolName,
		Input:       in.Call.Input,
		EditTargets: in.Call.EditTargets,
		Cwd:         in.Call.Cwd,
		AgentName:   in.Call.AgentName,
		Branch:      in.Branch,
		State:       in.State,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(checkVerdict{Block: response.Reason != "", Reason: response.Reason})
}

// reduceResults folds one reduce request's read results into the agent state and replies with that state.
// A request without results hydrates the state from its transcript branch.
//
// Example: a completed skill://coding-go read result replies with a state that credits coding-go.
func reduceResults(
	input io.Reader,
	output io.Writer,
) error {
	var in cliInput
	if err := json.NewDecoder(input).Decode(&in); err != nil {
		return err
	}
	req := Request{State: in.State, Branch: in.Branch}
	if len(in.Results) == 0 {
		req.Action = hydrateAction
		response, err := decide(req)
		if err != nil {
			return err
		}
		req.State = response.State
	}
	for _, event := range in.Results {
		req.Action, req.Tool, req.Input, req.Details, req.IsError = resultAction, event.ToolName, event.Input, event.Details, event.IsError
		response, err := decide(req)
		if err != nil {
			return err
		}
		req.State, req.Branch = response.State, nil
	}
	return json.NewEncoder(output).Encode(struct {
		State State `json:"state"`
	}{State: req.State})
}

// gateDisabled reports whether ~/.omp/agent/coding-skill-gate.disabled exists.
//
// Example: check allows the call when gateDisabled returns true.
func gateDisabled() (bool, error) {
	// TODO: when HOME is unset or relative, the TS bridge's hookAgentRoot uses the account home, but here an unset HOME fails every gated check and a relative one resolves against the helper's cwd; resolve the agent root like the bridge, here and in isInstructionPath, if omp ever runs that way.
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("locating ~/.omp/agent for %s: %w", disableFileName, err)
	}
	_, err = os.Stat(filepath.Join(home, ompDirName, agentDirName, disableFileName))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("checking %s: %w", disableFileName, err)
	}
}
