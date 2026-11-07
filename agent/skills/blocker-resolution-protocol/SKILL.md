---
name: blocker-resolution-protocol
description: Use when work is blocked, a review loop cannot converge, an ECI or ATE work-progress flow may escalate to the user, or a stop-hook bypass is being considered because progress is stuck
---

# Blocker Resolution Protocol

No blocker without an attempt log. No user escalation or stop-hook bypass while an internal path remains.

## When To Use

| Use | Skip |
| --- | --- |
| Agent reports "blocked", "stuck", or "cannot proceed" | Missing attempt log; bounce back first |
| Review pair or ECI loop hits its rejection/retry cap | Ordinary reviewer rejection still inside loop budget |
| ATE work blocker, review/iteration protocol-limit blocker, or task-progress pre-user escalation | QA verdict or user followup that already belongs to user |
| ECI may hard-escalate while staying active | Clean pass or user-closed teardown |
| Stop hook suggests a bypass because work cannot stop cleanly | Intentional dirty handoff with no claim of being blocked |
| Work blocker exposed by lifecycle/process-control recovery | Lifecycle/process-control recovery with no separate concrete work blocker |

## Required Record

| Record | Required content |
| --- | --- |
| Genuine blocker | Obvious resolutions tried, evidence for each, why each failed |
| Protocol-limit blocker | Rejected artifact, all rejection reasons, fixes attempted, evidence still failing, why the pair or loop cannot converge |
| User-owned blocker | Required input/resource/decision, why agents/tools cannot obtain it, impact of waiting, next concrete command or question |

Obvious resolutions include re-reading relevant source, retrying once when nondeterminism is plausible, trying a simpler faithful path, checking local specs/docs, and asking the paired reviewer or owner inside the active protocol.

## Workflow

1. **Validate the record.** Missing or thin attempt log means "not a blocker"; send the agent back to try obvious resolutions.
2. **Launch internal unblocking.** Use the active protocol's agent tools, never shell-wrapped agents.
3. **Brainstormer:** generate many distinct ideas. Positives only; no filtering, negatives, feasibility judgment, or winner.
4. **Explorer:** independently investigate the blocker, current code/docs, and technical landscape.
5. **Feasibility validator:** the active protocol assigns an agent, separate from the brainstormer and primary explorer, to validate brainstormer ideas against explorer facts. Discard ideas that violate requirements, security, ownership, or available tools.
6. **Route:** assign the best feasible internal path. Escalate to the user only when no feasible path remains or the blocker is genuinely user-owned.

## Adapter Boundary

ATE and ECI own role names, task states, loop limits, and lifecycle. This skill owns the shared blocker record, unblocking workflow, and escalation report.

ATE/ECI subagent blocker claims are not BRP triggers. Route through the active protocol first; enter BRP only after normal issue handling fails, protocol limits hit, or user-owned input is required.

ATE: feasibility validator is a second explorer launched after the brainstormer and primary explorer complete.

Unresponsive-agent recovery, repeated agent misbehavior, coordinator silence, and shutdown/lifecycle failures follow ATE lifecycle recovery unless they expose a separate concrete work blocker. Do not run BRP merely because those lifecycle paths can end in user escalation.

Stop hook: `skip-stop` is not blocker resolution. Use it only after this protocol is exhausted, or for intentional dirty handoff. The final blocker report names the failed BRP step, user-owned input if any, and exact next command/question.

## Escalation Report

When escalation is unavoidable, report:

- Original objective and current protocol context.
- Attempt log and evidence.
- BRP agents used and their conclusions.
- Last blocking issue.
- Best rejected internal path and why it failed.
- Exact user-owned input/resource/decision needed.

## Red Flags

| Symptom | Fix |
| --- | --- |
| "I'm blocked" without attempts | Bounce back; require the Required Record |
| Brainstormer filters or chooses | Re-prompt: ideas only |
| Explorer trusts another agent summary | Re-read source/docs independently |
| User asked before BRP finishes | Continue internal unblocking |
| Stop-hook bypass used to avoid verification | Stop. Run BRP or state intentional dirty handoff |
