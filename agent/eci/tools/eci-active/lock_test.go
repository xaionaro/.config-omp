package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// holdLock takes an exclusive flock on path in the test process and releases it after d (d<0: at test end).
func holdLock(t *testing.T, path string, d time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold %s: %v", path, err)
	}
	if d < 0 {
		t.Cleanup(func() { f.Close() })
		return
	}
	go func() { time.Sleep(d); f.Close() }()
}

// runAs runs eci-active in session sid with extra env.
func (fx wrapperFixture) runAs(t *testing.T, sid, cwd string, extra []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(fx.binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"HOME="+fx.home,
		"ECI_PROOF_ROOT="+fx.proof,
		"ECI_SESSION_ID="+sid,
		"PATH="+fx.shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GO_LOG="+fx.goLog,
		"WRAPPER_LOG="+fx.wrapLog,
		"WRAPPER_EXIT=0",
	)
	cmd.Env = append(cmd.Env, extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("run %v: %v", args, err)
	}
	return stdout.String(), stderr.String(), ee.ExitCode()
}

func ctxDir(fx wrapperFixture, sid string) string {
	return filepath.Join(fx.home, ".agent", "context", "sessions", sid)
}

func perUserLock(fx wrapperFixture) string { return filepath.Join(fx.proof, ".eci-active.lock") }

func cwdNow(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// Hold BOTH the per-user lock and session A's context lock for 300 ms;
// two concurrent appends in A must both wait and succeed. HEAD fails immediately on the per-user lock.
func TestConcurrentLedgerAppendSameSessionBothSucceed(t *testing.T) {
	fx := newWrapperFixture(t)
	holdLock(t, perUserLock(fx), 300*time.Millisecond)
	holdLock(t, filepath.Join(ctxDir(fx, wrapperSessionID), ".eci-session.lock"), 300*time.Millisecond)
	start := time.Now()
	var wg sync.WaitGroup
	codes := make([]int, 2)
	errs := make([]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e, c := fx.runAs(t, wrapperSessionID, cwdNow(t), []string{"ECI_LOCK_WAIT_SECONDS=5"},
				"ledger-append", "--json", `{"event":"decision","summary":"parallel `+string(rune('a'+i))+`"}`)
			codes[i], errs[i] = c, e
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	for i := range 2 {
		if codes[i] != 0 {
			t.Errorf("append %d exit %d stderr %q", i, codes[i], errs[i])
		}
	}
	if elapsed < 300*time.Millisecond {
		t.Errorf("appends finished in %v, before the 300 ms hold ended; they did not wait", elapsed)
	}
	lines := readLines(t, filepath.Join(ctxDir(fx, wrapperSessionID), ledgerLogName))
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2: %q", len(lines), lines)
	}
}

// A held session-A lock and per-user lock do not delay session B.
func TestLedgerAppendDifferentSessionsDoNotContend(t *testing.T) {
	fx := newWrapperFixture(t)
	holdLock(t, perUserLock(fx), -1)
	holdLock(t, filepath.Join(ctxDir(fx, wrapperSessionID), ".eci-session.lock"), -1)
	if err := os.MkdirAll(filepath.Join(fx.proof, "other-session"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, stderr, code := fx.runAs(t, "other-session", cwdNow(t), []string{"ECI_LOCK_WAIT_SECONDS=1"},
		"ledger-append", "--json", `{"event":"decision","summary":"session B"}`)
	if code != 0 {
		t.Fatalf("session B append exit %d stderr %q", code, stderr)
	}
	if el := time.Since(start); el >= time.Second {
		t.Errorf("session B append took %v; it contended", el)
	}
}

// The bounded wait expires, names the lock and leaves the log unchanged.
func TestSessionLockTimeoutMessage(t *testing.T) {
	fx := newWrapperFixture(t)
	lock := filepath.Join(ctxDir(fx, wrapperSessionID), ".eci-session.lock")
	holdLock(t, lock, -1)
	start := time.Now()
	_, stderr, code := fx.runAs(t, wrapperSessionID, cwdNow(t), []string{"ECI_LOCK_WAIT_SECONDS=1"},
		"ledger-append", "--json", `{"event":"decision","summary":"must not land"}`)
	el := time.Since(start)
	if code != 1 || !strings.Contains(stderr, lock) {
		t.Fatalf("exit %d stderr %q; want 1 naming %s", code, stderr, lock)
	}
	if el < time.Second {
		t.Errorf("failed after %v, before the 1 s wait", el)
	}
	if lines := readLines(t, filepath.Join(ctxDir(fx, wrapperSessionID), ledgerLogName)); len(lines) != 0 {
		t.Errorf("log changed: %q", lines)
	}
}

// ledger-append keeps the proof-root requirement it had under the per-user lock.
func TestLedgerAppendRequiresProofRoot(t *testing.T) {
	fx := newWrapperFixture(t)
	if err := os.RemoveAll(fx.proof); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := fx.runAs(t, wrapperSessionID, cwdNow(t), nil,
		"ledger-append", "--json", `{"event":"decision","summary":"no proof root"}`)
	if code != 1 {
		t.Fatalf("exit %d stderr %q; want 1", code, stderr)
	}
	for _, name := range []string{ledgerLogName, ".eci-session.lock"} {
		if exists(filepath.Join(ctxDir(fx, wrapperSessionID), name)) {
			t.Errorf("%s created without a proof root", name)
		}
	}
}

// A command that stays on the per-user lock (permissive-on global) waits for a
// held per-user lock, then succeeds.
func TestPerUserLockWaitsThenSucceedsGlobalPermissive(t *testing.T) {
	fx := newWrapperFixture(t)
	if err := os.MkdirAll(filepath.Join(fx.home, ".omp"), 0o700); err != nil {
		t.Fatal(err)
	}
	holdLock(t, perUserLock(fx), 300*time.Millisecond)
	start := time.Now()
	_, stderr, code := fx.runAs(t, wrapperSessionID, cwdNow(t), []string{"ECI_LOCK_WAIT_SECONDS=5"},
		"permissive-on", "global", "gate-a", "op-a", "request", "60")
	if code != 0 {
		t.Fatalf("permissive-on exit %d stderr %q", code, stderr)
	}
	if el := time.Since(start); el < 300*time.Millisecond {
		t.Errorf("finished in %v, before the hold ended", el)
	}
}

// The per-user wait expires, names the lock and writes no record.
func TestPerUserLockTimeoutMessage(t *testing.T) {
	fx := newWrapperFixture(t)
	if err := os.MkdirAll(filepath.Join(fx.home, ".omp"), 0o700); err != nil {
		t.Fatal(err)
	}
	holdLock(t, perUserLock(fx), -1)
	start := time.Now()
	_, stderr, code := fx.runAs(t, wrapperSessionID, cwdNow(t), []string{"ECI_LOCK_WAIT_SECONDS=1"},
		"permissive-on", "global", "gate-a", "op-a", "request", "60")
	if code != 1 || !strings.Contains(stderr, perUserLock(fx)) {
		t.Fatalf("exit %d stderr %q; want 1 naming %s", code, stderr, perUserLock(fx))
	}
	if el := time.Since(start); el < time.Second {
		t.Errorf("failed after %v, before the 1 s wait", el)
	}
	if exists(filepath.Join(fx.home, ".omp", globalPermissiveName)) {
		t.Error("record written despite timeout")
	}
}
