//go:build linux

// owned-command preserves command I/O and bounds retirement of proved host descendants.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Guard limits bound work between common-clock retirement checks.
//
// Example: continuing terminal children cannot occupy an unlimited drain.
const (
	guardFailure        = 125
	scanBudget          = 256
	drainBudget         = 64
	observationInterval = 10 * time.Millisecond
)

// ProcessIdentity is metadata; Birth zero means unavailable, never cancellation authority.
//
// Example: a pidfd binds a lifetime even when its birth metadata cannot be read.
type ProcessIdentity struct {
	PID   int
	Birth uint64
}

// Event is immutable history independent of current descriptor custody.
//
// Example: history retains a lifetime after its pidfd closes.
type Event struct {
	At             time.Time
	ElapsedSeconds float64
	PID            int
	Birth          uint64
	Action         string
	Error          string
}

// Receipt describes host custody and native completion, never phone cleanup.
//
// Example: settlement requires a final actual ECHILD drain.
type Receipt struct {
	Scope               string
	Backend             string
	FamilyCoverage      string
	Cgroup              string
	Started             time.Time
	CommandStarted      time.Time
	Finished            time.Time
	ElapsedSeconds      float64
	CommandPID          int
	CommandExit         int
	Cause               string
	HostChildrenSettled bool
	Survivors           []ProcessIdentity
	Events              []Event
	Errors              []string
}

// processStat is fresh metadata, not ancestry authority by itself.
//
// Example: Parent must pass a retained parent's post-read kernel-lifetime check.
type processStat struct {
	ProcessIdentity
	Parent int
	State  string
}

// metadataRead preserves valid data separately from inspection and cleanup failures.
//
// Example: stat close failure cannot erase a completed anchored read.
type metadataRead struct {
	Stat            processStat
	InspectionError error
	CloseError      error
}

// processState distinguishes kernel-confirmed exit from uncertainty.
//
// Example: lost proc visibility cannot establish processExited.
type processState int

// Process states prevent unknown observations from becoming success.
//
// Example: invalid poll readiness remains processUnknown.
const (
	processUnknown processState = iota
	processRunning
	processExited
)

// custody owns one bound pidfd; tentative slots have no signal authority.
//
// Example: admitted detached descendants remain Owned despite later reparenting.
type custody struct {
	ProcessIdentity
	FD         int
	Owned      bool
	State      processState
	Terminated bool
	Killed     bool
}

// pendingAcquisition owns an unresolved anchored directory without signal authority.
//
// Example: post-open A failure retains the same directory for retry.
type pendingAcquisition struct {
	PID         int
	DirectoryFD int
	Before      processStat
	Cancel      bool
}

// nativeResult describes one nonblocking native wait.
//
// Example: a terminal root result preserves its actual status once.
type nativeResult struct {
	PID    int
	Status syscall.WaitStatus
	Error  error
}

// processKernel supplies coherent Linux custody primitives and synthetic control seams.
//
// Example: foreign cancellation shares anchored custody primitives with cgroup retirement.
type processKernel interface {
	// Childless checks existing native children without consuming status.
	//
	// Example: only ECHILD admits construction.
	Childless() error
	// Namespace verifies numeric procfs/PID namespace coherence.
	//
	// Example: exactly one matching NStgid admits the supported profile.
	Namespace() error
	// Subreaper enables adoption after the childless proof.
	//
	// Example: detached descendants become native guard children.
	Subreaper() error
	// Entries supplies numeric discovery hints.
	//
	// Example: foreign cancellation takes one finite static-family snapshot.
	Entries() ([]int, error)
	// Hint supplies numeric-path metadata solely for prefiltering.
	//
	// Example: cached Parent never authorizes admission.
	Hint(pid int) (processStat, error)
	// OpenDirectory anchors one proc lifetime.
	//
	// Example: unresolved acquisition retains this exact reference.
	OpenDirectory(pid int) (int, error)
	// ReadStat consumes a newly opened anchored stat descriptor.
	//
	// Example: A is generated after pidfd_open returns.
	ReadStat(
		directoryFD int,
		pid int,
	) metadataRead
	// OpenPIDFD opens a numeric descriptor, unbound until anchored A succeeds.
	//
	// Example: native ESRCH is prebinding absence evidence.
	OpenPIDFD(pid int) (int, error)
	// Poll checks retained lifetime readiness without native reaping.
	//
	// Example: timeout zero distinguishes running, exited and unknown.
	Poll(fd int) (processState, error)
	// Send targets only a proved retained pidfd.
	//
	// Example: numeric PID reuse cannot redirect TERM.
	Send(
		fd int,
		sig syscall.Signal,
	) error
	// Wait performs one nonblocking consuming native wait including clone children.
	//
	// Example: only ECHILD completes a drain.
	Wait() nativeResult
	// Close invalidates one owned descriptor with no retry.
	//
	// Example: a failed Linux close never targets a reused fd number.
	Close(fd int) error
}

// guardClock is the shared monotonic schedule for every loop boundary.
//
// Example: synthetic controls advance time between native wait results.
type guardClock interface {
	// Now reads the common clock.
	//
	// Example: all grace stages derive from one retirement instant.
	Now() time.Time
	// Pause yields between opaque process observations.
	//
	// Example: native proc changes are polled at a bounded interval.
	Pause(duration time.Duration)
}

// realClock uses Go monotonic time for cleanup intervals.
//
// Example: wall-clock adjustment cannot restart grace.
type realClock struct{}

// Now reads the native clock.
//
// Example: retirement stores this value once.
func (realClock) Now() time.Time { return time.Now() }

// Pause yields before another opaque native observation.
//
// Example: normal observations request a ten-millisecond pause.
func (realClock) Pause(duration time.Duration) { time.Sleep(duration) }

// linuxKernel implements custody with typed Linux APIs.
//
// Example: the standalone binary requests a clone-bound root pidfd.
type linuxKernel struct{}

// Childless accepts only nonconsuming ECHILD; success may mean an unwaitable live child.
//
// Example: preexisting children are rejected without reaping, adoption or signaling.
func (linuxKernel) Childless() error {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_ALL, 0, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT|unix.WALL, nil)
	if errors.Is(err, unix.ECHILD) {
		return nil
	}
	if err == nil {
		return errors.New("preexisting native children prevent construction")
	}
	return fmt.Errorf("nonconsuming child check: %w", err)
}

