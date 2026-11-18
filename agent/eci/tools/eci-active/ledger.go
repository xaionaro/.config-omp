package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var refreshEvents = map[string]bool{
	"worker_progress": true, "assignment_change": true, "review_result": true,
	"decision": true, "verification_result": true, "requirement_change": true,
	"forecast_change": true, "stuck_change": true, "checkpoint": true,
	"lifecycle_change": true, "correction": true,
}

var nonSpacePattern = regexp.MustCompile(`\S`)

func nonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && nonSpacePattern.MatchString(s)
}

func identityValue(v any) bool {
	m, ok := v.(map[string]any)
	if !ok || !nonEmptyString(m["id"]) || !nonEmptyString(m["role"]) {
		return false
	}
	if _, has := m["turn"]; has {
		return nonEmptyString(m["turn"])
	}
	return true
}

func referencesValue(v any) bool {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return false
	}
	for _, item := range arr {
		if !nonEmptyString(item) {
			return false
		}
	}
	return true
}

// validateLedgerEntry accepts exactly one JSON object that satisfies the
// high-level log schema. It returns the decoded object for formatting.
func validateLedgerEntry(raw string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one JSON object")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected exactly one JSON object")
	}
	event, _ := obj["event"].(string)
	_, hasActor := obj["actor"]
	_, hasRecorder := obj["recorded_by"]
	_, hasNext := obj["next_action"]
	actorOK := !hasActor || identityValue(obj["actor"])
	recorderOK := !hasRecorder || identityValue(obj["recorded_by"])
	nextOK := !hasNext || nonEmptyString(obj["next_action"])
	base := refreshEvents[event] && nonEmptyString(obj["summary"]) && actorOK && recorderOK && nextOK
	if !base {
		return nil, fmt.Errorf("invalid event, summary, identity, or next_action")
	}
	if event == "worker_progress" {
		change, _ := obj["change"].(map[string]any)
		ok := identityValue(obj["actor"]) && referencesValue(obj["requirements"]) &&
			referencesValue(obj["evidence"]) && change != nil &&
			nonEmptyString(change["before"]) && nonEmptyString(change["after"]) &&
			nonEmptyString(obj["reason"])
		if !ok {
			return nil, fmt.Errorf("worker_progress requires actor, requirements, change, reason, and evidence")
		}
	}
	return obj, nil
}

// ledgerMarkerCwdBound rejects an append when the session's marker declares a
// cwd other than the current one. A session without a marker is not bound.
func ledgerMarkerCwdBound(sessionDir string) error {
	mismatch := exitError{code: 1, msg: "ECI high-level log append rejected a marker bound to another session or cwd."}
	path := filepath.Join(sessionDir, "eci_active")
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !fi.Mode().IsRegular() {
		return mismatch
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return mismatch
	}
	cwd, err := currentCwd()
	if err != nil {
		return err
	}
	declared := ""
	for _, line := range strings.Split(string(data), "\n") {
		if d, ok := strings.CutPrefix(line, "cwd: "); ok {
			declared = d
			break
		}
	}
	if declared != "" && canonicalCwd(declared) == cwd {
		return nil
	}
	mismatch.msg += fmt.Sprintf(" Marker cwd %q does not match the current cwd %q.", declared, cwd)
	return mismatch
}

func ledgerAppend(args []string) error {
	if len(args) < 2 || args[1] != "--json" {
		return usageError()
	}
	if len(args) != 3 {
		return usageError()
	}
	return appendLedgerEntry(args[2], true)
}

// appendLedgerEntry validates and appends one structured event. announce
// controls the success line so internal callers keep stdout clean.
func appendLedgerEntry(raw string, announce bool) error {
	obj, err := validateLedgerEntry(raw)
	if err != nil {
		// TODO: include err in the message and have validateLedgerEntry name the failing
		// field (for example an event outside refreshEvents); the bare rejection hides why
		// the append failed.
		return exitError{code: 2, msg: "ECI high-level log append rejected invalid structured entry before log mutation."}
	}
	sid, err := currentSession()
	if err != nil {
		return err
	}
	if err := verifyCallerSession("ledger-append", sid, contextSessionsRoot()); err != nil {
		return err
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return exitError{code: 1, msg: "eci-active: no current session directory found under the proof root."}
	}
	if err := ledgerMarkerCwdBound(sessionDir); err != nil {
		return err
	}
	ledgerDir, err := contextSessionDir(filepath.Base(sessionDir))
	if err != nil {
		return err
	}
	if err := proofRootReady(); err != nil {
		return err
	}
	lock, err := acquireSessionLock(ledgerDir)
	if err != nil {
		return err
	}
	defer lock.Close()

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	obj["schema"] = "eci-high-level-log/v1"
	obj["timestamp"] = timestamp
	obj["session_id"] = filepath.Base(ledgerDir)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return err
	}
	formatted := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))

	seq, err := appendLedgerLog(context.Background(), ledgerDir, formatted)
	if err != nil {
		return exitError{code: 1, msg: fmt.Sprintf("ECI high-level log append failed; no entry was recorded: %v", err)}
	}
	event, _ := obj["event"].(string)
	if err := runStatusRefresh(ledgerDir, event); err != nil {
		fmt.Fprintf(os.Stderr, "ECI advisory: log append succeeded; status refresh failed. Reconcile the report, then run eci-active status-refresh --event %s without appending the event again.\n", event)
	}
	if announce {
		fmt.Printf("ECI high-level log appended: %s seq %d\n", filepath.Join(ledgerDir, ledgerDBName), seq)
	}
	return nil
}

// runStatusRefresh runs the status-report refresh for a ledger dir through bin/status-report, which builds
// tools/status-report on demand and execs the cached binary. No go command runs per call.
func runStatusRefresh(ledgerDir, event string) error {
	cmd := exec.Command(filepath.Join(eciBaseDir(), "bin", "status-report"), "refresh", ledgerDir, event)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func statusRefresh(args []string) error {
	if len(args) != 3 || args[1] != "--event" {
		return usageError()
	}
	if !refreshEvents[args[2]] {
		return usageError()
	}
	sid, err := currentSession()
	if err != nil {
		return err
	}
	if err := verifyCallerSession("status-refresh", sid, contextSessionsRoot()); err != nil {
		return err
	}
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return exitError{code: 1}
	}
	ledgerDir, err := contextSessionDir(filepath.Base(sessionDir))
	if err != nil {
		return exitError{code: 1}
	}
	if err := proofRootReady(); err != nil {
		return err
	}
	lock, err := acquireSessionLock(ledgerDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := runStatusRefresh(ledgerDir, args[2]); err != nil {
		return exitError{code: 1}
	}
	return nil
}
