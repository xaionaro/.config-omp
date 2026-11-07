//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	systemddbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/godbus/dbus/v5"
	"github.com/xaionaro-go/observability"
	"golang.org/x/sys/unix"
)

// cgroupSetupTimeout bounds manager acquisition independently of command runtime.
//
// Example: a missing manager cannot consume an unbounded prelaunch wait.
const cgroupSetupTimeout = 5 * time.Second

// commandCgroup owns the exclusive inherited command membership below a delegated scope.
// The supervisor remains in the scope parent and is never included in group retirement.
//
// Example: a setsid grandchild remains in Directory despite leaving the root's process group.
type commandCgroup struct {
	Directory *os.File
	Events    *os.File
	Kill      *os.File
	Path      string
	Relative  string
}

// acquireCommandCgroup moves this supervisor into a fresh manager-owned delegated scope.
// In-process registration preserves signals, environment, cwd, descriptors and PID.
//
// Example: a direct-built executable acquires the same ownership as the cache wrapper.
func acquireCommandCgroup(ctx context.Context) (*commandCgroup, error) {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		address = "unix:path=" + dbus.EscapeBusAddressValue(fmt.Sprintf("/run/user/%d/bus", os.Getuid()))
	}
	connection, err := systemddbus.NewConnection(
		// Connect uses an explicit address so bus discovery cannot mutate environment or launch helpers.
		//
		// Example: an omitted DBUS_SESSION_BUS_ADDRESS still leaves the command's environment untouched.
		func() (*dbus.Conn, error) { return dbus.Connect(address, dbus.WithContext(ctx)) },
	)
	if err != nil {
		return nil, fmt.Errorf("connect to systemd user manager for cgroup delegation: %w", err)
	}
	defer connection.Close()
	unit := fmt.Sprintf("owned-command-%d-%d.scope", os.Getpid(), time.Now().UnixNano())
	completed := make(chan string, 1)
	properties := []systemddbus.Property{
		systemddbus.PropPids(uint32(os.Getpid())),
		{Name: "Delegate", Value: dbus.MakeVariant(true)},
		{Name: "CollectMode", Value: dbus.MakeVariant("inactive-or-failed")},
	}
	if _, err := connection.StartTransientUnitContext(ctx, unit, "fail", properties, completed); err != nil {
		return nil, fmt.Errorf("create delegated scope %s: %w", unit, err)
	}
	select {
	case result := <-completed:
		if result != "done" {
			return nil, fmt.Errorf("delegated scope %s start result: %s", unit, result)
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for delegated scope %s: %w", unit, ctx.Err())
	}
	values, err := connection.GetAllPropertiesContext(ctx, unit)
	if err != nil {
		return nil, fmt.Errorf("inspect delegated scope %s: %w", unit, err)
	}
	delegated, ok := values["Delegate"].(bool)
	if !ok || !delegated {
		return nil, fmt.Errorf("scope %s did not acquire cgroup delegation", unit)
	}
	parent, ok := values["ControlGroup"].(string)
	if !ok || parent == "" || !strings.HasPrefix(parent, "/") || filepath.Clean(parent) != parent {
		return nil, fmt.Errorf("scope %s returned invalid ControlGroup", unit)
	}
	membership, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, fmt.Errorf("verify delegated scope %s membership: %w", unit, err)
	}
	if strings.TrimSpace(string(membership)) != "0::"+parent {
		return nil, fmt.Errorf("supervisor did not enter delegated scope %s", unit)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("delegated scope %s setup canceled: %w", unit, err)
	}
	relative := parent + "/command"
	path := filepath.Join("/sys/fs/cgroup", relative)
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, fmt.Errorf("create exclusive command cgroup %s: %w", path, err)
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open command cgroup: %w", err), os.Remove(path))
	}
	events, err := os.Open(filepath.Join(path, "cgroup.events"))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open command cgroup events: %w", err), directory.Close(), os.Remove(path))
	}
	kill, err := os.OpenFile(filepath.Join(path, "cgroup.kill"), os.O_WRONLY, 0)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open command cgroup kill control: %w", err), events.Close(), directory.Close(), os.Remove(path))
	}
	return &commandCgroup{Directory: directory, Events: events, Kill: kill, Path: path, Relative: relative}, nil
}

