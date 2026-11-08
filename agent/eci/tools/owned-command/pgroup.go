//go:build darwin || pgroupcheck

// owned-command's darwin-pgroup backend launches a command in its own process group and retires that group on
// deadline, interruption, or command exit. It has no subreaper and no birth identity, so a descendant that leaves
// the group is neither signaled nor waited for, and cancellation by PID is refused. The pgroupcheck build tag
// compiles this backend on Linux so that its logic runs in the Linux test suite.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	// pgroupScope is the receipt Scope value, shared with the Linux receipt.
	//
	// Example: host settlement here excludes phone and remote cleanup.
	pgroupScope = "HOST_COMMAND_CHILDREN_ONLY_NOT_PHONE_CLEANUP"

	// pgroupBackend is the receipt Backend value for this backend.
	//
	// Example: a receipt reader selects darwin-pgroup fields by this value.
	pgroupBackend = "darwin-pgroup"

	// pgroupFamilyCoverage states the family this backend covers.
	//
	// Example: a descendant outside the command's process group is outside this coverage.
	pgroupFamilyCoverage = "command process group only"

	// pgroupGuarantee is the exact guarantee text that every darwin-pgroup receipt carries.
	//
	// Example: a settled receipt means only that the command was reaped and its group is empty.
	pgroupGuarantee = "deadline and cleanup only: on deadline, interrupt (SIGINT, SIGTERM or SIGHUP to owned-command) or command exit, SIGTERM and SIGCONT, then SIGKILL after -grace, go to the command's own process group (pgid = command PID); no birth identity, so -cancel-pid returns 125 and signals nothing; no PID-reuse guard; no subreaper, so descendants that leave the group (setsid or setpgid) are not cleaned up and can outlive HostChildrenSettled=true, which means only that the command was reaped and its process group is empty"

	// pgroupFailure is the status for deadlines, interruptions, guard errors, and unsettled groups.
	//
	// Example: a deadline returns this status even when the command would have exited zero.
	pgroupFailure = 125

	// pgroupPollInterval is the period of the group-emptiness probe once the root has been reaped.
	//
	// Example: the probe samples kill(-pgid, 0) at this period and is the only sampled wait in this backend.
	pgroupPollInterval = 10 * time.Millisecond

	// pgroupSettlementCap is how long settlement continues after the final check at twice -grace finds the group not
	// settled. It is a fixed bound, independent of -deadline and -grace. A member with SIGKILL pending still counts until
	// the kernel runs its exit (on Linux, until it is reaped), which CPU starvation delayed by up to 1.131 s in Linux
	// measurements, and a member that never leaves must not hold the run open without limit.
	//
	// Example: a forked shell that has not run since its fork leaves the group shortly after the final check, and the run
	// settles within this cap instead of returning pgroupFailure.
	pgroupSettlementCap = 2 * time.Second

	// pgroupCancelError is the receipt error for a cancellation request this backend cannot prove.
	//
	// Example: the receipt records this message and nothing is signaled.
	pgroupCancelError = "cancel: -cancel-pid is unavailable on darwin-pgroup (no birth identity); nothing was signaled"

	// pgroupCancelDiagnostic is the single stderr line printed for a refused cancellation.
	//
	// Example: the diagnostic names the missing birth identity and states that nothing was signaled.
	pgroupCancelDiagnostic = "owned-command: -cancel-pid is unavailable on darwin-pgroup (no birth identity); nothing was signaled"
)

// pgroupIdentity names one process in a receipt. Birth is zero because this backend reads no birth identity.
//
// Example: the unreaped root at the final check appears as {PID: root, Birth: 0}.
type pgroupIdentity struct {
	PID   int
	Birth uint64
}

// pgroupEvent is one immutable step of a run, in the order the backend took it.
//
// Example: "group TERM" is recorded before "group KILL" when the grace period expires.
type pgroupEvent struct {
	At             time.Time
	ElapsedSeconds float64
	PID            int
	Birth          uint64
	Action         string
	Error          string
}

