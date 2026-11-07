---
name: android-device
description: Use when working with Android phones or tablets — fastboot, adb, flashing, kernel updates, device debugging, on-device experiments, benchmarks, perf measurement.
---

# Android Device Operations

Routine health checks and fixes happen in-place in the current session; no separate agent is usually needed.

**User data is sacred.** `fastboot erase userdata`, `fastboot erase metadata`, `fastboot -w`, and factory reset require explicit user request. Erasing user data is never a side effect of another operation.

**Avoid `adb forward` / `adb reverse` unless necessary.** Device usually reaches the host directly over LAN — use the host IP. Tunnels add hidden state, mask real connectivity issues, and break when adbd restarts. Justify every `forward`/`reverse` (e.g. USB-only device, no routable network).

## Experiment Hygiene

Use health criteria relevant to the experiment, its intended conditions, and the device's observed baseline. A suspicious reading is diagnostic evidence, not an automatic blocker or proof that results are contaminated. Preserve a bad state needed to reproduce the issue. Honor platform safety warnings and protections without normalizing ordinary throttling under study.

Observe relevant conditions -> make justified changes if needed -> record baseline -> run -> verify -> restore owned changes. Keep observations in existing experiment notes; do not require a separate criteria artifact before work.

### 1. Health Check And Remediate

Check relevant rows. For comparisons, choose conditions and tolerances from the protocol, device guidance, or repeated baseline observations; these commands suggest observations, not universal pass thresholds. Unavailable signals are unknown; seek equivalent evidence when relevant and limit unsupported claims without blocking unrelated work. Change state only when evidence supports the need, the action is authorized, and its scope and restoration are understood. Avoid broad process killing, cache dropping, radio changes, or unplugging that would erase the failure or alter intended conditions.
Treat Android load averages as diagnostic context only, never as a health gate. Never block work or kill processes based on a load average alone or a fixed load-average threshold; assess actual CPU activity (`top`) and pressure (`/proc/pressure/cpu`, when available), alongside thermal, battery, and the other checks.

| Check          | Command                                                                         | Interpret for this experiment | Possible response when justified |
| -------------- | ------------------------------------------------------------------------------- | ---------------------------- | ---------------------------------------------------------------------------------------------------- |
| Hot procs      | `adb shell top -n1 -b -m5`                                                      | Activity relative to intended load and baseline | Investigate the relevant process; stop only an identified process when authorized and appropriate |
| Thermal        | `adb shell dumpsys thermalservice \| grep -i status`                            | Status relevant to intended thermal conditions | Cool down for comparable runs if needed; preserve throttling when studying it |
| Battery temp   | `adb shell dumpsys battery \| grep temp`                                        | Device guidance and intended charging/thermal state | Investigate unusual temperature; change charging state only when appropriate |
| Battery level  | same                                                                            | Sufficient for the run and its intended power conditions | Charge if needed; preserve charging state when it matters |
| Memory         | `adb shell cat /proc/meminfo`                                                   | Availability and pressure relative to workload/baseline | Investigate suspected pressure; avoid blanket killing or cache dropping |
| Doze / idle    | `adb shell dumpsys deviceidle get deep`                                         | Matches intended idle conditions | Change only a relevant, authorized state and retain its restoration |
| Screen state   | `adb shell dumpsys power \| grep mWakefulness`                                  | Matches intended             | `input keyevent KEYCODE_WAKEUP` / `KEYCODE_SLEEP`                                                    |
| Crash handlers | `adb shell pgrep -a crash_dump64` or `adb shell ps -A \| grep '[c]rash_dump64'` | Relevant crash activity | Preserve crash evidence and investigate its effect on the claimed result |

Record relevant baseline observations and intended conditions after selected setup/stabilization and before the run for post-run comparison.

### 2. Stabilize For Run

| Goal                      | Action; record original and restore after                                           |
| ------------------------- | ----------------------------------------------------------------------------------- |
| Control background activity | Change only identified confounders when justified; preserve required connectivity |
| Log capture              | Preserve relevant existing logs; clear only when authorized and needed |
| Governor                  | `cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor`; pin only when required |
| Wait between runs         | Use thermal/power conditions relevant to comparability; retain natural variation |

### 3. Post-Experiment Verify

Recheck relevant observations against the baseline and intended conditions. Deltas suggest questions, not diagnoses; corroborate them before declaring a leak or changing state.

| Delta                          | Meaning -> action                |
| ------------------------------ | -------------------------------- |
| CPU still elevated             | Check which activity persists and whether it is expected; a reading alone does not prove a leaked process |
| Thermal throttling             | Assess its effect on this run and comparability before cooling or repeating |
| MemAvailable dropped           | Investigate workload, allocation and pressure evidence; a delta alone does not prove a leak |
| Battery temp increased sharply | Assess intended load/charging and device guidance; preserve diagnostic evidence |

### 4. Cleanup

Track and restore experiment-owned mutations to their recorded prior state where restoration is possible. Preserve pre-existing user state and report any unrestored change, irreversible effect, or unknown restoration; stopping a process or clearing logs is not undone by restoring settings.

| Mutation                       | Reversal                                               |
| ------------------------------ | ------------------------------------------------------ |
| Files in `/data/local/tmp/...` | `rm -rf <exact paths>`; never glob `/data/local/tmp/*` |
| Newly installed test apps      | `adb uninstall <exact owned pkg>`; preserve pre-existing installs |
| Spawned procs                  | Stop identified experiment-owned processes; verify identity before targeting and confirm exit |
| `settings put` / `setprop`     | Restore recorded value                                 |
| Pinned governor                | Restore recorded governor                              |
| Airplane mode / data           | Restore recorded state                                 |
| Logcat buffer size             | Restore recorded prior size |
| `deviceidle force-idle`        | Restore prior state; `unforce` only if this experiment forced it |
| Wakelocks                      | Release; check `dumpsys power`                         |

Verify owned changes were restored and report residual conditions or unknowns. Natural battery drain, temperature changes, or cache variation do not automatically mean the device is dirty; assess relevant unexpected behavior without requiring numeric baseline equality.