// Populated reads the kernel's subtree population independently of native zombie reaping.
//
// Example: populated zero still requires the sole reaper to report actual ECHILD.
func (c *commandCgroup) Populated() (bool, error) {
	data := make([]byte, 4096)
	count, err := c.Events.ReadAt(data, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read command cgroup events: %w", err)
	}
	return parseCgroupPopulation(data[:count])
}

// cgroupWatch converts kernel population notifications into coalesced driver wakeups.
// Its private eventfd permits shutdown before any descriptor is closed.
//
// Example: subtree emptiness wakes the driver without a periodic observation timer.
type cgroupWatch struct {
	Group   *commandCgroup
	StopFD  int
	RootFD  int
	Updates chan error
	Done    chan struct{}
	Error   error
}

// newCgroupWatch allocates one interruptible watcher before command creation.
//
// Example: eventfd exhaustion rejects setup while there is no command to retire.
func newCgroupWatch(
	group *commandCgroup,
	rootFD int,
) (*cgroupWatch, error) {
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("create cgroup watcher stop event: %w", err)
	}
	return &cgroupWatch{Group: group, StopFD: fd, RootFD: rootFD, Updates: make(chan error, 1), Done: make(chan struct{})}, nil
}

// Run blocks on population changes or its private stop event and owns no native waits.
//
// Example: rapid changes coalesce because the driver rereads current state.
func (w *cgroupWatch) Run(ctx context.Context) {
	defer close(w.Done)
	rootFD := w.RootFD
	for {
		fds := []unix.PollFd{{Fd: int32(w.Group.Events.Fd()), Events: unix.POLLPRI | unix.POLLERR}, {Fd: int32(w.StopFD), Events: unix.POLLIN}, {Fd: int32(rootFD), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err == nil && fds[1].Revents&unix.POLLIN != 0 {
			return
		}
		if err == nil && fds[0].Revents & ^int16(unix.POLLPRI|unix.POLLERR) != 0 {
			err = fmt.Errorf("unexpected cgroup event readiness %#x", fds[0].Revents)
		}
		if err == nil && fds[2].Revents != 0 {
			_, err = classifyReadiness(fds[2].Revents)
			rootFD = -1
		}
		if err == nil && fds[0].Revents != 0 {
			_, err = w.Group.Populated()
		}
		// The joined result preserves terminal failure even when a wake is already queued.
		w.Error = err
		select {
		case w.Updates <- err:
		default:
		}
		if err != nil {
			return
		}
	}
}

// Stop wakes and joins the watcher before releasing its private descriptor.
//
// Example: group descriptor closure cannot race a watcher polling a reused fd number.
func (w *cgroupWatch) Stop() error {
	var value [8]byte
	binary.NativeEndian.PutUint64(value[:], 1)
	count, err := unix.Write(w.StopFD, value[:])
	for errors.Is(err, unix.EINTR) {
		count, err = unix.Write(w.StopFD, value[:])
	}
	if err != nil {
		return fmt.Errorf("stop command cgroup watcher: %w", err)
	}
	if count != len(value) {
		return fmt.Errorf("short command cgroup watcher stop write: %d", count)
	}
	<-w.Done
	return errors.Join(w.Error, unix.Close(w.StopFD))
}

// parseCgroupPopulation requires one explicit kernel population observation.
//
// Example: a missing or duplicated populated field cannot establish an empty subtree.
func parseCgroupPopulation(data []byte) (bool, error) {
	found, populated := false, false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "populated" {
			continue
		}
		if found || len(fields) != 2 || (fields[1] != "0" && fields[1] != "1") {
			return false, fmt.Errorf("invalid cgroup populated field %q", line)
		}
		found, populated = true, fields[1] == "1"
	}
	if !found {
		return false, errors.New("cgroup events has no populated field")
	}
	return populated, nil
}

// signalCgroupMember binds an observed member before a graceful lifetime-targeted TERM.
// Fresh anchored membership supplies ownership instead of numeric ancestry or process groups.
//
// Example: a reused member PID that now belongs to another cgroup receives no signal.
func (g *guard) signalCgroupMember(
	c *commandCgroup,
	pid int,
) {
	kernel := linuxKernel{}
	directory, err := kernel.OpenDirectory(pid)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ESRCH) {
		return
	}
	if err != nil {
		g.fail("open command member proc directory", fmt.Errorf("PID %d: %w", pid, err))
		return
	}
	defer g.closeSlot(&directory, "close command member directory")
	before := kernel.ReadStat(directory, pid)
	g.metadataErrors(before)
	if errors.Is(before.InspectionError, unix.ENOENT) || errors.Is(before.InspectionError, unix.ESRCH) {
		return
	}
	if before.InspectionError != nil {
		g.fail("read command member before pidfd", before.InspectionError)
		return
	}
	fd, err := kernel.OpenPIDFD(pid)
	if errors.Is(err, unix.ESRCH) {
		return
	}
	if err != nil {
		g.fail("open command member pidfd", fmt.Errorf("PID %d: %w", pid, err))
		return
	}
	defer g.closeSlot(&fd, "close command member pidfd")
	after := kernel.ReadStat(directory, pid)
	g.metadataErrors(after)
	if errors.Is(after.InspectionError, unix.ENOENT) || errors.Is(after.InspectionError, unix.ESRCH) {
		return
	}
	if after.InspectionError != nil {
		g.fail("read command member after pidfd", after.InspectionError)
		return
	}
	if before.Stat.ProcessIdentity != after.Stat.ProcessIdentity {
		g.fail("command member lifetime changed", fmt.Errorf("PID %d changed in anchored acquisition", pid))
		return
	}
	membershipFD, err := unix.Openat(directory, "cgroup", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ESRCH) {
		return
	}
	if err != nil {
		g.fail("open anchored command membership", fmt.Errorf("PID %d: %w", pid, err))
		return
	}
	membershipFile := os.NewFile(uintptr(membershipFD), "anchored-command-membership")
	data, readErr := io.ReadAll(membershipFile)
	closeErr := membershipFile.Close()
	if closeErr != nil {
		g.fail("close anchored command membership", closeErr)
	}
	if errors.Is(readErr, unix.ENOENT) || errors.Is(readErr, unix.ESRCH) {
		return
	}
	if readErr != nil {
		g.fail("read anchored command membership", readErr)
		return
	}
	membership := strings.TrimSpace(string(data))
	if membership != "0::"+c.Relative && !strings.HasPrefix(membership, "0::"+c.Relative+"/") {
		return
	}
	err = kernel.Send(fd, syscall.SIGTERM)
	if errors.Is(err, unix.ESRCH) {
		return
	}
	g.record(after.Stat.ProcessIdentity, "terminated", err)
}

