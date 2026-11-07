//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xaionaro-go/observability"
	"golang.org/x/sys/unix"
)

// TestCgroupPopulation requires explicit population evidence and rejects malformed emptiness.
//
// Example: a missing field or duplicate zero never establishes subtree settlement.
func TestCgroupPopulation(t *testing.T) {
	for _, control := range []struct {
		text      string
		populated bool
		valid     bool
	}{
		{"populated 0\nfrozen 0\n", false, true},
		{"populated 1\nfrozen 0\n", true, true},
		{"frozen 0\n", false, false},
		{"populated 0\npopulated 0\n", false, false},
		{"populated 2\n", false, false},
		{"populated 0 extra\n", false, false},
	} {
		populated, err := parseCgroupPopulation([]byte(control.text))
		if (err == nil) != control.valid || (err == nil && populated != control.populated) {
			t.Errorf("events=%q populated=%t err=%v", control.text, populated, err)
		}
	}
}

// TestCgroupInterruptedSetupReportsSignal preserves the reason for canceled manager acquisition.
//
// Example: a bus accepting connections without authenticating cannot hide interruption as a generic error.
func TestCgroupInterruptedSetupReportsSignal(t *testing.T) {
	assertCgroupBlockedSetup(t, true)
}

// TestCgroupStalledSetupReportsBudget distinguishes manager acquisition timeout from command runtime.
//
// Example: a non-authenticating bus exhausts setup without launching a requested child.
func TestCgroupStalledSetupReportsBudget(t *testing.T) {
	assertCgroupBlockedSetup(t, false)
}

