package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Run dispatches a typed operation or an explicitly acknowledged native Git exception.
//
// Example: a protected restore exits nonzero before worktree or index writes.
func Run(
	arguments []string,
	streams Streams,
) int {
	native := len(arguments) >= 3 && arguments[0] == "--repo" && arguments[2] == "run-once"
	if err := LogInvocation(len(arguments), native); err != nil {
		warnInvocationLogUnavailable(streams.Error)
	}
	if native {
		return RunOnce(arguments, streams)
	}
	operation, err := ParseOperation(arguments)
	if err == nil {
		err = ExecuteOperation(context.Background(), operation, streams)
	}
	if err != nil {
		if _, writeErr := fmt.Fprintf(streams.Error, "eci-worker-git: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}
	if _, err := io.WriteString(streams.Output, "eci-worker-git: completed fixed operation; inspect the full staged result before checkpoint\n"); err != nil {
		return 1
	}
	return 0
}

// main binds process streams to the Git CLI.
//
// Example: the registered hook refers workers to this executable.
func main() {
	os.Exit(Run(os.Args[1:], Streams{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr}))
}
