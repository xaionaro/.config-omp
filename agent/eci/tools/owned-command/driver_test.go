package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// driverClock supplies deterministic synthetic time and bounds runaway loops.
//
// Example: a scripted Wait crosses the KILL boundary without sleeping.
type driverClock struct {
	now       time.Time
	pauses    int
	kernel    *driverKernel
	reads     int
	advanceAt int
	advance   time.Duration
}

// Now returns the injected clock value.
//
// Example: no assertion depends on native wall time.
func (c *driverClock) Now() time.Time {
	c.reads++
	if c.reads == c.advanceAt {
		c.now = c.now.Add(c.advance)
		if c.kernel != nil {
			c.kernel.operations = append(c.kernel.operations, "kill-due")
		}
	}
	return c.now
}

// Pause advances one synthetic observation interval.
//
// Example: an unfinished driver must terminate before exhausting this bound.
func (c *driverClock) Pause(duration time.Duration) {
	if c.kernel != nil {
		c.kernel.operations = append(c.kernel.operations, "pause")
	}
	c.pauses++
	if c.pauses > 2000 {
		panic("synthetic driver exceeded finite observation bound")
	}
	c.now = c.now.Add(duration)
}

// driverWait carries a native result and changes occurring during that call.
//
// Example: a notice returned with ECHILD must still prevent settlement.
type driverWait struct {
	result  nativeResult
	advance time.Duration
	notice  os.Signal
}

// driverSignal records the selected signal authority.
//
// Example: raw root rollback differs from retained pidfd signaling.
type driverSignal struct {
	target int
	signal syscall.Signal
}

// driverKernel overrides each used seam and has no native runtime fallback.
//
// Example: an unexpected unimplemented operation panics through the nil interface.
type driverKernel struct {
	processKernel
	clock              *driverClock
	notices            chan os.Signal
	waits              []driverWait
	waitCalls          int
	setupErrors        [3]error
	setupCalls         []string
	metadataError      error
	directoryError     error
	pidfdError         error
	pidfdCalls         int
	closeError         error
	closed             []int
	signals            []driverSignal
	entries            [][]int
	entryCalls         int
	hintCalls          int
	hints              map[int]processStat
	polls              map[int]processState
	selfOpenError      error
	selfFD             int
	selfPollState      processState
	selfPollError      error
	selfCloseError     error
	selfNoticeAt       string
	operations         []string
	readPIDs           []int
	reparentOnRootTERM bool
	exitOnSend         bool
	hintAdvanceAt      int
	hintAdvance        time.Duration
	readStats          map[int]processStat
	readAdvanceAt      int
	readAdvance        time.Duration
	metadataErrors     []error
	entryErrors        []error
	hintErrors         map[int]error
}

// Childless models the nonconsuming constructor predicate.
//
// Example: failure forbids consuming Wait and target actions.
func (k *driverKernel) Childless() error {
	k.setupCalls = append(k.setupCalls, "childless")
	return k.setupErrors[0]
}

// Namespace models constructor namespace coherence.
//
// Example: an incoherent namespace rejects before launch.
func (k *driverKernel) Namespace() error {
	k.setupCalls = append(k.setupCalls, "namespace")
	return k.setupErrors[1]
}

// Subreaper models adoption setup.
//
// Example: failed setup cannot enter the family driver.
func (k *driverKernel) Subreaper() error {
	k.operations = append(k.operations, "subreaper")
	k.setupCalls = append(k.setupCalls, "subreaper")
	return k.setupErrors[2]
}

// OpenDirectory returns an inert synthetic directory identifier.
//
// Example: metadata failure occurs after root publication.
func (k *driverKernel) OpenDirectory(pid int) (int, error) {
	if k.directoryError != nil {
		return -1, k.directoryError
	}
	return pid + 10000, nil
}

// ReadStat supplies anchored metadata with an optional injected error.
//
// Example: a postpublication error retains the root descriptor.
func (k *driverKernel) ReadStat(
	fd int,
	pid int,
) metadataRead {
	k.operations = append(k.operations, "read")
	k.readPIDs = append(k.readPIDs, pid)
	if len(k.readPIDs) == k.readAdvanceAt {
		k.clock.now = k.clock.now.Add(k.readAdvance)
		k.operations = append(k.operations, "kill-due")
	}
	stat := k.hints[pid]
	if fresh, ok := k.readStats[pid]; ok {
		stat = fresh
	}
	if stat.PID == 0 {
		stat = processStat{ProcessIdentity: ProcessIdentity{PID: pid, Birth: 1}, Parent: os.Getpid(), State: "S"}
	}
	err := k.metadataError
	if len(k.metadataErrors) != 0 {
		err = k.metadataErrors[0]
		k.metadataErrors = k.metadataErrors[1:]
	}
	return metadataRead{Stat: stat, InspectionError: err}
}

// OpenPIDFD returns a synthetic lifetime handle.
//
// Example: a newly discovered child uses a different handle from its directory.
func (k *driverKernel) OpenPIDFD(pid int) (int, error) {
	if pid == os.Getpid() {
		k.operations = append(k.operations, "self-open")
		if k.selfNoticeAt == "open" {
			k.notices <- syscall.SIGTERM
		}
		return k.selfFD, k.selfOpenError
	}
	k.pidfdCalls++
	return pid + 20000, k.pidfdError
}