// assertCgroupBlockedSetup checks prelaunch ownership against a deliberately non-authenticating bus.
//
// Example: interruption preserves its cause, while timeout names the independent setup budget.
func assertCgroupBlockedSetup(t *testing.T, interrupt bool) {
	t.Helper()
	directory := t.TempDir()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(directory, "bus"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	path, marker := filepath.Join(directory, "receipt.json"), filepath.Join(directory, "launched")
	command := exec.Command(os.Getenv("GUARD_TEST_BINARY"), "-receipt", path, "--", "/usr/bin/touch", marker)
	command.Env = append(os.Environ(), "DBUS_SESSION_BUS_ADDRESS=unix:path="+filepath.Join(directory, "bus"))
	started := time.Now()
	startControlledCommand(t, command)
	connection, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if interrupt {
		if err := command.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
	}
	err = command.Wait()
	finished := time.Now()
	t.Logf("started_at_utc=%s finished_at_utc=%s elapsed_monotonic_seconds=%.9f command=%q environment=%s/%s %s service=non-authenticating-owned-unix-bus", started.UTC().Format(time.RFC3339Nano), finished.UTC().Format(time.RFC3339Nano), finished.Sub(started).Seconds(), command.Args, runtime.GOOS, runtime.GOARCH, runtime.Version())
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != guardFailure {
		t.Fatalf("interrupted setup status: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.CommandPID != 0 || !receipt.CommandStarted.IsZero() || receipt.HostChildrenSettled || len(receipt.Errors) == 0 {
		t.Fatalf("interrupted prelaunch ownership lost: %+v", receipt)
	}
	if interrupt && !strings.HasPrefix(receipt.Cause, "signal: ") {
		t.Fatalf("setup interruption cause missing: %+v", receipt)
	}
	if !interrupt && !strings.Contains(strings.Join(receipt.Errors, "\n"), "setup budget 5s expired") {
		t.Fatalf("setup timeout budget missing: %+v", receipt)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("requested child ran during interrupted setup: %v", err)
	}
}

// TestCgroupMembersIncludesNestedGroups limits discovery to the exclusive command subtree.
//
// Example: graceful retirement includes nested members and excludes a sibling group.
func TestCgroupMembersIncludesNestedGroups(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "command")
	for _, group := range []string{path, filepath.Join(path, "nested"), filepath.Join(parent, "unrelated")} {
		if err := os.Mkdir(group, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{
		filepath.Join(path, "cgroup.procs"):                "10\n11\n",
		filepath.Join(path, "nested", "cgroup.procs"):      "12\n11\n",
		filepath.Join(parent, "unrelated", "cgroup.procs"): "99\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	group := &commandCgroup{Path: path}
	members, err := group.inspectMembers(cgroupTraversalBudget)
	if err != nil || !reflect.DeepEqual(members, []int{10, 11, 12}) {
		t.Fatalf("members=%v err=%v", members, err)
	}
	if err := os.WriteFile(filepath.Join(path, "cgroup.procs"), []byte("invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if members, err := group.inspectMembers(cgroupTraversalBudget); err == nil || members != nil {
		t.Fatalf("malformed members accepted: %v %v", members, err)
	}
}

// TestCgroupWatchStopJoinsBeforeDescriptorClose exercises the private stop event.
//
// Example: shutdown wakes a blocked poll and closes its descriptor exactly once.
func TestCgroupWatchStopJoinsBeforeDescriptorClose(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "events")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	watch, err := newCgroupWatch(&commandCgroup{Events: file}, -1)
	if err != nil {
		t.Fatal(err)
	}
	observability.Go(context.Background(), watch.Run)
	if err := watch.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watch.Done:
	default:
		t.Fatal("watcher returned without joined completion")
	}
	if _, err := unix.FcntlInt(uintptr(watch.StopFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("stop descriptor remained open: %v", err)
	}
}

// TestCgroupMemberRejectsUnrelatedLifetime falsifies numeric-membership signal authority.
//
// Example: a stale PID hint for a process outside the owned group remains running.
func TestCgroupMemberRejectsUnrelatedLifetime(t *testing.T) {
	child := startControlledCommand(t, exec.Command("/bin/sleep", "30"))
	fd, err := unix.PidfdOpen(child.Process.Pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	g := newGuard(linuxKernel{}, realClock{}, nil, time.Second, time.Second, ProcessIdentity{})
	g.signalCgroupMember(&commandCgroup{Relative: "/unrelated-owned-command-test"}, child.Process.Pid)
	state, err := (linuxKernel{}).Poll(fd)
	if err != nil || state != processRunning || len(g.receipt.Events) != 0 {
		t.Fatalf("foreign lifetime signaled: state=%v err=%v receipt=%+v", state, err, g.receipt)
	}
}

// TestCgroupUnavailableBusReturnsReceipt rejects setup before the requested command runs.
//
// Example: a missing user bus returns 125 with its failed operation and no command PID.
func TestCgroupUnavailableBusReturnsReceipt(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent-owned-command-bus")
	marker := filepath.Join(t.TempDir(), "launched")
	receipt, code, _ := runGuard(t, "--", "/usr/bin/touch", marker)
	if code != guardFailure || receipt.CommandPID != 0 || receipt.HostChildrenSettled || len(receipt.Errors) == 0 || !strings.Contains(receipt.Errors[0], "connect to systemd user manager") {
		t.Fatalf("bus failure lost prelaunch failure reporting: code=%d receipt=%+v", code, receipt)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran after failed acquisition: %v", err)
	}
}

// TestCgroupMissingBusEnvironmentPreservesExactEnvironment exercises explicit bus resolution.
//
// Example: omitted DBUS_SESSION_BUS_ADDRESS neither launches a helper nor adds environment keys.
func TestCgroupMissingBusEnvironmentPreservesExactEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	command := exec.Command(os.Getenv("GUARD_TEST_BINARY"), "-receipt", path, "--", "/usr/bin/env", "-0")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DBUS_SESSION_BUS_ADDRESS=") {
			command.Env = append(command.Env, entry)
		}
	}
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != strings.Join(command.Env, "\x00")+"\x00" {
		t.Fatal("command environment differed from the exact caller environment")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.HostChildrenSettled || receipt.CommandExit != 0 || len(receipt.Errors) != 0 {
		t.Fatalf("no-address acquisition failed: %+v", receipt)
	}
}

// TestCgroupWatchRetainsFailureBehindPendingWake preserves errors when readiness coalesces.
//
// Example: an invalid root descriptor cannot silently disappear behind an earlier population wake.
func TestCgroupWatchRetainsFailureBehindPendingWake(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "events")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	watch, err := newCgroupWatch(&commandCgroup{Events: file}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	watch.Updates <- nil
	observability.Go(context.Background(), watch.Run)
	<-watch.Done
	if err := watch.Stop(); err == nil {
		t.Fatal("joined watcher silently lost terminal error behind a pending wake")
	}
	if _, err := unix.FcntlInt(uintptr(watch.StopFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("failed observer left stop descriptor open: %v", err)
	}
}

// TestCgroupNativeRootStatusIsConsumedOnce preserves native status across later numeric PID reuse.
//
// Example: a later child sharing the root PID cannot overwrite the original exit code.
func TestCgroupNativeRootStatusIsConsumedOnce(t *testing.T) {
	g, kernel := newDriverControl()
	g.rootUnreaped = true
	g.appliedStage = retirementTERM
	kernel.waits = []driverWait{{result: nativeResult{PID: 4101, Status: syscall.WaitStatus((int(syscall.SIGSTOP) << 8) | 0x7f)}}, {result: nativeResult{PID: 4101, Status: syscall.WaitStatus(0xffff)}}, {result: nativeResult{PID: 0}}}
	if g.drainCgroupCommand(4101) != commandChildrenRunning || g.rootDone || !g.rootUnreaped || g.receipt.CommandExit != -1 {
		t.Fatalf("nonterminal root status consumed its native pin: %+v", g.receipt)
	}
	kernel.waits = []driverWait{{result: nativeResult{PID: 4101, Status: syscall.WaitStatus(7 << 8)}}, {result: nativeResult{Error: unix.ECHILD}}}
	if g.drainCgroupCommand(4101) != commandChildrenAbsent || !g.rootDone || g.rootUnreaped || g.receipt.CommandExit != 7 {
		t.Fatalf("native root result lost: %+v", g.receipt)
	}
	kernel.waits = []driverWait{{result: nativeResult{PID: 4101, Status: syscall.WaitStatus(9 << 8)}}, {result: nativeResult{Error: unix.ECHILD}}}
	if g.drainCgroupCommand(4101) != commandChildrenAbsent || g.receipt.CommandExit != 7 {
		t.Fatalf("later lifetime replaced root status: %+v", g.receipt)
	}
}

// TestCgroupTraversalYieldsBeforeRemainingDiscovery requires cutoff before any further I/O.
//
// Example: due KILL skips even a missing root; crossing the boundary skips malformed remaining data.
func TestCgroupTraversalYieldsBeforeRemainingDiscovery(t *testing.T) {
	group := &commandCgroup{Path: filepath.Join(t.TempDir(), "absent")}
	g, kernel := newDriverControl()
	g.beginRetirement("deadline")
	kernel.clock.now = g.retirement.Add(g.grace)
	term := cgroupTERM{Guard: g, Group: group}
	result, err := group.visitMembers(term, term)
	if result != cgroupTraversalCutoff || err != nil {
		t.Fatalf("due KILL continued discovery: result=%v err=%v", result, err)
	}
	group.Path = t.TempDir()
	if err := os.WriteFile(filepath.Join(group.Path, "cgroup.procs"), []byte("10\ninvalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	killDue := false
	control := testCgroupTraversal{Proceed: func() bool { return !killDue }, Visit: func(int) { killDue = true }}
	result, err = group.visitMembers(control, control)
	if result != cgroupTraversalCutoff || err != nil || !killDue {
		t.Fatalf("remaining discovery delayed due KILL: result=%v err=%v", result, err)
	}
}

// TestCgroupTraversalClockCutoff checks duplicate parsing against the shared KILL clock.
//
// Example: a repeated first PID cannot postpone KILL until a malformed later token is read.
func TestCgroupTraversalClockCutoff(t *testing.T) {
	group := &commandCgroup{Path: t.TempDir()}
	if err := os.WriteFile(filepath.Join(group.Path, "cgroup.procs"), []byte(strings.Repeat("10\n", 5000)+"invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	g, kernel := newDriverControl()
	g.beginRetirement("deadline")
	kernel.clock.advanceAt = kernel.clock.reads + 40
	kernel.clock.advance = g.grace
	var members []int
	control := testCgroupTraversal{Proceed: (cgroupTERM{Guard: g, Group: group}).take, Visit: func(pid int) { members = append(members, pid) }}
	result, err := group.visitMembers(control, control)
	if result != cgroupTraversalCutoff || err != nil || !reflect.DeepEqual(members, []int{10}) || g.stage() != retirementKILL {
		t.Fatalf("clock cutoff lost: result=%v err=%v members=%v stage=%v", result, err, members, g.stage())
	}
	// The lifecycle owner performs its already-due bulk action without inspecting the rest.
	kill, err := os.CreateTemp(t.TempDir(), "kill")
	if err != nil {
		t.Fatal(err)
	}
	defer kill.Close()
	group.Kill = kill
	if g.stage() >= retirementKILL {
		g.killCommandCgroup(group)
	}
	data, err := os.ReadFile(kill.Name())
	if err != nil || string(data) != "1" {
		t.Fatalf("due bulk KILL not reached: data=%q err=%v", data, err)
	}
	assertCgroupTraversalClosed(t, group.Path)
}

// TestCgroupTraversalInspectionBudget reports partial lists without implying completeness.
//
// Example: duplicate-heavy input and ordinary directory controls both consume finite work.
func TestCgroupTraversalInspectionBudget(t *testing.T) {
	for _, controls := range []bool{false, true} {
		group := &commandCgroup{Path: t.TempDir()}
		text := strings.Repeat("10\n", 5000)
		if controls {
			text = "10\n"
			for index := 0; index < 100; index++ {
				if err := os.WriteFile(filepath.Join(group.Path, fmt.Sprintf("control-%03d", index)), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := os.WriteFile(filepath.Join(group.Path, "cgroup.procs"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		members, err := group.inspectMembers(40)
		if err == nil || !strings.Contains(err.Error(), "survivor inspection incomplete") || !reflect.DeepEqual(members, []int{10}) {
			t.Fatalf("partial inspection hidden: controls=%v members=%v err=%v", controls, members, err)
		}
		g, _ := newDriverControl()
		g.fail("inspect unsettled command members", err)
		receipt, code := g.finalize()
		if code != guardFailure || receipt.HostChildrenSettled || len(receipt.Errors) == 0 {
			t.Fatalf("incomplete report status=%d receipt=%+v", code, receipt)
		}
		assertCgroupTraversalClosed(t, group.Path)
	}
}

// TestCgroupTraversalErrorAndDisappearance preserves substantive errors and closes handles.
//
// Example: a vanished nested group is benign; malformed nested membership remains an error.
func TestCgroupTraversalErrorAndDisappearance(t *testing.T) {
	group := &commandCgroup{Path: t.TempDir()}
	nested := filepath.Join(group.Path, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(group.Path, "cgroup.procs"), []byte("10\n"), 0600); err != nil {
		t.Fatal(err)
	}
	members, err := group.inspectMembers(cgroupTraversalBudget)
	if err != nil || !reflect.DeepEqual(members, []int{10}) {
		t.Fatalf("missing descendant control: members=%v err=%v", members, err)
	}
	if err := os.WriteFile(filepath.Join(nested, "cgroup.procs"), []byte("invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if members, err := group.inspectMembers(cgroupTraversalBudget); err == nil || !strings.Contains(err.Error(), "survivor inspection incomplete") || !reflect.DeepEqual(members, []int{10}) {
		t.Fatalf("malformed nested inspection must identify partial list: members=%v err=%v", members, err)
	}
	assertCgroupTraversalClosed(t, group.Path)
	if err := os.WriteFile(filepath.Join(nested, "cgroup.procs"), []byte("12\ninvalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cutoff := false
	control := testCgroupTraversal{Proceed: func() bool { return !cutoff }, Visit: func(pid int) { cutoff = pid == 12 }}
	result, err := group.visitMembers(control, control)
	if result != cgroupTraversalCutoff || err != nil {
		t.Fatalf("nested cutoff continued discovery: result=%v err=%v", result, err)
	}
	assertCgroupTraversalClosed(t, group.Path)
}

// TestCgroupCleanupBudgetAndFiniteCompletion distinguishes physical settlement from cleanup success.
//
// Example: exhausted cleanup returns125 while retaining independently proven physical settlement.
func TestCgroupCleanupBudgetAndFiniteCompletion(t *testing.T) {
	for _, limit := range []int{0, 4, cgroupTraversalBudget} {
		group := &commandCgroup{Path: filepath.Join(t.TempDir(), "command")}
		if err := os.MkdirAll(filepath.Join(group.Path, "a", "b"), 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		group.Directory, err = os.Open(group.Path)
		if err != nil {
			t.Fatal(err)
		}
		group.Events, err = os.CreateTemp(t.TempDir(), "events")
		if err != nil {
			t.Fatal(err)
		}
		group.Kill, err = os.CreateTemp(t.TempDir(), "kill")
		if err != nil {
			t.Fatal(err)
		}
		err = group.closeWithBudget(limit)
		g, _ := newDriverControl()
		g.receipt.HostChildrenSettled = true
		g.receipt.Cause = "command exited"
		g.receipt.CommandExit = 0
		if err != nil {
			g.fail("close command cgroup", err)
		}
		receipt, code := g.finalize()
		if limit == cgroupTraversalBudget {
			if err != nil || code != 0 {
				t.Fatalf("finite empty cleanup failed: err=%v status=%d", err, code)
			}
			if _, err := os.Stat(group.Path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("empty nested tree remains: %v", err)
			}
		} else {
			if err == nil || !strings.Contains(err.Error(), "cleanup incomplete") || code != guardFailure || !receipt.HostChildrenSettled {
				t.Fatalf("cleanup exhaustion hidden: err=%v status=%d receipt=%+v", err, code, receipt)
			}
		}
		for _, file := range []*os.File{group.Directory, group.Events, group.Kill} {
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("owned descriptor left open: %s %v", file.Name(), err)
			}
		}
		assertCgroupTraversalClosed(t, group.Path)
	}
}

// assertCgroupTraversalClosed checks only handles pointing into this test's private tree.
//
// Example: cutoff and malformed input cannot leave a traversal directory/member file open.
func assertCgroupTraversalClosed(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if target == path || strings.HasPrefix(target, path+"/") || target == path+" (deleted)" {
			t.Fatalf("traversal descriptor leaked: %s -> %s", entry.Name(), target)
		}
	}
}

// testCgroupTraversal scripts only the concrete traversal's lifecycle and member boundaries.
//
// Example: a visitor crossing KILL makes the next discovery step yield.
type testCgroupTraversal struct {
	Proceed func() bool
	Visit   func(int)
}

// take runs the scripted next-step decision.
//
// Example: false prevents reading even the root directory.
func (c testCgroupTraversal) take() bool { return c.Proceed() }

// visit records a scripted member observation.
//
// Example: a test advances its shared clock at the first member.
func (c testCgroupTraversal) visit(pid int) { c.Visit(pid) }

// TestCgroupTraversalChunkBoundary preserves tokens and deduplication across bounded reads.
//
// Example: a PID split across two chunks is visited once despite later repeated entries.
func TestCgroupTraversalChunkBoundary(t *testing.T) {
	group := &commandCgroup{Path: t.TempDir()}
	text := strings.Repeat(" \n", 2047) + "123\n" + strings.Repeat("123\n", 1000)
	if err := os.WriteFile(filepath.Join(group.Path, "cgroup.procs"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	members, err := group.inspectMembers(cgroupTraversalBudget)
	if err != nil || !reflect.DeepEqual(members, []int{123}) {
		t.Fatalf("split membership token: members=%v err=%v", members, err)
	}
	assertCgroupTraversalClosed(t, group.Path)
}

// TestCgroupCleanupChargesControlEntries bounds bookkeeping even without any child groups.
//
// Example: many control files exhaust cleanup before an attempted parent removal.
func TestCgroupCleanupChargesControlEntries(t *testing.T) {
	group := &commandCgroup{Path: t.TempDir()}
	for index := 0; index < 100; index++ {
		if err := os.WriteFile(filepath.Join(group.Path, fmt.Sprintf("control-%03d", index)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	work := cgroupWork{remaining: 40}
	result, err := group.walkGroups(group.Path, &work, nil, true)
	if result != cgroupTraversalCutoff || err != nil {
		t.Fatalf("control entries bypassed cleanup allowance: result=%v err=%v", result, err)
	}
	assertCgroupTraversalClosed(t, group.Path)
}
