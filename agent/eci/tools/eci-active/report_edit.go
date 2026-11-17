package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// reportEdit authorizes and serializes selective report changes.
//
// Example: an active session forwards its edit object to status-report.
func reportEdit(args []string) error {
	return runNativeReportCommand(args, "report-edit")
}

// reportCreate creates a report through its owner after reconciliation.
//
// Example: a missing report requires explicit owner-supplied decisions.
func reportCreate(args []string) error {
	return runNativeReportCommand(args, "report-create")
}

// reportReconcile authorizes one owner reconciliation through status-report.
//
// Example: a pending occurrence is reconciled under the session lock.
func reportReconcile(args []string) error {
	return runNativeReportCommand(args, "report-reconcile")
}

// runNativeReportCommand holds the session lock while the report owner mutates.
//
// Example: a failed report edit emits no successful result.
func runNativeReportCommand(
	args []string,
	command string,
) (_err error) {
	if len(args) != 2 || args[1] != "--stdin" {
		return usageError()
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read report request: %w", err)
	}
	sid, err := currentSession()
	if err != nil {
		return err
	}
	if err := verifyCallerSession(command, sid, contextSessionsRoot()); err != nil {
		return err
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return fmt.Errorf("resolve session: %w", err)
	}
	if err := ledgerMarkerCwdBound(sessionDir); err != nil {
		return err
	}
	dir, err := contextSessionDir(filepath.Base(sessionDir))
	if err != nil {
		return err
	}
	if err := proofRootReady(); err != nil {
		return err
	}
	lock, err := acquireSessionLock(dir)
	if err != nil {
		return err
	}
	defer
	// closeReportLock preserves lock-release errors.
	//
	// Example: a rejected report edit still releases its session lock.
	func() { _err = errors.Join(_err, lock.Close()) }()
	if err := validateNativeIfPresent(context.Background(), dir, filepath.Base(dir)); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, "ledger.db")); err != nil {
		return fmt.Errorf("native ledger unavailable: %w", err)
	}
	cmd := exec.Command(filepath.Join(eciBaseDir(), "bin", "status-report"), command+"-locked", dir, "--stdin")
	cmd.Stdin = bytes.NewReader(data)
	spill, err := openPrivateSpill("eci-report-result-")
	if err != nil {
		return err
	}
	defer
	// closeReportResult preserves errors when discarding the private result.
	//
	// Example: an unsuccessful child cannot emit a partial response.
	func() { _err = errors.Join(_err, spill.Close()) }()
	cmd.Stdout, cmd.Stderr = spill, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("report command: %w", err)
	}
	if _, err := spill.Seek(0, io.SeekStart); err != nil {
		return err
	}
	result, err := io.ReadAll(spill)
	if err != nil {
		return err
	}
	if _, err := decodeNativeObservation(result); err != nil {
		return fmt.Errorf("invalid report command result: %w", err)
	}
	_, err = os.Stdout.Write(result)
	return err
}