// validateNamespace rejects missing, duplicate, nested or mismatched NStgid fields.
//
// Example: a single positive PID equal to the guard PID proves the supported view.
func validateNamespace(
	data []byte,
	pid int,
) error {
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "NStgid:") {
			continue
		}
		count++
		fields := strings.Fields(strings.TrimPrefix(line, "NStgid:"))
		if len(fields) != 1 {
			return errors.New("NStgid must identify one PID namespace")
		}
		value, err := strconv.Atoi(fields[0])
		if err != nil || value <= 0 || value != pid {
			return fmt.Errorf("NStgid %q does not match PID %d", fields[0], pid)
		}
	}
	if count != 1 {
		return fmt.Errorf("expected one NStgid field, got %d", count)
	}
	return nil
}

// Namespace checks the actual procfs numeric view before target actions.
//
// Example: an unverified mounted parent namespace is rejected.
func (linuxKernel) Namespace() error {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("namespace status: %w", err)
	}
	return validateNamespace(data, os.Getpid())
}

// Subreaper enables adoption after proving no preexisting native children.
//
// Example: a setsid descendant is adopted after its parent exits.
func (linuxKernel) Subreaper() error { return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }

// Entries snapshots numeric proc hints without establishing ownership.
//
// Example: cancellation preparation indexes each snapshot entry once.
func (linuxKernel) Entries() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// parseProcess validates numeric hint metadata, allowing zero birth for unassociated boot tasks.
//
// Example: malformed metadata remains inspection uncertainty.
func parseProcess(
	data []byte,
	pid int,
) (processStat, error) {
	text := string(data)
	start, end := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if start < 1 || end <= start || text[start-1] != ' ' {
		return processStat{}, fmt.Errorf("malformed stat for PID %d", pid)
	}
	value, err := strconv.Atoi(strings.TrimSpace(text[:start]))
	if err != nil || value != pid || value <= 0 {
		return processStat{}, fmt.Errorf("stat PID prefix mismatch for %d", pid)
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 20 || len(fields[0]) != 1 || !strings.Contains("RSDZTWtXxKPI", fields[0]) {
		return processStat{}, fmt.Errorf("invalid stat fields for PID %d", pid)
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil || parent < 0 {
		return processStat{}, fmt.Errorf("invalid parent for PID %d", pid)
	}
	birth, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return processStat{}, fmt.Errorf("invalid birth for PID %d", pid)
	}
	return processStat{ProcessIdentity: ProcessIdentity{PID: pid, Birth: birth}, Parent: parent, State: fields[0]}, nil
}

// errNumericStatClose marks cleanup uncertainty ahead of ignorable numeric visibility errors.
//
// Example: ENOENT joined with close EIO must still produce guard failure.
var errNumericStatClose = errors.New("numeric stat close failure")

// readProcess is numeric-path metadata convenience, never binding authority.
//
// Example: native controls obtain a controlled helper's cancellation birth.
func readProcess(pid int) (processStat, error) {
	return readNumericProcess(fmt.Sprintf("/proc/%d/stat", pid), pid)
}

// readNumericProcess consumes one complete proc-stat path with checked cleanup.
//
// Example: group and leader-task hints share EINTR and exactly-once close semantics.
func readNumericProcess(
	path string,
	pid int,
) (processStat, error) {
	// Blocking NewFile avoids procfs netpoll setup; ReadAll avoids a preliminary fstat.
	// Retry only EINTR, preserving os.Open semantics without a driver timer or cutoff.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	for errors.Is(err, unix.EINTR) {
		fd, err = unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return processStat{}, fmt.Errorf("open numeric stat PID %d: %w", pid, err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		wrapErr := fmt.Errorf("wrap numeric stat PID %d: invalid descriptor", pid)
		if err := unix.Close(fd); err != nil {
			return processStat{}, errors.Join(wrapErr, errNumericStatClose, fmt.Errorf("close numeric stat PID %d: %w", pid, err))
		}
		return processStat{}, wrapErr
	}
	// os.File.Read retains its EINTR handling. Close owns the descriptor exactly once.
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if closeErr != nil {
		closeErr = errors.Join(errNumericStatClose, fmt.Errorf("close numeric stat PID %d: %w", pid, closeErr))
	}
	if readErr != nil {
		return processStat{}, errors.Join(fmt.Errorf("read numeric stat PID %d: %w", pid, readErr), closeErr)
	}
	stat, parseErr := parseProcess(data, pid)
	return stat, errors.Join(parseErr, closeErr)
}

// Hint supplies a fresh numeric-path acquisition prefilter.
// The leader task view avoids whole-group accounting work for fields this
// prefilter does not use. It retains PID, birth, Parent and state observations;
// anchored acquisition supplies authority. Group lifetime follows retained
// pidfd custody or native terminal root status, not the leader's metadata state.
// In particular, a zombie leader does not prove worker exit.
//
// Example: its Parent is replaced with post-open anchored A before admission.
func (linuxKernel) Hint(pid int) (processStat, error) {
	stat, err := readNumericProcess(fmt.Sprintf("/proc/%d/task/%d/stat", pid, pid), pid)
	switch {
	case err == nil, errors.Is(err, errNumericStatClose):
		return stat, err
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ESRCH), errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		// The reader joins inspection causes only with marked cleanup failures above.
		// This single known inspection miss permits one original-path attempt whose
		// error chain alone must reach callers; task ENOENT must not hide fallback EIO.
		return readProcess(pid)
	default:
		return stat, err
	}
}

// OpenDirectory pins a proc reference with close-on-exec.
//
// Example: later numeric PID reuse cannot replace this anchored task.
func (linuxKernel) OpenDirectory(pid int) (int, error) {
	return unix.Open(fmt.Sprintf("/proc/%d", pid), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
}

// ReadStat consumes a fresh openat descriptor and separates successful data from close error.
//
// Example: post-open A never reuses B's buffered seq output.
func (linuxKernel) ReadStat(
	directoryFD int,
	pid int,
) metadataRead {
	fd, err := unix.Openat(directoryFD, "stat", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return metadataRead{InspectionError: err}
	}
	file := os.NewFile(uintptr(fd), "anchored-proc-stat")
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return metadataRead{InspectionError: readErr, CloseError: closeErr}
	}
	stat, parseErr := parseProcess(data, pid)
	if parseErr == nil && stat.Birth == 0 {
		parseErr = fmt.Errorf("anchored PID %d has no positive birth", pid)
	}
	return metadataRead{Stat: stat, InspectionError: parseErr, CloseError: closeErr}
}

