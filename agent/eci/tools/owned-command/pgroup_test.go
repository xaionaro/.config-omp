//go:build darwin || pgroupcheck

// The darwin-pgroup tests run real processes in their own groups. Each test records a PID in a scratch file and
// checks the process table, not only the receipt, so every settlement claim is verified against the kernel.
package main

import (
	"encoding/json"
	"errors"
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
	// pgroupDescendantScript starts one background sleep, records its PID atomically in $1, and waits.
	//
	// Example: the recorded sleep stays alive until the deadline retires the group.
	pgroupDescendantScript = `sleep 30 & echo $! >"$1.tmp" && mv "$1.tmp" "$1"; wait`

	// pgroupLeadershipScript records the leader's PID and its sleep's PID together in $1, then waits.
	//
	// Example: a verdict reads both PIDs from one atomically renamed file.
	pgroupLeadershipScript = `sleep 30 & echo "$$ $!" >"$1.tmp" && mv "$1.tmp" "$1"; wait`

	// pgroupMainEnv selects the pgroupMain path in a re-executed test binary.
	//
	// Example: a child started with this variable set runs owned-command instead of the tests.
	pgroupMainEnv = "OWNED_PGROUP_TEST_MAIN"

	// pgroupRoleEnv selects the escape-test role of a re-executed test binary.
	//
	// Example: the root role starts a session leader that leaves the command's group.
	pgroupRoleEnv = "OWNED_PGROUP_TEST_ROLE"

	// pgroupRecordEnv names the file where a role publishes a PID.
	//
	// Example: the root role publishes the escaped child's PID here.
	pgroupRecordEnv = "OWNED_PGROUP_TEST_RECORD"
)

// TestMain re-executes the test binary as owned-command when pgroupMainEnv is 1, so that a test can deliver a real
// signal to the darwin entry path. Otherwise the tests run normally.
//
// Example: a child started with pgroupMainEnv=1 exits with pgroupMain's status.
func TestMain(m *testing.M) {
	if os.Getenv(pgroupMainEnv) == "1" {
		os.Exit(pgroupMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// pgroupRecordedPID reads the PID that a test script recorded in path.
//
// Example: a missing or malformed record fails the test before any settlement claim is checked.
func pgroupRecordedPID(
	t *testing.T,
	path string,
) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("PID was not recorded: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("recorded PID %q is invalid: %v", data, err)
	}
	return pid
}

// pgroupRequireAbsent fails unless pid has no process-table entry within two seconds, zombies included.
//
// Example: a straggler that is a zombie for a few milliseconds passes once it has been reaped.
func pgroupRequireAbsent(
	t *testing.T,
	pid int,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d survived for 2 s: kill(pid, 0) returned %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pgroupWhenReady runs action once ready reports true, rechecking every 10 ms until deadline.
//
// Example: an interrupt is sent only after the command has recorded the process that it must observe.
func pgroupWhenReady(
	deadline time.Time,
	ready func() bool,
	action func(),
) {
	if ready() {
		action()
		return
	}
	if time.Now().Before(deadline) {
		// recheck runs the readiness check again after 10 ms.
		//
		// Example: a record written late is observed on the next recheck.
		recheck := func() {
			pgroupWhenReady(deadline, ready, action)
		}
		time.AfterFunc(10*time.Millisecond, recheck)
	}
}

// pgroupFileExists returns a readiness check that succeeds once path exists.
//
// Example: pgroupWhenReady(deadline, pgroupFileExists(path), action) waits for a renamed record.
func pgroupFileExists(path string) func() bool {
	// exists reports whether path exists.
	//
	// Example: a plain stat suffices, because every record is renamed into place.
	exists := func() bool {
		_, err := os.Stat(path)
		return err == nil
	}
	return exists
}

// pgroupRootReaped returns a readiness check that succeeds once the root PID recorded at path has been reaped.
//
// Example: a notice sent after this check lands after the command exited and while its group is still settling.
func pgroupRootReaped(path string) func() bool {
	// reaped reports whether the recorded PID has no process-table entry.
	//
	// Example: a zombie that is not yet reaped still answers kill(pid, 0), so the check keeps waiting.
	reaped := func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			return false
		}
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}
	return reaped
}

// pgroupPublishPID writes pid to path atomically, so that a reader never sees a partly written file.
//
// Example: a test role publishes its child's PID with this function before it exits.
func pgroupPublishPID(
	t *testing.T,
	path string,
	pid int,
) {
	t.Helper()
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(strconv.Itoa(pid)), 0600); err != nil {
		t.Fatalf("write record: %v", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatalf("publish record: %v", err)
	}
}

// pgroupLeadershipVerdict checks that the recorded leader and its sleep both belong to a group led by the leader.
//
// Example: an empty verdict means getpgid agrees for both processes.
func pgroupLeadershipVerdict(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "PIDs were not recorded: " + err.Error()
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return "expected leader and descendant PIDs, got " + strconv.Quote(string(data))
	}
	leader, errLeader := strconv.Atoi(fields[0])
	descendant, errDescendant := strconv.Atoi(fields[1])
	if errLeader != nil || errDescendant != nil || leader <= 0 || descendant <= 0 {
		return "invalid PIDs " + strconv.Quote(string(data))
	}
	for _, pid := range []int{leader, descendant} {
		group, err := syscall.Getpgid(pid)
		if err != nil || group != leader {
			return "process " + strconv.Itoa(pid) + " has group " + strconv.Itoa(group) + ", want " + strconv.Itoa(leader)
		}
	}
	return ""
}

