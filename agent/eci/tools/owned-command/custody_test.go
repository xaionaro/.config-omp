//go:build linux

package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// syntheticFDResult scripts descriptor acquisition without native process effects.
//
// Example: ESRCH supplies explicit prebinding absence.
type syntheticFDResult struct {
	FD    int
	Error error
}

// syntheticPollResult scripts kernel lifetime observations independently of birth ticks.
//
// Example: two equal identities can have exited and running descriptors.
type syntheticPollResult struct {
	State processState
	Error error
}

// syntheticSignal records attempted descriptor authority.
//
// Example: tentative descriptors must never appear here.
type syntheticSignal struct {
	FD     int
	Signal syscall.Signal
}

// syntheticKernel consumes concrete scripted results and records primitive order.
//
// Example: repeated ReadStat calls expose whether A follows pidfd opening.
type syntheticKernel struct {
	StatReads        []metadataRead
	PIDFDResults     []syntheticFDResult
	DirectoryResults []syntheticFDResult
	PollResults      map[int][]syntheticPollResult
	RunningFDs       map[int]bool
	Calls            []string
	Closed           []int
	Signals          []syntheticSignal
	StrictPoll       bool
}

// Childless provides a synthetic construction baseline.
//
// Example: custody tests never inspect native children.
func (*syntheticKernel) Childless() error { return nil }

// Namespace provides a synthetic supported profile.
//
// Example: namespace parser controls use their own supplied text.
func (*syntheticKernel) Namespace() error { return nil }

// Subreaper performs no native adoption.
//
// Example: tests cannot change the host process tree.
func (*syntheticKernel) Subreaper() error { return nil }

// Entries supplies an empty synthetic scan.
//
// Example: acquisition tests directly provide their anchored candidate.
func (*syntheticKernel) Entries() ([]int, error) { return nil, nil }

// Hint rejects accidental unanchored authority in this fixture.
//
// Example: ancestry tests supply distinct B and A metadata.
func (*syntheticKernel) Hint(pid int) (processStat, error) { panic("unexpected synthetic hint") }

// OpenDirectory consumes one scripted anchor.
//
// Example: a hidden target returns ENOENT.
func (k *syntheticKernel) OpenDirectory(pid int) (int, error) {
	k.Calls = append(k.Calls, fmt.Sprintf("directory:%d", pid))
	if len(k.DirectoryResults) == 0 {
		panic("unscripted directory")
	}
	result := k.DirectoryResults[0]
	k.DirectoryResults = k.DirectoryResults[1:]
	return result.FD, result.Error
}

// ReadStat consumes a fresh metadata result through the given anchor.
//
// Example: B and A each record the same directory descriptor.
func (k *syntheticKernel) ReadStat(
	directoryFD int,
	pid int,
) metadataRead {
	k.Calls = append(k.Calls, fmt.Sprintf("stat:%d:%d", directoryFD, pid))
	if len(k.StatReads) == 0 {
		panic("unscripted stat")
	}
	result := k.StatReads[0]
	k.StatReads = k.StatReads[1:]
	return result
}

// OpenPIDFD consumes one unbound descriptor result.
//
// Example: a successful hidden target descriptor is closed without signaling.
func (k *syntheticKernel) OpenPIDFD(pid int) (int, error) {
	k.Calls = append(k.Calls, fmt.Sprintf("pidfd:%d", pid))
	if len(k.PIDFDResults) == 0 {
		panic("unscripted pidfd")
	}
	result := k.PIDFDResults[0]
	k.PIDFDResults = k.PIDFDResults[1:]
	return result.FD, result.Error
}

// Poll consumes explicit readiness or a declared stable-running descriptor.
//
// Example: an exited parent provides no fresh ancestry authority.
func (k *syntheticKernel) Poll(fd int) (processState, error) {
	k.Calls = append(k.Calls, fmt.Sprintf("poll:%d", fd))
	results := k.PollResults[fd]
	if len(results) == 0 {
		if k.StrictPoll || !k.RunningFDs[fd] {
			panic(fmt.Sprintf("unscripted poll fd %d", fd))
		}
		return processRunning, nil
	}
	result := results[0]
	k.PollResults[fd] = results[1:]
	return result.State, result.Error
}

// Send records synthetic retirement attempts.
//
// Example: an owned candidate receives TERM through its retained fd.
func (k *syntheticKernel) Send(
	fd int,
	sig syscall.Signal,
) error {
	k.Signals = append(k.Signals, syntheticSignal{FD: fd, Signal: sig})
	return nil
}

// Wait supplies no consuming native process operation.
//
// Example: custody tests do not prove native reaping.
func (*syntheticKernel) Wait() nativeResult { return nativeResult{Error: unix.ECHILD} }

// Close records each descriptor retirement without touching the host.
//
// Example: an unresolved directory must be absent from Closed.
func (k *syntheticKernel) Close(fd int) error { k.Closed = append(k.Closed, fd); return nil }

// syntheticClock holds a controlled clock with no wall-time dependency.
//
// Example: retirement stays in TERM until a test advances Time.
type syntheticClock struct{ Time time.Time }

// Now reads the scripted time.
//
// Example: receipt events have deterministic timestamps.
func (c *syntheticClock) Now() time.Time { return c.Time }