// commandDrain distinguishes actual ECHILD from live children and a bounded batch yield.
//
// Example: a full batch is drained again after servicing the shared retirement clock.
type commandDrain int

// Native drain results retain incomplete work without turning it into settlement.
//
// Example: a zero-PID wait remains commandChildrenRunning.
const (
	commandChildrenRunning commandDrain = iota
	commandDrainYield
	commandChildrenAbsent
)

// drainCgroupCommand is the launch lifecycle's sole bounded, nonblocking native reaper.
//
// Example: SIGCHLD wakes this drain; no goroutine remains blocked in Wait4 at expiry.
func (g *guard) drainCgroupCommand(rootPID int) commandDrain {
	for count := 0; count < drainBudget; count++ {
		g.consumeNotices()
		result := g.kernel.Wait()
		pid, status, err := result.PID, result.Status, result.Error
		switch {
		case errors.Is(err, unix.ECHILD):
			return commandChildrenAbsent
		case errors.Is(err, unix.EINTR):
			return commandDrainYield
		case err != nil:
			g.fail("native command wait", err)
			return commandChildrenRunning
		case pid == 0:
			return commandChildrenRunning
		case pid < 0:
			g.fail("native command wait", errors.New("negative PID without native error"))
			return commandChildrenRunning
		}
		if g.rootUnreaped && pid == rootPID && (status.Exited() || status.Signaled()) {
			g.rootDone, g.rootUnreaped = true, false
			g.receipt.CommandExit = status.ExitStatus()
			if status.Signaled() {
				g.receipt.CommandExit = 128 + int(status.Signal())
			}
			g.beginRetirement("command exited")
		}
		if g.boundary() {
			return commandDrainYield
		}
	}
	return commandDrainYield
}