// OpenPIDFD opens a numeric descriptor which requires post-open anchored binding.
//
// Example: native ESRCH can resolve prebinding absence.
func (linuxKernel) OpenPIDFD(pid int) (int, error) { return unix.PidfdOpen(pid, 0) }

// classifyReadiness rejects unsupported bits even together with exit readiness.
//
// Example: POLLIN|POLLERR remains unknown.
func classifyReadiness(bits int16) (processState, error) {
	if bits & ^int16(unix.POLLIN|unix.POLLHUP) != 0 {
		return processUnknown, fmt.Errorf("invalid pidfd readiness %#x", bits)
	}
	if bits&int16(unix.POLLIN|unix.POLLHUP) != 0 {
		return processExited, nil
	}
	return processRunning, nil
}

// Poll checks errors before readiness using a zero timeout.
//
// Example: readiness never consumes native child status.
func (linuxKernel) Poll(fd int) (processState, error) {
	entries := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	_, err := unix.Poll(entries, 0)
	if err != nil {
		return processUnknown, err
	}
	return classifyReadiness(entries[0].Revents)
}

// Send addresses the retained kernel lifetime.
//
// Example: TERM is never sent to a numeric ancestry hint.
func (linuxKernel) Send(
	fd int,
	sig syscall.Signal,
) error {
	return unix.PidfdSendSignal(fd, unix.Signal(sig), nil, 0)
}

// Wait performs one nonblocking native reap including clone children.
//
// Example: pid zero means native children remain; only ECHILD completes the drain.
func (linuxKernel) Wait() nativeResult {
	var status syscall.WaitStatus
	pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG|unix.WALL, nil)
	return nativeResult{PID: pid, Status: status, Error: err}
}

// Close releases one descriptor without retrying Linux close errors.
//
// Example: finalization invalidates the slot before reporting any failure.
func (linuxKernel) Close(fd int) error { return unix.Close(fd) }

// retirementStage identifies due work on the one common cleanup clock.
//
// Example: a late descendant receives the remaining schedule, not fresh grace.
type retirementStage int

// Retirement stages give finite TERM, KILL and final observation boundaries.
//
// Example: the final attempt is due after two grace intervals.
const (
	retirementNone retirementStage = iota
	retirementTERM
	retirementKILL
	retirementFinal
)

// preparationPhase separates bounded indexing from parent-first acquisition.
//
// Example: reversed proc entries cannot hide a grandchild behind an unbound parent.
type preparationPhase int

// Preparation phases count every entry, hint, expansion and candidate as bounded work.
//
// Example: membership construction yields after scanBudget entries.
const (
	preparationMembers preparationPhase = iota
	preparationHints
	preparationFamily
)

// familyPreparation retains one finite external-family snapshot while initial TERM is held.
// Hints order attempts; anchored acquisition remains the sole ancestry authority.
//
// Example: a child listed before its parent is attempted after that parent is proved.
type familyPreparation struct {
	Snapshot   bool
	Entries    []int
	At         int
	Phase      preparationPhase
	Members    map[int]bool
	Hinted     map[int]bool
	Children   map[int][]int
	Queue      []*custody
	QueueAt    int
	Parent     *custody
	ChildAt    int
	Attempted  map[int]bool
	Expanded   map[*custody]bool
	ErrorCount int
}

// guard owns all custody mutation, temporary resources and native reaping.
// owned indexes full identities and current indexes their occupied numeric slots;
// both refer to the same custody objects. Pointer checks on release preserve any
// replacement entry. Keeping both indexes makes identity inspection explicit;
// neither supplies signal authority independently of retained custody.
//
// Example: parent records remain unchanged throughout an acquisition and its parent poll.
type guard struct {
	kernel            processKernel
	clock             guardClock
	notices           <-chan os.Signal
	receipt           Receipt
	started           time.Time
	expires           time.Time
	grace             time.Duration
	retirement        time.Time
	appliedStage      retirementStage
	owned             map[ProcessIdentity]*custody
	current           map[int]*custody
	tentative         map[int]*custody
	pending           map[int]*pendingAcquisition
	root              *custody
	rootUnreaped      bool
	rootDone          bool
	cancel            ProcessIdentity
	launchMode        bool
	interrupted       bool
	custodyGeneration uint64
	preparation       *familyPreparation
	familyUncertain   bool
}

// newGuard initializes receipt and every custody slot before native setup.
//
// Example: external cancellation starts without guard-parent ancestry authority.
func newGuard(
	kernel processKernel,
	clock guardClock,
	notices <-chan os.Signal,
	deadline time.Duration,
	grace time.Duration,
	cancel ProcessIdentity,
) *guard {
	started := clock.Now()
	return &guard{kernel: kernel, clock: clock, notices: notices, started: started, expires: started.Add(deadline), grace: grace, cancel: cancel, launchMode: cancel.PID == 0,
		owned: make(map[ProcessIdentity]*custody), current: make(map[int]*custody), tentative: make(map[int]*custody), pending: make(map[int]*pendingAcquisition),
		receipt: Receipt{Scope: "HOST_COMMAND_CHILDREN_ONLY_NOT_PHONE_CLEANUP", Started: started.UTC(), CommandExit: -1, Survivors: []ProcessIdentity{}, Events: []Event{}, Errors: []string{}}}
}

// record appends immutable history and an associated historical error.
//
// Example: a retired descriptor does not erase its earlier inspection failures.
func (g *guard) record(
	identity ProcessIdentity,
	action string,
	err error,
) {
	now := g.clock.Now()
	value := Event{At: now.UTC(), ElapsedSeconds: now.Sub(g.started).Seconds(), PID: identity.PID, Birth: identity.Birth, Action: action}
	if err != nil {
		value.Error = err.Error()
		g.receipt.Errors = append(g.receipt.Errors, action+": "+err.Error())
	}
	g.receipt.Events = append(g.receipt.Events, value)
}

// fail records an error and starts retirement without abandoning published custody.
//
// Example: postpublication metadata errors enter the same driver.
func (g *guard) fail(
	action string,
	err error,
) {
	g.record(ProcessIdentity{}, action, err)
	g.beginRetirement("guard error")
}

// failAcquisition records attempted identity without granting lifetime authority.
//
// Example: a failed current anchored B records birth zero rather than a stale hint.
func (g *guard) failAcquisition(
	pid int,
	anchoredBeforeBirth uint64,
	action string,
	err error,
) {
	g.record(ProcessIdentity{PID: pid, Birth: anchoredBeforeBirth}, action, fmt.Errorf("attempted PID %d anchored-before birth %d: %w", pid, anchoredBeforeBirth, err))
	g.beginRetirement("guard error")
}