// Poll observes the explicitly configured lifetime state.
//
// Example: an exited child can retire before its terminal native status is drained.
func (k *driverKernel) Poll(fd int) (processState, error) {
	if fd == k.selfFD {
		k.operations = append(k.operations, "self-poll")
		if k.selfNoticeAt == "poll" {
			k.notices <- syscall.SIGTERM
		}
		return k.selfPollState, k.selfPollError
	}
	if state, ok := k.polls[fd]; ok {
		return state, nil
	}
	return processRunning, nil
}

// Send records descriptor authority without sending a native signal.
//
// Example: metadata errors preserve a valid root pidfd for TERM.
func (k *driverKernel) Send(
	fd int,
	signal syscall.Signal,
) error {
	k.operations = append(k.operations, "send")
	if fd == 24101 && signal == syscall.SIGKILL {
		k.operations = append(k.operations, "root-kill")
	}
	k.signals = append(k.signals, driverSignal{target: fd, signal: signal})
	if k.reparentOnRootTERM && fd == 24101 && signal == syscall.SIGTERM {
		for pid, stat := range k.hints {
			if stat.Parent == 4101 {
				stat.Parent = 999999
				k.hints[pid] = stat
			}
		}
	}
	if k.exitOnSend {
		k.polls[fd] = processExited
	}
	return nil
}

// Wait returns only the scripted synthetic results.
//
// Example: every returned result can change the clock or queue a notice.
func (k *driverKernel) Wait() nativeResult {
	k.waitCalls++
	if len(k.waits) == 0 {
		return nativeResult{Error: unix.ECHILD}
	}
	next := k.waits[0]
	k.waits = k.waits[1:]
	k.clock.now = k.clock.now.Add(next.advance)
	if next.notice != nil {
		k.notices <- next.notice
	}
	return next.result
}

// Close records one synthetic descriptor retirement and returns injected errors.
//
// Example: final close failure overrides a previously successful receipt.
func (k *driverKernel) Close(fd int) error {
	k.closed = append(k.closed, fd)
	if fd == k.selfFD {
		k.operations = append(k.operations, "self-close")
		if k.selfNoticeAt == "close" {
			k.notices <- syscall.SIGTERM
		}
		return k.selfCloseError
	}
	return k.closeError
}

