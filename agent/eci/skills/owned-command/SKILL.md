---
name: owned-command
description: Use when running bounded Linux host process families with deadline or interruption cleanup, or cancelling a host PID with its recorded birth identity.
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

Cancel only a separately recorded positive host PID with its positive `/proc/PID/stat` field 22 birth identity:

```bash
"$HOME/.omp/agent/eci/bin/owned-command" -cancel-pid "$recorded_host_pid" \
  -cancel-birth "$recorded_stat_field22" -receipt "$receipt_dir/cancel.json"
```

The guard validates that identity through anchored reads and retained pidfd custody. A mismatch returns 125 without signaling. Names and process groups do not establish cancellation authority; birth ticks alone are not unique lifetime authority.

Launch uses a private inherited cgroup v2 subtree and native child reaping; it requires a reachable systemd user manager with delegation, including for direct-built binaries. Missing facilities return 125 before command launch. Foreign PID cancellation instead covers one bounded observed static-family snapshot; children outside that snapshot or changing ancestry are unproved. Read `Backend` and `FamilyCoverage` with settlement fields; cancellation never authorizes killing the target's ambient cgroup.

Settled ordinary commands preserve native status. Guard deadline/interruption/errors return 125; an ordinary command can also exit 125. Use the receipt's `Cause`, `CommandExit`, `Errors` and `HostChildrenSettled` when available to distinguish them. Inspect `Survivors` for unresolved custody; an absent receipt cannot prove settlement. Cancellation returns 0 only on settlement.

`HostChildrenSettled` concerns host processes. Verify phone processes, camera, media and other remote cleanup separately, including when the command uses `adb`.

The wrapper builds canonical `~/.omp/agent/eci/tools/owned-command` into an external cache and preserves caller cwd, arguments, environment and standard streams. Application checkouts do not own this shared tool. `OWNED_COMMAND_SOURCE` explicitly overrides the source module. Setup has a separate five-second scheduling budget; `-deadline` starts after successful native command launch. Native-call, inspection and finalization latency are additional, so neither budget promises a hard wall-time bound. Run `--wrapper-help` for source/cache configuration and `-help` for flags. Read the selected source module's `README.md` for Linux custody, namespace and signal-disposition limits. Keep canonical source ownership; generated executables stay outside Git.
