package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// requirementLedgerName is the project-understanding snapshot the helper reads
// and rewrites inside the session's context directory.
const requirementLedgerName = "project-understanding.yaml"

// requirementAssignment prepares one requirement assignment with the native
// helper while holding the mutation lock. The lock is released before the
// event goes through the ledger owner; the payload is printed only after that
// append succeeds, so a failed append never emits an assignment.
func requirementAssignment(args []string) error {
	if len(args) != 3 || args[1] != "--request" {
		return usageError()
	}
	requestPath, err := filepath.Abs(args[2])
	if err != nil {
		return exitError{code: 1}
	}
	if requestPath, err = filepath.EvalSymlinks(requestPath); err != nil {
		return exitError{code: 1}
	}
	sid, err := currentSession()
	if err != nil {
		return err
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return exitError{code: 1, msg: "eci-active: no current session directory found under the proof root."}
	}
	ledgerDir, err := contextSessionDir(filepath.Base(sessionDir))
	if err != nil {
		return err
	}
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "run", "-mod=readonly", ".",
		"--ledger", filepath.Join(ledgerDir, requirementLedgerName), "--request", requestPath)
	cmd.Dir = filepath.Join(eciBaseDir(), "hooks", "lib", "eci-requirement-assignment-go")
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	runErr := cmd.Run()
	lock.Close()
	if runErr != nil {
		return exitError{code: 1}
	}

	var prep struct {
		Event   json.RawMessage `json:"event"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(out.Bytes(), &prep); err != nil || len(prep.Event) == 0 || len(prep.Payload) == 0 {
		return exitError{code: 1, msg: "requirement-assignment: helper returned an invalid assignment envelope."}
	}
	if err := appendLedgerEntry(string(prep.Event), false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitError{code: 1, msg: "requirement-assignment: snapshot saved; no assignment emitted; inspect saved state and reconcile missing material-change history with ledger-append, then retry with empty edits for faithful reuse. Canonical log append failed."}
	}

	var payload bytes.Buffer
	if err := json.Compact(&payload, prep.Payload); err != nil {
		return exitError{code: 1, msg: "requirement-assignment: helper returned an invalid payload."}
	}
	payload.WriteByte('\n')
	_, err = os.Stdout.Write(payload.Bytes())
	return err
}