// Entries returns complete scripted scans and then an empty scan.
//
// Example: a second scan reveals a child after the first ECHILD.
func (k *driverKernel) Entries() ([]int, error) {
	k.entryCalls++
	if len(k.entryErrors) != 0 {
		err := k.entryErrors[0]
		k.entryErrors = k.entryErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(k.entries) == 0 {
		return nil, nil
	}
	entries := k.entries[0]
	k.entries = k.entries[1:]
	return entries, nil
}

// Hint returns discovery metadata without supplying family authority.
//
// Example: scan cursor tests count every unrelated numeric entry.
func (k *driverKernel) Hint(pid int) (processStat, error) {
	k.hintCalls++
	k.operations = append(k.operations, "hint")
	if k.hintCalls == k.hintAdvanceAt {
		k.clock.now = k.clock.now.Add(k.hintAdvance)
		k.operations = append(k.operations, "kill-due")
	}
	if err := k.hintErrors[pid]; err != nil {
		return processStat{}, err
	}
	if stat, ok := k.hints[pid]; ok {
		return stat, nil
	}
	return processStat{ProcessIdentity: ProcessIdentity{PID: pid, Birth: 1}, Parent: 999999, State: "S"}, nil
}

// newDriverControl initializes entirely synthetic guard dependencies.
//
// Example: every test remains independent of the current process's children.
func newDriverControl() (*guard, *driverKernel) {
	clock := &driverClock{now: time.Unix(100, 0)}
	notices := make(chan os.Signal, 16)
	kernel := &driverKernel{clock: clock, notices: notices, selfFD: 73, selfPollState: processRunning, hints: make(map[int]processStat), hintErrors: make(map[int]error), readStats: make(map[int]processStat), polls: make(map[int]processState)}
	clock.kernel = kernel
	return newGuard(kernel, clock, notices, time.Hour, time.Second, ProcessIdentity{}), kernel
}

// TestSyntheticAcquisitionFailureRetirement preserves fatal cleanup and modeled settlement.
//
// Example: later native absence settles custody but cannot erase the earlier acquisition error.
func TestSyntheticAcquisitionFailureRetirement(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		t.Run(fmt.Sprintf("resolved=%t", resolved), func(t *testing.T) {
			g, k := newDriverControl()
			g.cancel = ProcessIdentity{PID: 4101, Birth: 1}
			g.launchMode = false
			g.root = &custody{ProcessIdentity: g.cancel, FD: 42, Owned: true, State: processRunning}
			g.current[4101], g.owned[g.cancel] = g.root, g.root
			g.receipt.CommandExit = 0
			k.pidfdError = unix.EINVAL
			pending := &pendingAcquisition{PID: 4102, DirectoryFD: 14102}
			value, absent, rejected := g.acquire(pending, g.parents())
			if value != nil || absent || rejected || g.pending[4102] != pending || len(g.receipt.Errors) != 1 || k.pidfdCalls != 1 {
				t.Fatalf("acquisition classification changed: receipt=%+v pending=%v", g.receipt, g.pending)
			}
			event := g.receipt.Events[len(g.receipt.Events)-1]
			if event.PID != 4102 || event.Birth != 1 || event.Action != "open pidfd" || !strings.Contains(event.Error, unix.EINVAL.Error()) {
				t.Errorf("missing driver acquisition context: %+v", event)
			}
			retirement := g.retirement
			g.retire()
			k.clock.now = retirement.Add(g.grace)
			g.retire()
			if len(k.signals) != 2 || k.signals[0] != (driverSignal{target: 42, signal: syscall.SIGTERM}) || k.signals[1] != (driverSignal{target: 42, signal: syscall.SIGKILL}) {
				t.Fatalf("root schedule or candidate authority changed: %+v", k.signals)
			}
			k.polls[42] = processExited
			k.waits = []driverWait{{result: nativeResult{PID: 4101, Status: syscall.WaitStatus(syscall.SIGKILL)}}}
			if resolved {
				k.pidfdError = unix.ESRCH
			}
			g.drive()
			receipt, code := g.finalize()
			if code != guardFailure || receipt.HostChildrenSettled != resolved || receipt.CommandExit != 0 || receipt.Cause != "guard error" || !g.retirement.Equal(retirement) || !g.rootDone || g.rootUnreaped || k.waitCalls < 2 || len(k.signals) != 2 {
				t.Fatalf("fatal cleanup changed: code=%d receipt=%+v waits=%d signals=%+v", code, receipt, k.waitCalls, k.signals)
			}
			if resolved {
				if len(receipt.Errors) != 1 || len(receipt.Survivors) != 0 || len(g.pending) != 0 {
					t.Fatalf("resolved custody changed: %+v", receipt)
				}
			} else {
				if len(receipt.Survivors) != 1 || receipt.Survivors[0].PID != 4102 || g.pending[4102] != pending {
					t.Fatalf("unresolved custody hidden: receipt=%+v pending=%v", receipt, g.pending)
				}
			}
			anchorCloses := 0
			for _, fd := range k.closed {
				if fd == 14102 {
					anchorCloses++
				}
			}
			if anchorCloses != 1 {
				t.Fatalf("pending anchor closes=%d: %v", anchorCloses, k.closed)
			}
		})
	}
}

// TestSyntheticDrainFiniteBatch rejects an unending stream as a complete drain.
//
// Example: terminal children cannot starve the owner loop indefinitely.
func TestSyntheticDrainFiniteBatch(t *testing.T) {
	g, k := newDriverControl()
	for index := 0; index < drainBudget+1; index++ {
		k.waits = append(k.waits, driverWait{result: nativeResult{PID: 5000 + index, Status: 0}})
	}
	if g.drain() || k.waitCalls != drainBudget || len(k.waits) != 1 || g.receipt.HostChildrenSettled {
		t.Fatalf("finite incomplete drain: calls=%d remaining=%d settled=%t", k.waitCalls, len(k.waits), g.receipt.HostChildrenSettled)
	}
}

// TestSyntheticDrainKillBoundary checks the clock after each terminal result.
//
// Example: due KILL yields to retirement after the first wait.
func TestSyntheticDrainKillBoundary(t *testing.T) {
	g, k := newDriverControl()
	g.beginRetirement("deadline")
	g.appliedStage = retirementTERM
	root := &custody{ProcessIdentity: ProcessIdentity{PID: 4101}, FD: 42, Owned: true, State: processRunning, Terminated: true}
	g.current[root.PID] = root
	k.waits = []driverWait{{result: nativeResult{PID: 5000}, advance: time.Second}, {result: nativeResult{Error: unix.ECHILD}}}
	if g.drain() || k.waitCalls != 1 {
		t.Fatalf("due KILL failed to yield: calls=%d", k.waitCalls)
	}
	g.retire()
	if len(k.signals) != 1 || k.signals[0].signal != syscall.SIGKILL || k.signals[0].target != 42 || g.receipt.HostChildrenSettled {
		t.Fatalf("due KILL authority/settlement: signals=%+v receipt=%+v", k.signals, g.receipt)
	}
}

// TestSyntheticDrainConsumesEveryNotice covers every class of Wait return.
//
// Example: ECHILD with a just-consumed notice is incomplete.
func TestSyntheticDrainConsumesEveryNotice(t *testing.T) {
	results := []nativeResult{{PID: 5000}, {PID: 0}, {Error: unix.ECHILD}, {Error: unix.EINTR}, {Error: unix.EIO}, {PID: -1}}
	for _, result := range results {
		g, k := newDriverControl()
		k.waits = []driverWait{{result: result, notice: syscall.SIGTERM}}
		if g.drain() || !g.interrupted || g.retirement.IsZero() || k.waitCalls != 1 || len(k.notices) != 0 {
			t.Fatalf("notice missed for %+v: interrupted=%t calls=%d", result, g.interrupted, k.waitCalls)
		}
	}
}

