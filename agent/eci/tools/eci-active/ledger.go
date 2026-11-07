package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	ledgerLogName    = "high_level_log.jsonl"
	ledgerAnchorName = "high_level_log.anchor"
)

var refreshEvents = map[string]bool{
	"worker_progress": true, "assignment_change": true, "review_result": true,
	"decision": true, "verification_result": true, "requirement_change": true,
	"forecast_change": true, "blocker_change": true, "checkpoint": true,
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

func ledgerLinkCount(path string) (uint64, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink < 1 {
		return 0, fmt.Errorf("link count unavailable: %s", path)
	}
	return uint64(st.Nlink), nil
}

// ledgerPathSafe requires a real session directory and a regular, non-symlink
// ledger whose canonical path equals its lexical path.
func ledgerPathSafe(sessionDir string) bool {
	if !isDir(sessionDir) || isSymlink(sessionDir) {
		return false
	}
	path := filepath.Join(sessionDir, ledgerLogName)
	if !isRegularNoFollow(path) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}

func ledgerPathMutable(sessionDir string) bool {
	if !ledgerPathSafe(sessionDir) {
		return false
	}
	links, err := ledgerLinkCount(filepath.Join(sessionDir, ledgerLogName))
	return err == nil && links == 1
}

// ledgerDetachHardlinked replaces a linked ledger with a private copy so that
// appends never mutate other names of the same inode.
func ledgerDetachHardlinked(sessionDir string) error {
	if !ledgerPathSafe(sessionDir) {
		return fmt.Errorf("unsafe ledger")
	}
	log := filepath.Join(sessionDir, ledgerLogName)
	links, err := ledgerLinkCount(log)
	if err != nil {
		return err
	}
	if links == 1 {
		return nil
	}
	tmp := fmt.Sprintf("%s.tmp.%d", log, os.Getpid())
	if exists(tmp) {
		return fmt.Errorf("temporary ledger exists")
	}
	data, err := os.ReadFile(log)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if !ledgerPathSafe(sessionDir) {
		os.Remove(tmp)
		return fmt.Errorf("unsafe ledger")
	}
	if links, err := ledgerLinkCount(log); err != nil || links <= 1 {
		os.Remove(tmp)
		return fmt.Errorf("ledger is no longer linked")
	}
	if err := os.Rename(tmp, log); err != nil {
		os.Remove(tmp)
		return err
	}
	if !ledgerPathMutable(sessionDir) {
		return fmt.Errorf("ledger not mutable after detach")
	}
	return nil
}

func ledgerSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// writeAnchor publishes the anchor for the current ledger. With replace=false
// the anchor must not exist; with replace=true it must be a regular file.
func writeAnchor(sessionDir string, replace bool) error {
	if !ledgerPathSafe(sessionDir) {
		return fmt.Errorf("unsafe ledger")
	}
	log := filepath.Join(sessionDir, ledgerLogName)
	anchor := filepath.Join(sessionDir, ledgerAnchorName)
	if replace {
		if !isRegularNoFollow(anchor) {
			return fmt.Errorf("anchor is not a regular file")
		}
	} else if exists(anchor) {
		return fmt.Errorf("anchor exists")
	}
	tmp := fmt.Sprintf("%s.tmp.%d", anchor, os.Getpid())
	if exists(tmp) {
		return fmt.Errorf("temporary anchor exists")
	}
	fi, err := os.Stat(log)
	if err != nil {
		return err
	}
	hash, err := ledgerSHA256(log)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("schema: eci-high-level-log-anchor/v1\nsession_id: %s\nlog_path: %s\nbytes: %d\nsha256: %s\n",
		filepath.Base(sessionDir), log, fi.Size(), hash)
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, anchor); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ledgerEnsureAnchor bootstraps the ledger and anchor. Anchor problems are
// advisory; the ledger stays usable for normal lifecycle work.
func ledgerEnsureAnchor(sessionDir string) error {
	if !isDir(sessionDir) || isSymlink(sessionDir) {
		return fmt.Errorf("unsafe session directory")
	}
	log := filepath.Join(sessionDir, ledgerLogName)
	created := false
	if !exists(log) {
		f, err := os.OpenFile(log, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if exists(log) {
				if !ledgerPathSafe(sessionDir) {
					return fmt.Errorf("unsafe ledger")
				}
			} else {
				fmt.Fprintln(os.Stderr, "ECI advisory: high-level ledger bootstrap was unavailable; normal lifecycle work continues.")
				return nil
			}
		} else {
			f.Close()
			created = true
		}
	}
	if !ledgerPathSafe(sessionDir) {
		if created {
			_ = os.Remove(log)
		}
		return fmt.Errorf("unsafe ledger")
	}
	anchor := filepath.Join(sessionDir, ledgerAnchorName)
	if isSymlink(anchor) {
		fmt.Fprintln(os.Stderr, "ECI advisory: high-level ledger anchor is a symlink; leaving it untouched while normal lifecycle work continues.")
		return nil
	}
	if exists(anchor) {
		if !isRegularNoFollow(anchor) {
			fmt.Fprintln(os.Stderr, "ECI advisory: high-level ledger anchor is not a regular file; leaving it untouched while normal lifecycle work continues.")
			return nil
		}
		if err := writeAnchor(sessionDir, true); err != nil {
			fmt.Fprintln(os.Stderr, "ECI advisory: high-level ledger anchor could not be refreshed; normal lifecycle work continues.")
		}
		return nil
	}
	if err := writeAnchor(sessionDir, false); err != nil {
		fmt.Fprintln(os.Stderr, "ECI advisory: high-level ledger anchor could not be initialized; normal lifecycle work continues.")
	}
	return nil
}

func appendToFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
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
		return exitError{code: 2, msg: "ECI high-level log append rejected invalid structured entry before log mutation."}
	}
	sid, err := currentSession()
	if err != nil {
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
	lock, err := acquireLock()
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

	if err := ledgerEnsureAnchor(ledgerDir); err != nil {
		return exitError{code: 1, msg: "ECI high-level log append found an unsafe current ledger target."}
	}
	if err := ledgerDetachHardlinked(ledgerDir); err != nil {
		return exitError{code: 1, msg: "ECI high-level log append could not detach an externally linked current ledger target."}
	}
	logPath := filepath.Join(ledgerDir, ledgerLogName)
	if !ledgerPathMutable(ledgerDir) {
		return exitError{code: 1, msg: "ECI high-level log append could not access an exclusive current ledger target."}
	}
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 0 {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return err
		}
		if data[len(data)-1] != '\n' {
			if !ledgerPathMutable(ledgerDir) {
				return exitError{code: 1, msg: "ECI high-level log changed to an externally linked target during normalization."}
			}
			if err := appendToFile(logPath, []byte("\n")); err != nil {
				return exitError{code: 1, msg: "ECI high-level log append could not normalize its current regular ledger target."}
			}
		}
	}
	if !ledgerPathMutable(ledgerDir) {
		return exitError{code: 1, msg: "ECI high-level log changed to an unsafe or externally linked file during normalization."}
	}
	if err := appendToFile(logPath, append(append([]byte{}, formatted...), '\n')); err != nil {
		return err
	}
	if !ledgerPathMutable(ledgerDir) {
		return exitError{code: 1, msg: "ECI high-level log changed to an unsafe or externally linked file during append."}
	}
	if err := writeAnchor(ledgerDir, true); err != nil {
		fmt.Fprintln(os.Stderr, "ECI advisory: high-level ledger anchor could not be refreshed after append.")
	}
	event, _ := obj["event"].(string)
	if err := runStatusRefresh(ledgerDir, event); err != nil {
		fmt.Fprintf(os.Stderr, "ECI advisory: log append succeeded; status refresh failed. Reconcile the report, then run eci-active status-refresh --event %s without appending the event again.\n", event)
	}
	if announce {
		fmt.Printf("ECI high-level log appended at EOF: %s\n", logPath)
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
	sessionDir, err := resolveSessionDir(sid)
	if err != nil {
		return exitError{code: 1}
	}
	ledgerDir, err := contextSessionDir(filepath.Base(sessionDir))
	if err != nil {
		return exitError{code: 1}
	}
	lock, err := acquireLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := runStatusRefresh(ledgerDir, args[2]); err != nil {
		return exitError{code: 1}
	}
	return nil
}