// runCgroupCommand uses inherited kernel membership and native exit events for launch mode.
// Setup failures happen before command creation and enter the ordinary 125 receipt path.
//
// Example: an idle command blocks on native exit, a notice or the command deadline.
func (g *guard) runCgroupCommand(args []string) {
	commandEnvironment := os.Environ()
	g.receipt.Backend = "cgroup-v2"
	g.receipt.FamilyCoverage = "inherited command cgroup plus native child reaping"
	setupNotices, stopSetupNotices := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stopSetupNotices()
	ctx, cancel := context.WithTimeout(setupNotices, cgroupSetupTimeout)
	defer cancel()
	if !g.prepareKernel() {
		return
	}
	if err := ctx.Err(); err != nil {
		g.fail("native setup canceled", err)
		return
	}
	runtimeBudget := g.expires.Sub(g.started)
	group, err := acquireCommandCgroup(ctx)
	g.consumeNotices()
	if setupNotices.Err() != nil {
		g.interrupted = true
		g.beginRetirement("signal: setup interrupted")
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("setup budget %s expired: %w", cgroupSetupTimeout, errors.Join(err, ctx.Err()))
		}
		g.fail("acquire command cgroup", err)
		return
	}
	g.receipt.Cgroup = group.Relative
	// Group removal follows all command retirement and native settlement work.
	//
	// Example: failed removal remains a guard failure instead of hiding residual ownership.
	defer func() {
		if err := group.Close(); err != nil {
			g.fail("close command cgroup", err)
		}
	}()
	watch, err := newCgroupWatch(group, -1)
	if err != nil {
		g.fail("prepare command cgroup watcher", err)
		return
	}
	fd := -1
	defer g.closeSlot(&fd, "close root pidfd")
	watchStarted := false
	// Join before group descriptors are released by the earlier defer.
	//
	// Example: prelaunch interruption still consumes the watcher's stop descriptor.
	defer func() {
		var err error
		if watchStarted {
			err = watch.Stop()
		} else {
			err = unix.Close(watch.StopFD)
		}
		if err != nil {
			g.fail("close command cgroup watcher", err)
		}
	}()
	g.consumeNotices()
	if g.interrupted {
		return
	}
	if err := ctx.Err(); err != nil {
		g.fail("command cgroup setup canceled", err)
		return
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		g.fail("start failed", err)
		return
	}
	childNotices := make(chan os.Signal, 1)
	signal.Notify(childNotices, syscall.SIGCHLD)
	defer signal.Stop(childNotices)
	g.consumeNotices()
	if g.interrupted {
		return
	}
	if err := ctx.Err(); err != nil {
		g.fail("command launch setup canceled", err)
		return
	}
	pid, err := syscall.ForkExec(path, args, &syscall.ProcAttr{Env: commandEnvironment, Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()}, Sys: &syscall.SysProcAttr{PidFD: &fd, UseCgroupFD: true, CgroupFD: int(group.Directory.Fd())}})
	if err != nil {
		g.fail("start failed in command cgroup", err)
		return
	}
	cancel()
	stopSetupNotices()
	commandStarted := g.clock.Now()
	g.expires = commandStarted.Add(runtimeBudget)
	g.receipt.CommandPID = pid
	g.receipt.CommandStarted = commandStarted.UTC()
	g.rootUnreaped = true
	root := ProcessIdentity{PID: pid}
	stat, metadataErr := readProcess(pid)
	switch {
	case metadataErr == nil:
		root = stat.ProcessIdentity
	case errors.Is(metadataErr, unix.ENOENT), errors.Is(metadataErr, unix.ESRCH):
	default:
		g.fail("root metadata unavailable", metadataErr)
	}
	g.record(root, "started", nil)
	if fd < 0 {
		g.fail("root pidfd unavailable", errors.New("successful cgroup launch returned no pidfd"))
	}
	watch.RootFD, watchStarted = fd, true
	observability.Go(context.Background(), watch.Run)
	timer := time.NewTimer(max(time.Until(g.expires), 0))
	defer timer.Stop()
	terminated, killed, reaped := false, false, false
	for {
		g.boundary()
		if !g.retirement.IsZero() && !terminated {
			term := cgroupTERM{Guard: g, Group: group}
			_, err := group.visitMembers(term, term)
			if err != nil {
				g.fail("discover command cgroup for TERM", err)
			}
			terminated = true
			g.appliedStage = retirementTERM
			timer.Reset(max(time.Until(g.retirement.Add(g.grace)), 0))
		}
		if g.stage() >= retirementKILL && !killed {
			g.killCommandCgroup(group)
			killed = true
			g.appliedStage = retirementKILL
			timer.Reset(max(time.Until(g.retirement.Add(2*g.grace)), 0))
		}
		drained := g.drainCgroupCommand(pid)
		reaped = drained == commandChildrenAbsent
		if reaped && g.rootDone {
			populated, err := group.Populated()
			if err != nil {
				g.fail("verify command cgroup settlement", err)
			}
			if err == nil && !populated {
				g.receipt.HostChildrenSettled = true
				return
			}
		}
		if g.stage() >= retirementFinal {
			members, err := group.inspectMembers(cgroupTraversalBudget)
			if err != nil {
				g.fail("inspect unsettled command members", err)
			}
			for _, member := range members {
				g.receipt.Survivors = append(g.receipt.Survivors, ProcessIdentity{PID: member})
			}
			if !g.rootDone && len(members) == 0 {
				g.receipt.Survivors = append(g.receipt.Survivors, root)
			}
			g.fail("host children unsettled", fmt.Errorf("cgroup retirement incomplete: root=%t native-ECHILD=%t", g.rootDone, reaped))
			return
		}
		if drained == commandDrainYield {
			continue
		}
		select {
		case <-childNotices:
		case err := <-watch.Updates:
			if err != nil {
				g.fail("watch command cgroup population", err)
			}
		case notice := <-g.notices:
			if notice != nil {
				g.interrupted = true
				g.beginRetirement("signal: " + notice.String())
			}
		case <-timer.C:
		}
	}
}