// TestSyntheticConstructorFailureForbidsTargetActions rejects every setup failure.
//
// Example: no failed constructor may consume Wait or launch a target.
func TestSyntheticConstructorFailureForbidsTargetActions(t *testing.T) {
	for index := 0; index < 3; index++ {
		g, k := newDriverControl()
		k.setupErrors[index] = errors.New("synthetic setup failure")
		if g.prepareKernel() {
			t.Fatalf("setup %d accepted", index)
		}
		receipt, code := g.finalize()
		if code != guardFailure || len(receipt.Errors) == 0 || k.waitCalls != 0 || len(k.signals) != 0 || len(k.setupCalls) != index+1 {
			t.Fatalf("setup %d acted on target: receipt=%+v kernel=%+v", index, receipt, k)
		}
	}
}

// TestSyntheticIncompleteDrainRefusesSettlement exercises the final driver predicate.
//
// Example: PID zero is never equivalent to actual ECHILD.
func TestSyntheticIncompleteDrainRefusesSettlement(t *testing.T) {
	g, k := newDriverControl()
	g.rootDone = true
	g.beginRetirement("command exited")
	k.clock.now = k.clock.now.Add(2 * time.Second)
	k.waits = []driverWait{{result: nativeResult{PID: 0}}}
	g.drive()
	receipt, code := g.finalize()
	if receipt.HostChildrenSettled || code != guardFailure || k.waitCalls != 1 || len(receipt.Errors) == 0 {
		t.Fatalf("incomplete drain accepted: %+v code=%d calls=%d", receipt, code, k.waitCalls)
	}
}

// TestSyntheticFinalCloseErrorOverridesSuccess classifies after checked cleanup.
//
// Example: an otherwise settled command cannot report success after close failure.
func TestSyntheticFinalCloseErrorOverridesSuccess(t *testing.T) {
	g, k := newDriverControl()
	g.receipt.HostChildrenSettled = true
	g.receipt.CommandExit = 0
	g.receipt.Cause = "command exited"
	g.current[4101] = &custody{ProcessIdentity: ProcessIdentity{PID: 4101}, FD: 42}
	k.closeError = unix.EIO
	receipt, code := g.finalize()
	if code != guardFailure || len(receipt.Errors) == 0 || len(k.closed) != 1 || k.closed[0] != 42 || g.current[4101].FD != -1 {
		t.Fatalf("final close preserved success: receipt=%+v code=%d closes=%v", receipt, code, k.closed)
	}
}

// TestSyntheticFinalQueuedNoticeOverridesSuccess consumes the final notice queue.
//
// Example: buffered interruption overrides a previously settled zero exit.
func TestSyntheticFinalQueuedNoticeOverridesSuccess(t *testing.T) {
	g, k := newDriverControl()
	g.receipt.HostChildrenSettled = true
	g.receipt.CommandExit = 0
	g.receipt.Cause = "command exited"
	k.notices <- syscall.SIGINT
	receipt, code := g.finalize()
	if code != guardFailure || !g.interrupted || len(k.notices) != 0 || receipt.Cause != "signal: interrupt" {
		t.Fatalf("final notice preserved success: receipt=%+v code=%d", receipt, code)
	}
}

// TestSyntheticDrainChecksClockAfterEveryResult tests scheduling on all native returns.
//
// Example: ECHILD at a newly due KILL boundary yields incomplete to retirement.
func TestSyntheticDrainChecksClockAfterEveryResult(t *testing.T) {
	results := []nativeResult{{PID: 5000}, {PID: 0}, {Error: unix.ECHILD}, {Error: unix.EINTR}, {Error: unix.EIO}, {PID: -1}}
	for _, result := range results {
		g, k := newDriverControl()
		g.beginRetirement("deadline")
		g.appliedStage = retirementTERM
		k.waits = []driverWait{{result: result, advance: time.Second}}
		if g.drain() || k.waitCalls != 1 || g.stage() != retirementKILL || g.receipt.HostChildrenSettled {
			t.Fatalf("clock boundary missed for %+v: calls=%d stage=%d settled=%t", result, k.waitCalls, g.stage(), g.receipt.HostChildrenSettled)
		}
	}
}

// driverSelfFailure describes a synthetic constructor facility failure.
//
// Example: acquired self handles close once even when readiness fails.
type driverSelfFailure struct {
	name           string
	openError      error
	fd             int
	pollState      processState
	pollError      error
	closeError     error
	expectedCloses int
}

