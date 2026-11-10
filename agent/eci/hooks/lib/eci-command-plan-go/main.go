// Command eci-command-plan is the CLI for package plan.
package main

import (
	"os"

	"github.com/xaionaro-go/eci-command-plan/plan"
)

// main selects the callback envelope or the existing planner request protocol.
//
// Example: --hook consumes one PreToolUse callback; no arguments use the planner.
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--hook" {
		os.Exit(plan.RunHook(os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(plan.Run(os.Stdin, os.Stdout))
}
