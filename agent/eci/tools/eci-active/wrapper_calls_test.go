package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// wrapperSessionID is the session identity every wrapper test runs under.
const wrapperSessionID = "wrapper-test"

// wrapperStub is installed as each external tool wrapper. It records its arguments in $WRAPPER_LOG and exits with $WRAPPER_EXIT.
const wrapperStub = `#!/bin/sh
printf '%s\n' "$*" >> "$WRAPPER_LOG"
exit "${WRAPPER_EXIT:-0}"
`

// goShim is placed first on PATH. Every invocation is recorded in $GO_LOG and fails, so a call that still runs go is visible.
const goShim = `#!/bin/sh
printf '%s\n' "$*" >> "$GO_LOG"
exit 99
`

// wrapperFixture is an isolated installation: eci-active built under bin/, the external tool wrappers beside it, and a go shim that records any go invocation.
type wrapperFixture struct {
	binary  string // built eci-active, at <root>/bin/eci-active
	home    string // HOME for each run; the session context lives below it
	proof   string // ECI_PROOF_ROOT for each run
	shimDir string // directory holding the go shim, first on PATH
	goLog   string // invocations of the go shim
	wrapLog string // invocations of the wrapper stubs
}

// newWrapperFixture builds eci-active into a fresh installation root and installs the wrapper stubs and the go shim.
func newWrapperFixture(t *testing.T) wrapperFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	fx := wrapperFixture{
		binary:  filepath.Join(root, "bin", "eci-active"),
		home:    filepath.Join(root, "home"),
		proof:   filepath.Join(root, "proof"),
		shimDir: filepath.Join(root, "shim"),
		goLog:   filepath.Join(root, "go.log"),
		wrapLog: filepath.Join(root, "wrapper.log"),
	}
	for _, dir := range []string{filepath.Dir(fx.binary), fx.home, fx.shimDir, filepath.Join(fx.proof, wrapperSessionID)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("find go to build eci-active: %v", err)
	}
	if out, err := exec.Command(goBin, "build", "-o", fx.binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build eci-active: %v\n%s", err, out)
	}
	writeExecutable(t, filepath.Join(root, "bin", "status-report"), wrapperStub)
	writeExecutable(t, filepath.Join(root, "bin", "eci-requirement-assignment"), wrapperStub)
	writeExecutable(t, filepath.Join(fx.shimDir, "go"), goShim)
	return fx
}

// writeExecutable writes content to path with owner-execute permission, creating its directory.
func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// contextDir is the session context directory that ledger-append and status-refresh use under HOME.
func (fx wrapperFixture) contextDir() string {
	return filepath.Join(fx.home, ".agent", "context", "sessions", wrapperSessionID)
}

// run executes eci-active with args and returns its stdout, stderr and exit status. wrapperExit is the status every wrapper stub returns.
func (fx wrapperFixture) run(t *testing.T, wrapperExit int, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(fx.binary, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+fx.home,
		"ECI_PROOF_ROOT="+fx.proof,
		"ECI_SESSION_ID="+wrapperSessionID,
		"PATH="+fx.shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GO_LOG="+fx.goLog,
		"WRAPPER_LOG="+fx.wrapLog,
		"WRAPPER_EXIT="+strconv.Itoa(wrapperExit),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run eci-active %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), exitErr.ExitCode()
}

// assertWrapperCalls fails the test unless the wrapper stubs recorded exactly want, in order.
func (fx wrapperFixture) assertWrapperCalls(t *testing.T, want ...string) {
	t.Helper()
	if got := readLines(t, fx.wrapLog); !slices.Equal(got, want) {
		t.Errorf("wrapper calls = %q, want %q", got, want)
	}
}

// assertNoGoCalls fails the test when any run invoked the go shim.
func (fx wrapperFixture) assertNoGoCalls(t *testing.T) {
	t.Helper()
	if calls := readLines(t, fx.goLog); len(calls) > 0 {
		t.Errorf("go was invoked; calls: %q", calls)
	}
}

