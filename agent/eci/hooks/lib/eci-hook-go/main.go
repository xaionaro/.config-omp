// Command eci-hook runs the ECI provider hooks in one native process.
//
// Example: `eci-hook bash-gate <callback.json` prints the PreToolUse decision.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xaionaro-go/eci-command-plan/plan"
)

const (
	reasonNativeGateUnavailable = "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] "
	// exitUndeliverable blocks the tool call when the deny JSON cannot be written.
	exitUndeliverable = 2
	exitUsage         = 64
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: eci-hook bash-gate")
		return exitUsage
	}
	switch args[0] {
	case "bash-gate":
		return bashGate(stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "eci-hook: unknown subcommand %q\n", args[0])
		return exitUsage
	}
}

// bashGate is validate-bash.sh: a known proof root, then the planner callback.
func bashGate(stdin io.Reader, stdout, stderr io.Writer) (code int) {
	if !strings.HasPrefix(os.Getenv("HOME"), "/") && !strings.HasPrefix(os.Getenv("ECI_PROOF_ROOT"), "/") {
		return deny(stdout, stderr, "validate-bash", reasonNativeGateUnavailable+"hook validate-bash denied the tool call because HOME is empty, unset or relative and ECI_PROOF_ROOT is not an absolute path, so the proof root is unknown; set HOME to an absolute path, or set ECI_PROOF_ROOT to an absolute path, and retry")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			code = deny(stdout, stderr, "validate-bash", fmt.Sprintf("%seci-hook bash-gate failed internally (%v); fix the failing planner code under \"$HOME/.omp/agent/eci/hooks/lib/eci-command-plan-go\", rebuild with `go build -o eci-hook .` in \"$HOME/.omp/agent/eci/hooks/lib/eci-hook-go\", and retry", reasonNativeGateUnavailable, recovered))
		}
	}()
	return plan.RunHook(stdin, stdout, stderr)
}

// deny writes one PreToolUse denial; an unwritable denial exits 2 so the call stays blocked.
func deny(stdout, stderr io.Writer, hook, reason string) int {
	if err := writeDeny(stdout, reason); err != nil {
		fmt.Fprintf(stderr, "%s: could not write the deny decision to stdout; the tool call is blocked\n", hook)
		return exitUndeliverable
	}
	return 0
}
