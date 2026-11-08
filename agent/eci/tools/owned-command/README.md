# owned-command

Shared ECI command-family supervisor for Linux and macOS, maintained at
`~/.omp/agent/eci/tools/owned-command` independently of application checkouts.
The installed `~/.omp/agent/eci/bin/owned-command` wrapper builds this canonical
module into an external cache and forwards arguments and status. Manual build:
`GOWORK=off go build -o owned-command .`. The binary is invoked directly; it
acquires its own containment. Generated executables stay outside Git.

For repeated or timed phases, prepare once before starting their budgets:

```sh
guard=$("$HOME/.omp/agent/eci/bin/owned-command" --resolve-executable)
"$guard" -deadline 3s -receipt receipt.json -- command args...
```

Resolution accepts no other arguments, performs the same native build/cache
validation, and prints one absolute immutable executable path without running it.
Build diagnostics go to stderr; paths with CR/LF cannot use this line protocol.
Direct execution inherits its own caller's environment, cwd, arguments and streams.
Resolve again to select changed source/toolchain settings; retain the selected
cache version while callers use it. Ordinary wrapper invocations still recheck
source before each execution.

```sh
owned-command -deadline 3s -receipt receipt.json -- command args...
owned-command -cancel-pid PID -cancel-birth BIRTH -receipt receipt.json
```

PID and BIRTH must be positive for cancellation; BIRTH is the separately recorded
`/proc/PID/stat` field 22 (Linux only; on macOS `-cancel-pid` returns 125 with a receipt).
PID zero selects launch and requires command arguments. A mismatched cancellation
identity returns 125 without signaling.

## Command launch
Command launch and Foreign PID cancellation describe the Linux backend; macOS uses the darwin-pgroup section below.

Launch requires Linux cgroup v2, pidfds, clone-bound cgroup placement, procfs in the
supervisor's PID namespace, and a reachable systemd user manager supporting
cgroup delegation. On Linux, missing facilities fail before the requested command launches;
there is no process-group or host-scan launch fallback. The macOS process-group backend is a
separate darwin build, never a Linux fallback.

The binary initializes receipts and TERM/INT/HUP notification before native
setup. It proves a childless baseline with a nonconsuming native check returning
ECHILD, validates the PID namespace, and becomes a child subreaper. It acquires a
fresh delegated user scope in the existing supervisor process through D-Bus.
The original `DBUS_SESSION_BUS_ADDRESS` is used when set; otherwise the native
`/run/user/UID/bus` socket is addressed directly. Acquisition does not discover a
bus by spawning helpers or changing the environment.

Only the command root is launched into the scope's private `command` subgroup,
using clone-bound placement and a clone-returned pidfd. The supervisor remains in
the containing scope. Ordinary forks, reparenting and `setsid` retain command
membership. A command deliberately migrating through another service manager is
outside inherited-family coverage; this tool is not a sandbox.

The command inherits cwd, arguments, environment, stdin/stdout/stderr and process
group. Early signal notification can reset formerly ignored HUP/INT to default
in the child; transparent signal disposition inheritance is not promised.
Signals before notification, after notification stops, SIGKILL/SIGSTOP, or a
native call that never returns are outside the cleanup guarantee.

Setup has a five-second monotonic scheduling budget, separate from `-deadline`.
The command runtime budget starts after successful native launch. Defaults are
three seconds of runtime and 250 milliseconds of grace. With `-cancel-pid`,
`-deadline` instead bounds the time from guard start until retirement begins
(see Foreign PID cancellation). Cancellation has no separate setup budget.
Native-call, inspection and finalization latency are additional; these are not
hard wall-clock limits.
An interruption or setup error prevents launch and returns 125 through the same
receipt finalizer. An ambiguous manager reply never authorizes launch.

The running command waits for SIGCHLD, root pidfd readiness, cgroup population
changes, interruption or deadline. Launched supervision performs no global host
process enumeration. One interruptible observer supplies readiness notifications;
the lifecycle loop alone consumes native Wait4 results. Notifications request
checks and do not establish settlement.

Root exit, deadline, interruption or guard error establishes one retirement
instant. Graceful TERM acquires current private-subtree members through anchored
proc lifetime reads, retained pidfds and fresh subgroup membership. Numeric
`cgroup.procs` entries alone authorize no signal. TERM streams bounded directory
and member-read batches, checking the common clock before each operation, entry
and candidate. When KILL becomes due, discovery stops before further enumeration.
At retirement plus `-grace`, `cgroup.kill` targets only the private command
subgroup and its descendants.
Bulk KILL has a group event rather than invented individual delivery receipts.
At retirement plus twice `-grace`, the driver makes its bounded final settlement
attempt. TERM enumeration can miss concurrent forks; subtree KILL supplies the
common forced retirement boundary. Final survivor inspection and empty-group
removal each have a finite application-work budget, including control entries and
duplicate PID parsing. Exhaustion reports incomplete inspection or cleanup and
returns 125; a partial survivor list is explicitly incomplete. Individual native
call latency remains additional.