// killCommandCgroup applies and records the lifecycle owner's private subtree KILL.
//
// Example: ordinary TERM cutoff returns directly to this already-due bulk operation.
func (g *guard) killCommandCgroup(group *commandCgroup) {
	count, err := group.Kill.Write([]byte("1"))
	if err == nil && count != 1 {
		err = fmt.Errorf("short command cgroup kill write: %d", count)
	}
	g.record(ProcessIdentity{}, "cgroup killed", err)
}

// cgroupTraversalResult distinguishes a complete traversal from ordinary lifecycle cutoff.
//
// Example: KILL due stops TERM discovery without producing an inspection error.
type cgroupTraversalResult int

const (
	// cgroupTraversalComplete means every available entry was inspected.
	//
	// Example: no cutoff was requested by the lifecycle or work allowance.
	cgroupTraversalComplete cgroupTraversalResult = iota
	// cgroupTraversalCutoff means the traversal yielded before further discovery.
	//
	// Example: due KILL now takes priority over remaining TERM candidates.
	cgroupTraversalCutoff
)

// visitMembers visits weak PID hints only while steps permits another bounded step.
// The allowance is checked before I/O, entries, parsing and synchronous candidates.
//
// Example: a TERM visitor stops discovering immediately when KILL becomes due.
func (c *commandCgroup) visitMembers(
	steps cgroupTraversalSteps,
	visitor cgroupMemberVisitor,
) (cgroupTraversalResult, error) {
	scan := cgroupMemberScan{Seen: make(map[int]bool), Visitor: visitor}
	result, err := c.walkGroups(c.Path, steps, &scan, false)
	if err != nil {
		return result, fmt.Errorf("read command cgroup subtree members: %w", err)
	}
	return result, nil
}

// cgroupTraversalSteps grants another bounded private-subtree operation.
//
// Example: TERM uses the lifecycle clock; final inspection uses a finite allowance.
type cgroupTraversalSteps interface {
	// take permits the next operation and accounts for its work.
	//
	// Example: false yields before opening another subgroup.
	take() bool
}

// cgroupMemberVisitor consumes distinct weak member hints synchronously.
//
// Example: TERM revalidates lifetime authority; final inspection records survivor hints.
type cgroupMemberVisitor interface {
	// visit consumes one weak PID hint after the traversal boundary check.
	//
	// Example: an existing pidfd/stat authority check precedes TERM.
	visit(int)
}

// cgroupMemberScan retains deduplication only for the current bounded traversal.
//
// Example: one member appearing in multiple groups is visited once.
type cgroupMemberScan struct {
	Seen    map[int]bool
	Visitor cgroupMemberVisitor
}

