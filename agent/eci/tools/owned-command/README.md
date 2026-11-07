# owned-command

Shared ECI Linux command-family supervisor, maintained at
`~/.omp/agent/eci/tools/owned-command` independently of application checkouts.
`agent/eci/hooks/build-native-binaries.sh` builds it with the pinned Go 1.26
toolchain (`GOPROXY=off GOTOOLCHAIN=local`) into
`~/.omp/agent/eci/bin/owned-command` and writes a sha256 receipt beside it.
Manual build: `GOWORK=off go build -o owned-command .`. The binary is invoked
directly; it acquires its own containment. Generated executables stay outside Git.

For repeated or timed phases, the caller may keep the built executable path and
invoke it directly with its own arguments and environment:

```sh
guard="$HOME/.omp/agent/eci/bin/owned-command"
"$guard" -deadline 3s -receipt receipt.json -- command args...
```

Rebuild with the build script after source changes; a retained executable path is
not rechecked against source at execution time.

```sh
owned-command -deadline 3s -receipt receipt.json -- command args...
owned-command -cancel-pid PID -cancel-birth BIRTH -receipt receipt.json
```

PID and BIRTH must be positive for cancellation; BIRTH is the separately recorded
`/proc/PID/stat` field 22. PID zero selects launch and requires command arguments.
A mismatched cancellation identity returns 125 without signaling.

## Command launch

Launch requires Linux cgroup v2, pidfds, clone-bound cgroup placement, procfs in the
supervisor's PID namespace, and a reachable systemd user manager supporting
cgroup delegation. Missing facilities fail before the requested command launches;
there is no process-group or host-scan launch fallback.

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
three seconds of runtime and 250 milliseconds of grace. Native-call, inspection
and finalization latency are additional; these are not hard wall-clock limits.
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

Before initial root TERM, one finite host proc snapshot is acquired parent-first.
Finite preparation batches check interruption and the common retirement clock.
If preparation is incomplete when KILL becomes due, it ends permanently, known
handles receive the due schedule, and the result remains unsettled with status
125. Later custody emptiness cannot erase that uncertainty. Children outside the
snapshot or changing ancestry during it are not proved covered.

Unknown metadata, visibility, scan or poll failures do not mean exit. Anchored
ENOENT alone is insufficient. Prebinding absence requires native pidfd_open ESRCH
without an earlier mismatch/error and the native settlement predicate. Bound
completion uses retained pidfds independently of numeric-path replacements.
Current descriptors close after confirmed exit; immutable receipt history remains.

## Status and receipts

Ordinary settled commands preserve native exit status, including `128+signal`.
Cancellation returns 0 only for its documented observed-family settlement.
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