// Pause advances the scripted observation interval.
//
// Example: a driver can progress without sleeping.
func (c *syntheticClock) Pause(duration time.Duration) { c.Time = c.Time.Add(duration) }

// custodyGuard creates synthetic cancellation-mode custody without guard-parent shortcuts.
//
// Example: parent PID 10 must pass its retained descriptor poll.
func custodyGuard(k *syntheticKernel) *guard {
	return newGuard(k, &syntheticClock{Time: time.Unix(100, 0)}, nil, time.Second, time.Second, ProcessIdentity{PID: 20, Birth: 2})
}

// custodyStat builds a valid synthetic process metadata value.
//
// Example: A can change Parent while preserving its anchored identity.
func custodyStat(
	pid int,
	birth uint64,
	parent int,
) processStat {
	return processStat{ProcessIdentity: ProcessIdentity{PID: pid, Birth: birth}, Parent: parent, State: "R"}
}

// custodyParent publishes a preexisting owned parent without acquisition side effects.
//
// Example: its fd 30 predates the child's fresh A read.
func custodyParent(g *guard) *custody {
	kernel := g.kernel.(*syntheticKernel)
	if kernel.RunningFDs == nil {
		kernel.RunningFDs = make(map[int]bool)
	}
	kernel.RunningFDs[30] = true
	value := &custody{ProcessIdentity: ProcessIdentity{PID: 10, Birth: 1}, FD: 30, Owned: true, State: processRunning}
	g.current[10], g.owned[value.ProcessIdentity] = value, value
	return value
}

// TestSyntheticAnchoredOrdering checks both binding order and fresh foreign-parent rejection.
//
// Example: cached B Parent 10 cannot authorize A Parent 99.
func TestSyntheticAnchoredOrdering(t *testing.T) {
	for _, parents := range [][2]int{{10, 10}, {10, 99}, {99, 10}} {
		parent := parents[1]
		k := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, parents[0])}, {Stat: custodyStat(20, 2, parent)}}, PIDFDResults: []syntheticFDResult{{FD: 40}}}
		g := custodyGuard(k)
		custodyParent(g)
		value, absent, rejected := g.acquire(&pendingAcquisition{PID: 20, DirectoryFD: 50}, g.parents())
		want := []string{"stat:50:20", "pidfd:20", "stat:50:20"}
		if parent == 10 {
			want = append(want, "poll:30")
		}
		if !reflect.DeepEqual(k.Calls, want) || absent || rejected {
			t.Fatalf("parent=%d calls=%v absent=%t rejected=%t", parent, k.Calls, absent, rejected)
		}
		if (value != nil) != (parent == 10) || (g.current[20] != nil) != (parent == 10) {
			t.Fatalf("fresh parent=%d value=%+v current=%+v", parent, value, g.current)
		}
		g.beginRetirement("test")
		g.retire()
		signaled := false
		for _, signal := range k.Signals {
			if signal.FD == 40 && signal.Signal == syscall.SIGTERM {
				signaled = true
			}
			if signal.FD == 40 && parent == 99 {
				t.Fatal("foreign candidate signaled")
			}
		}
		if signaled != (parent == 10) {
			t.Fatalf("fresh parent=%d signaled=%t", parent, signaled)
		}
		if parent == 99 && !reflect.DeepEqual(k.Closed, []int{40, 50}) {
			t.Fatalf("foreign cleanup=%v", k.Closed)
		}
	}
}

// TestSyntheticCancellationMismatches rejects initial B and A birth mismatches without authority.
//
// Example: an A mismatch closes both scratch slots and never sends TERM.
func TestSyntheticCancellationMismatches(t *testing.T) {
	for _, before := range []bool{true, false} {
		b, a := custodyStat(20, 2, 99), custodyStat(20, 3, 99)
		if before {
			b.Birth = 3
		}
		k := &syntheticKernel{StatReads: []metadataRead{{Stat: b}, {Stat: a}}, PIDFDResults: []syntheticFDResult{{FD: 40}}}
		g := custodyGuard(k)
		value, absent, rejected := g.acquire(&pendingAcquisition{PID: 20, DirectoryFD: 50, Cancel: true}, g.parents())
		g.retire()
		if value != nil || absent || !rejected || len(g.current) != 0 || len(g.pending) != 0 || len(k.Signals) != 0 || len(g.receipt.Errors) == 0 {
			t.Fatalf("before=%t value=%+v guard=%+v signals=%v", before, value, g, k.Signals)
		}
		want := []int{40, 50}
		if before {
			want = []int{50}
		}
		if !reflect.DeepEqual(k.Closed, want) {
			t.Fatalf("before=%t closed=%v", before, k.Closed)
		}
	}
}

// TestSyntheticHiddenCancellation distinguishes hidden proc visibility from native absence.
//
// Example: ENOENT followed by successful pidfd keeps rootDone false.
func TestSyntheticHiddenCancellation(t *testing.T) {
	for _, absent := range []bool{false, true} {
		result := syntheticFDResult{FD: 40}
		if absent {
			result.Error = unix.ESRCH
		}
		k := &syntheticKernel{DirectoryResults: []syntheticFDResult{{FD: -1, Error: unix.ENOENT}}, PIDFDResults: []syntheticFDResult{result}}
		g := custodyGuard(k)
		g.initialCancel()
		g.retire()
		if g.rootDone != absent || len(k.Signals) != 0 || (len(g.receipt.Errors) == 0) != absent {
			t.Fatalf("absent=%t rootDone=%t errors=%v signals=%v", absent, g.rootDone, g.receipt.Errors, k.Signals)
		}
		if !absent && !reflect.DeepEqual(k.Closed, []int{40}) {
			t.Fatalf("hidden fd cleanup=%v", k.Closed)
		}
	}
}

