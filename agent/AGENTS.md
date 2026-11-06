# omp ↔ Codex bridge

Authority is `~/.codex`. Read `~/.codex/CODEX.md` as the binding contract. This file only maps omp to it.

## Tool glossary (Codex name → omp tool)
- `spawn_agent` → `task`
- `update_plan` → `todo`
- `apply_patch` → `edit`

## Skills
`~/.omp/agent/skills` is a symlink to `~/.codex/skills`. Load a matching skill with its `SKILL.md` before acting, as CODEX.md requires.

## Hooks
Codex hooks run through the factories in `~/.omp/agent/hooks/pre/` and `post/`, which call the scripts in `~/.codex/hooks/`. PreToolUse fails closed in omp: a hook that errors, times out, or returns unparseable output blocks the tool call.

## State locations
Proof state is shared with Codex under `$HOME/tmp/agent/proof`; context ledgers under `$HOME/.agent/context`. Do not write them by hand.