// pgroupReceipt is the JSON receipt for one run. Field names and order follow the Linux receipt where both define
// them; the receipt has no Cgroup field.
//
// Example: a settled ordinary command has CommandExit set, Cause "command exited", and no Errors.
type pgroupReceipt struct {
	Scope               string
	Backend             string
	FamilyCoverage      string
	Guarantee           string
	Started             time.Time
	CommandStarted      time.Time
	Finished            time.Time
	ElapsedSeconds      float64
	CommandPID          int
	CommandExit         int
	Cause               string
	HostChildrenSettled bool
	Survivors           []pgroupIdentity
	Events              []pgroupEvent
	Errors              []string
}

// pgroupOptions are the validated launch inputs for pgroupRun.
//
// Example: Args[0] names the executable, and the remaining elements are its arguments.
type pgroupOptions struct {
	Deadline time.Duration
	Grace    time.Duration
	Args     []string
}

// pgroupKiller sends sig to every member of process group pgid that the caller may signal; sig 0 only probes. It is
// the runner's only access to kill(2), so a test can observe or stand in for the group.
//
// Example: the runner probes the command's group with KillGroup(pid, 0) and reads ESRCH as empty.
type pgroupKiller interface {
	// KillGroup sends sig to process group pgid and returns kill(2)'s error.
	//
	// Example: KillGroup(pgid, syscall.SIGKILL) returns ESRCH when the group has no member.
	KillGroup(pgid int, sig syscall.Signal) error
}

// pgroupKernel is the pgroupKiller that calls kill(-pgid, sig).
//
// Example: every production runner uses pgroupKernel{}.
type pgroupKernel struct{}

// KillGroup calls kill(-pgid, sig).
//
// Example: KillGroup(pgid, 0) returns nil while the group has a member.
func (pgroupKernel) KillGroup(
	pgid int,
	sig syscall.Signal,
) error {
	return syscall.Kill(-pgid, sig)
}

// pgroupRunner holds the state of one run. Only the goroutine that called execute reads or writes it.
//
// Example: a root reap, a notice, and a timer each update this one value in turn.
type pgroupRunner struct {
	options          pgroupOptions
	notices          <-chan os.Signal
	group            pgroupKiller
	childExit        chan os.Signal
	started          time.Time
	launched         time.Time
	receipt          pgroupReceipt
	pid              int
	rootReaped       bool
	interrupted      bool
	settled          bool
	killSent         bool
	final            bool
	extended         bool
	waitErrRecorded  bool
	probeErrRecorded bool
	killTimer        *time.Timer
	finalTimer       *time.Timer
	probe            *time.Ticker
}

// pgroupNewReceipt returns the receipt fields that are known before any launch.
//
// Example: a refused cancellation starts from this receipt and then adds its error.
func pgroupNewReceipt(started time.Time) pgroupReceipt {
	return pgroupReceipt{
		Scope:          pgroupScope,
		Backend:        pgroupBackend,
		FamilyCoverage: pgroupFamilyCoverage,
		Guarantee:      pgroupGuarantee,
		Started:        started.UTC(),
		CommandExit:    -1,
		Survivors:      []pgroupIdentity{},
		Events:         []pgroupEvent{},
		Errors:         []string{},
	}
}

// pgroupRun launches options.Args in a new process group and supervises it until the group settles or the settlement
// cap after the final check passes. It returns the receipt and the process status: the command's native status only
// for an ordinary settled exit with no errors and no interruption, and pgroupFailure otherwise.
//
// Example: "sleep 60" under a 5 s deadline returns pgroupFailure after about 5 s with the group settled.
func pgroupRun(
	options pgroupOptions,
	notices <-chan os.Signal,
) (pgroupReceipt, int) {
	return pgroupNewRunner(options, notices).execute()
}