// beginRetirement preserves the first cause assigned from consumed events.
//
// Example: later observations cannot reset the grace clock.
func (g *guard) beginRetirement(cause string) {
	if !g.retirement.IsZero() {
		return
	}
	g.retirement = g.clock.Now()
	g.receipt.Cause = cause
}

// consumeNotices observes buffered signals without asserting kernel arrival ordering.
//
// Example: a notice consumed before native launch commitment prevents child creation.
func (g *guard) consumeNotices() {
	for {
		select {
		case notice := <-g.notices:
			if notice == nil {
				return
			}
			g.interrupted = true
			g.beginRetirement("signal: " + notice.String())
		default:
			return
		}
	}
}

// stage computes the due common-clock retirement stage.
//
// Example: KILL becomes due at the original retirement plus grace.
func (g *guard) stage() retirementStage {
	if g.retirement.IsZero() {
		return retirementNone
	}
	elapsed := g.clock.Now().Sub(g.retirement)
	switch {
	case elapsed >= 2*g.grace:
		return retirementFinal
	case elapsed >= g.grace:
		return retirementKILL
	default:
		return retirementTERM
	}
}

// boundary consumes notices and checks the shared clock between bounded operations.
//
// Example: due retirement yields an incomplete native drain to its owner loop.
func (g *guard) boundary() bool {
	g.consumeNotices()
	if g.retirement.IsZero() && !g.clock.Now().Before(g.expires) {
		g.beginRetirement("deadline")
	}
	if g.retirement.IsZero() && g.rootDone {
		g.beginRetirement("command exited")
	}
	return g.stage() > g.appliedStage
}

// closeSlot invalidates before one checked close, never retrying a Linux fd number.
//
// Example: final close failure overrides ordinary success.
func (g *guard) closeSlot(
	fd *int,
	action string,
) {
	if *fd < 0 {
		return
	}
	value := *fd
	*fd = -1
	if err := g.kernel.Close(value); err != nil {
		g.fail(action, err)
	}
}

// metadataErrors records cleanup separately from completed stat validity.
//
// Example: a valid A is still admitted after its stat close fails.
func (g *guard) metadataErrors(value metadataRead) {
	if value.CloseError != nil {
		g.fail("close stat", value.CloseError)
	}
}

// parents snapshots the already-bound owned slots before acquisition begins.
//
// Example: a later reused numeric parent cannot authorize an earlier A read.
func (g *guard) parents() map[int]*custody {
	result := make(map[int]*custody, len(g.current))
	for pid, value := range g.current {
		if value.Owned && value.FD >= 0 {
			result[pid] = value
		}
	}
	return result
}

// ancestry validates fresh A using a retained parent's post-A kernel lifetime.
//
// Example: exited or unknown parent readiness leaves a bound child tentative.
func (g *guard) ancestry(
	stat processStat,
	parents map[int]*custody,
) (bool, bool) {
	parent := parents[stat.Parent]
	if parent == nil {
		return false, false
	}
	state, err := g.kernel.Poll(parent.FD)
	if err != nil {
		if !errors.Is(err, unix.EINTR) {
			g.fail("poll retained parent", err)
		}
		return false, true
	}
	switch {
	case state == processRunning:
		return true, false
	case state == processExited:
		return false, true
	default:
		g.fail("poll retained parent", errors.New("unknown parent readiness"))
		return false, true
	}
}

// acquire binds a pidfd through fresh B and post-open A in one retained proc directory.
// Parent snapshot remains unchanged through A and the following parent poll.
//
// Example: either initial cancellation mismatch rejects without signal authority.
func (g *guard) acquire(
	value *pendingAcquisition,
	parents map[int]*custody,
) (bound *custody, absent bool, rejected bool) {
	g.consumeNotices()
	if value.DirectoryFD < 0 {
		fd, err := g.kernel.OpenPIDFD(value.PID)
		if errors.Is(err, unix.ESRCH) {
			return nil, true, false
		}
		if err == nil {
			g.closeSlot(&fd, "close unbound pending pidfd")
		} else {
			g.failAcquisition(value.PID, 0, "resolve unavailable proc lifetime", err)
		}
		g.pending[value.PID] = value
		return nil, false, false
	}
	before := g.kernel.ReadStat(value.DirectoryFD, value.PID)
	g.consumeNotices()
	g.metadataErrors(before)
	if before.InspectionError != nil {
		if !errors.Is(before.InspectionError, unix.ENOENT) && !errors.Is(before.InspectionError, unix.ESRCH) {
			g.failAcquisition(value.PID, 0, "read anchored B", before.InspectionError)
		}
		fd, openErr := g.kernel.OpenPIDFD(value.PID)
		switch {
		case errors.Is(openErr, unix.ESRCH):
			g.closeSlot(&value.DirectoryFD, "close absent proc directory")
			return nil, true, false
		case openErr == nil:
			g.closeSlot(&fd, "close unbound pidfd")
		default:
			g.failAcquisition(value.PID, 0, "pidfd after unavailable anchored stat", openErr)
		}
		g.pending[value.PID] = value
		return nil, false, false
	}
	value.Before = before.Stat
	if value.Cancel && before.Stat.Birth != g.cancel.Birth {
		g.failAcquisition(value.PID, before.Stat.Birth, "cancel identity rejected", fmt.Errorf("PID %d birth mismatch before pidfd", value.PID))
		g.closeSlot(&value.DirectoryFD, "close rejected proc directory")
		return nil, false, true
	}
	fd, err := g.kernel.OpenPIDFD(value.PID)
	g.consumeNotices()
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			g.closeSlot(&value.DirectoryFD, "close absent proc directory")
			return nil, true, false
		}
		g.failAcquisition(value.PID, before.Stat.Birth, "open pidfd", err)
		g.pending[value.PID] = value
		return nil, false, false
	}
	after := g.kernel.ReadStat(value.DirectoryFD, value.PID)
	g.consumeNotices()
	defer g.metadataErrors(after)
	if after.InspectionError != nil {
		if !errors.Is(after.InspectionError, unix.ENOENT) && !errors.Is(after.InspectionError, unix.ESRCH) {
			g.failAcquisition(value.PID, before.Stat.Birth, "read anchored A", after.InspectionError)
		}
		g.closeSlot(&fd, "close unbound pidfd")
		g.pending[value.PID] = value
		return nil, false, false
	}
	if value.Cancel && after.Stat.Birth != g.cancel.Birth {
		g.failAcquisition(value.PID, before.Stat.Birth, "cancel identity rejected", fmt.Errorf("PID %d birth mismatch after pidfd", value.PID))
		g.closeSlot(&fd, "close rejected pidfd")
		g.closeSlot(&value.DirectoryFD, "close rejected proc directory")
		return nil, false, true
	}
	if after.Stat.ProcessIdentity != before.Stat.ProcessIdentity {
		g.failAcquisition(value.PID, before.Stat.Birth, "anchored identity changed", fmt.Errorf("PID %d changed between B and A", value.PID))
		g.closeSlot(&fd, "close mismatched pidfd")
		g.pending[value.PID] = value
		return nil, false, false
	}
	owned, uncertain := g.ancestry(after.Stat, parents)
	if value.Cancel {
		owned, uncertain = true, false
	}
	candidate := &custody{ProcessIdentity: after.Stat.ProcessIdentity, FD: fd, Owned: owned, State: processUnknown}
	// Completed binding is published before a scratch close can fail.
	if owned || uncertain {
		g.publish(candidate)
	} else {
		g.closeSlot(&candidate.FD, "close unowned pidfd")
	}
	g.closeSlot(&value.DirectoryFD, "close proc directory")
	if owned || uncertain {
		return candidate, false, false
	}
	return nil, false, false
}