// TestSyntheticPendingVisibility preserves its original anchor until coherent native absence.
//
// Example: repeated ENOENT and successful pidfd cannot settle a pending candidate.
func TestSyntheticPendingVisibility(t *testing.T) {
	for _, absent := range []bool{false, true} {
		result := syntheticFDResult{FD: 40}
		if absent {
			result.Error = unix.ESRCH
		}
		k := &syntheticKernel{StatReads: []metadataRead{{InspectionError: unix.ENOENT}}, PIDFDResults: []syntheticFDResult{result}}
		g := custodyGuard(k)
		pending := &pendingAcquisition{PID: 20, DirectoryFD: 50}
		value, resolved, rejected := g.acquire(pending, g.parents())
		if value != nil || rejected || resolved != absent || (g.pending[20] == pending) == absent || len(k.Signals) != 0 {
			t.Fatalf("absent=%t resolved=%t pending=%v", absent, resolved, g.pending)
		}
		want := []int{40}
		if absent {
			want = []int{50}
		}
		if !reflect.DeepEqual(k.Closed, want) {
			t.Fatalf("absent=%t cleanup=%v", absent, k.Closed)
		}
	}
}

// TestSyntheticCandidateAUncertainty retains its anchor and closes the unbound descriptor.
//
// Example: post-open identity mismatch cannot authorize signaling.
func TestSyntheticCandidateAUncertainty(t *testing.T) {
	for _, failure := range []metadataRead{{Stat: custodyStat(20, 3, 10)}, {InspectionError: unix.ENOENT}, {InspectionError: unix.EIO}} {
		k := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, failure}, PIDFDResults: []syntheticFDResult{{FD: 40}}}
		g := custodyGuard(k)
		custodyParent(g)
		pending := &pendingAcquisition{PID: 20, DirectoryFD: 50}
		value, absent, rejected := g.acquire(pending, g.parents())
		g.retire()
		if value != nil || absent || rejected || g.pending[20] != pending || pending.DirectoryFD != 50 || !reflect.DeepEqual(k.Closed, []int{40}) {
			t.Fatalf("failure=%+v pending=%+v closed=%v", failure, pending, k.Closed)
		}
		for _, signal := range k.Signals {
			if signal.FD == 40 {
				t.Fatal("unbound candidate signaled")
			}
		}
	}
}

// TestSyntheticStatClosePreservesCustody keeps a valid A despite its cleanup error.
//
// Example: close failure enters retirement through the still-owned bound pidfd.
func TestSyntheticStatClosePreservesCustody(t *testing.T) {
	k := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, {Stat: custodyStat(20, 2, 10), CloseError: unix.EIO}}, PIDFDResults: []syntheticFDResult{{FD: 40}}}
	g := custodyGuard(k)
	custodyParent(g)
	value, absent, rejected := g.acquire(&pendingAcquisition{PID: 20, DirectoryFD: 50}, g.parents())
	g.retire()
	if value == nil || !value.Owned || g.current[20] != value || absent || rejected || len(g.receipt.Errors) != 1 {
		t.Fatalf("value=%+v errors=%v", value, g.receipt.Errors)
	}
	if !reflect.DeepEqual(k.Closed, []int{50}) {
		t.Fatalf("bound pidfd discarded: %v", k.Closed)
	}
	found := false
	for _, signal := range k.Signals {
		if signal.FD == 40 && signal.Signal == syscall.SIGTERM {
			found = true
		}
	}
	if !found {
		t.Fatal("valid custody abandoned after stat close error")
	}
}

// TestSyntheticParentUncertaintyRetainsTentative avoids authority from exited or unknown parents.
//
// Example: fresh A under an exited parent is bound but cannot receive TERM.
func TestSyntheticParentUncertaintyRetainsTentative(t *testing.T) {
	for _, state := range []processState{processExited, processUnknown} {
		k := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, {Stat: custodyStat(20, 2, 10)}}, PIDFDResults: []syntheticFDResult{{FD: 40}}, PollResults: map[int][]syntheticPollResult{30: {{State: state}}}}
		g := custodyGuard(k)
		custodyParent(g)
		value, absent, rejected := g.acquire(&pendingAcquisition{PID: 20, DirectoryFD: 50}, g.parents())
		g.beginRetirement("test")
		g.retire()
		if value == nil || value.Owned || absent || rejected || g.tentative[20] != value || g.current[20] != nil {
			t.Fatalf("state=%d value=%+v tentative=%v", state, value, g.tentative)
		}
		for _, signal := range k.Signals {
			if signal.FD == 40 {
				t.Fatal("tentative candidate signaled")
			}
		}
		if !reflect.DeepEqual(k.Closed, []int{50}) {
			t.Fatalf("tentative custody discarded: %v", k.Closed)
		}
	}
}