// pgroupNewRunner returns an unlaunched runner for options whose group signals and probes go through kill(2).
//
// Example: pgroupRun calls pgroupNewRunner(options, notices).execute(); a test replaces group before execute.
func pgroupNewRunner(
	options pgroupOptions,
	notices <-chan os.Signal,
) *pgroupRunner {
	started := time.Now()
	return &pgroupRunner{
		options:   options,
		notices:   notices,
		group:     pgroupKernel{},
		childExit: make(chan os.Signal, 1),
		started:   started,
		receipt:   pgroupNewReceipt(started),
	}
}

// execute launches the command, supervises it, and maps the outcome to the receipt and status that pgroupRun returns.
//
// Example: a runner whose executable is missing returns the guard-error receipt without supervising.
func (run *pgroupRunner) execute() (pgroupReceipt, int) {
	if run.launch() {
		run.supervise()
	}
	return run.finish()
}

// launch resolves the executable, watches SIGCHLD, and starts the command in a new process group. It reports false
// when no command started, and the receipt then holds the cause.
//
// Example: a notice that is already pending prevents the command, so CommandPID stays zero.
func (run *pgroupRunner) launch() bool {
	path, err := exec.LookPath(run.options.Args[0])
	if err != nil {
		run.setCause("guard error")
		run.receipt.Errors = append(run.receipt.Errors, "start failed: "+err.Error())
		return false
	}
	signal.Notify(run.childExit, syscall.SIGCHLD)
	if run.takeNotice() {
		return false
	}
	pid, err := syscall.ForkExec(path, run.options.Args, &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
		Sys:   &syscall.SysProcAttr{Setpgid: true},
	})
	if err != nil {
		run.setCause("guard error")
		run.receipt.Errors = append(run.receipt.Errors, "start failed: "+err.Error())
		return false
	}
	run.pid = pid
	run.launched = time.Now()
	run.receipt.CommandPID = pid
	run.receipt.CommandStarted = run.launched.UTC()
	run.record("started", pid)
	return true
}

// supervise waits for the root, for interruption, and for the deadline, and retires the group when a trigger fires.
// Each wait is an event: SIGCHLD, a notice, or a timer. The only sampled step is the group probe after the root is
// reaped, which the loop reaches through its probe ticker.
//
// Example: a command that exits on its own wakes on SIGCHLD and retires any leftover group at once.
func (run *pgroupRunner) supervise() {
	deadline := time.NewTimer(time.Until(run.launched.Add(run.options.Deadline)))
	defer deadline.Stop()
	for !run.settled && !run.final {
		killDue, finalDue, probeDue := run.dueChannels()
		select {
		case <-run.childExit:
			run.reap()
		case sig := <-run.notices:
			run.interrupt(sig)
			run.begin()
		case <-deadline.C:
			run.setCause("deadline")
			run.begin()
		case <-killDue:
			run.killSent = true
			run.signalGroup(syscall.SIGKILL, "group KILL")
		case <-finalDue:
			run.finalCheck()
		case <-probeDue:
		}
		run.reap()
		run.checkSettled()
	}
}

// dueChannels returns the timer channels armed in the current phase. A nil channel never fires, so an unarmed
// phase cannot wake the loop.
//
// Example: before retirement there is no KILL or final timer, so only SIGCHLD, notices, and the deadline wake the loop.
func (run *pgroupRunner) dueChannels() (<-chan time.Time, <-chan time.Time, <-chan time.Time) {
	var killDue, finalDue, probeDue <-chan time.Time
	if run.killTimer != nil && !run.killSent {
		killDue = run.killTimer.C
	}
	if run.finalTimer != nil {
		finalDue = run.finalTimer.C
	}
	if run.probe != nil {
		probeDue = run.probe.C
	}
	return killDue, finalDue, probeDue
}