// pgroupEventOrder reports whether the named actions occur in events in the given order, with other events allowed
// between them.
//
// Example: TERM, CONT and KILL occur in that order when the grace period expires.
func pgroupEventOrder(
	events []pgroupEvent,
	actions ...string,
) bool {
	next := 0
	for _, event := range events {
		if next < len(actions) && event.Action == actions[next] {
			next++
		}
	}
	return next == len(actions)
}

// TestPgroupExitStatusAndStragglerRetired proves that an ordinary exit reports its status and retires a straggler.
//
// Example: a shell that exits 7 while its background sleep runs returns 7, and the sleep is gone afterwards.
func TestPgroupExitStatusAndStragglerRetired(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "straggler")
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", `sleep 30 & echo $! >"$1"; exit 7`, "sh", recorded},
	}, nil)
	if status != 7 || receipt.Cause != "command exited" || receipt.CommandExit != 7 || !receipt.HostChildrenSettled || len(receipt.Errors) != 0 {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	pgroupRequireAbsent(t, pgroupRecordedPID(t, recorded))
}

// TestPgroupDeadlineKillsTermIgnoringGroup proves that SIGKILL after the grace period settles a group that ignores TERM.
//
// Example: a shell and its sleep ignore TERM, so the deadline's KILL retires them, in TERM, CONT, KILL order.
func TestPgroupDeadlineKillsTermIgnoringGroup(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "straggler")
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 300 * time.Millisecond,
		Grace:    200 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", `trap '' TERM; ` + pgroupDescendantScript, "sh", recorded},
	}, nil)
	if status != pgroupFailure || receipt.Cause != "deadline" || receipt.CommandExit != 137 || !receipt.HostChildrenSettled || len(receipt.Errors) != 0 {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	if receipt.ElapsedSeconds >= 2 {
		t.Fatalf("retirement took %.3f s, want under 2 s", receipt.ElapsedSeconds)
	}
	if !pgroupEventOrder(receipt.Events, "group TERM", "group CONT", "group KILL") {
		t.Fatalf("events are out of order: %+v", receipt.Events)
	}
	pgroupRequireAbsent(t, pgroupRecordedPID(t, recorded))
}

// TestPgroupDeadlineReportsSignalStatus proves that a deadline on a sleeping command records death by TERM as 143.
//
// Example: sleep 60 under a 1 s deadline returns pgroupFailure with CommandExit 143 after about one second.
func TestPgroupDeadlineReportsSignalStatus(t *testing.T) {
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sleep", "60"},
	}, nil)
	if status != pgroupFailure || receipt.Cause != "deadline" || receipt.CommandExit != 143 || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	if receipt.ElapsedSeconds < 1 {
		t.Fatalf("retirement began before the deadline: elapsed %.3f s", receipt.ElapsedSeconds)
	}
}

// TestPgroupInterruptKillsGroup proves that a notice delivered during supervision retires the group and is reported.
//
// Example: SIGINT sent after the straggler is recorded yields "signal: interrupt" and no surviving straggler.
func TestPgroupInterruptKillsGroup(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "straggler")
	notices := make(chan os.Signal, 1)
	// interrupt delivers SIGINT on the notice channel.
	//
	// Example: the interrupt reaches pgroupRun during supervision, after launch.
	interrupt := func() {
		notices <- syscall.SIGINT
	}
	pgroupWhenReady(time.Now().Add(10*time.Second), pgroupFileExists(recorded), interrupt)
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", pgroupDescendantScript, "sh", recorded},
	}, notices)
	if status != pgroupFailure || receipt.Cause != "signal: interrupt" || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	pgroupRequireAbsent(t, pgroupRecordedPID(t, recorded))
}

