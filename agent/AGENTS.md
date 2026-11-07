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
Native hooks live in `~/.omp/agent/eci/hooks/`. They are run through the factories in `~/.omp/agent/hooks/pre/` and `post/`. PreToolUse fails closed in omp: a hook that errors, times out, or returns unparseable output blocks the tool call.

## State locations
Proof state lives under `$HOME/tmp/agent/proof`; context ledgers under `$HOME/.agent/context`. Do not write them by hand.