// TestSyntheticSameTickPublication separates lifetime observation from metadata equality.
//
// Example: exited old fd allows a new identical PID/Birth slot.
func TestSyntheticSameTickPublication(t *testing.T) {
	for _, state := range []processState{processExited, processRunning, processUnknown} {
		k := &syntheticKernel{PollResults: map[int][]syntheticPollResult{30: {{State: state}}}}
		g := custodyGuard(k)
		old := custodyParent(g)
		value := &custody{ProcessIdentity: old.ProcessIdentity, FD: 40, Owned: true}
		g.publish(value)
		switch state {
		case processExited:
			if g.current[10] != value || g.owned[value.ProcessIdentity] != value || old.FD != -1 || !reflect.DeepEqual(k.Closed, []int{30}) {
				t.Fatalf("replacement failed: old=%+v new=%+v closed=%v", old, value, k.Closed)
			}
		case processRunning:
			if g.current[10] != old || value.FD != -1 || !reflect.DeepEqual(k.Closed, []int{40}) {
				t.Fatalf("duplicate failed: old=%+v new=%+v closed=%v", old, value, k.Closed)
			}
		case processUnknown:
			if g.current[10] != old || g.tentative[10] != value || value.Owned || len(k.Closed) != 0 {
				t.Fatalf("unknown overwritten: old=%+v new=%+v", old, value)
			}
		}
	}
}

// TestSyntheticTentativeForeignResolution closes a freshly foreign candidate without signals.
//
// Example: a later A Parent 99 resolves previous ancestry ambiguity.
func TestSyntheticTentativeForeignResolution(t *testing.T) {
	k := &syntheticKernel{DirectoryResults: []syntheticFDResult{{FD: 50}}, StatReads: []metadataRead{{Stat: custodyStat(20, 2, 99)}}, RunningFDs: map[int]bool{40: true}}
	g := custodyGuard(k)
	value := &custody{ProcessIdentity: ProcessIdentity{PID: 20, Birth: 2}, FD: 40}
	g.tentative[20] = value
	g.pollCustody()
	g.beginRetirement("test")
	g.retire()
	if len(g.tentative) != 0 || len(k.Signals) != 0 || !reflect.DeepEqual(k.Closed, []int{40, 50}) {
		t.Fatalf("tentative=%v signals=%v closed=%v", g.tentative, k.Signals, k.Closed)
	}
	if !reflect.DeepEqual(k.Calls, []string{"poll:40", "directory:20", "stat:50:20", "poll:40"}) {
		t.Fatalf("fresh candidate order=%v", k.Calls)
	}
}

// TestSyntheticNamespaceValidation rejects unsupported numeric namespace views.
//
// Example: a nested NStgid list cannot establish PID coherence.
func TestSyntheticNamespaceValidation(t *testing.T) {
	for _, text := range []string{"NStgid:\t20\n", "NStgid:\t19\n", "NStgid:\t20 1\n", "Name:\tguard\n", "NStgid:\t20\nNStgid:\t20\n", "NStgid:\tbad\n"} {
		err := validateNamespace([]byte(text), 20)
		if (err == nil) != (text == "NStgid:\t20\n") {
			t.Fatalf("text=%q error=%v", text, err)
		}
	}
}

// TestSyntheticStatValidation checks PID, comm, parent, state and birth token boundaries.
//
// Example: embedded closing parentheses in comm do not truncate required fields.
func TestSyntheticStatValidation(t *testing.T) {
	valid := "20 (name with ) boundary) R 10 " + strings.Repeat("0 ", 17) + "2"
	stat, err := parseProcess([]byte(valid), 20)
	if err != nil || stat.ProcessIdentity != (ProcessIdentity{PID: 20, Birth: 2}) || stat.Parent != 10 {
		t.Fatalf("valid stat=%+v error=%v", stat, err)
	}
	for _, invalid := range []string{strings.Replace(valid, "20 (", "21 (", 1), strings.Replace(valid, ") R ", ") ? ", 1), strings.Replace(valid, " R 10 ", " R -1 ", 1), "20 (bad R 10 2", strings.TrimSuffix(valid, "2") + "bad", "20 (short) R 10 2"} {
		if _, err := parseProcess([]byte(invalid), 20); err == nil {
			t.Fatalf("accepted malformed stat %q", invalid)
		}
	}
}