// publish distinguishes current kernel lifetimes independently of metadata/history collisions.
//
// Example: confirmed old exit permits a new same-tick lifetime without overwriting its slot.
func (g *guard) publish(value *custody) {
	previous := g.current[value.PID]
	if previous != nil && previous != value {
		state, err := g.kernel.Poll(previous.FD)
		switch {
		case err == nil && state == processRunning:
			g.closeSlot(&value.FD, "close duplicate pidfd")
			return
		case err == nil && state == processExited:
			g.release(previous)
		default:
			if err != nil && !errors.Is(err, unix.EINTR) {
				g.fail("poll previous lifetime", err)
			}
			value.Owned = false
			g.custodyGeneration++
			g.tentative[value.PID] = value
			g.record(value.ProcessIdentity, "tentative", nil)
			return
		}
	}
	if value.Owned {
		g.custodyGeneration++
		g.owned[value.ProcessIdentity] = value
		g.current[value.PID] = value
		g.record(value.ProcessIdentity, "observed", nil)
		return
	}
	g.tentative[value.PID] = value
	g.custodyGeneration++
	g.record(value.ProcessIdentity, "tentative", nil)
}

// release removes one current slot and closes its descriptor once.
//
// Example: separately pinned root status remains after its pidfd retires.
func (g *guard) release(value *custody) {
	if g.current[value.PID] == value {
		delete(g.current, value.PID)
	}
	if g.owned[value.ProcessIdentity] == value {
		delete(g.owned, value.ProcessIdentity)
	}
	if g.tentative[value.PID] == value {
		delete(g.tentative, value.PID)
	}
	g.closeSlot(&value.FD, "close exited pidfd")
}

// initialCancel binds external identity or establishes native prebinding absence.
//
// Example: hidden proc ENOENT plus successful pidfd remains unknown with no signals.
func (g *guard) initialCancel() bool {
	directory, err := g.kernel.OpenDirectory(g.cancel.PID)
	if err != nil {
		if !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ESRCH) {
			g.failAcquisition(g.cancel.PID, 0, "open cancellation proc directory", err)
			return false
		}
		fd, openErr := g.kernel.OpenPIDFD(g.cancel.PID)
		if errors.Is(openErr, unix.ESRCH) {
			g.rootDone = true
			g.receipt.CommandExit = 0
			g.beginRetirement("already absent")
			return true
		}
		if openErr == nil {
			g.closeSlot(&fd, "close hidden cancellation pidfd")
			g.failAcquisition(g.cancel.PID, 0, "cancellation target unknown", errors.New("proc unavailable despite successful pidfd"))
			return false
		}
		g.failAcquisition(g.cancel.PID, 0, "open cancellation pidfd", openErr)
		return false
	}
	value := &pendingAcquisition{PID: g.cancel.PID, DirectoryFD: directory, Cancel: true}
	root, absent, rejected := g.acquire(value, g.parents())
	if rejected {
		return false
	}
	if absent && len(g.receipt.Errors) == 0 {
		g.rootDone = true
		g.receipt.CommandExit = 0
		g.beginRetirement("already absent")
		return true
	}
	if root != nil {
		g.root = g.current[g.cancel.PID]
		g.receipt.CommandPID = g.cancel.PID
		g.receipt.CommandExit = 0
		g.beginRetirement("cancel requested")
		g.armPreparation()
		return true
	}
	g.fail("cancellation target unresolved", errors.New("initial acquisition has no bound target"))
	return true
}

// retryPending preserves original anchors until binding or coherent native resolution.
//
// Example: repeated anchored ENOENT with successful pidfd never becomes settlement.
func (g *guard) retryPending() bool {
	for pid, value := range g.pending {
		if g.boundary() && !(g.root == nil && g.stage() == retirementTERM) {
			return false
		}
		delete(g.pending, pid)
		bound, absent, rejected := g.acquire(value, g.parents())
		if value.Cancel && bound != nil && g.root == nil {
			g.root = g.current[pid]
			g.receipt.CommandPID = pid
			g.receipt.CommandExit = 0
			g.armPreparation()
			return false
		}
		if value.Cancel && absent && !rejected && len(g.receipt.Errors) == 0 {
			g.rootDone = true
			g.receipt.CommandExit = 0
		}
	}
	return len(g.pending) == 0
}

// armPreparation holds initial external signals until one finite family pass completes.
//
// Example: a pending cancellation root starts the same pass when it first binds.
func (g *guard) armPreparation() {
	if g.root == nil || g.root.Terminated || g.preparation != nil || g.familyUncertain {
		return
	}
	g.preparation = &familyPreparation{Members: make(map[int]bool), Hinted: make(map[int]bool), Children: make(map[int][]int),
		Queue: []*custody{g.root}, Attempted: make(map[int]bool), Expanded: make(map[*custody]bool), ErrorCount: len(g.receipt.Errors)}
}