// TestSyntheticConstructorSelfFacilityFailures rejects unavailable facilities before target action.
//
// Example: neither launch nor cancellation may proceed after scratch cleanup failure.
func TestSyntheticConstructorSelfFacilityFailures(t *testing.T) {
	cases := []driverSelfFailure{
		{name: "open", fd: -1, openError: unix.EMFILE},
		{name: "open-with-acquired-fd", fd: 73, openError: unix.EIO, expectedCloses: 1},
		{name: "negative", fd: -1},
		{name: "exited", fd: 73, pollState: processExited, expectedCloses: 1},
		{name: "unknown", fd: 73, pollState: processUnknown, expectedCloses: 1},
		{name: "interrupted-poll", fd: 73, pollError: unix.EINTR, expectedCloses: 1},
		{name: "poll", fd: 73, pollError: unix.EIO, expectedCloses: 1},
		{name: "close", fd: 73, pollState: processRunning, closeError: unix.EIO, expectedCloses: 1},
	}
	for _, mode := range []bool{false, true} {
		for _, testCase := range cases {
			g, k := newDriverControl()
			if mode {
				g.cancel = ProcessIdentity{PID: 4101, Birth: 1}
				g.launchMode = false
			}
			k.selfFD, k.selfOpenError = testCase.fd, testCase.openError
			k.selfPollState, k.selfPollError, k.selfCloseError = testCase.pollState, testCase.pollError, testCase.closeError
			constructed := g.prepareKernel()
			receipt, code := g.finalize()
			closes := 0
			for _, fd := range k.closed {
				if fd == testCase.fd {
					closes++
				}
			}
			if constructed || code != guardFailure || receipt.CommandPID != 0 || receipt.HostChildrenSettled || k.pidfdCalls != 0 || k.waitCalls != 0 || len(k.signals) != 0 || len(k.setupCalls) != 2 || closes != testCase.expectedCloses {
				t.Errorf("self failure %s cancel=%t accepted/acted: constructed=%t code=%d receipt=%+v setup=%v closes=%d target-opens=%d", testCase.name, mode, constructed, code, receipt, k.setupCalls, closes, k.pidfdCalls)
			}
		}
	}
}

// TestSyntheticConstructorSelfNoticesCleanScratch rejects consumed notices at probe boundaries.
//
// Example: opening the self pidfd never transfers it into target custody.
func TestSyntheticConstructorSelfNoticesCleanScratch(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		for _, boundary := range []string{"open", "poll", "close"} {
			g, k := newDriverControl()
			if cancel {
				g.cancel = ProcessIdentity{PID: 4101, Birth: 1}
				g.launchMode = false
			}
			k.selfNoticeAt = boundary
			constructed := g.prepareKernel()
			receipt, code := g.finalize()
			if constructed || code != guardFailure || !g.interrupted || receipt.CommandPID != 0 || k.waitCalls != 0 || k.pidfdCalls != 0 || len(k.signals) != 0 || len(k.closed) != 1 || k.closed[0] != 73 || len(k.setupCalls) != 2 {
				t.Errorf("self %s notice lost cleanup/no-target obligation: receipt=%+v calls=%v closes=%v", boundary, receipt, k.operations, k.closed)
			}
		}
	}
}

// TestSyntheticConstructorSelfCleanupPrecedesDispatch orders successful facility admission.
//
// Example: self close completes before subreaper and either target dispatch.
func TestSyntheticConstructorSelfCleanupPrecedesDispatch(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		g, k := newDriverControl()
		if cancel {
			g.cancel = ProcessIdentity{PID: 4101, Birth: 1}
			g.launchMode = false
		}
		if !g.prepareKernel() {
			t.Fatal("successful constructor rejected")
		}
		expected := []string{"self-open", "self-poll", "self-close", "subreaper"}
		if len(k.operations) < len(expected) {
			t.Fatalf("self facility omitted cancel=%t operations=%v", cancel, k.operations)
		}
		for index, value := range expected {
			if k.operations[index] != value {
				t.Fatalf("self cleanup dispatch order cancel=%t operations=%v", cancel, k.operations)
			}
		}
		g.finalize()
		closes := 0
		for _, fd := range k.closed {
			if fd == 73 {
				closes++
			}
		}
		if closes != 1 {
			t.Fatalf("self scratch closed %d times", closes)
		}
	}
}

// configureDriverExternalFamily scripts a stable reverse-listed chain and foreign entry.
//
// Example: child ancestry survives root TERM only if acquired beforehand.
func configureDriverExternalFamily(
	g *guard,
	k *driverKernel,
	depth int,
	unrelated int,
) {
	g.cancel = ProcessIdentity{PID: 4101, Birth: 1}
	g.launchMode = false
	k.hints[4101] = processStat{ProcessIdentity: g.cancel, Parent: 999999, State: "S"}
	entries := make([]int, 0, depth+unrelated)
	for index := depth; index > 0; index-- {
		pid := 4101 + index
		k.hints[pid] = processStat{ProcessIdentity: ProcessIdentity{PID: pid, Birth: uint64(index + 1)}, Parent: pid - 1, State: "S"}
		entries = append(entries, pid)
	}
	for index := 0; index < unrelated; index++ {
		entries = append(entries, 8000+index)
	}
	k.entries = [][]int{entries}
	k.reparentOnRootTERM = true
	k.exitOnSend = true
}

