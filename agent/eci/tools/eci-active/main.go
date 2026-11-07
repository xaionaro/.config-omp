// Command eci-active is the session-owned ECI coordination CLI for omp.
//
// Lifecycle belongs to the Supervisor. Commands that depend on legacy hook
// markers or on helpers that are not yet ported are rejected before any state
// is published.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// exitError carries a process exit status and an optional stderr message.
type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

// usageError reports a bad invocation: usage on stderr, exit 2.
func usageError() error {
	usage(os.Stderr)
	return exitError{code: 2}
}

func main() {
	err := run(os.Args[1:])
	if err == nil {
		return
	}
	var ee exitError
	if errors.As(err, &ee) {
		if ee.msg != "" {
			fmt.Fprintln(os.Stderr, ee.msg)
		}
		os.Exit(ee.code)
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// run mirrors the shell dispatcher. args includes the command word, so arity
// checks match the original `$#` counts.
func run(args []string) error {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "coordinator-edit-on", "aggregate-migrate", "aggregate-stage", "aggregate-manifest-write", "aggregate-commit":
		return exitError{code: 2, msg: "[ECI_COORDINATION_DEFERRED] This mutator depends on legacy hook marker validation; no state was changed."}
	case "permissive-on":
		if len(args) > 1 && args[1] == "session" {
			return exitError{code: 2, msg: "[ECI_COORDINATION_DEFERRED] Session permissive validation depends on the legacy hook marker; no state was changed."}
		}
	case "status":
		fmt.Println("ECI lifecycle is managed by the Supervisor; native goals own continuation.")
		return nil
	case "on", "off", "wait", "wait-repair", "resume", "nested-enter", "nested-accept", "nested-exit", "aggregate-off":
		return exitError{code: 2, msg: "[ECI_LIFECYCLE_RETIRED] Native goals own continuation; the Supervisor owns ECI lifecycle."}
	case "--help", "-h":
		usage(os.Stdout)
		return nil
	}

	switch cmd {
	case "coordinator-edit-status":
		return coordinatorEditStatus(args)
	case "repository-allow-status":
		return repositoryAllowStatus(args)
	case "repository-allow-on":
		return repositoryAllowOn(args)
	case "repository-allow-off":
		return repositoryAllowOff(args)
	case "permissive-status":
		return permissiveStatus(args)
	case "permissive-on":
		return permissiveOn(args)
	case "permissive-off":
		return permissiveOff(args)
	case "status-refresh":
		return statusRefresh(args)
	case "ledger-append":
		return ledgerAppend(args)
	case "requirement-assignment":
		return requirementAssignment(args)
	case "manifest-write":
		return manifestWrite(args)
	case "accidental-override-cleanup":
		return accidentalOverrideCleanup(args)
	case "maintain-planner":
		return maintainPlanner(args)
	case "sync-runtime":
		return syncRuntime(args)
	case "aggregate-review":
		return exitError{code: 2, msg: fmt.Sprintf("[ECI_DEPENDENCY_UNPORTED] %s depends on a helper that is not yet ported to omp; no state was changed.", cmd)}
	}
	return usageError()
}

// eciExecutable is the path shown in usage. It is the resolved binary path.
func eciExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return "eci-active"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// eciBaseDir is the ECI root: the binary lives at <base>/bin/eci-active.
func eciBaseDir() string {
	return filepath.Dir(filepath.Dir(eciExecutable()))
}

func usage(w io.Writer) {
	exe := eciExecutable()
	fmt.Fprintf(w, `Usage:
  %[1]s ledger-append --json <event-object>
  %[1]s status-refresh --event <event-name>
  %[1]s coordinator-edit-status
  %[1]s repository-allow-status
  %[1]s repository-allow-on <repository> <reason>
  %[1]s repository-allow-off <repository>
  %[1]s permissive-on global <gate> <operation> <request> <ttl-seconds>
  %[1]s permissive-off <session|global> <gate> <operation> <request>
  %[1]s permissive-status <session|global> <gate> <operation> <request>
  %[1]s requirement-assignment --request <request-json>
  %[1]s manifest-write <source-json>
  %[1]s accidental-override-cleanup --authorized-by-user <claim-dir>
  %[1]s maintain-planner
  %[1]s sync-runtime
  %[1]s status

Set ECI_SESSION_ID to target a session. Lifecycle commands are retired; the native goal owns continuation and the
Supervisor owns ECI lifecycle. Coordination uses explicit session identity
and the current working directory.
Marker-dependent coordination mutators are deferred until their hooks migrate.
Not yet ported to omp: aggregate-review.
`, exe)
}