// abortPreparation permanently records an incomplete external-family pass and releases signals.
//
// Example: KILL due during acquisition immediately services every proved handle.
func (g *guard) abortPreparation(reason string) {
	g.preparation = nil
	g.familyUncertain = true
	g.fail("external family preparation incomplete", errors.New(reason))
	g.retire()
}

// preparationBoundary permits held TERM work but services KILL before another operation.
//
// Example: a finite batch yields without pretending initial TERM has been applied.
func (g *guard) preparationBoundary() bool {
	g.boundary()
	if g.preparation == nil {
		return true
	}
	if g.stage() >= retirementKILL {
		g.abortPreparation("original KILL boundary reached before complete discovery")
		return true
	}
	if len(g.receipt.Errors) != g.preparation.ErrorCount {
		g.abortPreparation("discovery or acquisition reported an error")
		return true
	}
	return false
}

// prepareCandidate attempts one hinted lifetime using a current pre-A parent snapshot.
//
// Example: a fresh foreign Parent closes the candidate without granting signal authority.
func (g *guard) prepareCandidate(pid int) {
	if g.pending[pid] != nil || g.tentative[pid] != nil {
		return
	}
	if value := g.current[pid]; value != nil && value.Owned && value.FD >= 0 {
		return
	}
	parents := g.parents()
	directory, err := g.kernel.OpenDirectory(pid)
	if err != nil {
		if !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ESRCH) {
			g.fail("open initial-family proc directory", fmt.Errorf("PID %d: %w", pid, err))
		}
		directory = -1
	}
	g.acquire(&pendingAcquisition{PID: pid, DirectoryFD: directory}, parents)
}

// indexFamilyMember records one snapshot entry or advances to hint collection.
//
// Example: duplicate numeric entries do not create additional family authority.
func (g *guard) indexFamilyMember(p *familyPreparation) {
	if p.At == len(p.Entries) {
		p.At, p.Phase = 0, preparationHints
		return
	}
	p.Members[p.Entries[p.At]] = true
	p.At++
}

// indexFamilyHint reads at most one numeric hint and indexes its parent for ordering only.
//
// Example: an unrelated permission failure contributes no authority or candidate.
func (g *guard) indexFamilyHint(p *familyPreparation) {
	if p.At == len(p.Entries) {
		p.Phase = preparationFamily
		return
	}
	pid := p.Entries[p.At]
	p.At++
	if p.Hinted[pid] {
		return
	}
	p.Hinted[pid] = true
	hint, err := g.kernel.Hint(pid)
	switch {
	case err == nil:
		p.Children[hint.Parent] = append(p.Children[hint.Parent], pid)
	case errors.Is(err, errNumericStatClose):
		g.fail("initial-family proc hint", fmt.Errorf("PID %d: %w", pid, err))
	case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ESRCH):
	case (errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM)) && g.current[pid] == nil && g.pending[pid] == nil && g.tentative[pid] == nil:
	default:
		g.fail("initial-family proc hint", fmt.Errorf("PID %d: %w", pid, err))
	}
}

// expandFamilyParent starts one proved parent or completes the finite queue.
//
// Example: an empty parent consumes work without an unbounded queue walk.
func (g *guard) expandFamilyParent(p *familyPreparation) {
	if p.QueueAt == len(p.Queue) {
		if len(g.pending) != 0 || len(g.tentative) != 0 {
			g.abortPreparation("unresolved acquisition remains after finite discovery")
			return
		}
		if !g.preparationBoundary() {
			g.preparation = nil
			g.retire()
		}
		return
	}
	parent := p.Queue[p.QueueAt]
	p.QueueAt++
	if p.Expanded[parent] {
		return
	}
	p.Expanded[parent] = true
	if g.current[parent.PID] == parent && parent.Owned && parent.FD >= 0 {
		p.Parent, p.ChildAt = parent, 0
	}
}

// advanceFamilyQueue expands one parent or attempts one child and queues only proved custody.
//
// Example: an unbound, tentative or duplicate-closed candidate never expands the family.
func (g *guard) advanceFamilyQueue(p *familyPreparation) {
	if p.Parent == nil {
		g.expandFamilyParent(p)
		return
	}
	children := p.Children[p.Parent.PID]
	if p.ChildAt == len(children) {
		p.Parent = nil
		return
	}
	pid := children[p.ChildAt]
	p.ChildAt++
	if !p.Members[pid] || p.Attempted[pid] {
		return
	}
	p.Attempted[pid] = true
	g.prepareCandidate(pid)
	if g.preparationBoundary() {
		return
	}
	if value := g.current[pid]; value != nil && value.Owned && value.FD >= 0 && !p.Expanded[value] {
		p.Queue = append(p.Queue, value)
	}
}

// prepareFamily advances at most scanBudget indexing, hint, expansion or candidate items.
// One snapshot and a parent-first queue cover the stable visible family before initial TERM.
//
// Example: more than 256 reversed entries retain their cursor across bounded owner turns.
func (g *guard) prepareFamily() {
	if g.preparationBoundary() {
		return
	}
	p := g.preparation
	if !p.Snapshot {
		entries, err := g.kernel.Entries()
		if err != nil {
			g.fail("initial-family proc snapshot", err)
		}
		p.Entries, p.Snapshot = entries, true
		if g.preparationBoundary() {
			return
		}
	}
	for count := 0; count < scanBudget; count++ {
		if g.preparationBoundary() {
			return
		}
		switch p.Phase {
		case preparationMembers:
			g.indexFamilyMember(p)
		case preparationHints:
			g.indexFamilyHint(p)
		case preparationFamily:
			g.advanceFamilyQueue(p)
		default:
			g.abortPreparation("invalid preparation phase")
			return
		}
		if g.preparationBoundary() {
			return
		}
	}
}

