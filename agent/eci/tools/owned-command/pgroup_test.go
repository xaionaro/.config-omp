//go:build darwin || pgroupcheck

// The darwin-pgroup tests run real processes in their own groups. Each test records a descendant PID in a
// scratch file, so settlement claims are checked against the process table and not only the receipt.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	// pgroupDescendantScript starts one background sleep in the command's group, records its PID in $1, and waits.
	// The record is renamed into place so a poller never reads a partly written file.
	//
	// Example: the recorded sleep stays alive until the deadline retires the group.
	pgroupDescendantScript = `sleep 30 & echo $! > "$1.tmp" && mv "$1.tmp" "$1"; wait`

	// pgroupLeadershipScript records the leader's PID and its sleep's PID together, then waits.
	//
	// Example: the two PIDs are read from one atomically renamed file.
	pgroupLeadershipScript = `sleep 30 & echo "$$ $!" > "$1.tmp" && mv "$1.tmp" "$1"; wait`

	// pgroupMainEnv selects the re-executed test binary's darwin entry path in TestMain.
	//
	// Example: a child started with this variable set runs pgroupMain instead of the tests.
	pgroupMainEnv = "OWNED_PGROUP_TEST_MAIN"
)

// TestMain re-executes the test binary as owned-command when pgroupMainEnv is set, so a test can deliver a real
// signal to the darwin entry path. Otherwise the tests run normally.
//
// Example: a child started with pgroupMainEnv=1 exits with pgroupMain's status.
func TestMain(m *testing.M) {
	if os.Getenv(pgroupMainEnv) == "1" {
		os.Exit(pgroupMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// pgroupRecordedPID reads the descendant PID that a test script wrote to path.
//
// Example: a missing or malformed record fails the test before any settlement claim is checked.
func pgroupRecordedPID(
	t *testing.T,
	path string,
) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("descendant PID was not recorded: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("recorded descendant PID %q is invalid: %v", data, err)
	}
	return pid
}

// pgroupRequireGone fails unless pid has no process-table entry, zombies included.
//
// Example: a descendant that stayed as an unreaped zombie still fails this check.
func pgroupRequireGone(
	t *testing.T,
	pid int,
) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process %d survived settlement: kill(pid, 0) returned %v", pid, err)
	}
}

// pgroupWhenRecorded runs action once path exists, checking every 10 ms until deadline.
//
// Example: an interrupt or a group probe runs only after the command has recorded its descendant.
func pgroupWhenRecorded(
	path string,
	deadline time.Time,
	action func(),
) {
	if _, err := os.Stat(path); err == nil {
		action()
		return
	}
	if time.Now().Before(deadline) {
		time.AfterFunc(10*time.Millisecond, func() {
			pgroupWhenRecorded(path, deadline, action)
		})
	}
}

// pgroupLeadershipVerdict checks that the recorded leader and its sleep both belong to a group led by the leader.
//
// Example: an empty verdict means getpgid agrees for both processes.
func pgroupLeadershipVerdict(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "pids were not recorded: " + err.Error()
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return fmt.Sprintf("expected leader and descendant PIDs, got %q", data)
	}
	leader, errLeader := strconv.Atoi(fields[0])
	descendant, errDescendant := strconv.Atoi(fields[1])
	if errLeader != nil || errDescendant != nil || leader <= 0 || descendant <= 0 {
		return fmt.Sprintf("invalid PIDs %q", data)
	}
	for _, pid := range []int{leader, descendant} {
		group, err := syscall.Getpgid(pid)
		if err != nil || group != leader {
			return fmt.Sprintf("process %d has group %d (err %v), want leader group %d", pid, group, err, leader)
		}
	}
	return ""
}

// TestPgroupDeadlineRetiresGroup proves that the deadline kills a running descendant and settles the group.
//
// Example: a 1 s deadline returns after about 1 s, settled, with the background sleep gone.
func TestPgroupDeadlineRetiresGroup(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "descendant")
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", pgroupDescendantScript, "sh", recorded},
	}, nil)
	if status != pgroupFailure || receipt.Cause != "deadline" || !receipt.HostChildrenSettled || len(receipt.Errors) != 0 {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	pgroupRequireGone(t, pgroupRecordedPID(t, recorded))
	if receipt.ElapsedSeconds < 1 || receipt.ElapsedSeconds > 10 {
		t.Fatalf("elapsed %.3fs is outside the deadline window", receipt.ElapsedSeconds)
	}
}

