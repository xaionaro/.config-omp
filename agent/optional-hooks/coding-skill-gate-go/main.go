// Command coding-skill-gate evaluates omp events against agent-local skill evidence.
//
// Example: the optional omp bridge invokes describe, reduce, and check.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// cliEvent carries the host data required to evaluate a single tool event.
//
// Example: a read result contains the actual displayed content and error flag.
type cliEvent struct {
	ToolName    string         `json:"toolName"`
	Input       map[string]any `json:"input"`
	Details     map[string]any `json:"details"`
	IsError     bool           `json:"isError"`
	EditTargets []string       `json:"editTargets"`
}

// cliInput provides one state transition or preflight request.
//
// Example: the first reduce includes the current agent's active transcript branch.
type cliInput struct {
	State   State            `json:"state"`
	CWD     string           `json:"cwd"`
	Branch  []map[string]any `json:"branch"`
	Results []cliEvent       `json:"results"`
	Call    cliEvent         `json:"call"`
}

// main invokes the selected command and reports errors to the host.
//
// Example: the bridge calls check before a write reaches the runtime.
func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run applies a single JSON request or describes the supported events.
//
// Example: reduce folds completed result events into an opaque agent state.
func run(
	args []string,
	input io.Reader,
	output io.Writer,
) error {
	if len(args) != 1 {
		return fmt.Errorf("expected describe, reduce, or check")
	}
	if args[0] == "describe" {
		return json.NewEncoder(output).Encode(map[string]any{
			"callTools":   []string{"write", "edit"},
			"resultTools": []string{"read"},
		})
	}
	if args[0] != "reduce" && args[0] != "check" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	var in cliInput
	if err := json.NewDecoder(input).Decode(&in); err != nil {
		return err
	}
	req := Request{State: in.State, CWD: in.CWD, Branch: in.Branch}
	if args[0] == "check" {
		req.Action, req.Tool, req.Input = "call", in.Call.ToolName, in.Call.Input
		req.EditTargets = in.Call.EditTargets
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
		req.Action = "result"
		response, err := decide(req)
		if err != nil {
			return err
		}
		req.State = response.State
	}
	for _, event := range in.Results {
		req.Action, req.Tool, req.Input, req.Details, req.IsError = "result", event.ToolName, event.Input, event.Details, event.IsError
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