// TestSyntheticExternalFamilyPreparedBeforeRootTERM falsifies signal-before-discovery ordering.
//
// Example: reversed deep families beyond 256 entries retire through every proved handle.
func TestSyntheticExternalFamilyPreparedBeforeRootTERM(t *testing.T) {
	for _, depth := range []int{1, 5, 270} {
		g, k := newDriverControl()
		configureDriverExternalFamily(g, k, depth, 300)
		if !g.initialCancel() {
			t.Fatal("valid root binding rejected")
		}
		retirement := g.retirement
		g.drive()
		receipt, code := g.finalize()
		targets := make(map[int]int)
		for _, sent := range k.signals {
			if sent.signal == syscall.SIGTERM {
				targets[sent.target]++
			}
		}
		if code != 0 || !receipt.HostChildrenSettled || len(targets) != depth+1 || !g.retirement.Equal(retirement) {
			t.Fatalf("external family lost before TERM depth=%d targets=%d expected=%d receipt=%+v code=%d", depth, len(targets), depth+1, receipt, code)
		}
		for index := 0; index <= depth; index++ {
			if targets[24101+index] != 1 {
				t.Fatalf("unretired family depth=%d handle=%d targets=%v", depth, 24101+index, targets)
			}
		}
	}
}

// TestSyntheticExternalPreparationKillCrossingLatchesFailure preserves original retirement timing.
//
// Example: collection crossing grace retires proved handles and can never settle zero later.
func TestSyntheticExternalPreparationKillCrossingLatchesFailure(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 5, 300)
	k.hintAdvanceAt, k.hintAdvance = 1, time.Second
	if !g.initialCancel() {
		t.Fatal("valid root binding rejected")
	}
	retirement := g.retirement
	g.drive()
	receipt, code := g.finalize()
	term, kill := 0, 0
	for _, sent := range k.signals {
		if sent.target == 24101 && sent.signal == syscall.SIGTERM {
			term++
		}
		if sent.target == 24101 && sent.signal == syscall.SIGKILL {
			kill++
		}
	}
	if code != guardFailure || receipt.HostChildrenSettled || !g.retirement.Equal(retirement) || term != 1 || kill != 1 {
		t.Fatalf("preparation crossing lost latch/schedule: code=%d receipt=%+v term=%d kill=%d hints=%d", code, receipt, term, kill, k.hintCalls)
	}
	assertDriverDueRetirementBeforeWork(t, k)
}

// TestSyntheticExternalFreshReparentRejectsHintAuthority separates ordering hints from ownership.
//
// Example: a child reparented before fresh anchored reads never receives a signal.
func TestSyntheticExternalFreshReparentRejectsHintAuthority(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 1, 0)
	fresh := k.hints[4102]
	fresh.Parent = 999999
	k.readStats[4102] = fresh
	if !g.initialCancel() {
		t.Fatal("valid root binding rejected")
	}
	g.drive()
	receipt, code := g.finalize()
	if code != 0 || !receipt.HostChildrenSettled || len(k.signals) != 1 || k.signals[0].target != 24101 {
		t.Fatalf("fresh foreign edge gained authority: receipt=%+v signals=%+v code=%d", receipt, k.signals, code)
	}
	inspectedChild := false
	for _, pid := range k.readPIDs {
		if pid == 4102 {
			inspectedChild = true
		}
	}
	if !inspectedChild {
		t.Fatal("cached hint candidate never received fresh rejection check")
	}
}

// TestSyntheticExternalAcquisitionKillCrossingServesProvedHandle latches incomplete acquisition.
//
// Example: anchored A crosses grace and due retirement runs before another candidate.
func TestSyntheticExternalAcquisitionKillCrossingServesProvedHandle(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 2, 0)
	k.readAdvanceAt, k.readAdvance = 4, time.Second
	if !g.initialCancel() {
		t.Fatal("valid root binding rejected")
	}
	retirement := g.retirement
	g.drive()
	receipt, code := g.finalize()
	if code != guardFailure || receipt.HostChildrenSettled || !g.retirement.Equal(retirement) {
		t.Fatalf("late acquisition erased uncertainty/restarted schedule: receipt=%+v code=%d", receipt, code)
	}
	rootKill := false
	for _, sent := range k.signals {
		if sent.target == 24101 && sent.signal == syscall.SIGKILL {
			rootKill = true
		}
	}
	if !rootKill {
		t.Fatalf("late acquisition failed due root cleanup: signals=%+v", k.signals)
	}
	assertDriverDueRetirementBeforeWork(t, k)
}

// assertDriverDueRetirementBeforeWork checks cleanup before another preparation item or pause.
//
// Example: crossing the original grace cannot spend another batch before root KILL.
func assertDriverDueRetirementBeforeWork(
	t *testing.T,
	k *driverKernel,
) {
	t.Helper()
	due := false
	for _, operation := range k.operations {
		switch operation {
		case "kill-due":
			due = true
		case "root-kill":
			if due {
				return
			}
		case "hint", "read", "pause":
			if due {
				t.Fatalf("more work before due root KILL: operations=%v", k.operations)
			}
		}
	}
	t.Fatalf("no due root KILL after crossing: operations=%v", k.operations)
}