// pollCustody observes bound identities and retries tentative ancestry with fresh metadata.
//
// Example: only confirmed retained-pidfd exit resolves a bound unknown lifetime.
func (g *guard) pollCustody() bool {
	complete := true
	for _, value := range g.current {
		if g.boundary() {
			return false
		}
		if value.FD < 0 {
			value.State = processUnknown
			complete = false
			continue
		}
		state, err := g.kernel.Poll(value.FD)
		value.State = state
		if err != nil {
			if !errors.Is(err, unix.EINTR) {
				g.fail("poll owned pidfd", err)
			}
			complete = false
			continue
		}
		if state == processUnknown {
			g.fail("poll owned pidfd", errors.New("unknown readiness"))
			complete = false
		}
		if state == processExited {
			if value == g.root {
				g.rootDone = true
			}
			g.release(value)
		}
	}
	for pid, value := range g.tentative {
		if g.boundary() {
			return false
		}
		state, err := g.kernel.Poll(value.FD)
		value.State = state
		if err != nil {
			if !errors.Is(err, unix.EINTR) {
				g.fail("poll tentative pidfd", err)
			}
			complete = false
			continue
		}
		if state == processExited {
			g.release(value)
			continue
		}
		complete = false
		if state != processRunning {
			g.fail("poll tentative pidfd", errors.New("unknown readiness"))
			continue
		}
		if previous := g.current[pid]; previous != nil {
			oldState, oldErr := g.kernel.Poll(previous.FD)
			if oldErr != nil {
				if !errors.Is(oldErr, unix.EINTR) {
					g.fail("poll prior tentative generation", oldErr)
				}
				continue
			}
			if oldState != processExited {
				continue
			}
			g.release(previous)
		}
		parents := g.parents()
		directory, openErr := g.kernel.OpenDirectory(pid)
		if openErr != nil {
			if !errors.Is(openErr, unix.ENOENT) && !errors.Is(openErr, unix.ESRCH) {
				g.fail("open tentative proc directory", openErr)
			}
			continue
		}
		metadata := g.kernel.ReadStat(directory, pid)
		g.metadataErrors(metadata)
		switch {
		case metadata.InspectionError == nil && metadata.Stat.ProcessIdentity == value.ProcessIdentity:
			candidateState, pollErr := g.kernel.Poll(value.FD)
			switch {
			case pollErr == nil && candidateState == processRunning:
				owned, uncertain := g.ancestry(metadata.Stat, parents)
				if owned {
					delete(g.tentative, pid)
					value.Owned = true
					g.publish(value)
				}
				if !owned && !uncertain {
					g.release(value)
				}
			case pollErr != nil && !errors.Is(pollErr, unix.EINTR):
				g.fail("poll tentative after stat", pollErr)
			}
		case metadata.InspectionError != nil && !errors.Is(metadata.InspectionError, unix.ENOENT) && !errors.Is(metadata.InspectionError, unix.ESRCH):
			g.fail("read tentative stat", metadata.InspectionError)
		}
		g.closeSlot(&directory, "close tentative proc directory")
	}
	return complete && len(g.current) == 0 && len(g.tentative) == 0 && len(g.pending) == 0
}

// drain checks notices and clock after every result, and yields after a finite batch.
//
// Example: only actual ECHILD completes; pid zero or due KILL cannot settle the host.
func (g *guard) drain() bool {
	for count := 0; count < drainBudget; count++ {
		result := g.kernel.Wait()
		due := g.boundary()
		switch {
		case errors.Is(result.Error, unix.ECHILD):
			return !due
		case errors.Is(result.Error, unix.EINTR):
			return false
		case result.Error != nil:
			g.fail("native wait", result.Error)
			return false
		case result.PID == 0:
			return false
		case result.PID < 0:
			g.fail("native wait", errors.New("negative PID without native error"))
			return false
		}
		if due {
			return false
		}
	}
	return false
}

// retire makes scheduled attempts only for owned pidfds or the narrowly pinned root.
//
// Example: unknown tentative candidates never receive TERM or KILL.
func (g *guard) retire() {
	if g.preparation != nil {
		return
	}
	stage := g.stage()
	if stage == retirementNone {
		return
	}
	g.appliedStage = stage
	for _, value := range g.current {
		if !value.Owned || value.State == processExited {
			continue
		}
		if !value.Terminated {
			g.signalOwned(value, syscall.SIGTERM)
			value.Terminated = true
		}
		if stage >= retirementKILL && !value.Killed {
			g.signalOwned(value, syscall.SIGKILL)
			value.Killed = true
		}
	}
}

// signalOwned targets only retained, lifetime-bound foreign-family pidfds.
//
// Example: ESRCH still requires a later exit observation and never means native reap.
func (g *guard) signalOwned(
	value *custody,
	sig syscall.Signal,
) {
	var err error
	switch {
	case value.FD >= 0:
		err = g.kernel.Send(value.FD, sig)
	default:
		g.fail("retirement authority unavailable", errors.New("owned slot lacks bound descriptor"))
		return
	}
	action := "terminated"
	if sig == syscall.SIGKILL {
		action = "killed"
	}
	if errors.Is(err, unix.ESRCH) {
		g.record(value.ProcessIdentity, action+" (ESRCH, awaiting exit)", nil)
		return
	}
	g.record(value.ProcessIdentity, action, err)
}

// drive integrates one bounded foreign-family snapshot and subsequent known-custody checks.
//
// Example: settlement repolls known custody and requires another actual ECHILD without rescanning the host.
func (g *guard) drive() {
	verifying := false
	var verifiedGeneration uint64
	for {
		g.boundary()
		if g.root == nil && len(g.pending) != 0 {
			g.retryPending()
		}
		if g.preparation != nil {
			g.prepareFamily()
			if g.preparation != nil {
				// Held TERM never advances appliedStage; check KILL before yielding.
				if !g.preparationBoundary() {
					g.clock.Pause(observationInterval)
				}
				continue
			}
		}
		g.retire()
		final := g.stage() == retirementFinal
		resolved := g.retryPending()
		if g.preparation != nil {
			continue
		}
		empty, drained := g.pollCustody(), g.drain()
		g.retire()
		if len(g.current) != 0 || len(g.tentative) != 0 || len(g.pending) != 0 || verifiedGeneration != g.custodyGeneration {
			verifying = false
		}
		if g.rootDone && resolved && empty && drained {
			if verifying {
				g.receipt.HostChildrenSettled = !g.familyUncertain
				return
			}
			// A second fresh custody observation and actual ECHILD confirm static-family settlement.
			verifying, verifiedGeneration = true, g.custodyGeneration
		}
		if final {
			for _, value := range g.current {
				g.receipt.Survivors = append(g.receipt.Survivors, value.ProcessIdentity)
			}
			for _, value := range g.tentative {
				g.receipt.Survivors = append(g.receipt.Survivors, value.ProcessIdentity)
			}
			for _, value := range g.pending {
				identity := value.Before.ProcessIdentity
				identity.PID = value.PID
				g.receipt.Survivors = append(g.receipt.Survivors, identity)
			}
			g.fail("host children unsettled", fmt.Errorf("final predicate incomplete: root=%t pending-resolved=%t custody=%t drain=%t pending=%d tentative=%d", g.rootDone, resolved, empty, drained, len(g.pending), len(g.tentative)))
			return
		}
		g.clock.Pause(observationInterval)
	}
}