// TestPgroupNoticeDuringSettlementStill125 proves that an interruption during settlement after a clean exit still
// returns pgroupFailure, as the Linux finalizer does.
//
// Example: the root exits zero, its straggler ignores TERM, and SIGINT arrives while the grace period runs.
func TestPgroupNoticeDuringSettlementStill125(t *testing.T) {
	dir := t.TempDir()
	rootRecord := filepath.Join(dir, "root")
	stragglerRecord := filepath.Join(dir, "straggler")
	notices := make(chan os.Signal, 1)
	// interrupt delivers SIGINT once the root has been reaped and the group is still settling.
	//
	// Example: the notice arrives after the exit and before the one-second KILL.
	interrupt := func() {
		notices <- syscall.SIGINT
	}
	pgroupWhenReady(time.Now().Add(10*time.Second), pgroupRootReaped(rootRecord), interrupt)
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    time.Second,
		Args: []string{"/bin/sh", "-c",
			`trap '' TERM; echo $$ >"$1.tmp" && mv "$1.tmp" "$1"; sleep 30 & echo $! >"$2"; exit 0`,
			"sh", rootRecord, stragglerRecord},
	}, notices)
	if status != pgroupFailure || receipt.Cause != "command exited" || receipt.CommandExit != 0 || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	pgroupRequireAbsent(t, pgroupRecordedPID(t, stragglerRecord))
}

// TestPgroupRealSignalToGuardProcess proves that real SIGINT and SIGTERM sent to the owned-command process reach
// pgroupMain's signal registration and retire the group.
//
// Example: SIGTERM to the re-executed owned-command retires the command and records cause "signal: terminated".
func TestPgroupRealSignalToGuardProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	cases := []struct {
		name   string
		signal syscall.Signal
		cause  string
	}{
		{name: "interrupt", signal: syscall.SIGINT, cause: "signal: interrupt"},
		{name: "terminate", signal: syscall.SIGTERM, cause: "signal: terminated"},
	}
	for _, tc := range cases {
		// runSignal starts owned-command, signals it once the straggler is recorded, and checks the outcome.
		//
		// Example: the subtest passes only when the real signal changed the receipt and the straggler is gone.
		runSignal := func(t *testing.T) {
			dir := t.TempDir()
			recorded := filepath.Join(dir, "straggler")
			receiptPath := filepath.Join(dir, "receipt.json")
			command := exec.Command(executable, "-deadline", "30s", "-grace", "100ms", "-receipt", receiptPath,
				"--", "/bin/sh", "-c", pgroupDescendantScript, "sh", recorded)
			command.Env = append(os.Environ(), pgroupMainEnv+"=1")
			if err := command.Start(); err != nil {
				t.Fatalf("start owned-command: %v", err)
			}
			ready := pgroupFileExists(recorded)
			until := time.Now().Add(10 * time.Second)
			for !ready() {
				if time.Now().After(until) {
					_ = command.Process.Kill()
					t.Fatal("straggler was not recorded before the deadline")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := command.Process.Signal(tc.signal); err != nil {
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
			if receipt.Cause != tc.cause || !receipt.HostChildrenSettled {
				t.Fatalf("receipt=%+v, want cause %q and settled", receipt, tc.cause)
			}
			pgroupRequireAbsent(t, pgroupRecordedPID(t, recorded))
		}
		t.Run(tc.name, runSignal)
	}
}

// TestPgroupLeaderLeadsItsGroup is a positive control: the command leads its own group, and its sleep shares it.
//
// Example: getpgid of the leader and of its sleep both equal the leader's PID while the command runs.
func TestPgroupLeaderLeadsItsGroup(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "pids")
	notices := make(chan os.Signal, 1)
	var verdict string
	ran := false
	// inspect records the leadership verdict and then interrupts the run.
	//
	// Example: the verdict is written before the notice is sent, so the test reads it after pgroupRun returns.
	inspect := func() {
		verdict = pgroupLeadershipVerdict(recorded)
		ran = true
		notices <- syscall.SIGINT
	}
	pgroupWhenReady(time.Now().Add(10*time.Second), pgroupFileExists(recorded), inspect)
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    100 * time.Millisecond,
		Args:     []string{"/bin/sh", "-c", pgroupLeadershipScript, "sh", recorded},
	}, notices)
	if !ran {
		t.Fatal("the leadership check never ran, so the verdict proves nothing")
	}
	if verdict != "" {
		t.Fatalf("command does not lead its own group: %s", verdict)
	}
	if status != pgroupFailure || receipt.Cause != "signal: interrupt" || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
}