// TestSyntheticAcquisitionErrorsRetainAnchor preserves unresolved B and descriptor-open failures.
//
// Example: an unexpected B error followed by a successful pidfd retains the original directory.
func TestSyntheticAcquisitionErrorsRetainAnchor(t *testing.T) {
	for _, beforeError := range []bool{true, false} {
		metadata := metadataRead{Stat: custodyStat(20, 2, 10)}
		descriptor := syntheticFDResult{FD: -1, Error: unix.EIO}
		if beforeError {
			metadata.InspectionError = unix.EIO
			descriptor = syntheticFDResult{FD: 40}
		}
		k := &syntheticKernel{StatReads: []metadataRead{metadata}, PIDFDResults: []syntheticFDResult{descriptor}, StrictPoll: true}
		g := custodyGuard(k)
		pending := &pendingAcquisition{PID: 20, DirectoryFD: 50}
		value, absent, rejected := g.acquire(pending, g.parents())
		g.retire()
		if value != nil || absent || rejected || g.pending[20] != pending || pending.DirectoryFD != 50 || len(g.receipt.Errors) != 1 || len(k.Signals) != 0 {
			t.Fatalf("B error=%t value=%+v pending=%+v errors=%v signals=%v", beforeError, value, pending, g.receipt.Errors, k.Signals)
		}
		want := []int{}
		if beforeError {
			want = []int{40}
		}
		if len(k.Closed) != len(want) || (len(want) != 0 && !reflect.DeepEqual(k.Closed, want)) {
			t.Fatalf("B error=%t closed=%v", beforeError, k.Closed)
		}
		if !reflect.DeepEqual(k.Calls, []string{"stat:50:20", "pidfd:20"}) {
			t.Fatalf("B error=%t calls=%v", beforeError, k.Calls)
		}
		g.finalize()
		if k.Closed[len(k.Closed)-1] != 50 {
			t.Fatalf("pending anchor not finalized: %v", k.Closed)
		}
	}
}

// TestSyntheticAcquisitionFailureContext distinguishes current anchored B from stale hints.
//
// Example: a failed current B reports birth zero even when an earlier hint had a birth.
func TestSyntheticAcquisitionFailureContext(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		directory int
		reads     []metadataRead
		pidfd     syntheticFDResult
		actions   []string
		birth     uint64
		closed    []int
		pending   bool
		rejected  bool
		nonCancel bool
	}{
		{name: "unavailable anchor", directory: -1, pidfd: syntheticFDResult{FD: -1, Error: unix.EINVAL}, actions: []string{"resolve unavailable proc lifetime"}, pending: true},
		{name: "failed current B", directory: 50, reads: []metadataRead{{InspectionError: unix.EIO}}, pidfd: syntheticFDResult{FD: -1, Error: unix.EINVAL}, actions: []string{"read anchored B", "pidfd after unavailable anchored stat"}, pending: true},
		{name: "pidfd after valid B", directory: 50, reads: []metadataRead{{Stat: custodyStat(20, 2, 10)}}, pidfd: syntheticFDResult{FD: -1, Error: unix.EINVAL}, actions: []string{"open pidfd"}, birth: 2, pending: true},
		{name: "failed A", directory: 50, reads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, {InspectionError: unix.EIO}}, pidfd: syntheticFDResult{FD: 40}, actions: []string{"read anchored A"}, birth: 2, closed: []int{40}, pending: true},
		{name: "mismatched B", directory: 50, reads: []metadataRead{{Stat: custodyStat(20, 3, 10)}}, actions: []string{"cancel identity rejected"}, birth: 3, closed: []int{50}, rejected: true},
		{name: "mismatched A", directory: 50, reads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, {Stat: custodyStat(20, 3, 10)}}, pidfd: syntheticFDResult{FD: 40}, actions: []string{"cancel identity rejected"}, birth: 2, closed: []int{40, 50}, rejected: true},
		{name: "changed lifetime", directory: 50, reads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, {Stat: custodyStat(20, 3, 10)}}, pidfd: syntheticFDResult{FD: 40}, actions: []string{"anchored identity changed"}, birth: 2, closed: []int{40}, pending: true, nonCancel: true},
	} {
		for _, priorCause := range []string{"", "signal: terminated"} {
			t.Run(testCase.name+"/"+priorCause, func(t *testing.T) {
				k := &syntheticKernel{StatReads: testCase.reads, PIDFDResults: []syntheticFDResult{testCase.pidfd}, StrictPoll: true}
				g := custodyGuard(k)
				if priorCause != "" {
					g.beginRetirement(priorCause)
				}
				retirement := g.clock.Now()
				g.clock.(*syntheticClock).Time = retirement.Add(time.Millisecond)
				pending := &pendingAcquisition{PID: 20, DirectoryFD: testCase.directory, Before: custodyStat(20, 999, 10), Cancel: !testCase.nonCancel}
				value, absent, rejected := g.acquire(pending, g.parents())
				if value != nil || absent || rejected != testCase.rejected || (g.pending[20] == pending) != testCase.pending || len(k.Signals) != 0 || !reflect.DeepEqual(k.Closed, testCase.closed) {
					t.Fatalf("custody changed: value=%+v absent=%t rejected=%t pending=%v signals=%v closed=%v", value, absent, rejected, g.pending, k.Signals, k.Closed)
				}
				if len(g.receipt.Errors) != len(testCase.actions) || len(g.receipt.Events) != len(testCase.actions) {
					t.Fatalf("error counts: %+v", g.receipt)
				}
				for index, action := range testCase.actions {
					event := g.receipt.Events[index]
					context := fmt.Sprintf("attempted PID %d anchored-before birth %d:", 20, testCase.birth)
					if event.PID != 20 || event.Birth != testCase.birth || event.Action != action || !strings.Contains(event.Error, context) || g.receipt.Errors[index] != action+": "+event.Error {
						t.Errorf("missing acquisition context: event=%+v error=%q", event, g.receipt.Errors[index])
					}
				}
				if testCase.pidfd.Error != nil && !strings.Contains(g.receipt.Errors[len(g.receipt.Errors)-1], testCase.pidfd.Error.Error()) {
					t.Error("original pidfd error lost")
				}
				wantCause := priorCause
				if wantCause == "" {
					wantCause = "guard error"
					retirement = g.clock.Now()
				}
				if g.receipt.Cause != wantCause || !g.retirement.Equal(retirement) {
					t.Fatalf("first retirement changed: cause=%q time=%v", g.receipt.Cause, g.retirement)
				}
				_, code := g.finalize()
				if code != guardFailure || len(k.Signals) != 0 || (testCase.pending && testCase.directory >= 0 && k.Closed[len(k.Closed)-1] != 50) {
					t.Fatalf("final classification/custody changed: code=%d closed=%v signals=%v", code, k.Closed, k.Signals)
				}
			})
		}
	}
}