// cgroupTERM checks the shared retirement clock and uses existing anchored signal authority.
//
// Example: it yields immediately when the lifecycle owner must apply bulk KILL.
type cgroupTERM struct {
	Guard *guard
	Group *commandCgroup
}

// take checks notices and the lifecycle before another TERM discovery step.
//
// Example: a grace boundary crossing stops even duplicate membership parsing.
func (t cgroupTERM) take() bool {
	t.Guard.consumeNotices()
	return t.Guard.stage() < retirementKILL
}

// visit applies TERM only through the retained anchored member authority.
//
// Example: numeric membership alone never authorizes a signal.
func (t cgroupTERM) visit(pid int) { t.Guard.signalCgroupMember(t.Group, pid) }

// cgroupSurvivors collects finite final inspection hints, never signal authority.
//
// Example: an exhausted inspection returns its partial list with an explicit error.
type cgroupSurvivors []int

// visit records a distinct member hint after the finite work check.
//
// Example: duplicate membership does not grow the survivor list.
func (s *cgroupSurvivors) visit(pid int) { *s = append(*s, pid) }

// readGroup streams membership while closing its descriptor on every result.
//
// Example: benign descendant disappearance cannot hide a separate close error.
func (c *commandCgroup) readGroup(
	path string,
	steps cgroupTraversalSteps,
	scan *cgroupMemberScan,
) (cgroupTraversalResult, error) {
	if !steps.take() {
		return cgroupTraversalCutoff, nil
	}
	file, err := os.Open(filepath.Join(path, "cgroup.procs"))
	if errors.Is(err, unix.ENOENT) && path != c.Path {
		return cgroupTraversalComplete, nil
	}
	if err != nil {
		return cgroupTraversalComplete, err
	}
	result, readErr := readCgroupMembers(file, steps, scan)
	if errors.Is(readErr, unix.ENOENT) && path != c.Path {
		readErr = nil
	}
	return result, errors.Join(readErr, file.Close())
}

// cgroupTraversalBudget bounds final inspection and removal application work.
// Each directory, entry, read chunk, parsing byte, candidate and removal consumes a step.
//
// Example: duplicate-heavy membership cannot bypass the final reporting budget.
const (
	cgroupTraversalBudget = 65536
	// cgroupDirectoryBatch limits entries materialized by a single directory read.
	//
	// Example: a directory with many control files still yields between small batches.
	cgroupDirectoryBatch = 32
	// cgroupMemberChunk limits bytes materialized by a single membership read.
	//
	// Example: a large worker family never requires reading the whole membership file.
	cgroupMemberChunk = 4096
	// cgroupMemberDigits bounds a decimal PID token before numeric conversion.
	//
	// Example: malformed long tokens cannot cause unbounded token accumulation.
	cgroupMemberDigits = 20
)

// cgroupWork tracks a finite traversal allowance, independent of wall-clock syscall latency.
//
// Example: final reporting and cleanup each receive their own finite allowance.
type cgroupWork struct{ remaining int }

// take permits one more application-controlled step while work remains.
//
// Example: a zero allowance stops before opening another directory.
func (w *cgroupWork) take() bool {
	if w.remaining <= 0 {
		return false
	}
	w.remaining--
	return true
}