// TestSyntheticExternalPendingRootPromotionPreparesFamily arms preparation on delayed binding.
//
// Example: a resolved cancellation anchor discovers a reversed deep family before TERM.
func TestSyntheticExternalPendingRootPromotionPreparesFamily(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 5, 300)
	g.pending[4101] = &pendingAcquisition{PID: 4101, DirectoryFD: 14101, Cancel: true}
	g.beginRetirement("guard error")
	retirement := g.retirement
	g.drive()
	receipt, code := g.finalize()
	targets := make(map[int]bool)
	for _, sent := range k.signals {
		targets[sent.target] = true
	}
	if len(targets) != 6 || !g.retirement.Equal(retirement) || g.receipt.CommandPID != 4101 || !g.rootDone || code != guardFailure {
		t.Fatalf("pending root promotion lost family/schedule: targets=%v receipt=%+v code=%d", targets, receipt, code)
	}
}

// newDriverPreparation constructs explicit finite synthetic phase state.
//
// Example: internal membership and expansion budgets remain observable without callbacks.
func newDriverPreparation(
	phase preparationPhase,
) *familyPreparation {
	return &familyPreparation{Snapshot: true, Phase: phase, Members: make(map[int]bool), Hinted: make(map[int]bool), Children: make(map[int][]int), Attempted: make(map[int]bool), Expanded: make(map[*custody]bool)}
}

// TestSyntheticPreparationMembershipBudgetHoldsTERM counts membership-only work.
//
// Example: a held TERM never advances appliedStage or restarts the shared clock.
func TestSyntheticPreparationMembershipBudgetHoldsTERM(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 1, 0)
	if !g.initialCancel() {
		t.Fatal("valid cancellation rejected")
	}
	p := newDriverPreparation(preparationMembers)
	for index := 0; index < 3*scanBudget; index++ {
		p.Entries = append(p.Entries, 8000+index)
	}
	g.preparation = p
	retirement := g.retirement
	g.retire()
	if g.appliedStage != retirementNone || len(k.signals) != 0 {
		t.Fatalf("held TERM advanced without signals: stage=%d signals=%v", g.appliedStage, k.signals)
	}
	g.prepareFamily()
	if p.At <= 0 || p.At > scanBudget || len(p.Members) != p.At || p.Phase != preparationMembers || len(k.signals) != 0 || g.appliedStage != retirementNone || !g.retirement.Equal(retirement) || k.clock.pauses != 0 {
		t.Fatalf("unbounded membership/artificial stage: at=%d members=%d phase=%d stage=%d signals=%v", p.At, len(p.Members), p.Phase, g.appliedStage, k.signals)
	}
	g.finalize()
}

// TestSyntheticPreparationExpansionBudgetCountsEmptyParents counts queued-parent work.
//
// Example: many childless proved parents cannot form an unbudgeted expansion loop.
func TestSyntheticPreparationExpansionBudgetCountsEmptyParents(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 1, 0)
	if !g.initialCancel() {
		t.Fatal("valid cancellation rejected")
	}
	p := newDriverPreparation(preparationFamily)
	for index := 0; index < 3*scanBudget; index++ {
		parent := &custody{ProcessIdentity: ProcessIdentity{PID: 8000 + index, Birth: 1}, FD: 30000 + index, Owned: true, State: processRunning}
		g.current[parent.PID], g.owned[parent.ProcessIdentity] = parent, parent
		p.Queue = append(p.Queue, parent)
	}
	g.preparation = p
	g.prepareFamily()
	if len(p.Expanded) == 0 || len(p.Expanded) > scanBudget || p.QueueAt > scanBudget || g.preparation == nil || len(k.signals) != 0 || g.appliedStage != retirementNone {
		t.Fatalf("unbounded empty-parent expansion: expanded=%d queueAt=%d stage=%d", len(p.Expanded), p.QueueAt, g.appliedStage)
	}
	g.finalize()
}

// TestSyntheticPreparationAcquisitionBudgetCountsCandidates bounds fresh authority work.
//
// Example: a wide external family takes multiple finite acquisition batches.
func TestSyntheticPreparationAcquisitionBudgetCountsCandidates(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 0, 0)
	if !g.initialCancel() {
		t.Fatal("valid cancellation rejected")
	}
	p := newDriverPreparation(preparationFamily)
	p.Parent = g.root
	p.Expanded[g.root] = true
	for index := 0; index < 3*scanBudget; index++ {
		pid := 8000 + index
		k.hints[pid] = processStat{ProcessIdentity: ProcessIdentity{PID: pid, Birth: 1}, Parent: 4101, State: "S"}
		p.Members[pid], p.Hinted[pid] = true, true
		p.Children[4101] = append(p.Children[4101], pid)
	}
	g.preparation = p
	before := k.pidfdCalls
	g.prepareFamily()
	calls := k.pidfdCalls - before
	if calls <= 0 || calls > scanBudget || p.ChildAt > scanBudget || len(p.Attempted) != calls || g.preparation == nil || len(k.signals) != 0 {
		t.Fatalf("unbounded candidate acquisition: calls=%d childAt=%d attempted=%d signals=%v", calls, p.ChildAt, len(p.Attempted), k.signals)
	}
	g.finalize()
}

