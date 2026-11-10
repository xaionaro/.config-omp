// Command eci-hook-helper provides the bounded hook-input readers, the
// opt-in security reminder, and the omp edit gate used by the ECI hook scripts.
//
// Modes (argv must match exactly; anything else exits 2):
//
//	stdin                                   echo bounded, valid stdin
//	hook-transcript-first-record <root>     first JSON record of the transcript named by stdin
//	hook-transcript-thread-spawn-metadata <root>
//	                                        parent_thread_id of the transcript named by stdin
//	security-reminder                       opt-in advisory for an edit tool event on stdin
//	reviewer-filter <corpus> <extra> <threshold>
//	                                        filter reviewer violations on stdin (fail closed)
//	cleanup-route <command> <allow-private> cleanup route check
//	environment-command <command>           environment command classification
//	approval-commit-command <command>       approval commit command check
//	edit-gate <bash> <dispatcher>           run the edit dispatcher once per
//	                                        target of the omp edit request on stdin
//	coding-skill <describe|reduce|check>     evaluate coding skill evidence
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/xaionaro-go/eci-hook-helper/codingskill"
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
	case len(args) == 2 && args[0] == "hook-transcript-thread-spawn-metadata":
		path, status := hookTranscriptPath(stdin)
		if status != statusOK {
			return status
		}
		return transcriptThreadSpawnMetadata(args[1], path, stdout)
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
	case len(args) >= 1 && args[0] == "edit-gate":
		return runEditGate(args[1:], stdin, stdout, stderr)
	case len(args) >= 1 && args[0] == "coding-skill":
		if err := codingskill.Run(args[1:], stdin, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return statusRejected
		}
		return statusOK
	}
	return statusRejected
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
