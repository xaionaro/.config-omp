---
name: omp-crash-recovery
description: Use when omp CLI/session has just relaunched after a crash with unfinished work or active spawned agents.
---

# omp Crash Recovery

Use only right after crash relaunch; no new crash means out of scope. Resume; do not restart.

1. Reconstruct state from summary, plan, git, ledgers, handovers, and active-mode state.
2. Preserve agent context: for each still-live agent id, message `agent://<id>` with exactly `continue` and nothing else; if an agent is gone, spawn a replacement with `task` and the full prompt.
3. Continue main-thread work from the last in-progress task.
4. Inspect dirty files before editing.
5. Report resumed/missing agents and current main-thread task.
