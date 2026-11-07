//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// configStoreFromEnvironment resolves the same provider-neutral configuration
// root used by the Python compatibility CLI.  ConfigStore performs the
// absolute-path, ownership, and descriptor validation; this resolver only
// selects the caller-requested root.
func configStoreFromEnvironment() ConfigStore {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		if home := os.Getenv("HOME"); home != "" {
			root = filepath.Join(home, ".config")
		}
	}
	return ConfigStore{Root: root}
}

func printUsage(writer io.Writer) int {
	_, _ = fmt.Fprintln(writer, "usage: eci-command-gate-mode get | set permissive | set enforcing")
	return 2
}

// run executes the compatibility CLI with injectable streams for exact tests.
// No subprocess or interpreter is used: all config, reduction, and telemetry
// work stays inside this compiled binary.
func run(arguments []string, stdin io.Reader, stdout, stderr io.Writer) int {
	command, err := ParseArguments(arguments)
	if err != nil {
		return printUsage(stderr)
	}

	config := configStoreFromEnvironment()
	switch command.Kind {
	case CommandGet:
		state := config.ReadMode()
		payload := struct {
			Mode        string `json:"mode"`
			ConfigState string `json:"config_state"`
		}{
			Mode:        string(state.Mode),
			ConfigState: string(state.ConfigState),
		}
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			// The payload contains only strings and cannot fail under normal
			// operation, but preserve a nonzero CLI result if that invariant
			// ever changes.
			_, _ = fmt.Fprintf(stderr, "eci-command-gate-mode: get failed: %v\n", marshalErr)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(encoded))
		return 0

	case CommandSet:
		if err := config.SetMode(command.Mode); err != nil {
			_, _ = fmt.Fprintf(stderr, "eci-command-gate-mode: set failed: %v\n", err)
			return 1
		}
		return 0

	case CommandFinalize:
		state, stateErr := TelemetryStoreFromEnvironment()
		if stateErr != nil {
			// A missing or unsafe telemetry root must not turn permissive
			// finalization into a blocker. finalizeDenial emits the fixed
			// warning after reduction/storage fails.
			state = TelemetryStore{}
		}
		return finalizeDenial(config, state, command, stdin, stdout, stderr)

	default:
		return printUsage(stderr)
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
