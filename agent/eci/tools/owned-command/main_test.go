//go:build linux

package main

import (
	"bytes"
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

// runGuard exercises the native executable and decodes its receipt.
//
// Example: runGuard(t, "--", "/bin/true") observes a settled command.
func runGuard(
	t *testing.T,
	args ...string,
) (Receipt, int, string) {
	t.Helper()
	binary := os.Getenv("GUARD_TEST_BINARY")
	if binary == "" {
		t.Fatal("GUARD_TEST_BINARY must name the freshly built guard")
	}
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	cmd := exec.Command(binary, append([]string{"-receipt", receiptPath}, args...)...)
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("receipt: %v output=%s", err, output)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt, code, string(output)
}

// TestExitAndOutput checks native statuses and exact inherited output.
//
// Example: a child exiting seven retains status seven.
func TestExitAndOutput(t *testing.T) {
	for _, exit := range []int{0, 7} {
		r, code, output := runGuard(t, "--", "/bin/sh", "-c", "printf stdout; printf stderr >&2; exit "+strconv.Itoa(exit))
		if code != exit || r.CommandExit != exit || !r.HostChildrenSettled || len(r.Errors) != 0 || output != "stdoutstderr" {
			t.Fatalf("exit=%d code=%d output=%q receipt=%+v", exit, code, output, r)
		}
	}
}

// TestDeadlineAndEscapedChild checks retirement of a detached resistant child.
//
// Example: a setsid child ignoring TERM still receives KILL.
func TestDeadlineAndEscapedChild(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	r, code, _ := runGuard(t, "-deadline", "200ms", "-grace", "200ms", "--", "/bin/sh", "-c", `setsid /bin/sh -c 'trap "" TERM; echo $$ > "$1"; while :; do sleep 1; done' guard "$1" & wait`, "guard", pidPath)
	if code != guardFailure || r.Cause != "deadline" || !r.HostChildrenSettled || len(r.Survivors) != 0 {
		t.Fatalf("code=%d receipt=%+v", code, r)
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(stringTrim(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); !os.IsNotExist(err) {
		t.Fatalf("escaped child %d still present: %v", pid, err)
	}
	foundKill := false
	for _, event := range r.Events {
		if (event.PID == pid && event.Action == "killed") || event.Action == "cgroup killed" {
			foundKill = true
		}
	}
	if !foundKill {
		t.Fatalf("TERM-resistant child not explicitly KILLed: %+v", r.Events)
	}
}

// stringTrim removes trailing whitespace from a helper PID file.
//
// Example: a newline terminated PID becomes a decimal string.
func stringTrim(data []byte) string {
	end := len(data)
	for end > 0 && (data[end-1] == '\n' || data[end-1] == ' ') {
		end--
	}
	return string(data[:end])
}

// TestExitRetiresChild checks cleanup after a root exits with descendants.
//
// Example: background sleep cannot survive its root exiting seven.
func TestExitRetiresChild(t *testing.T) {
	r, code, _ := runGuard(t, "--", "/bin/sh", "-c", "sleep 30 & exit 7")
	if code != 7 || r.CommandExit != 7 || !r.HostChildrenSettled || len(r.Errors) != 0 {
		t.Fatalf("code=%d receipt=%+v", code, r)
	}
	if len(r.Events) < 2 {
		t.Fatalf("descendant not observed/retired: %+v", r)
	}
}

// TestCancelIdentity checks rejection and retirement of a controlled target.
//
// Example: an incorrect birth cannot signal the live helper.
func TestCancelIdentity(t *testing.T) {
	child := startControlledCommand(t, exec.Command("/bin/sleep", "30"))
	stat, err := readProcess(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	r, code, _ := runGuard(t, "-cancel-pid", strconv.Itoa(stat.PID), "-cancel-birth", strconv.FormatUint(stat.Birth+1, 10))
	if code != guardFailure || r.HostChildrenSettled {
		t.Fatalf("mismatch accepted identity: code=%d receipt=%+v", code, r)
	}
	for _, event := range r.Events {
		switch event.Action {
		case "signal", "terminated", "killed":
			t.Fatalf("mismatch attempted signal: event=%+v receipt=%+v", event, r)
		}
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("mismatched target no longer alive: %v", err)
	}
	current, err := readProcess(child.Process.Pid)
	if err != nil || current.State == "Z" || current.State == "X" {
		t.Fatalf("mismatched target stopped: stat=%+v err=%v", current, err)
	}
	r, code, _ = runGuard(t, "-cancel-pid", strconv.Itoa(stat.PID), "-cancel-birth", strconv.FormatUint(stat.Birth, 10))
	if code != 0 || !r.HostChildrenSettled {
		t.Fatalf("cancel code=%d receipt=%+v", code, r)
	}
	current, err = readProcess(child.Process.Pid)
	if err != nil || (current.State != "Z" && current.State != "X") {
		t.Fatalf("cancelled target still running: stat=%+v err=%v", current, err)
	}
}

// TestSignalRetiresChildren checks interruption and bounded family cleanup.
//
// Example: interrupting the guard retires its sleep child.
func TestSignalRetiresChildren(t *testing.T) {
	binary := os.Getenv("GUARD_TEST_BINARY")
	path := filepath.Join(t.TempDir(), "receipt.json")
	pidPath := filepath.Join(t.TempDir(), "pid")
	childPath := filepath.Join(t.TempDir(), "child-pid")
	cmd := exec.Command(binary, "-receipt", path, "--", "/bin/sh", "-c", `sleep 30 & echo $! > "$2"; echo $$ > "$1"; wait`, "guard", pidPath, childPath)
	startControlledCommand(t, cmd)
	until := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("child did not start")
		}
		time.Sleep(time.Millisecond)
	}
	retainControlledProcess(t, pidPath)
	retainControlledProcess(t, childPath)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal("interrupted guard did not write receipt")
		}
		time.Sleep(time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if !r.HostChildrenSettled || len(r.Survivors) != 0 || r.Cause != "signal: interrupt" {
		t.Fatalf("receipt=%+v", r)
	}
}

// retainControlledProcess keeps a launched fixture's process handle for cleanup.
//
// Example: an interruption assertion timeout still retires the known sleep.
func retainControlledProcess(
	t *testing.T,
	pidPath string,
) {
	t.Helper()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("controlled PID: %q err=%v", data, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(
		// Cleanup retires this known fixture; its guard owns native reaping.
		//
		// Example: an already exited fixture needs only handle release.
		func() {
			if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("controlled process Kill: %v", err)
			}
			if err := process.Release(); err != nil {
				t.Errorf("controlled process Release: %v", err)
			}
		},
	)
}

// startControlledCommand registers unconditional cleanup with one Wait owner.
//
// Example: an assertion failure still kills and reaps a sleep helper.
func startControlledCommand(
	t *testing.T,
	cmd *exec.Cmd,
) *exec.Cmd {
	t.Helper()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(
		// Cleanup owns the only Wait and classifies expected termination explicitly.
		//
		// Example: a cancellation helper may already have received TERM.
		func() {
			if cmd.ProcessState != nil {
				return
			}
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("helper Kill: %v", err)
			}

			err := cmd.Wait()
			if err == nil {
				return
			}

			var exit *exec.ExitError
			if errors.As(err, &exit) {
				status, ok := exit.Sys().(syscall.WaitStatus)
				if ok && status.Signaled() {
					return
				}
				if cmd.Path == os.Getenv("GUARD_TEST_BINARY") && exit.ExitCode() == guardFailure {
					return
				}
			}
			t.Errorf("helper Wait: %v", err)
		},
	)
	return cmd
}

// TestNegativeCancelPID rejects invalid PID values without target actions.
//
// Example: PID minus one must not become already-absent success.
func TestNegativeCancelPID(t *testing.T) {
	child := startControlledCommand(t, exec.Command("/bin/sleep", "30"))
	stat, err := readProcess(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "unexpected-launch")
	for _, args := range [][]string{
		{"-cancel-pid=-1", "-cancel-birth=1"},
		{"-cancel-pid", strconv.Itoa(stat.PID), "-cancel-birth=0"},
		{"-cancel-pid", strconv.Itoa(stat.PID), "-cancel-birth", strconv.FormatUint(stat.Birth, 10), "--", "/bin/sh", "-c", `printf launched > "$1"`, "guard", marker},
	} {
		cmd := exec.Command(os.Getenv("GUARD_TEST_BINARY"), append([]string{"-receipt", filepath.Join(t.TempDir(), "receipt.json")}, args...)...)
		output, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != guardFailure {
			t.Errorf("invalid cancellation %v: err=%v output=%q", args, err, output)
		}
		current, err := readProcess(child.Process.Pid)
		if err != nil || current.Birth != stat.Birth || current.State == "Z" || current.State == "X" {
			t.Fatalf("invalid cancellation affected helper: stat=%+v err=%v", current, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid CLI launched command: %v", err)
	}
}

// TestInheritedStandardStreams checks exact bytes on each inherited descriptor.
//
// Example: binary stdin survives a shell cat without diagnostic contamination.
func TestInheritedStandardStreams(t *testing.T) {
	input := []byte{'a', 0, '\n', 255, 'z'}
	var stdout, stderr bytes.Buffer
	receiptPath := filepath.Join(t.TempDir(), "receipt.json")
	cmd := exec.Command(os.Getenv("GUARD_TEST_BINARY"), "-receipt", receiptPath, "--", "/bin/sh", "-c", "cat; printf 'error\\000bytes' >&2")
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), input) || !bytes.Equal(stderr.Bytes(), []byte("error\x00bytes")) {
		t.Fatalf("stdout=%q stderr=%q", stdout.Bytes(), stderr.Bytes())
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.CommandExit != 0 || !receipt.HostChildrenSettled || len(receipt.Errors) != 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
}

// TestInheritedProcessGroup checks that command launch retains caller grouping.
//
// Example: a shell child remains in the test caller's process group.
func TestInheritedProcessGroup(t *testing.T) {
	group, err := syscall.Getpgid(0)
	if err != nil {
		t.Fatal(err)
	}
	r, code, output := runGuard(t, "--", "/bin/sh", "-c", `ps -o pgid= -p $$`)
	observed, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil || observed != group || code != 0 || !r.HostChildrenSettled {
		t.Fatalf("group=%d want=%d code=%d err=%v receipt=%+v", observed, group, code, err, r)
	}
}

// TestCancelJustExited checks cancellation of a controlled unreaped lifetime.
//
// Example: a zombie helper supplies exact birth metadata without live work.
func TestCancelJustExited(t *testing.T) {
	child := startControlledCommand(t, exec.Command("/bin/true"))
	until := time.Now().Add(time.Second)
	for {
		stat, err := readProcess(child.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if stat.State == "Z" {
			r, code, _ := runGuard(t, "-cancel-pid", strconv.Itoa(stat.PID), "-cancel-birth", strconv.FormatUint(stat.Birth, 10))
			if code != 0 || !r.HostChildrenSettled || len(r.Errors) != 0 {
				t.Fatalf("code=%d receipt=%+v", code, r)
			}
			return
		}
		if time.Now().After(until) {
			t.Fatal("helper did not exit")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCancelAlreadyAbsent checks a controlled helper after native reap.
//
// Example: an already reaped true command needs no target signals.
func TestCancelAlreadyAbsent(t *testing.T) {
	child := exec.Command("/bin/true")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	r, code, _ := runGuard(t, "-cancel-pid", strconv.Itoa(child.Process.Pid), "-cancel-birth=1")
	if code != 0 || !r.HostChildrenSettled || len(r.Events) != 0 || len(r.Errors) != 0 {
		t.Fatalf("code=%d receipt=%+v", code, r)
	}
}

// TestNativePreexistingChild checks nonconsuming constructor rejection.
//
// Example: an existing sleep child cannot become guard-owned custody.
func TestNativePreexistingChild(t *testing.T) {
	child := startControlledCommand(t, exec.Command("/bin/sleep", "30"))
	before, err := readProcess(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := (linuxKernel{}).Childless(); err == nil {
		t.Fatal("preexisting native child was accepted")
	}
	after, err := readProcess(child.Process.Pid)
	if err != nil || after.Birth != before.Birth || after.State == "Z" || after.State == "X" {
		t.Fatalf("childless check changed live helper: stat=%+v err=%v", after, err)
	}
	if child.ProcessState != nil {
		t.Fatal("helper Wait was consumed before cleanup")
	}
}

// TestNonexistentCommand checks contextual launch failure without published custody.
//
// Example: a missing executable returns 125 and cannot report settlement.
func TestNonexistentCommand(t *testing.T) {
	commandPath := filepath.Join(t.TempDir(), "nonexistent-command")
	r, code, _ := runGuard(t, "--", commandPath)
	diagnostic := strings.Join(r.Errors, "\n")
	if code != guardFailure || r.CommandPID != 0 || r.HostChildrenSettled || !strings.Contains(diagnostic, "start failed") || !strings.Contains(diagnostic, commandPath) {
		t.Fatalf("missing executable code=%d receipt=%+v", code, r)
	}
	for _, event := range r.Events {
		if event.Action == "started" || event.Action == "terminated" || event.Action == "killed" {
			t.Fatalf("missing executable created target activity: %+v", event)
		}
	}
}

// TestReceiptWriteFailure checks that output persistence errors override success.
//
// Example: a directory receipt destination returns 125 after true exits zero.
func TestReceiptWriteFailure(t *testing.T) {
	receiptPath := t.TempDir()
	cmd := exec.Command(os.Getenv("GUARD_TEST_BINARY"), "-receipt", receiptPath, "--", "/bin/true")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != guardFailure || len(output) == 0 {
		t.Fatalf("receipt failure: err=%v output=%q", err, output)
	}
	stat, err := os.Stat(receiptPath)
	if err != nil || !stat.IsDir() {
		t.Fatalf("receipt directory changed: stat=%v err=%v", stat, err)
	}
}

// TestProfileNativeSupervision profiles the actual launch driver with an idle native child.
// It is opt-in because collecting representative CPU samples takes over 31 seconds.
//
// Example: GUARD_CPU_PROFILE=1 go test -run '^TestProfileNativeSupervision$' -cpuprofile /tmp/guard.cpu.
func TestProfileNativeSupervision(t *testing.T) {
	if os.Getenv("GUARD_CPU_PROFILE") != "1" {
		t.Skip("set GUARD_CPU_PROFILE=1 for a 33-second native supervision profile")
	}
	var before, childrenBefore syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &childrenBefore); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	t.Logf("profile started_at_utc=%s guard_pid=%d child_command=/bin/sleep 33", started.UTC().Format(time.RFC3339Nano), os.Getpid())
	receipt, code := supervise(time.Minute, 250*time.Millisecond, ProcessIdentity{}, []string{"/bin/sleep", "33"})
	finished := time.Now()
	var after, childrenAfter syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &childrenAfter); err != nil {
		t.Fatal(err)
	}
	wall := finished.Sub(started).Seconds()
	user := float64(after.Utime.Nano()-before.Utime.Nano()) / float64(time.Second)
	system := float64(after.Stime.Nano()-before.Stime.Nano()) / float64(time.Second)
	childUser := float64(childrenAfter.Utime.Nano()-childrenBefore.Utime.Nano()) / float64(time.Second)
	childSystem := float64(childrenAfter.Stime.Nano()-childrenBefore.Stime.Nano()) / float64(time.Second)
	t.Logf("profile finished_at_utc=%s elapsed_monotonic_seconds=%.9f guard_user_cpu_seconds=%.6f guard_system_cpu_seconds=%.6f guard_cpu_percent=%.3f child_user_cpu_seconds=%.6f child_system_cpu_seconds=%.6f", finished.UTC().Format(time.RFC3339Nano), wall, user, system, 100*(user+system)/wall, childUser, childSystem)
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("profile receipt=%s", data)
	if wall < 31 || code != 0 || receipt.CommandPID <= 0 || receipt.CommandExit != 0 || receipt.Cause != "command exited" || !receipt.HostChildrenSettled || len(receipt.Survivors) != 0 || len(receipt.Errors) != 0 {
		t.Fatalf("profile did not continuously supervise and settle the controlled native child: wall=%.9f code=%d receipt=%+v", wall, code, receipt)
	}
	for _, event := range receipt.Events {
		switch event.Action {
		case "terminated", "killed":
			t.Fatalf("idle child unexpectedly needed retirement: event=%+v", event)
		}
	}
}

// TestReadProcessNative checks numeric hints against pinned native metadata and absence.
//
// Example: the current task has the same birth and parent through both readers.
func TestReadProcessNative(t *testing.T) {
	kernel := linuxKernel{}
	pid := os.Getpid()
	directory, err := kernel.OpenDirectory(pid)
	if err != nil {
		t.Fatal(err)
	}
	anchored := kernel.ReadStat(directory, pid)
	if err := syscall.Close(directory); err != nil {
		t.Fatal(err)
	}
	stat, err := readProcess(pid)
	if err != nil || anchored.InspectionError != nil || anchored.CloseError != nil || stat.ProcessIdentity != anchored.Stat.ProcessIdentity || stat.Parent != anchored.Stat.Parent || stat.Birth == 0 {
		t.Fatalf("numeric=%+v err=%v anchored=%+v", stat, err, anchored)
	}
	child := exec.Command("/bin/true")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []int{-1, child.Process.Pid} {
		stat, err := readProcess(absent)
		if !errors.Is(err, os.ErrNotExist) || stat != (processStat{}) {
			t.Fatalf("absent PID %d: stat=%+v err=%v", absent, stat, err)
		}
	}
}

// TestReadProcessSyscalls traces only a controlled test subprocess's numeric stat reads.
//
// Example: GUARD_STAT_TRACE=1 verifies sixteen reads have no fstat or poll registration.
func TestReadProcessSyscalls(t *testing.T) {
	if os.Getenv("GUARD_STAT_TRACE_CHILD") == "1" {
		for range 16 {
			stat, err := readProcess(os.Getpid())
			if err != nil || stat.PID != os.Getpid() || stat.Birth == 0 || stat.Parent != os.Getppid() {
				t.Fatalf("stat=%+v err=%v", stat, err)
			}
		}
		return
	}
	if os.Getenv("GUARD_STAT_TRACE") != "1" {
		t.Skip("set GUARD_STAT_TRACE=1 for a controlled strace subprocess")
	}
	tracePath := filepath.Join(t.TempDir(), "stat.trace")
	cmd := exec.Command("strace", "-f", "-qq", "-yy", "-o", tracePath, "-e", "trace=openat,read,close,fstat,fcntl,epoll_ctl", os.Args[0], "-test.run=^TestReadProcessSyscalls$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GUARD_STAT_TRACE_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("controlled trace: %v output=%s", err, output)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	reads, closes, flags := 0, 0, 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "</proc/") || !strings.Contains(line, "/stat>") {
			continue
		}
		switch {
		case strings.Contains(line, "fstat("), strings.Contains(line, "epoll_ctl("), strings.Contains(line, "F_SETFL"):
			t.Errorf("unnecessary stat setup: %s", line)
		case strings.Contains(line, "read("):
			reads++
		case strings.Contains(line, "close("):
			closes++
		case strings.Contains(line, "F_GETFL"):
			flags++
		}
	}
	if reads < 16 || closes != 16 || flags != 16 {
		t.Fatalf("stat trace: reads=%d closes=%d getflags=%d", reads, closes, flags)
	}
	t.Logf("stat trace: reads=%d closes=%d getflags=%d; no fstat, epoll_ctl or F_SETFL", reads, closes, flags)
}

// TestReadProcessHintZeroBirth preserves unknown birth in numeric hint parsing.
//
// Example: zero birth remains metadata and never becomes anchored lifetime authority.
func TestReadProcessHintZeroBirth(t *testing.T) {
	data := []byte("20 (name with ) boundary) R 10 " + strings.Repeat("0 ", 18))
	stat, err := parseProcess(data, 20)
	if err != nil || stat.ProcessIdentity != (ProcessIdentity{PID: 20}) || stat.Parent != 10 || stat.State != "R" {
		t.Fatalf("zero birth hint: stat=%+v err=%v", stat, err)
	}
	if _, err := parseProcess(data, 21); err == nil {
		t.Fatal("zero birth hint accepted mismatched PID")
	}
}

// TestSyntheticNumericHintCloseFailure preserves cleanup diagnostics in both hint callers.
//
// Example: benign absence stays benign, while absence plus close EIO retires proved custody.
func TestSyntheticNumericHintCloseFailure(t *testing.T) {
	for _, path := range []string{"preparation"} {
		for _, known := range []error{syscall.ENOENT, syscall.ESRCH, syscall.EACCES, syscall.EPERM} {
			for _, closeFailed := range []bool{false, true} {
				g, k := newDriverControl()
				configureDriverExternalFamily(g, k, 0, 1)
				err := known
				if closeFailed {
					err = errors.Join(known, errNumericStatClose, syscall.EIO)
				}
				k.hintErrors[8000] = err
				if !g.initialCancel() {
					t.Fatal("proved root rejected")
				}
				retirement := g.retirement
				g.prepareFamily()
				if (len(g.receipt.Errors) != 0) != closeFailed {
					t.Errorf("%s known=%v close=%t diagnostics=%v", path, known, closeFailed, g.receipt.Errors)
				}
				g.drive()
				receipt, code := g.finalize()
				if closeFailed && (code != guardFailure || len(receipt.Errors) == 0 || (path == "preparation" && receipt.HostChildrenSettled)) {
					t.Errorf("%s lost mixed failure: code=%d receipt=%+v", path, code, receipt)
				}
				if closeFailed && (!strings.Contains(strings.Join(receipt.Errors, "\n"), known.Error()) || !strings.Contains(strings.Join(receipt.Errors, "\n"), syscall.EIO.Error())) {
					t.Errorf("%s lost diagnostic causes: %v", path, receipt.Errors)
				}
				if !closeFailed && (code != 0 || !receipt.HostChildrenSettled || len(receipt.Errors) != 0) {
					t.Errorf("%s rejected benign %v: code=%d receipt=%+v", path, known, code, receipt)
				}
				if !g.retirement.Equal(retirement) || len(k.signals) != 1 || k.signals[0].target != 24101 || k.signals[0].signal != syscall.SIGTERM || len(g.current) != 0 || len(g.pending) != 0 || len(g.tentative) != 0 {
					t.Errorf("%s changed published retirement: signals=%v receipt=%+v", path, k.signals, receipt)
				}
			}
		}
	}
}

// TestReadProcessInjectedOpen checks actual syscall EINTR recovery and non-EINTR failure.
//
// Example: one injected open interruption still reads complete numeric metadata.
func TestReadProcessInjectedOpen(t *testing.T) {
	if mode := os.Getenv("GUARD_STAT_OPEN_CHILD"); mode != "" {
		pid, err := strconv.Atoi(os.Getenv("GUARD_STAT_OPEN_PID"))
		if err != nil {
			t.Fatal(err)
		}
		stat, err := readProcess(pid)
		switch mode {
		case "EINTR":
			if err != nil || stat.PID != pid || stat.Birth == 0 {
				t.Fatalf("interrupted open: stat=%+v err=%v", stat, err)
			}
		case "EIO":
			if !errors.Is(err, syscall.EIO) || stat != (processStat{}) || !strings.Contains(err.Error(), "open numeric stat PID "+strconv.Itoa(pid)) {
				t.Fatalf("failed open: stat=%+v err=%v", stat, err)
			}
		case "READ_ENOENT_CLOSE_EIO", "READ_EACCES_CLOSE_EIO":
			known := error(syscall.ENOENT)
			if mode == "READ_EACCES_CLOSE_EIO" {
				known = syscall.EACCES
			}
			if !errors.Is(err, known) || !errors.Is(err, syscall.EIO) || !errors.Is(err, errNumericStatClose) || stat != (processStat{}) || !strings.Contains(err.Error(), "read numeric stat PID "+strconv.Itoa(pid)) || !strings.Contains(err.Error(), "close numeric stat PID "+strconv.Itoa(pid)) {
				t.Fatalf("mixed read/close: stat=%+v err=%v", stat, err)
			}
		case "CLOSE_EIO":
			if !errors.Is(err, syscall.EIO) || !errors.Is(err, errNumericStatClose) || stat.PID != pid || stat.Birth == 0 {
				t.Fatalf("successful read/failed close: stat=%+v err=%v", stat, err)
			}
		default:
			t.Fatalf("unknown injection %q", mode)
		}
		return
	}
	if os.Getenv("GUARD_STAT_TRACE") != "1" {
		t.Skip("set GUARD_STAT_TRACE=1 for controlled syscall injection")
	}
	pid := strconv.Itoa(os.Getpid())
	for _, mode := range []string{"EINTR", "EIO", "READ_ENOENT_CLOSE_EIO", "READ_EACCES_CLOSE_EIO", "CLOSE_EIO"} {
		tracePath := filepath.Join(t.TempDir(), "open.trace")
		args := []string{"-f", "-qq", "-yy", "-P", "/proc/" + pid + "/stat", "-o", tracePath, "-e", "trace=openat,read,close"}
		switch mode {
		case "EINTR", "EIO":
			args = append(args, "-e", "inject=openat:error="+mode+":when=1")
		case "READ_ENOENT_CLOSE_EIO":
			args = append(args, "-e", "inject=read:error=ENOENT:when=1", "-e", "inject=close:error=EIO:when=1")
		case "READ_EACCES_CLOSE_EIO":
			args = append(args, "-e", "inject=read:error=EACCES:when=1", "-e", "inject=close:error=EIO:when=1")
		case "CLOSE_EIO":
			args = append(args, "-e", "inject=close:error=EIO:when=1")
		}
		args = append(args, os.Args[0], "-test.run=^TestReadProcessInjectedOpen$", "-test.count=1")
		cmd := exec.Command("strace", args...)
		cmd.Env = append(os.Environ(), "GUARD_STAT_OPEN_CHILD="+mode, "GUARD_STAT_OPEN_PID="+pid)
		output, runErr := cmd.CombinedOutput()
		trace, readErr := os.ReadFile(tracePath)
		if runErr != nil || readErr != nil || !strings.Contains(string(trace), "(INJECTED)") {
			t.Errorf("%s controlled injection: run=%v read=%v output=%s trace=%s", mode, runErr, readErr, output, trace)
		}
		expectedCloses := 1
		if mode == "EIO" {
			expectedCloses = 0
		}
		if strings.Count(string(trace), "close(") != expectedCloses {
			t.Errorf("%s close count=%d wanted=%d trace=%s", mode, strings.Count(string(trace), "close("), expectedCloses, trace)
		}
	}
}
