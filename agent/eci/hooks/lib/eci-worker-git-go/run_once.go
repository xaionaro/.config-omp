package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// RunOnce executes one native Git invocation after an explicit authorization acknowledgment.
//
// Example: --repo /project run-once --reason "approved repair" --user-authorized -- reset -- file.txt.
func RunOnce(
	arguments []string,
	streams Streams,
) int {
	if len(arguments) < 8 || arguments[0] != "--repo" || arguments[1] == "" ||
		arguments[2] != "run-once" || arguments[3] != "--reason" ||
		strings.TrimSpace(arguments[4]) == "" || arguments[5] != "--user-authorized" ||
		arguments[6] != "--" || arguments[7] == "" {
		if _, err := fmt.Fprintln(streams.Error, "usage: eci-worker-git --repo REPOSITORY run-once --reason TEXT --user-authorized -- GIT_ARGS...; use only after the user authorizes this exact command and environment"); err != nil {
			return 1
		}
		return 1
	}
	command := exec.Command("/usr/bin/git", append([]string{"-C", arguments[1]}, arguments[7:]...)...)
	command.Stdin, command.Stdout, command.Stderr = streams.Input, streams.Output, streams.Error
	err := command.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	if _, writeErr := fmt.Fprintf(streams.Error, "eci-worker-git run-once: %v\n", err); writeErr != nil {
		return 1
	}
	return 1
}
