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
	AgentName   string         `json:"agentName"`
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
	case "reduce", "check":
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	var in cliInput
	if err := json.NewDecoder(input).Decode(&in); err != nil {
		return err
	}
	req := Request{State: in.State, Branch: in.Branch}
	if args[0] == "check" {
		disabled, err := gateDisabled()
		if err != nil {
			return err
		}
		if disabled {
			return json.NewEncoder(output).Encode(struct {
				Block bool `json:"block"`
			}{})
		}
		req.Action, req.Tool, req.Input = callAction, in.Call.ToolName, in.Call.Input
		req.EditTargets, req.Cwd, req.AgentName = in.Call.EditTargets, in.Call.Cwd, in.Call.AgentName
		response, err := decide(req)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(struct {
			Block  bool   `json:"block"`
			Reason string `json:"reason,omitempty"`
		}{Block: response.Reason != "", Reason: response.Reason})
	}
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
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("locating ~/.omp/agent for %s: %w", disableFileName, err)
	}
	_, err = os.Stat(filepath.Join(home, ".omp", "agent", disableFileName))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("checking %s: %w", disableFileName, err)
	}
}