// walkGroups streams bounded directory batches, optionally removing groups bottom-up.
// steps guards every operation/entry; scan reads one group's members synchronously.
// Every opened directory closes on completion, cutoff and failure.
//
// Example: a disappearing descendant is benign, but a missing ownership root is an error.
func (c *commandCgroup) walkGroups(
	path string,
	steps cgroupTraversalSteps,
	scan *cgroupMemberScan,
	remove bool,
) (result cgroupTraversalResult, resultErr error) {
	if !steps.take() {
		return cgroupTraversalCutoff, nil
	}
	directory, err := os.Open(path)
	if errors.Is(err, unix.ENOENT) && path != c.Path {
		return cgroupTraversalComplete, nil
	}
	if err != nil {
		return cgroupTraversalComplete, err
	}
	defer
	// Dispose closes this directory even when a descendant fails or yields.
	//
	// Example: KILL cutoff unwinds all opened ancestor handles.
	func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	if scan != nil {
		result, err = c.readGroup(path, steps, scan)
		if err != nil || result == cgroupTraversalCutoff {
			return result, err
		}
	}
	for {
		if !steps.take() {
			return cgroupTraversalCutoff, nil
		}
		entries, readErr := directory.ReadDir(cgroupDirectoryBatch)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			if errors.Is(readErr, unix.ENOENT) && path != c.Path {
				return cgroupTraversalComplete, nil
			}
			return cgroupTraversalComplete, readErr
		}
		for _, entry := range entries {
			if !steps.take() {
				return cgroupTraversalCutoff, nil
			}
			if !entry.IsDir() {
				continue
			}
			result, err = c.walkGroups(filepath.Join(path, entry.Name()), steps, scan, remove)
			if err != nil || result == cgroupTraversalCutoff {
				return result, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if remove {
		if !steps.take() {
			return cgroupTraversalCutoff, nil
		}
		if err := os.Remove(path); err != nil && !(errors.Is(err, unix.ENOENT) && path != c.Path) {
			return cgroupTraversalComplete, fmt.Errorf("remove command cgroup %s: %w", path, err)
		}
	}
	return cgroupTraversalComplete, nil
}

// readCgroupMembers parses bounded chunks and tokens, checking even duplicate candidates.
// visit receives each distinct positive PID synchronously, as a weak hint only.
//
// Example: an invalid token beyond KILL cutoff is never inspected during TERM.
func readCgroupMembers(
	file *os.File,
	steps cgroupTraversalSteps,
	scan *cgroupMemberScan,
) (cgroupTraversalResult, error) {
	var chunk [cgroupMemberChunk]byte
	var token [cgroupMemberDigits]byte
	length := 0
	for {
		if !steps.take() {
			return cgroupTraversalCutoff, nil
		}
		count, readErr := file.Read(chunk[:])
		for index := 0; index <= count; index++ {
			if index == count && !errors.Is(readErr, io.EOF) {
				break
			}
			if !steps.take() {
				return cgroupTraversalCutoff, nil
			}
			value := byte('\n')
			if index < count {
				value = chunk[index]
			}
			if value >= '0' && value <= '9' {
				if length == len(token) {
					return cgroupTraversalComplete, errors.New("command cgroup member token too long")
				}
				token[length] = value
				length++
				continue
			}
			if value != '\n' && value != ' ' && value != '\t' && value != '\r' {
				return cgroupTraversalComplete, fmt.Errorf("invalid command cgroup member byte %q", value)
			}
			if length == 0 {
				continue
			}
			if !steps.take() {
				return cgroupTraversalCutoff, nil
			}
			pid, err := strconv.Atoi(string(token[:length]))
			length = 0
			if err != nil || pid <= 0 {
				return cgroupTraversalComplete, errors.New("invalid command cgroup member PID")
			}
			if !scan.Seen[pid] {
				scan.Seen[pid] = true
				scan.Visitor.visit(pid)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return cgroupTraversalComplete, nil
		}
		if readErr != nil {
			return cgroupTraversalComplete, readErr
		}
	}
}

// inspectMembers reports a finite, potentially partial survivor list with explicit incompleteness.
//
// Example: exhausted duplicate-heavy input cannot be mistaken for exhaustive inspection.
func (c *commandCgroup) inspectMembers(limit int) ([]int, error) {
	var members cgroupSurvivors
	work := cgroupWork{remaining: limit}
	result, err := c.visitMembers(&work, &members)
	if result == cgroupTraversalCutoff {
		err = errors.Join(err, errors.New("work budget exhausted"))
	}
	if err != nil {
		err = fmt.Errorf("command cgroup survivor inspection incomplete: %w", err)
	}
	return members, err
}

// Close closes owned descriptors and finitely removes only this command's empty subgroups.
//
// Example: cleanup exhaustion returns an error even if physical settlement was independently proven.
func (c *commandCgroup) Close() error { return c.closeWithBudget(cgroupTraversalBudget) }

// closeWithBudget applies the cleanup allowance while always disposing owned descriptors.
//
// Example: a cutoff leaves remaining empty groups for service-manager cleanup.
func (c *commandCgroup) closeWithBudget(limit int) error {
	work := cgroupWork{remaining: limit}
	result, err := c.walkGroups(c.Path, &work, nil, true)
	if result == cgroupTraversalCutoff {
		err = errors.Join(err, errors.New("command cgroup cleanup incomplete: work budget exhausted"))
	}
	return errors.Join(err, c.Kill.Close(), c.Events.Close(), c.Directory.Close())
}