// TestPgroupLeavesOutsideProcessesAlone proves that retirement signals the command's group and nothing else.
//
// Example: a sleep started by the test, outside the command's group, is still running after retirement.
func TestPgroupLeavesOutsideProcessesAlone(t *testing.T) {
	bystander := exec.Command("/bin/sleep", "30")
	if err := bystander.Start(); err != nil {
		t.Fatalf("start bystander: %v", err)
	}
	// stopBystander kills and reaps the bystander when the test ends.
	//
	// Example: a test that fails early still leaves no sleep behind.
	stopBystander := func() {
		_ = bystander.Process.Kill()
		_ = bystander.Wait()
	}
	t.Cleanup(stopBystander)
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

// TestPgroupPendingNoticeStartsNothing proves that a notice pending before launch prevents the command.
//
// Example: a SIGTERM already queued leaves no marker file, because the command never started.
func TestPgroupPendingNoticeStartsNothing(t *testing.T) {
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
// Example: a path that does not exist yields cause "guard error", a "start failed" error, and CommandPID zero.
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

// TestPgroupNativeStatus proves that an ordinary settled exit reports the command's native status.
//
// Example: a shell exit of 3 returns 3, and a shell that kills itself with TERM returns 143.
func TestPgroupNativeStatus(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   int
	}{
		{name: "exit code", script: "exit 3", want: 3},
		{name: "signal", script: "kill -TERM $$", want: 143},
	}
	for _, tc := range cases {
		// runStatus runs one shell script and checks its native status.
		//
		// Example: the signal case reports 143, which is 128 plus the signal number.
		runStatus := func(t *testing.T) {
			receipt, status := pgroupRun(pgroupOptions{
				Deadline: 10 * time.Second,
				Grace:    100 * time.Millisecond,
				Args:     []string{"/bin/sh", "-c", tc.script},
			}, nil)
			if status != tc.want || receipt.CommandExit != tc.want || receipt.Cause != "command exited" || !receipt.HostChildrenSettled {
				t.Fatalf("status=%d receipt=%+v", status, receipt)
			}
		}
		t.Run(tc.name, runStatus)
	}
}

// TestPgroupEscapedChildIsOutsideGuarantee proves the documented gap: a child that calls setsid leaves the group,
// so retirement neither signals nor waits for it, and it outlives HostChildrenSettled=true.
//
// Example: the root exits zero and the session leader keeps running after the receipt says the group is settled.
func TestPgroupEscapedChildIsOutsideGuarantee(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	record := filepath.Join(t.TempDir(), "escaped")
	t.Setenv(pgroupRoleEnv, "root")
	t.Setenv(pgroupRecordEnv, record)
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 10 * time.Second,
		Grace:    200 * time.Millisecond,
		Args:     []string{executable, "-test.run=^TestPgroupHelperEscapeRoot$"},
	}, nil)
	pid := pgroupRecordedPID(t, record)
	// stopEscaped kills the escaped session leader when the test ends.
	//
	// Example: the child is outside the group, so it is killed by PID.
	stopEscaped := func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	t.Cleanup(stopEscaped)
	if status != 0 || receipt.Cause != "command exited" || !receipt.HostChildrenSettled {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("escaped child is gone (%v); the documented gap did not hold", err)
	}
}

// TestPgroupHelperEscapeRoot is the root role of TestPgroupEscapedChildIsOutsideGuarantee. Outside that test it skips.
//
// Example: the root starts a session leader with Setsid, publishes its PID, and exits zero.
func TestPgroupHelperEscapeRoot(t *testing.T) {
	if os.Getenv(pgroupRoleEnv) != "root" {
		t.Skip("helper role runs only under TestPgroupEscapedChildIsOutsideGuarantee")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestPgroupHelperEscapeChild$")
	child.Env = append(os.Environ(), pgroupRoleEnv+"=child")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		t.Fatalf("start escaped child: %v", err)
	}
	pgroupPublishPID(t, os.Getenv(pgroupRecordEnv), child.Process.Pid)
}

// TestPgroupHelperEscapeChild is the escaped child of TestPgroupEscapedChildIsOutsideGuarantee. Outside that test it skips.
//
// Example: the child sleeps until the test kills it, long after the group has settled.
func TestPgroupHelperEscapeChild(t *testing.T) {
	if os.Getenv(pgroupRoleEnv) != "child" {
		t.Skip("helper role runs only under TestPgroupEscapedChildIsOutsideGuarantee")
	}
	time.Sleep(30 * time.Second)
}