Successful settlement requires the root's native terminal status, private subtree
`populated=0`, a final actual ECHILD from bounded WNOHANG/WALL reaping, and checked
cleanup. Zero PID, EINTR and a batch yield are incomplete drains. Stopped or
continued status does not complete the root. Observation is armed before checking
predicates; its observer is stopped and joined before descriptors close.
Only owned empty subgroup directories are removed. Cleanup failure can preserve
independently proven physical `HostChildrenSettled` while still returning 125.
The enclosing scope containing the receipt writer is never stopped or killed;
systemd collects it after exit
when empty. Live descendants prevent a claim of collection or settlement.

## macOS darwin-pgroup backend

On Darwin the wrapper builds the same module and runs the `darwin-pgroup` backend
(`main_darwin.go` and `pgroup.go`). It starts the command in a new process group
(`Setpgid`, so the group ID equals the command PID). Retirement begins at the first of
command exit, the `-deadline` measured from launch, or SIGINT, SIGTERM or SIGHUP to
owned-command. Retirement sends SIGTERM and then SIGCONT to `kill(-pgid, ...)`, sends
SIGKILL one `-grace` later, and makes its final check at twice `-grace`. The group is
settled only when the command has been reaped and `kill(-pgid, 0)` reports ESRCH.

Every receipt's `Guarantee` field states the guarantee verbatim:

> deadline and cleanup only: on deadline, interrupt (SIGINT, SIGTERM or SIGHUP to owned-command) or command exit, SIGTERM and SIGCONT, then SIGKILL after -grace, go to the command's own process group (pgid = command PID); no birth identity, so -cancel-pid returns 125 and signals nothing; no PID-reuse guard; no subreaper, so descendants that leave the group (setsid or setpgid) are not cleaned up and can outlive HostChildrenSettled=true, which means only that the command was reaped and its process group is empty

`Guarantee` states the subreaper gap (setsid and setpgid escapes), the missing birth
identity and PID-reuse guard, and that `-cancel-pid` returns 125. The cgroup, terminal
(background process group), Survivors and Events, and XNU and launchd limits are
documented only in the bullets below:
- No subreaper and no cgroup. A descendant that calls `setsid` or `setpgid` leaves
  the group. It is neither signaled nor waited for, and it survives retirement. A
  receipt with `HostChildrenSettled` true then describes only the group.
- No birth identity and no PID-reuse guard. `-cancel-pid` with `-cancel-birth` returns
  125 and signals nothing. It writes a receipt: `Cause` is `guard error`, `CommandPID`
  is 0, `HostChildrenSettled` is false, and `Errors` holds one `cancel:` message. A
  malformed cancellation request is refused without a receipt.
- If owned-command itself is killed with SIGKILL, nothing retires the group.
- The receipt has no `Cgroup` field, and there is no five-second setup budget on Darwin.
  `Survivors` holds the root only when it is still unreaped at the final check, and it is
  empty otherwise. `Events` is present and records the steps: started, command reaped,
  group TERM, group CONT, group KILL, and group already empty.
- The command runs in a background process group, so this backend is for non-interactive
  command families only.
- XNU's killpg and ESRCH behaviour, and launchd's reaping of orphaned group members, are
  unverified until the macOS run.
- Host settlement proves nothing about phone, camera, media or remote cleanup, as on Linux.

## Foreign PID cancellation

`-cancel-pid/-cancel-birth` uses a separate compatibility driver with
`Backend=external-pidfd` and observed static-family coverage. It does not infer
permission over the target's ambient cgroup. Names, process groups and cached
numeric parent IDs do not authorize signaling.

An opened `/proc/PID` directory anchors the before-stat, pidfd acquisition and
fresh after-stat to one proc lifetime. Birth ticks alone are not unique lifetime
authority. Descendant admission requires a fresh parent observation and a retained
parent descriptor whose post-read poll proves that its lifetime covered the read.
An admitted identity remains owned after detachment or reparenting. Launch-only
subreaper adoption is unavailable to this driver.

