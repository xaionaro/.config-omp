---
name: stuck-work-resolution
description: Use when work is stuck, a review loop cannot converge, an ECI or ATE work-progress flow may escalate to the user, or a stop-hook bypass is being considered because progress is stuck
---

# Stuck Work Resolution Protocol

No stuck claim without an attempt log. No user escalation or stop-hook bypass while an internal path remains.

## When To Use

| Use | Skip |
| --- | --- |
| Agent reports "stuck", "waiting on", or "cannot proceed" | Missing attempt log; bounce back first |
| Review pair or ECI loop hits its rejection/retry cap | Ordinary reviewer rejection still inside loop budget |
| ATE stuck work, review/iteration protocol-limit record, or task-progress pre-user escalation | QA verdict or user followup that already belongs to user |
| ECI may hard-escalate while staying active | Clean pass or user-closed teardown |
| Stop hook suggests a bypass because work cannot stop cleanly | Intentional dirty handoff with no claim of stuck work |
| Stuck work exposed by lifecycle/process-control recovery | Lifecycle/process-control recovery with no separate concrete stuck work |

## Required Record

Start each Genuine stuck and User-owned stuck record with the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires.

| Record | Required content |
| --- | --- |
| Genuine stuck | Obvious resolutions tried, evidence for each, why each failed |
| Protocol-limit stuck | Rejected artifact, all rejection reasons, fixes attempted, evidence still failing, why the pair or loop cannot converge |
| User-owned stuck | Required input/resource/decision, why agents/tools cannot obtain it, impact of waiting, next concrete command or question |

Obvious resolutions include re-reading relevant source, retrying once when nondeterminism is plausible, trying a simpler faithful path, checking local specs/docs, and asking the paired reviewer or owner inside the active protocol.

## Workflow

1. **Validate the record.** Missing or thin attempt log means "not stuck"; send the agent back to try obvious resolutions.
2. **Launch internal resolution.** Use the active protocol's agent tools, never shell-wrapped agents.
3. **Brainstormer:** generate many distinct ideas. Positives only; no filtering, negatives, feasibility judgment, or winner.
4. **Explorer:** independently investigate the stuck work, current code/docs, and technical landscape.
5. **Feasibility validator:** the active protocol assigns an agent, separate from the brainstormer and primary explorer, to validate brainstormer ideas against explorer facts. Discard ideas that violate requirements, security, ownership, or available tools.
6. **Route:** assign the best feasible internal path. Escalate to the user only when no feasible path remains or the stuck work genuinely awaits the user.

## Adapter Boundary

ATE and ECI own role names, task states, loop limits, and lifecycle. This skill owns the shared stuck record, resolution workflow, and escalation report.

ATE/ECI subagent stuck claims are not SWR triggers. Route through the active protocol first; enter SWR only after normal issue handling fails, protocol limits hit, or user-owned input is required.

ATE: feasibility validator is a second explorer launched after the brainstormer and primary explorer complete.

Unresponsive-agent recovery, repeated agent misbehavior, coordinator silence, and shutdown/lifecycle failures follow ATE lifecycle recovery unless they expose separate concrete stuck work. Do not run SWR merely because those lifecycle paths can end in user escalation.

Stop hook: `skip-stop` is not stuck-work resolution. Use it only after this protocol is exhausted, or for intentional dirty handoff. The final stuck-work report names the failed SWR step, user-owned input if any, and exact next command/question.

## Escalation Report

When escalation is unavoidable, report:

- Original objective and current protocol context.
- Attempt log and evidence.
- SWR agents used and their conclusions.
- Last stuck issue.
- Best rejected internal path and why it failed.
- Exact user-owned input/resource/decision needed.

## Red Flags

| Symptom | Fix |
| --- | --- |
| "I'm stuck" without attempts | Bounce back; require the Required Record |
| Brainstormer filters or chooses | Re-prompt: ideas only |
| Explorer trusts another agent summary | Re-read source/docs independently |
| User asked before SWR finishes | Continue internal resolution |
| Stop-hook bypass used to avoid verification | Stop. Run SWR or state intentional dirty handoff |