// TestSyntheticPreparationIndexAndExpansionKillCrossingAbortServicesProvedRoot checks nonnative loops.
//
// Example: clock crossing inside index construction or parent expansion cannot delay due KILL.
func TestSyntheticPreparationIndexAndExpansionKillCrossingAbortServicesProvedRoot(t *testing.T) {
	for _, phase := range []preparationPhase{preparationMembers, preparationFamily} {
		g, k := newDriverControl()
		configureDriverExternalFamily(g, k, 0, 0)
		k.exitOnSend = false
		if !g.initialCancel() {
			t.Fatal("valid cancellation rejected")
		}
		p := newDriverPreparation(phase)
		for index := 0; index < 3*scanBudget; index++ {
			p.Entries = append(p.Entries, 8000+index)
			p.Queue = append(p.Queue, &custody{ProcessIdentity: ProcessIdentity{PID: 8000 + index, Birth: 1}, FD: 30000 + index, Owned: true, State: processRunning})
		}
		g.preparation = p
		retirement := g.retirement
		k.clock.advanceAt, k.clock.advance = k.clock.reads+10, time.Second
		g.prepareFamily()
		if g.preparation != nil || !g.familyUncertain || !g.retirement.Equal(retirement) || g.appliedStage != retirementKILL || len(k.signals) != 2 || k.signals[0].signal != syscall.SIGTERM || k.signals[1].signal != syscall.SIGKILL || p.At >= scanBudget || len(p.Expanded) >= scanBudget || k.clock.pauses != 0 {
			t.Fatalf("phase %d crossing skipped service/latch: at=%d expanded=%d stage=%d uncertain=%t signals=%v", phase, p.At, len(p.Expanded), g.appliedStage, g.familyUncertain, k.signals)
		}
		assertDriverDueRetirementBeforeWork(t, k)
		g.finalize()
	}
}

// TestSyntheticPreparationUncertaintyNeverBecomesSettlement preserves failed initial discovery.
//
// Example: later empty custody and repeated ECHILD cannot erase unknown family membership.
func TestSyntheticPreparationUncertaintyNeverBecomesSettlement(t *testing.T) {
	for _, scenario := range []string{"snapshot", "hint", "anchored-A", "parent-readiness"} {
		g, k := newDriverControl()
		configureDriverExternalFamily(g, k, 1, 0)
		switch scenario {
		case "snapshot":
			k.entryErrors = []error{unix.EIO}
		case "hint":
			k.hintErrors[4102] = unix.EIO
		case "anchored-A":
			k.metadataErrors = []error{nil, nil, nil, unix.ENOENT}
		case "parent-readiness":
			k.polls[24101] = processUnknown
		}
		if !g.initialCancel() {
			t.Fatalf("root binding rejected in %s", scenario)
		}
		retirement := g.retirement
		g.drive()
		receipt, code := g.finalize()
		if code != guardFailure || receipt.HostChildrenSettled || !g.familyUncertain || !g.retirement.Equal(retirement) || len(receipt.Errors) == 0 {
			t.Fatalf("%s uncertainty erased: code=%d receipt=%+v uncertain=%t", scenario, code, receipt, g.familyUncertain)
		}
		rootTERM := 0
		for _, sent := range k.signals {
			if sent.target != 24101 {
				t.Fatalf("%s unproved candidate signaled: %+v", scenario, sent)
			}
			if sent.signal == syscall.SIGTERM {
				rootTERM++
			}
		}
		if rootTERM != 1 || len(g.current) != 0 || len(g.pending) != 0 || len(g.tentative) != 0 || k.waitCalls < 2 {
			t.Fatalf("%s cleanup/empty-native control missing: term=%d current=%d pending=%d tentative=%d waits=%d", scenario, rootTERM, len(g.current), len(g.pending), len(g.tentative), k.waitCalls)
		}
	}
}

// TestSyntheticPreparationHintBudgetCountsEveryEntry bounds adjacency-index collection.
//
// Example: unrelated hints still consume finite work while TERM remains held.
func TestSyntheticPreparationHintBudgetCountsEveryEntry(t *testing.T) {
	g, k := newDriverControl()
	configureDriverExternalFamily(g, k, 0, 0)
	if !g.initialCancel() {
		t.Fatal("valid cancellation rejected")
	}
	p := newDriverPreparation(preparationHints)
	for index := 0; index < 3*scanBudget; index++ {
		pid := 8000 + index
		p.Entries = append(p.Entries, pid)
		p.Members[pid] = true
	}
	g.preparation = p
	retirement := g.retirement
	g.prepareFamily()
	if p.At <= 0 || p.At > scanBudget || k.hintCalls != p.At || len(p.Hinted) != p.At || g.preparation == nil || len(k.signals) != 0 || g.appliedStage != retirementNone || !g.retirement.Equal(retirement) {
		t.Fatalf("unbounded hint collection/artificial stage: at=%d hints=%d stage=%d signals=%v", p.At, k.hintCalls, g.appliedStage, k.signals)
	}
	g.finalize()
}