// reap collects the root without blocking. EINTR is retried at once. Any other error is recorded once, and it
// starts retirement as a guard error because the root can no longer be observed.
//
// Example: a root that is still running reports nothing and waits for the next SIGCHLD.
func (run *pgroupRunner) reap() {
	if run.rootReaped {
		return
	}
	var status syscall.WaitStatus
	for {
		pid, err := syscall.Wait4(run.pid, &status, syscall.WNOHANG, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case err != nil:
			run.recordErrorOnce(&run.waitErrRecorded, "wait for command: "+err.Error())
			run.setCause("guard error")
			run.begin()
			return
		case pid == run.pid:
			run.rootReaped = true
			run.receipt.CommandExit = pgroupExitStatus(status)
			run.record("command reaped", run.pid)
			run.setCause("command exited")
			run.probe = time.NewTicker(pgroupPollInterval)
			run.begin()
			return
		default:
			return
		}
	}
}

// begin starts retirement once. It arms the KILL and final timers from this instant. Unless the root is reaped and
// the group is already empty, it sends SIGTERM and then SIGCONT to the command's process group, so that members
// stopped by job control act on the TERM.
//
// Example: a command that exited with a straggler gets TERM and CONT, and an empty group gets neither.
func (run *pgroupRunner) begin() {
	if run.killTimer != nil {
		return
	}
	run.killTimer = time.NewTimer(run.options.Grace)
	run.finalTimer = time.NewTimer(2 * run.options.Grace)
	if run.rootReaped && run.groupEmpty() {
		return
	}
	run.signalGroup(syscall.SIGTERM, "group TERM")
	run.signalGroup(syscall.SIGCONT, "group CONT")
}

// finalCheck runs each time the final timer fires. The first time is the final check at twice -grace: it reaps and,
// if the root is reaped, probes; a group that is not settled gets exactly one SIGKILL in this pass. That is the first
// KILL if the KILL timer has not been taken, otherwise a repeat for members that joined or were forked after it; once
// this pass has run, the KILL timer sends nothing. The pass then records "settlement extended" and re-arms the final
// timer with pgroupSettlementCap. Until that cap, only the probe is sent, once the root is reaped, every
// pgroupPollInterval; a notice sets interrupted (status pgroupFailure) without ending the wait. The second firing
// ends the run. The loop's reap and probe after this call are the last look in both passes.
//
// Example: a pre-exec straggler starved past twice -grace leaves the group during the extension, and the run settles.
func (run *pgroupRunner) finalCheck() {
	if run.extended {
		run.final = true
		return
	}
	run.reap()
	run.checkSettled()
	if run.settled {
		return
	}
	run.killSent = true
	run.signalGroup(syscall.SIGKILL, "group KILL")
	run.extended = true
	run.record("settlement extended", run.pid)
	run.finalTimer = time.NewTimer(pgroupSettlementCap)
}

// checkSettled marks the run settled when the root has been reaped and the group has no member left.
//
// Example: a probe that reports ESRCH after the reap sets HostChildrenSettled.
func (run *pgroupRunner) checkSettled() {
	if run.settled || !run.rootReaped {
		return
	}
	if run.groupEmpty() {
		run.settled = true
	}
}

// groupEmpty reports whether kill(-pgid, 0) finds no member. An error other than ESRCH is recorded once and counts
// as not empty, so a member that this process cannot signal keeps the run unsettled.
//
// Example: the probe samples every pgroupPollInterval because nothing signals that the group became empty in this
// iteration; this sample is the only poll the backend makes.
func (run *pgroupRunner) groupEmpty() bool {
	err := run.group.KillGroup(run.pid, 0)
	switch {
	case err == nil:
		return false
	case errors.Is(err, syscall.ESRCH):
		return true
	default:
		run.recordErrorOnce(&run.probeErrRecorded, fmt.Sprintf("probe process group %d: %v", run.pid, err))
		return false
	}
}

// signalGroup sends sig to the command's process group and records the action. ESRCH means the group is already
// empty, which is an event rather than an error; any other failure is recorded in Errors.
//
// Example: a SIGKILL sent after the last member exited records "group already empty".
func (run *pgroupRunner) signalGroup(
	sig syscall.Signal,
	action string,
) {
	err := run.group.KillGroup(run.pid, sig)
	switch {
	case err == nil:
		run.record(action, run.pid)
	case errors.Is(err, syscall.ESRCH):
		run.record("group already empty", run.pid)
	default:
		run.receipt.Errors = append(run.receipt.Errors, fmt.Sprintf("%s: %v", action, err))
	}
}