// TestPgroupNormalExitRetiresLeftoverDescendants proves that a root which exits on its own still retires its group.
//
// Example: the shell exits zero while its background sleep is still a group member, and the sleep is then dead.
func TestPgroupNormalExitRetiresLeftoverDescendants(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "descendant")
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", `sleep 30 & echo $! > "$1"`, "sh", recorded},
	}, nil)
	if status != 0 || receipt.Cause != "command exited" || receipt.CommandExit != 0 || !receipt.HostChildrenSettled || len(receipt.Errors) != 0 {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	pgroupRequireGone(t, pgroupRecordedPID(t, recorded))
}

// TestPgroupPropagatesNativeStatus proves that an ordinary settled exit reports the command's native status.
//
// Example: a shell exit of 3 returns 3, and death by SIGTERM returns 143.
func TestPgroupPropagatesNativeStatus(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   int
	}{
		{name: "exit code", script: "exit 3", want: 3},
		{name: "signal", script: "kill -TERM $$", want: 143},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			receipt, status := pgroupRun(pgroupOptions{
				Deadline: 10 * time.Second,
				Grace:    100 * time.Millisecond,
				Args:     []string{"/bin/sh", "-c", tc.script},
			}, nil)
			if status != tc.want || receipt.CommandExit != tc.want || receipt.Cause != "command exited" || !receipt.HostChildrenSettled {
				t.Fatalf("status=%d receipt=%+v", status, receipt)
			}
		})
	}
}

// TestPgroupInterruptRetiresGroup proves that an interrupt delivered on the notice channel retires the group.
//
// Example: SIGINT delivered after the descendant is recorded yields cause "signal: interrupt" and no survivor.
func TestPgroupInterruptRetiresGroup(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "descendant")
	notices := make(chan os.Signal, 1)
	pgroupWhenRecorded(recorded, time.Now().Add(10*time.Second), func() {
		notices <- syscall.SIGINT
	})
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", pgroupDescendantScript, "sh", recorded},
	}, notices)
	if status != pgroupFailure || receipt.Cause != "signal: interrupt" || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	pgroupRequireGone(t, pgroupRecordedPID(t, recorded))
}

// TestPgroupRealSignalRetiresGroup proves that a real SIGINT to the owned-command process, through pgroupMain's
// signal registration, retires the group and exits with pgroupFailure.
//
// Example: SIGINT to the re-executed owned-command leaves no descendant and writes cause "signal: interrupt".
func TestPgroupRealSignalRetiresGroup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	dir := t.TempDir()
	recorded := filepath.Join(dir, "descendant")
	receiptPath := filepath.Join(dir, "receipt.json")
	command := exec.Command(executable, "-deadline", "30s", "-grace", "100ms", "-receipt", receiptPath,
		"--", "/bin/sh", "-c", pgroupDescendantScript, "sh", recorded)
	command.Env = append(os.Environ(), pgroupMainEnv+"=1")
	if err := command.Start(); err != nil {
		t.Fatalf("start owned-command: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(recorded); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			t.Fatal("descendant was not recorded before the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal owned-command: %v", err)
	}
	waitErr := command.Wait()
	var exit *exec.ExitError
	if !errors.As(waitErr, &exit) || exit.ExitCode() != pgroupFailure {
		t.Fatalf("owned-command exit = %v, want status %d", waitErr, pgroupFailure)
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("receipt not written: %v", err)
	}
	var receipt pgroupReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v", err)
	}
	if receipt.Cause != "signal: interrupt" || !receipt.HostChildrenSettled {
		t.Fatalf("receipt=%+v", receipt)
	}
	pgroupRequireGone(t, pgroupRecordedPID(t, recorded))
}

// TestPgroupCommandLeadsItsOwnGroup is a positive control: the command and its sleep share a group led by the command.
//
// Example: while the command runs, getpgid of its PID and of its sleep both equal the command's PID.
func TestPgroupCommandLeadsItsOwnGroup(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "pids")
	notices := make(chan os.Signal, 1)
	var verdict string
	pgroupWhenRecorded(recorded, time.Now().Add(10*time.Second), func() {
		verdict = pgroupLeadershipVerdict(recorded)
		notices <- syscall.SIGINT
	})
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", pgroupLeadershipScript, "sh", recorded},
	}, notices)
	if verdict != "" {
		t.Fatalf("command did not lead its own group: %s", verdict)
	}
	if status != pgroupFailure || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
}

// TestPgroupKillsMembersThatIgnoreTERM proves that SIGKILL after the grace period settles a group that ignores SIGTERM.
//
// Example: a shell and its sleep ignore TERM, and the deadline's KILL still leaves no member.
func TestPgroupKillsMembersThatIgnoreTERM(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "descendant")
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 2 * time.Second,
		Grace:    200 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", `trap '' TERM; ` + pgroupDescendantScript, "sh", recorded},
	}, nil)
	if status != pgroupFailure || receipt.Cause != "deadline" || !receipt.HostChildrenSettled || len(receipt.Errors) != 0 {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	if receipt.ElapsedSeconds < 2 {
		t.Fatalf("settled before the deadline: elapsed %.3fs", receipt.ElapsedSeconds)
	}
	pgroupRequireGone(t, pgroupRecordedPID(t, recorded))
}