Before initial root TERM, one finite host proc snapshot is acquired parent-first
in finite batches, with no observation pause between batches. Retirement starts
when the pass completes, so `-grace` does not include discovery. `-deadline` is
measured from guard start; setup and binding use it but are not cut short by it.
During the pass, reaching it or an interruption starts retirement while TERM
stays held, and a discovery or acquisition error starts retirement and ends the
pass. `Cause` names what started retirement (`cancel requested` when the
completed pass starts it), and the root's `observed` event marks binding. Every
batch checks interruption, the deadline and the common retirement clock. If
preparation is incomplete when KILL becomes due, it ends permanently, known
handles receive the due schedule, and the result remains unsettled with status
125. Later custody emptiness cannot erase that uncertainty. Children outside the
snapshot, including any forked during or after the pass, or changing ancestry
during it are not proved covered. The pass ends by KILL, one `-grace` after
retirement starts, and retirement starts by the first check at or after
`-deadline`.

Unknown metadata, visibility, scan or poll failures do not mean exit. Anchored
ENOENT alone is insufficient. Prebinding absence requires native pidfd_open ESRCH
without an earlier mismatch/error and the native settlement predicate. Bound
completion uses retained pidfds independently of numeric-path replacements.
Current descriptors close after confirmed exit; immutable receipt history remains.

## Status and receipts

Ordinary settled commands preserve native exit status, including `128+signal`.
Cancellation returns 0 only for its documented observed-family settlement, with
`Cause` `cancel requested` or `already absent`; a discovery that reaches
`-deadline` returns 125 even when settlement is later proved.
Deadline, interruption, inspection, custody, cleanup and receipt errors return
125. Historical errors force 125 even if physical settlement is later proved.
A command can itself exit 125: inspect `Cause`, `CommandExit`, `Errors` and
`HostChildrenSettled` together.

`Backend` and `FamilyCoverage` distinguish inherited command-cgroup coverage from
foreign cancellation's conditional static-family coverage. `CommandStarted`
records native command launch separately from setup. `Survivors` reports known
unresolved identities; empty known custody or an absent receipt cannot prove an
unknown family empty. A zero birth value is unavailable metadata and supplies no
cancellation authority. Receipt output errors also force 125.
darwin-pgroup receipts add `Guarantee`, and their `Backend` and `FamilyCoverage` name process-group coverage.

Host settlement proves no remote Android, camera, media or profile cleanup.
Verify those resources separately. The reconstructed implementation does not
establish equivalence to the deleted original external binary.

## Checks

Build once and run the focused/native controls from this module:

```sh
guard_checks_dir=$(mktemp -d "${TMPDIR:-/tmp}/owned-command-checks.XXXXXXXX")
GOWORK=off go build -o "$guard_checks_dir/owned-command" .
GUARD_TEST_BINARY="$guard_checks_dir/owned-command" GOWORK=off \
  go test -count=1 -timeout 2m -v .
```

Darwin checks: the cross-builds run on any host; the tests run on macOS natively and on Linux with the build tag.

```sh
GOWORK=off GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null .
GOWORK=off GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go vet .
GOWORK=off GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null .
GOWORK=off GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go vet .
GOWORK=off go test -count=1 -v -run '^TestPgroup' .                    # macOS
GOWORK=off go test -tags pgroupcheck -count=1 -v -run '^TestPgroup' .  # Linux
```

Tests distinguish deterministic synthetic kernel schedules from native CLI
controls. Native controls prove their exercised Linux profile, not every kernel,
native PID reuse or hard latency. Keep scratch artifacts until reviewed and remove
only the owned scratch directory after its processes have exited. The executable
and parser at the old temporary path are compatibility links; consult their owner
before removing that path.

## Camera client parser

`current-client.awk` validates exactly one complete Active Camera Clients section
through Allowed user IDs and returns held=0, absent=1, unknown=2. Establish dump
command success first. It validates the entire section before returning held,
including subsequent records and remainder. Empty `[]` is absent.

Parenthesized AOSP-format records require Camera ID, Cost, PID, Score, State and
Conflicting Client Devices. Supported optional User Id/package forms include
adjacent State/User Id, omitted-package double commas and conflict trailing commas.
Signed numeric fields are validated in full. Empty conflict braces are valid;
comma-only braces and repeated empty elements are unknown, including in
nonmatching records. PID and target are canonical positive decimal text compared
exactly. A flat single Camera ID/PID record is explicit synthetic compatibility.
Duplicate/partial values, underfilled records, ambiguous fields and unbalanced or
trailing material are unknown. History and listener PIDs do not establish current
ownership. Upstream-shaped nonempty fixtures are synthetic from formatter source;
installed nonempty vendor compatibility remains unproved.