// record appends one event stamped with the elapsed time since the run started.
//
// Example: the "started" event is the first entry of every launched run.
func (run *pgroupRunner) record(
	action string,
	pid int,
) {
	now := time.Now()
	run.receipt.Events = append(run.receipt.Events, pgroupEvent{
		At:             now.UTC(),
		ElapsedSeconds: now.Sub(run.started).Seconds(),
		PID:            pid,
		Action:         action,
	})
}

// recordErrorOnce appends message to Errors the first time this kind of failure occurs, so a persistent failure
// does not repeat on every wake or probe.
//
// Example: the first EPERM from the probe is recorded, and identical later probes add nothing.
func (run *pgroupRunner) recordErrorOnce(
	recorded *bool,
	message string,
) {
	if *recorded {
		return
	}
	*recorded = true
	run.receipt.Errors = append(run.receipt.Errors, message)
}

// setCause records the first cause of the run; later causes do not replace it.
//
// Example: a deadline that fires before the root exits keeps the cause "deadline".
func (run *pgroupRunner) setCause(cause string) {
	if run.receipt.Cause == "" {
		run.receipt.Cause = cause
	}
}

// interrupt records a notice at any time. The run is then interrupted, so its status is pgroupFailure even when the
// command exited and the group settled afterwards.
//
// Example: SIGTERM during settlement after a clean exit still reports 125, as the Linux finalizer does.
func (run *pgroupRunner) interrupt(sig os.Signal) {
	run.interrupted = true
	run.setCause("signal: " + sig.String())
}

// takeNotice consumes one queued notice, if any, before launch or during finish. It reports whether it consumed one.
//
// Example: a notice queued before launch sets the interruption, and the command is then not started.
func (run *pgroupRunner) takeNotice() bool {
	select {
	case sig := <-run.notices:
		run.interrupt(sig)
		return true
	default:
		return false
	}
}

// finish drains queued notices, stops the SIGCHLD watch and the timers, stamps the receipt, and maps the outcome to
// the status. An unsettled launched run records the root survivor and the incomplete retirement.
//
// Example: a command that exited 7 and settled returns 7, and the same command interrupted after its exit returns 125.
func (run *pgroupRunner) finish() (pgroupReceipt, int) {
	for run.takeNotice() {
	}
	signal.Stop(run.childExit)
	run.stopTimers()
	if run.pid != 0 && !run.settled {
		if !run.rootReaped {
			run.receipt.Survivors = []pgroupIdentity{{PID: run.pid}}
		}
		run.receipt.Errors = append(run.receipt.Errors, fmt.Sprintf(
			"host children unsettled: process group %d retirement incomplete: root=%t",
			run.pid,
			run.rootReaped,
		))
	}
	run.receipt.HostChildrenSettled = run.settled && run.rootReaped
	finished := time.Now()
	run.receipt.Finished = finished.UTC()
	run.receipt.ElapsedSeconds = finished.Sub(run.started).Seconds()
	status := pgroupFailure
	if run.receipt.HostChildrenSettled && !run.interrupted && run.receipt.Cause == "command exited" && len(run.receipt.Errors) == 0 {
		status = run.receipt.CommandExit
	}
	return run.receipt, status
}

// stopTimers releases the KILL, final, and probe timers. Stopping a timer that has fired is harmless.
//
// Example: a run that settled before its KILL deadline stops the pending KILL timer here.
func (run *pgroupRunner) stopTimers() {
	if run.killTimer != nil {
		run.killTimer.Stop()
	}
	if run.finalTimer != nil {
		run.finalTimer.Stop()
	}
	if run.probe != nil {
		run.probe.Stop()
	}
}