// TestSyntheticCancellationFailureContext never treats requested birth as observed birth.
//
// Example: a hidden cancellation target reports its attempted PID and unavailable birth.
func TestSyntheticCancellationFailureContext(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		directory error
		pidfd     syntheticFDResult
		action    string
		closed    []int
	}{
		{name: "directory", directory: unix.EIO, action: "open cancellation proc directory"},
		{name: "hidden", directory: unix.ENOENT, pidfd: syntheticFDResult{FD: 40}, action: "cancellation target unknown", closed: []int{40}},
		{name: "pidfd", directory: unix.ENOENT, pidfd: syntheticFDResult{FD: -1, Error: unix.EINVAL}, action: "open cancellation pidfd"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			k := &syntheticKernel{DirectoryResults: []syntheticFDResult{{FD: -1, Error: testCase.directory}}, PIDFDResults: []syntheticFDResult{testCase.pidfd}, StrictPoll: true}
			g := custodyGuard(k)
			if g.initialCancel() || len(g.receipt.Events) != 1 || len(g.receipt.Errors) != 1 || len(k.Signals) != 0 || !reflect.DeepEqual(k.Closed, testCase.closed) {
				t.Fatalf("cancellation behavior changed: receipt=%+v signals=%v closed=%v", g.receipt, k.Signals, k.Closed)
			}
			event := g.receipt.Events[0]
			if event.PID != 20 || event.Birth != 0 || event.Action != testCase.action || !strings.Contains(event.Error, "attempted PID 20 anchored-before birth 0:") {
				t.Fatalf("missing cancellation context: %+v", event)
			}
		})
	}
}

// TestSyntheticUnavailableAnchorPending distinguishes a hidden live PID from native ESRCH.
//
// Example: DirectoryFD -1 never grants signal authority to a successful temporary descriptor.
func TestSyntheticUnavailableAnchorPending(t *testing.T) {
	for _, absent := range []bool{false, true} {
		result := syntheticFDResult{FD: 40}
		if absent {
			result.Error = unix.ESRCH
		}
		k := &syntheticKernel{PIDFDResults: []syntheticFDResult{result}, StrictPoll: true}
		g := custodyGuard(k)
		pending := &pendingAcquisition{PID: 20, DirectoryFD: -1, Before: custodyStat(20, 2, 10)}
		value, resolved, rejected := g.acquire(pending, g.parents())
		g.beginRetirement("test")
		g.retire()
		if value != nil || rejected || resolved != absent || (g.pending[20] == pending) == absent || len(k.Signals) != 0 || len(g.receipt.Errors) != 0 {
			t.Fatalf("absent=%t resolved=%t pending=%v errors=%v signals=%v", absent, resolved, g.pending, g.receipt.Errors, k.Signals)
		}
		if absent && len(k.Closed) != 0 {
			t.Fatalf("ESRCH closed invalid descriptor: %v", k.Closed)
		}
		if !absent && !reflect.DeepEqual(k.Closed, []int{40}) {
			t.Fatalf("hidden temporary descriptor leaked: %v", k.Closed)
		}
	}
}

// TestSyntheticCancellationInspectionErrorCannotBecomeAbsent preserves historical acquisition errors.
//
// Example: malformed B followed by ESRCH cannot select already-absent success.
func TestSyntheticCancellationInspectionErrorCannotBecomeAbsent(t *testing.T) {
	for _, inspectionError := range []error{unix.EIO, errors.New("malformed anchored stat")} {
		k := &syntheticKernel{DirectoryResults: []syntheticFDResult{{FD: 50}}, StatReads: []metadataRead{{InspectionError: inspectionError}}, PIDFDResults: []syntheticFDResult{{FD: -1, Error: unix.ESRCH}}, StrictPoll: true}
		g := custodyGuard(k)
		g.initialCancel()
		g.retire()
		receipt, code := g.finalize()
		if g.rootDone || code != guardFailure || receipt.Cause == "already absent" || receipt.HostChildrenSettled || len(receipt.Errors) == 0 || len(k.Signals) != 0 {
			t.Fatalf("inspection=%v receipt=%+v code=%d signals=%v", inspectionError, receipt, code, k.Signals)
		}
		if !reflect.DeepEqual(k.Closed, []int{50}) || len(g.current) != 0 || len(g.pending) != 0 {
			t.Fatalf("inspection=%v closed=%v current=%v pending=%v", inspectionError, k.Closed, g.current, g.pending)
		}
	}
}