// TestPgroupLeavesProcessesOutsideTheGroupAlone proves that retirement signals only the command's group.
//
// Example: a sleep started by the test, outside the command's group, is still running after retirement.
func TestPgroupLeavesProcessesOutsideTheGroupAlone(t *testing.T) {
	bystander := exec.Command("/bin/sleep", "30")
	if err := bystander.Start(); err != nil {
		t.Fatalf("start bystander: %v", err)
	}
	t.Cleanup(func() {
		_ = bystander.Process.Kill()
		_ = bystander.Wait()
	})
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 300 * time.Millisecond,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sleep", "30"},
	}, nil)
	if status != pgroupFailure || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	var waitStatus syscall.WaitStatus
	pid, err := syscall.Wait4(bystander.Process.Pid, &waitStatus, syscall.WNOHANG, nil)
	if err != nil || pid != 0 {
		t.Fatalf("bystander outside the group exited: pid=%d err=%v status=%v", pid, err, waitStatus)
	}
}

// TestPgroupNoticeBeforeLaunchStartsNothing proves that a notice pending before launch prevents the command.
//
// Example: a SIGTERM already queued leaves no marker file, because the command never started.
func TestPgroupNoticeBeforeLaunchStartsNothing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "launched")
	notices := make(chan os.Signal, 1)
	notices <- syscall.SIGTERM
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", `touch "$1"`, "sh", marker},
	}, notices)
	if status != pgroupFailure || receipt.Cause != "signal: terminated" || receipt.CommandPID != 0 || !receipt.CommandStarted.IsZero() {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command ran after a pending notice: stat error %v", err)
	}
}

// TestPgroupStartFailureIsGuardError proves that a missing executable is a guard error before any group exists.
//
// Example: a path that does not exist yields "start failed" and CommandPID zero.
func TestPgroupStartFailureIsGuardError(t *testing.T) {
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{filepath.Join(t.TempDir(), "missing-command")},
	}, nil)
	if status != pgroupFailure || receipt.Cause != "guard error" || receipt.CommandPID != 0 || len(receipt.Errors) != 1 || !strings.HasPrefix(receipt.Errors[0], "start failed: ") {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
}

// TestPgroupMainWritesReceiptWithGuarantee proves that the command line writes a receipt with its scope fields.
//
// Example: the receipt names darwin-pgroup, states the subreaper gap, and serializes Errors as an empty array.
func TestPgroupMainWritesReceiptWithGuarantee(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	status := pgroupMain([]string{"-deadline", "10s", "-receipt", path, "--", "/bin/sh", "-c", "exit 3"})
	if status != 3 {
		t.Fatalf("status=%d, want the command's native status 3", status)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("receipt not written: %v", err)
	}
	var receipt pgroupReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v", err)
	}
	if receipt.Backend != pgroupBackend || receipt.Guarantee != pgroupGuarantee || !strings.Contains(receipt.FamilyCoverage, "no subreaper") || receipt.CommandExit != 3 || !receipt.HostChildrenSettled {
		t.Fatalf("receipt=%+v", receipt)
	}
	if !strings.Contains(receipt.Guarantee, "setsid") {
		t.Fatalf("Guarantee must state the setsid escape gap: %q", receipt.Guarantee)
	}
	if !strings.Contains(string(data), `"Errors": []`) {
		t.Fatalf("Errors must serialize as an empty array:\n%s", data)
	}
}

// TestPgroupMainRefusesPIDCancellation proves that cancellation by PID is refused before any receipt or signal.
//
// Example: a recorded PID and birth are refused with status 125 and no receipt file.
func TestPgroupMainRefusesPIDCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	status := pgroupMain([]string{"-cancel-pid", "123", "-cancel-birth", "7", "-receipt", path})
	if status != pgroupFailure {
		t.Fatalf("status=%d, want %d", status, pgroupFailure)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused cancellation wrote a receipt: %v", err)
	}
}

// TestPgroupMainRejectsInvalidArguments proves that malformed launches fail before any command starts.
//
// Example: a launch without a command, with a zero deadline, or without -receipt is refused with status 125.
func TestPgroupMainRejectsInvalidArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	cases := [][]string{
		{"-receipt", path},
		{"-receipt", path, "-deadline", "0s", "--", "/bin/true"},
		{"--", "/bin/true"},
	}
	for _, args := range cases {
		if status := pgroupMain(args); status != pgroupFailure {
			t.Fatalf("args %q: status=%d, want %d", args, status, pgroupFailure)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid launch wrote a receipt: %v", err)
	}
}