// pgroupExitStatus converts a reaped wait status to the native exit code, with 128+N for death by signal N.
//
// Example: a process killed by SIGTERM reports 143, the same convention the shell uses.
func pgroupExitStatus(status syscall.WaitStatus) int {
	if status.Signaled() {
		return 128 + int(status.Signal())
	}
	return status.ExitStatus()
}

// pgroupValidArguments mirrors the Linux validation. A launch needs positive bounds, a command, and no cancellation
// identity. A cancellation needs a positive PID with a birth identity and no command.
//
// Example: -cancel-pid with -cancel-birth and no command is valid, so it reaches the refusal.
func pgroupValidArguments(
	deadline time.Duration,
	grace time.Duration,
	cancelPID int,
	cancelBirth uint64,
	args []string,
) bool {
	if deadline <= 0 || grace <= 0 || cancelPID < 0 {
		return false
	}
	if cancelPID == 0 {
		return cancelBirth == 0 && len(args) > 0
	}
	return cancelBirth > 0 && len(args) == 0
}

// pgroupRefuseCancellation writes the receipt for a cancellation this backend cannot prove and returns pgroupFailure.
// Nothing is signaled.
//
// Example: a recorded PID and birth yield one stderr line and a guard-error receipt with CommandPID zero.
func pgroupRefuseCancellation(path string) int {
	started := time.Now()
	receipt := pgroupNewReceipt(started)
	receipt.Cause = "guard error"
	receipt.Errors = append(receipt.Errors, pgroupCancelError)
	finished := time.Now()
	receipt.Finished = finished.UTC()
	receipt.ElapsedSeconds = finished.Sub(started).Seconds()
	_, _ = fmt.Fprintln(os.Stderr, pgroupCancelDiagnostic)
	if err := pgroupWriteReceipt(path, receipt); err != nil {
		return pgroupDiagnose(err.Error())
	}
	return pgroupFailure
}

// pgroupWriteReceipt replaces path with receipt as indented JSON, followed by a newline, with mode 0600.
//
// Example: a failed encoding or write returns an error, and the caller reports it with status 125.
func pgroupWriteReceipt(
	path string,
	receipt pgroupReceipt,
) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

// pgroupMain registers the notice channel, parses the command line, runs or refuses the request, and writes the
// receipt. It returns the process status. An argument error prints one diagnostic and writes no receipt, as the
// Linux backend does.
//
// Example: owned-command -deadline 5s -receipt f -- sleep 60 returns 125 after about 5 s with the receipt written.
func pgroupMain(args []string) int {
	notices := make(chan os.Signal, 16)
	signal.Notify(notices, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(notices)
	flags := flag.NewFlagSet("owned-command", flag.ContinueOnError)
	deadline := flags.Duration("deadline", 3*time.Second, "maximum command runtime before retirement")
	grace := flags.Duration("grace", 250*time.Millisecond, "TERM grace; KILL follows after one grace, the final check after two, then settlement for up to 2s")
	receipt := flags.String("receipt", "", "JSON receipt path")
	cancelPID := flags.Int("cancel-pid", 0, "cancel this exact host process (refused on darwin-pgroup)")
	cancelBirth := flags.Uint64("cancel-birth", 0, "required birth identity (refused on darwin-pgroup)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *receipt == "" || !pgroupValidArguments(*deadline, *grace, *cancelPID, *cancelBirth, flags.Args()) {
		return pgroupDiagnose("invalid owned-command arguments")
	}
	if *cancelPID > 0 {
		return pgroupRefuseCancellation(*receipt)
	}
	result, status := pgroupRun(pgroupOptions{Deadline: *deadline, Grace: *grace, Args: flags.Args()}, notices)
	if err := pgroupWriteReceipt(*receipt, result); err != nil {
		return pgroupDiagnose(err.Error())
	}
	return status
}

// pgroupDiagnose writes one diagnostic line to standard error and returns the guard failure status.
//
// Example: an invalid argument list reports its reason and exits 125 without writing a receipt.
func pgroupDiagnose(message string) int {
	_, _ = fmt.Fprintln(os.Stderr, message)
	return pgroupFailure
}