// readLines returns the non-empty lines of path, or nil when the file was never written.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// TestStatusRefreshRunsStatusReportWrapper runs status-refresh through bin/status-report without invoking go.
func TestStatusRefreshRunsStatusReportWrapper(t *testing.T) {
	fx := newWrapperFixture(t)
	stdout, stderr, code := fx.run(t, 0, "status-refresh", "--event", "decision")
	if code != 0 {
		t.Fatalf("status-refresh exit = %d, want 0; stdout %q stderr %q", code, stdout, stderr)
	}
	fx.assertWrapperCalls(t, "refresh "+fx.contextDir()+" decision")
	fx.assertNoGoCalls(t)
}

// TestStatusRefreshExitsOneWhenStatusReportFails reports a failed wrapper as exit 1 and does not fall back to go.
func TestStatusRefreshExitsOneWhenStatusReportFails(t *testing.T) {
	fx := newWrapperFixture(t)
	_, _, code := fx.run(t, 7, "status-refresh", "--event", "decision")
	if code != 1 {
		t.Fatalf("status-refresh exit = %d, want 1", code)
	}
	fx.assertWrapperCalls(t, "refresh "+fx.contextDir()+" decision")
	fx.assertNoGoCalls(t)
}

// TestLedgerAppendRefreshesThroughStatusReportWrapper appends the entry and refreshes the report through bin/status-report.
func TestLedgerAppendRefreshesThroughStatusReportWrapper(t *testing.T) {
	fx := newWrapperFixture(t)
	stdout, stderr, code := fx.run(t, 0, "ledger-append", "--json", `{"event":"decision","summary":"wrapper refresh"}`)
	if code != 0 {
		t.Fatalf("ledger-append exit = %d, want 0; stdout %q stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "ECI high-level log appended at EOF:") {
		t.Errorf("ledger-append stdout = %q, want the append confirmation", stdout)
	}
	fx.assertWrapperCalls(t, "refresh "+fx.contextDir()+" decision")
	fx.assertNoGoCalls(t)
}

// TestLedgerAppendKeepsAppendWhenStatusReportFails keeps the appended entry and exit 0, and prints the refresh advisory on stderr.
func TestLedgerAppendKeepsAppendWhenStatusReportFails(t *testing.T) {
	fx := newWrapperFixture(t)
	_, stderr, code := fx.run(t, 7, "ledger-append", "--json", `{"event":"decision","summary":"refresh failure"}`)
	if code != 0 {
		t.Fatalf("ledger-append exit = %d, want 0 after the append succeeded; stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "ECI advisory: log append succeeded; status refresh failed.") {
		t.Errorf("ledger-append stderr = %q, want the refresh advisory", stderr)
	}
	log, err := os.ReadFile(filepath.Join(fx.contextDir(), ledgerLogName))
	if err != nil || !strings.Contains(string(log), "refresh failure") {
		t.Errorf("ledger log after failed refresh: err %v, content %q; want the appended entry", err, log)
	}
	fx.assertNoGoCalls(t)
}

// TestRequirementAssignmentRunsWrapperWithoutGo hands the request to bin/eci-requirement-assignment and emits nothing when the wrapper fails.
func TestRequirementAssignmentRunsWrapperWithoutGo(t *testing.T) {
	fx := newWrapperFixture(t)
	request := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(request, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(request)
	if err != nil {
		t.Fatalf("resolve request: %v", err)
	}
	stdout, _, code := fx.run(t, 3, "requirement-assignment", "--request", request)
	if code != 1 {
		t.Fatalf("requirement-assignment exit = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("requirement-assignment stdout = %q, want empty after a wrapper failure", stdout)
	}
	ledgerPath := filepath.Join(fx.contextDir(), requirementLedgerName)
	fx.assertWrapperCalls(t, "--ledger "+ledgerPath+" --request "+resolved)
	fx.assertNoGoCalls(t)
}
