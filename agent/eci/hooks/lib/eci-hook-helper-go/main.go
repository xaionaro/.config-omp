// Command eci-hook-helper provides the bounded hook-input readers and the
// opt-in security reminder used by the ECI hook scripts.
//
// Modes (argv must match exactly; anything else exits 2):
//
//	stdin                                   echo bounded, valid stdin
//	hook-transcript-first-record <root>     first JSON record of the transcript named by stdin
//	hook-transcript-status <root>           main, subagent, or unknown for the transcript named by stdin
//	security-reminder                       opt-in advisory for an edit tool event on stdin
//	reviewer-filter <corpus> <extra> <threshold>
//	                                        filter reviewer violations on stdin (fail closed)
//	cleanup-route <command> <allow-private> cleanup route check
//	environment-command <command>           environment command classification
//	approval-commit-command <command>       approval commit command check
package main

import (
	"io"
	"os"
)

const (
	statusOK       = 0
	statusFailed   = 1 // I/O failure on stdin or stdout
	statusRejected = 2 // any rejected input or unknown mode
)

// run dispatches one helper mode and returns its process status.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 1 && args[0] == "stdin":
		data, status := readHookStdin(stdin)
		if status != statusOK {
			return status
		}
		return emitBounded(data, stdout)
	case len(args) == 2 && args[0] == "hook-transcript-first-record":
		path, status := hookTranscriptPath(stdin)
		if status != statusOK {
			return status
		}
		return transcriptFirstRecord(args[1], path, stdout)
	case len(args) == 2 && args[0] == "hook-transcript-status":
		path, status := hookTranscriptPath(stdin)
		if status != statusOK {
			return status
		}
		return transcriptStatus(args[1], path, stdout)
	case len(args) == 1 && args[0] == "security-reminder":
		return securityReminder(stdin, stderr)
	case len(args) >= 1 && args[0] == "reviewer-filter":
		return runReviewerFilter(args[1:], stdin, stdout, stderr)
	case len(args) >= 1 && args[0] == "cleanup-route":
		return runCleanupRoute(args[1:], stdin, stdout, stderr)
	case len(args) >= 1 && args[0] == "environment-command":
		return runEnvironmentCommand(args[1:], stdin, stdout, stderr)
	case len(args) >= 1 && args[0] == "approval-commit-command":
		return runApprovalCommitCommand(args[1:], stdin, stdout, stderr)
	}
	return statusRejected
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
