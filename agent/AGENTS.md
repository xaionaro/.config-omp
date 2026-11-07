# omp native ECI hooks

@~/.omp/agent/eci-binding-contract.md

This file maps omp tool names to the tools ECI hooks expect, and states where native hooks and state live.

## Tool glossary
- `spawn_agent` → `task`
- `update_plan` → `todo`
- `apply_patch` → `edit`

## Skills
`~/.omp/agent/skills` links to the skills directory. Load a matching skill with its `SKILL.md` before acting.

## Hooks
Native hook adapters are tracked in git under `~/.omp/agent/hooks/` (`pre/`, `post/`, and `lib/eci-hook-runner.ts`). They run the native scripts in `~/.omp/agent/eci/hooks/`. In the current working tree they are DISABLED: the directory is moved to `~/.omp/agent/hooks-disabled/`, so omp does not discover or run them until the user re-enables them. Until then, no ECI hook gate is enforced.

Intended behavior once enabled: PreToolUse fails closed in omp. A hook that errors, times out, or returns unparseable output blocks the tool call.

## State locations
Proof state lives under `$HOME/tmp/agent/proof`; context ledgers under `$HOME/.agent/context`. Do not write them by hand.
