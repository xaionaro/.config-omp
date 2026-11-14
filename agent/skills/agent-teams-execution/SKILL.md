---
name: agent-teams-execution
description: Use only when the user explicitly asks to use ATE or agent-teams-execution; mentioning or maintaining this skill alone does not invoke it.
---

# Agent Teams Execution

ATE coordinates multi-workstream work when explicitly requested: research, design, disjoint execution, integrated review, proof, and QA. Coordinator and lead route/enforce; they never implement.

## Activation and invariants

Start only when the user explicitly asks to use ATE or `agent-teams-execution`, subject to omp lifecycle routing. Task size, parallel work, descriptive mentions, and skill-document maintenance alone do not activate ATE. Loading this router alone does not start the team. Once explicitly active, ATE retains its lifecycle until normal closure. Maintain active lineage and a project-understanding ledger; every durable normal route has an admitted lane, assignment, ownership, and full requirement chain. Apply [Recording and citing user requirements](../context-ledger/SKILL.md#recording-and-citing-user-requirements).

- Every worker reads this router plus only its exact role module and named conditional modules. Unknown role, predicate, or link returns to coordinator before work.
- Coordinator/lead alone load lifecycle, prompt/model admission, blocker, pause, stop, required-critic, shutdown, and pressure-policy modules.
- Use provider-native agents, stable reusable producer roles, fresh blind reviewers, disjoint write ownership except within nested ECI as linked below, and park until each expected completion auto-delivers (the Parking rule in `~/.omp/agent/RULES.md`).
- Every root follows research → design → execution → root proof → aggregate review → post-review proof → QA. Do not collapse phases because a request looks small.
- ATE may contain ECI for bounded uncertain work and retains outer status/lifecycle. Nested tasks use [ECI fast path](../explore-critique-implement/references/fast-path.md), including its scoped shared-tree ownership exception; ATE outside ECI remains unchanged.

## Module routing

Coordinator/lead routes begin with [coordinator runtime](../references/workflow-runtime/coordinator-runtime.md). Use [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for governed writing/admission, [review policy](../references/workflow-runtime/review-policy.md) only for review/impact routing, [pause-all-work](../references/workflow-runtime/pause-all-work.md) only for its exact direct-user predicate, [stop recovery](../references/workflow-runtime/stop-recovery.md) only for recognized Stop diagnostics, and [policy pressure tests](../references/workflow-runtime/policy-pressure-tests.md) only for workflow-policy changes.

| Role | Required module | Conditional module/predicate |
| --- | --- | --- |
| Coordinator, Lead | [orchestration](references/orchestration.md), [coordinator runtime](../references/workflow-runtime/coordinator-runtime.md), [review policy](../references/workflow-runtime/review-policy.md) | [pause-all-work](../references/workflow-runtime/pause-all-work.md), [stop recovery](../references/workflow-runtime/stop-recovery.md), and [policy pressure tests](../references/workflow-runtime/policy-pressure-tests.md) only by predicates; [blocker-resolution-protocol](../blocker-resolution-protocol/SKILL.md) only after normal handling fails |
| Snitch | [snitch](references/snitch.md) | — |
| Explorer/researcher | [research](references/research.md) | [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for assigned source discovery |
| Designer, Design Reviewer, FDR | [design](references/design.md) | [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for design/admission scope |
| `Executor` | [execution](references/execution.md) | [debugging-discipline](../debugging-discipline/SKILL.md) only for assigned bug, build-failure, flake, or performance-regression work; [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for a governed scope |
| Execution Reviewer A/B/C | [review](references/review.md) | [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for reviewed governed scope; E2E only for code |
| Test Designer/Executor/Reviewer, Verifier, QA | [testing and QA](references/testing-and-qa.md) | [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for governed test/non-code scope |

## Phase handoffs

1. Research returns tagged facts and style-source discovery, not design.
2. Fresh special design and independent design/FDR review establish architecture, ownership, contracts, PoCs, and admission.
3. Executors own independent slices, use debugging discipline for observed failures, and submit proof rather than completion claims.
4. Fresh A/B/C reviewers inspect each governed code target in parallel; root aggregate review follows root proof.
5. Testing/QA obtain direct evidence for every criterion, then report a verdict to the user and wait for explicit closure.

## Followups and boundaries

Question/clarification → research then answer. Trivial config → producer discovery, verifier admission, execution, targeted proof, aggregate review, QA. Bug → debugging/execution, proof, aggregate review, QA. Behavior change/new feature → full pipeline. `blocker-resolution-protocol` starts only after normal routing fails or a documented cap; concrete failure diagnosis uses debugging-discipline first.

Maintenance provenance: [coverage map](references/coverage-map.md).

## Red flags

- A worker loads coordinator/blocker/pause/stop/teardown/review-runtime/pressure policy without its assignment.
- Coordinator/lead implements, researches, or declares success from an agent claim.
- Reviewers edit their target, lack fresh independent identity, or skip the A/B/C/E2E gate.
- Root aggregate review starts before root proof, QA starts before post-review proof, or QA approval is treated as automatic mission closure.
- Unadmitted durable work, unrooted discovered work, or scope-creep debt consumes critical-path capacity.