// probeSelf validates pidfd acquisition/readiness and consumes its scratch fd before adoption.
//
// Example: an unsupported facility rejects a side-effecting command before launch.
func (g *guard) probeSelf() bool {
	fd, err := g.kernel.OpenPIDFD(os.Getpid())
	g.consumeNotices()
	if err != nil {
		g.fail("constructor self pidfd open", err)
		if fd >= 0 {
			g.closeSlot(&fd, "constructor self pidfd close")
		}
		g.consumeNotices()
		return false
	}
	if fd < 0 {
		g.fail("constructor self pidfd open", errors.New("negative descriptor without native error"))
		return false
	}
	state, pollErr := g.kernel.Poll(fd)
	g.consumeNotices()
	if pollErr != nil {
		g.fail("constructor self pidfd poll", pollErr)
	}
	if pollErr == nil && state != processRunning {
		g.fail("constructor self pidfd poll", fmt.Errorf("self lifetime readiness %d is not running", state))
	}
	priorErrors := len(g.receipt.Errors)
	g.closeSlot(&fd, "constructor self pidfd close")
	g.consumeNotices()
	return pollErr == nil && state == processRunning && len(g.receipt.Errors) == priorErrors && !g.interrupted
}

// prepareKernel rejects incomplete setup before adoption or child creation.
//
// Example: startup signals consumed before launch commitment return without a child.
func (g *guard) prepareKernel() bool {
	g.consumeNotices()
	if g.interrupted {
		return false
	}
	if err := g.kernel.Childless(); err != nil {
		g.fail("constructor native-child check", err)
		return false
	}
	g.consumeNotices()
	if g.interrupted {
		return false
	}
	if err := g.kernel.Namespace(); err != nil {
		g.fail("constructor namespace check", err)
		return false
	}
	g.consumeNotices()
	if g.interrupted {
		return false
	}
	if !g.probeSelf() {
		return false
	}
	if err := g.kernel.Subreaper(); err != nil {
		g.fail("constructor subreaper", err)
		return false
	}
	g.consumeNotices()
	if g.interrupted {
		return false
	}
	return true
}

// constructCancellation prepares native custody before binding the external cancellation target.
//
// Example: failed native admission cannot signal a foreign process.
func (g *guard) constructCancellation() bool {
	g.receipt.Backend = "external-pidfd"
	g.receipt.FamilyCoverage = "one observed static external-family snapshot"
	if !g.prepareKernel() {
		return false
	}
	return g.initialCancel()
}

// finalize closes all slots and consumes queued notices before status classification.
//
// Example: descriptor close errors override otherwise ordinary success.
func (g *guard) finalize() (Receipt, int) {
	for _, value := range g.current {
		g.closeSlot(&value.FD, "final close owned pidfd")
	}
	for _, value := range g.tentative {
		g.closeSlot(&value.FD, "final close tentative pidfd")
	}
	for _, value := range g.pending {
		g.closeSlot(&value.DirectoryFD, "final close pending proc directory")
	}
	g.consumeNotices()
	now := g.clock.Now()
	g.receipt.Finished, g.receipt.ElapsedSeconds = now.UTC(), now.Sub(g.started).Seconds()
	code := guardFailure
	if g.receipt.HostChildrenSettled && len(g.receipt.Errors) == 0 && !g.interrupted {
		switch g.receipt.Cause {
		case "command exited":
			code = g.receipt.CommandExit
		case "cancel requested", "already absent":
			code = 0
		}
	}
	return g.receipt, code
}

// validArguments rejects negative cancellation IDs before native observation.
//
// Example: PID zero selects launch; positive cancellation needs birth and no command.
func validArguments(
	deadline time.Duration,
	grace time.Duration,
	cancel ProcessIdentity,
	args []string,
) bool {
	if deadline <= 0 || grace <= 0 || cancel.PID < 0 {
		return false
	}
	if cancel.PID == 0 {
		return cancel.Birth == 0 && len(args) > 0
	}
	return cancel.Birth > 0 && len(args) == 0
}

// supervise registers signals before setup and classifies only after checked finalization.
//
// Example: startup inspection failure never leaves a published child without retirement.
func supervise(
	deadline time.Duration,
	grace time.Duration,
	cancel ProcessIdentity,
	args []string,
) (Receipt, int) {
	notices := make(chan os.Signal, 16)
	g := newGuard(linuxKernel{}, realClock{}, notices, deadline, grace, cancel)
	signal.Notify(notices, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	switch {
	case !validArguments(deadline, grace, cancel, args):
		g.fail("invalid arguments", errors.New("invalid owned-command arguments"))
	case g.launchMode:
		g.runCgroupCommand(args)
	case g.constructCancellation():
		g.drive()
	}
	signal.Stop(notices)
	return g.finalize()
}

// main validates CLI inputs and writes the caller's receipt before returning status.
//
// Example: receipt and unexpected diagnostic-write errors return 125.
func main() {
	deadline := flag.Duration("deadline", 3*time.Second, "maximum command runtime before retirement")
	grace := flag.Duration("grace", 250*time.Millisecond, "TERM grace and subsequent KILL settlement bound")
	receipt := flag.String("receipt", "", "JSON receipt path")
	cancelPID := flag.Int("cancel-pid", 0, "cancel this exact host process")
	cancelBirth := flag.Uint64("cancel-birth", 0, "required /proc stat field 22 identity")
	flag.Parse()
	cancel := ProcessIdentity{PID: *cancelPID, Birth: *cancelBirth}
	if *receipt == "" || !validArguments(*deadline, *grace, cancel, flag.Args()) {
		if _, err := fmt.Fprintln(os.Stderr, "invalid owned-command arguments"); err != nil {
			os.Exit(guardFailure)
		}
		os.Exit(guardFailure)
	}
	r, code := supervise(*deadline, *grace, cancel, flag.Args())
	data, err := json.MarshalIndent(r, "", "  ")
	if err == nil {
		err = os.WriteFile(*receipt, append(data, '\n'), 0600)
	}
	if err != nil {
		code = guardFailure
		if _, diagnosticErr := fmt.Fprintln(os.Stderr, err); diagnosticErr != nil {
			code = guardFailure
		}
	}
	os.Exit(code)
}