// TestPgroupMainWritesReceiptWithGuarantee proves that the command line writes a receipt with its scope fields.
//
// Example: the receipt carries the Guarantee text, names the setsid gap, and keeps Survivors and Events present.
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
	if receipt.Backend != pgroupBackend || receipt.Guarantee != pgroupGuarantee || receipt.FamilyCoverage != pgroupFamilyCoverage || receipt.CommandExit != 3 || !receipt.HostChildrenSettled {
		t.Fatalf("receipt=%+v", receipt)
	}
	if !strings.Contains(receipt.Guarantee, "setsid") {
		t.Fatalf("Guarantee must name the setsid gap: %q", receipt.Guarantee)
	}
	if !strings.Contains(receipt.Guarantee, "no subreaper") {
		t.Fatalf("Guarantee must name the missing subreaper: %q", receipt.Guarantee)
	}
	text := string(data)
	if !strings.Contains(text, `"Survivors": []`) || !strings.Contains(text, `"Events": [`) {
		t.Fatalf("Survivors and Events must be present in the receipt:\n%s", data)
	}
}

// TestPgroupMainCancelWritesRefusalReceipt proves that a PID cancellation is refused with a receipt and one diagnostic,
// and that the target is left running.
//
// Example: -cancel-pid of a live sleep with a birth identity returns pgroupFailure, and the sleep still exists.
func TestPgroupMainCancelWritesRefusalReceipt(t *testing.T) {
	target := exec.Command("/bin/sleep", "30")
	if err := target.Start(); err != nil {
		t.Fatalf("start target: %v", err)
	}
	// stopTarget kills and reaps the target when the test ends.
	//
	// Example: a failed assertion still leaves no sleep behind.
	stopTarget := func() {
		_ = target.Process.Kill()
		_ = target.Wait()
	}
	t.Cleanup(stopTarget)
	path := filepath.Join(t.TempDir(), "cancel.json")
	status := pgroupMain([]string{"-cancel-pid", strconv.Itoa(target.Process.Pid), "-cancel-birth", "7", "-receipt", path})
	if status != pgroupFailure {
		t.Fatalf("status=%d, want %d", status, pgroupFailure)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("refusal wrote no receipt: %v", err)
	}
	var receipt pgroupReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatalf("receipt is not JSON: %v", err)
	}
	if receipt.Cause != "guard error" || receipt.CommandPID != 0 || receipt.HostChildrenSettled || len(receipt.Errors) != 1 || receipt.Errors[0] != pgroupCancelError {
		t.Fatalf("receipt=%+v", receipt)
	}
	var waitStatus syscall.WaitStatus
	pid, err := syscall.Wait4(target.Process.Pid, &waitStatus, syscall.WNOHANG, nil)
	if err != nil || pid != 0 {
		t.Fatalf("refused cancellation changed the target: pid=%d err=%v status=%v", pid, err, waitStatus)
	}
}

// TestPgroupMainRejectsInvalidArguments proves that malformed launches fail before any command starts or receipt is written.
//
// Example: a launch without a command, with a zero deadline, or with a PID and a command is refused with status 125.
func TestPgroupMainRejectsInvalidArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	cases := [][]string{
		{"-receipt", path},
		{"-receipt", path, "-deadline", "0s", "--", "/bin/true"},
		{"--", "/bin/true"},
		{"-receipt", path, "-cancel-birth", "7", "--", "/bin/true"},
		{"-receipt", path, "-cancel-pid", "5", "--", "/bin/true"},
	}
	for _, args := range cases {
		if status := pgroupMain(args); status != pgroupFailure {
			t.Fatalf("args %q: status=%d, want %d", args, status, pgroupFailure)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an invalid launch wrote a receipt: %v", err)
	}
}

// TestPgroupFinalCheckSendsKillWhenKillTimerIsLate proves that a final check reached before the KILL timer still sends
// the group KILL, so a TERM-ignoring member cannot survive the final pass.
//
// Example: with a one-microsecond grace, the member is killed and the receipt records a group KILL.
func TestPgroupFinalCheckSendsKillWhenKillTimerIsLate(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "straggler")
	receipt, status := pgroupRun(pgroupOptions{
		Deadline: 300 * time.Millisecond,
		Grace:    time.Microsecond,
		Args:     []string{"/bin/sh", "-c", `trap '' TERM; ` + pgroupDescendantScript, "sh", recorded},
	}, nil)
	if status != pgroupFailure || receipt.Cause != "deadline" {
		t.Fatalf("status=%d receipt=%+v", status, receipt)
	}
	killed := false
	for _, event := range receipt.Events {
		if event.Action == "group KILL" {
			killed = true
		}
	}
	if !killed {
		t.Fatalf("the group KILL was never sent: events=%+v", receipt.Events)
	}
	pgroupRequireAbsent(t, pgroupRecordedPID(t, recorded))
}