// TestSyntheticTentativeBirthMismatchRetainsLifetime refuses authority from changed metadata.
//
// Example: a fresh numeric identity with another birth cannot replace the retained tentative fd.
func TestSyntheticTentativeBirthMismatchRetainsLifetime(t *testing.T) {
	k := &syntheticKernel{DirectoryResults: []syntheticFDResult{{FD: 50}}, StatReads: []metadataRead{{Stat: custodyStat(20, 3, 10)}}, PollResults: map[int][]syntheticPollResult{40: {{State: processRunning}}}, StrictPoll: true}
	g := custodyGuard(k)
	value := &custody{ProcessIdentity: ProcessIdentity{PID: 20, Birth: 2}, FD: 40}
	g.tentative[20] = value
	complete := g.pollCustody()
	g.beginRetirement("test")
	g.retire()
	if complete || g.tentative[20] != value || value.Owned || value.FD != 40 || len(g.current) != 0 || len(k.Signals) != 0 || !reflect.DeepEqual(k.Closed, []int{50}) {
		t.Fatalf("complete=%t tentative=%v value=%+v signals=%v closed=%v", complete, g.tentative, value, k.Signals, k.Closed)
	}
	g.finalize()
	if !reflect.DeepEqual(k.Closed, []int{50, 40}) {
		t.Fatalf("retained tentative final cleanup=%v", k.Closed)
	}
}

// TestSyntheticParentPollErrorsHaveNoAuthority retains tentative custody for interrupted and failed polls.
//
// Example: EINTR yields uncertainty without fabricating an ancestry edge or recording a permanent error.
func TestSyntheticParentPollErrorsHaveNoAuthority(t *testing.T) {
	for _, pollError := range []error{unix.EINTR, unix.EIO} {
		k := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, {Stat: custodyStat(20, 2, 10)}}, PIDFDResults: []syntheticFDResult{{FD: 40}}, PollResults: map[int][]syntheticPollResult{30: {{State: processRunning, Error: pollError}}}, StrictPoll: true}
		g := custodyGuard(k)
		custodyParent(g)
		value, absent, rejected := g.acquire(&pendingAcquisition{PID: 20, DirectoryFD: 50}, g.parents())
		g.beginRetirement("test")
		g.retire()
		if value == nil || value.Owned || absent || rejected || g.tentative[20] != value || (len(g.receipt.Errors) == 0) != errors.Is(pollError, unix.EINTR) {
			t.Fatalf("poll=%v value=%+v errors=%v", pollError, value, g.receipt.Errors)
		}
		for _, signal := range k.Signals {
			if signal.FD == 40 {
				t.Fatal("parent poll error granted child authority")
			}
		}
		if !reflect.DeepEqual(k.Closed, []int{50}) {
			t.Fatalf("tentative lost on poll=%v closed=%v", pollError, k.Closed)
		}
	}
}

// TestSyntheticTentativeExitResolvesWithoutMetadata uses retained readiness independently of proc visibility.
//
// Example: confirmed candidate exit closes its descriptor without opening a directory or sending a signal.
func TestSyntheticTentativeExitResolvesWithoutMetadata(t *testing.T) {
	k := &syntheticKernel{PollResults: map[int][]syntheticPollResult{40: {{State: processExited}}}, StrictPoll: true}
	g := custodyGuard(k)
	value := &custody{ProcessIdentity: ProcessIdentity{PID: 20, Birth: 2}, FD: 40}
	g.tentative[20] = value
	complete := g.pollCustody()
	g.beginRetirement("test")
	g.retire()
	g.finalize()
	if !complete || len(g.tentative) != 0 || value.FD != -1 || len(k.Signals) != 0 || !reflect.DeepEqual(k.Closed, []int{40}) || !reflect.DeepEqual(k.Calls, []string{"poll:40"}) {
		t.Fatalf("complete=%t value=%+v closed=%v calls=%v signals=%v", complete, value, k.Closed, k.Calls, k.Signals)
	}
}

// TestSyntheticExitedUnboundCandidateStillPending refuses exit authority before anchored mapping exists.
//
// Example: candidate disappearance during A retains the old directory despite an exit-ready unbound fd.
func TestSyntheticExitedUnboundCandidateStillPending(t *testing.T) {
	k := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, 10)}, {InspectionError: unix.ESRCH}}, PIDFDResults: []syntheticFDResult{{FD: 40}}, PollResults: map[int][]syntheticPollResult{40: {{State: processExited}}}, StrictPoll: true}
	g := custodyGuard(k)
	pending := &pendingAcquisition{PID: 20, DirectoryFD: 50}
	value, absent, rejected := g.acquire(pending, g.parents())
	g.beginRetirement("test")
	g.retire()
	if value != nil || absent || rejected || g.pending[20] != pending || pending.DirectoryFD != 50 || len(k.Signals) != 0 || !reflect.DeepEqual(k.Closed, []int{40}) {
		t.Fatalf("value=%+v absent=%t rejected=%t pending=%v closed=%v signals=%v", value, absent, rejected, g.pending, k.Closed, k.Signals)
	}
	if len(k.PollResults[40]) != 1 || !reflect.DeepEqual(k.Calls, []string{"stat:50:20", "pidfd:20", "stat:50:20"}) {
		t.Fatalf("unbound readiness used: calls=%v poll=%v", k.Calls, k.PollResults)
	}
}

