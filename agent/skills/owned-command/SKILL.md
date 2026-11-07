---
name: owned-command
description: Use when running bounded host process families with deadline or interruption cleanup (Linux; macOS with a narrower process-group guarantee), or cancelling a Linux host PID with its recorded birth identity.
---

# Owned command

Use `"$HOME/.omp/agent/eci/bin/owned-command"` for finite host command families requiring descendant retirement. Run ordinary foreground inspection such as `rg --files` directly. Persistent services use their intended service lifecycle.

Launch with a unique receipt and `--` before the command:

```bash
receipt_dir=$(mktemp -d "${TMPDIR:-/tmp}/owned-command.XXXXXXXX")
status=0
"$HOME/.omp/agent/eci/bin/owned-command" -deadline 150ms \
  -receipt "$receipt_dir/launch.json" -- \
  /bin/sh -c 'sleep 30 & wait' || status=$?
printf 'status=%s\n' "$status"
cat "$receipt_dir/launch.json"
```

Cancel only a separately recorded positive Linux host PID with its positive `/proc/PID/stat` field 22 birth identity (on macOS `-cancel-pid` with `-cancel-birth` returns 125 with a receipt and signals nothing):

```bash
"$HOME/.omp/agent/eci/bin/owned-command" -cancel-pid "$recorded_host_pid" \
  -cancel-birth "$recorded_stat_field22" -receipt "$receipt_dir/cancel.json"
```

The guard validates that identity through anchored reads and retained pidfd custody. A mismatch returns 125 without signaling. Names and process groups do not establish cancellation authority; birth ticks alone are not unique lifetime authority.

Launch uses a private inherited cgroup v2 subtree and native child reaping; it requires a reachable systemd user manager with delegation, including for direct-built binaries. Missing facilities return 125 before command launch. Foreign PID cancellation instead covers one bounded observed static-family snapshot; children outside that snapshot or changing ancestry are unproved. Read `Backend` and `FamilyCoverage` with settlement fields; cancellation never authorizes killing the target's ambient cgroup.

On macOS the wrapper runs the `darwin-pgroup` backend (`Backend=darwin-pgroup`; `main_darwin.go`, `pgroup.go`): the command runs in its own process group, and a deadline, an interruption (SIGINT, SIGTERM or SIGHUP to the guard) or the command's exit sends SIGTERM and SIGCONT to that group, then SIGKILL after `-grace`; the final check runs at 2·`-grace`. The guarantee is deadline and cleanup only, with `FamilyCoverage` set to "command process group only": there is no birth identity and no PID-reuse guard, so `-cancel-pid` with `-cancel-birth` returns 125 with a receipt (`Cause` "guard error") and signals nothing, and without `-cancel-birth` the arguments are invalid and no receipt is written. There is no subreaper and no cgroup, so a descendant that calls `setsid` or `setpgid` leaves the group and is not cleaned up; it can outlive `HostChildrenSettled: true`, which means only that the command was reaped and its process group is empty. The receipt's `Guarantee` field states these limits, and its `Survivors` and `Events` fields are present. Use this backend for non-interactive families only.

Settled ordinary commands preserve native status. Guard deadline/interruption/errors return 125; an ordinary command can also exit 125. Use the receipt's `Cause`, `CommandExit`, `Errors` and `HostChildrenSettled` when available to distinguish them. On Linux and macOS, inspect `Survivors` for unresolved custody: an unsettled darwin-pgroup run lists the unreaped command root there, and `Errors` holds guard diagnostics. An absent receipt cannot prove settlement. Cancellation returns 0 only on settlement.

`HostChildrenSettled` concerns host processes. Verify phone processes, camera, media and other remote cleanup separately, including when the command uses `adb`.

The wrapper builds canonical `~/.omp/agent/eci/tools/owned-command` into an external cache and preserves caller cwd, arguments, environment and standard streams. Application checkouts do not own this shared tool. `OWNED_COMMAND_SOURCE` explicitly overrides the source module. Setup has a separate five-second scheduling budget (Linux); `-deadline` starts after successful native command launch. Native-call, inspection and finalization latency are additional, so neither budget promises a hard wall-time bound. Run `--wrapper-help` for source/cache configuration and `-help` for flags. Read the selected source module's `README.md` for the Linux custody, namespace and signal-disposition limits and the macOS darwin-pgroup gaps. Keep canonical source ownership; generated executables stay outside Git.