// TestSyntheticSameTickUnknownClosesBothSlotsOnce preserves both ambiguous descriptors until finalization.
//
// Example: the owned previous lifetime and no-authority tentative replacement have separate close ownership.
func TestSyntheticSameTickUnknownClosesBothSlotsOnce(t *testing.T) {
	k := &syntheticKernel{PollResults: map[int][]syntheticPollResult{30: {{State: processUnknown}}}, StrictPoll: true}
	g := custodyGuard(k)
	previous := custodyParent(g)
	candidate := &custody{ProcessIdentity: previous.ProcessIdentity, FD: 40, Owned: true}
	g.publish(candidate)
	g.beginRetirement("test")
	g.retire()
	if g.current[10] != previous || g.tentative[10] != candidate || previous.FD != 30 || candidate.FD != 40 || candidate.Owned || len(k.Closed) != 0 {
		t.Fatalf("previous=%+v candidate=%+v closed=%v", previous, candidate, k.Closed)
	}
	if !reflect.DeepEqual(k.Signals, []syntheticSignal{{FD: 30, Signal: syscall.SIGTERM}}) {
		t.Fatalf("ambiguous replacement signaled: %v", k.Signals)
	}
	g.finalize()
	g.finalize()
	if !reflect.DeepEqual(k.Closed, []int{30, 40}) || previous.FD != -1 || candidate.FD != -1 {
		t.Fatalf("descriptor ownership closes=%v previous=%+v candidate=%+v", k.Closed, previous, candidate)
	}
}

// TestSyntheticCandidateNativeAbsenceClosesAnchor resolves prebinding disappearance only through ESRCH.
//
// Example: a valid B followed by failed pidfd opening sends no signal and releases its anchor.
func TestSyntheticCandidateNativeAbsenceClosesAnchor(t *testing.T) {
	k := &syntheticKernel{StatReads: []metadataRead{{Stat: custodyStat(20, 2, 10)}}, PIDFDResults: []syntheticFDResult{{FD: -1, Error: unix.ESRCH}}, StrictPoll: true}
	g := custodyGuard(k)
	value, absent, rejected := g.acquire(&pendingAcquisition{PID: 20, DirectoryFD: 50}, g.parents())
	g.beginRetirement("test")
	g.retire()
	if value != nil || !absent || rejected || len(g.pending) != 0 || len(g.current) != 0 || len(k.Signals) != 0 || !reflect.DeepEqual(k.Closed, []int{50}) || !reflect.DeepEqual(k.Calls, []string{"stat:50:20", "pidfd:20"}) {
		t.Fatalf("value=%+v absent=%t rejected=%t pending=%v closed=%v calls=%v signals=%v", value, absent, rejected, g.pending, k.Closed, k.Calls, k.Signals)
	}
}

// TestSyntheticTentativeExitDuringFreshReadCannotGainAuthority prevents admission after candidate readiness changes.
//
// Example: running before fresh metadata and exited after it supplies no parent authority.
func TestSyntheticTentativeExitDuringFreshReadCannotGainAuthority(t *testing.T) {
	k := &syntheticKernel{DirectoryResults: []syntheticFDResult{{FD: 50}}, StatReads: []metadataRead{{Stat: custodyStat(20, 2, 10)}}, PollResults: map[int][]syntheticPollResult{40: {{State: processRunning}, {State: processExited}, {State: processExited}}}, StrictPoll: true}
	g := custodyGuard(k)
	custodyParent(g)
	value := &custody{ProcessIdentity: ProcessIdentity{PID: 20, Birth: 2}, FD: 40}
	g.tentative[20] = value
	// Remove the unrelated current parent from polling while keeping its ancestry snapshot unavailable.
	delete(g.current, 10)
	g.pollCustody()
	g.beginRetirement("test")
	g.retire()
	if value.Owned || len(k.Signals) != 0 || g.current[20] != nil || !reflect.DeepEqual(k.Closed, []int{50}) {
		t.Fatalf("candidate=%+v current=%v closed=%v signals=%v", value, g.current, k.Closed, k.Signals)
	}
	g.appliedStage = g.stage()
	if !g.pollCustody() || len(g.tentative) != 0 || !reflect.DeepEqual(k.Closed, []int{50, 40}) {
		t.Fatalf("exited tentative not resolved: %v closed=%v", g.tentative, k.Closed)
	}
}

// TestSyntheticPollReadinessRejectsMixedErrors validates readiness independently of proc metadata.
//
// Example: exit readiness combined with an error bit cannot settle custody.
func TestSyntheticPollReadinessRejectsMixedErrors(t *testing.T) {
	for _, fixture := range []struct {
		Bits    int16
		State   processState
		Invalid bool
	}{
		{0, processRunning, false},
		{unix.POLLIN, processExited, false},
		{unix.POLLHUP, processExited, false},
		{unix.POLLIN | unix.POLLHUP, processExited, false},
		{unix.POLLIN | unix.POLLERR, processUnknown, true},
		{unix.POLLHUP | unix.POLLNVAL, processUnknown, true},
		{unix.POLLOUT, processUnknown, true},
	} {
		state, err := classifyReadiness(fixture.Bits)
		if state != fixture.State || (err != nil) != fixture.Invalid {
			t.Fatalf("bits=%#x state=%d error=%v", fixture.Bits, state, err)
		}
	}
}
