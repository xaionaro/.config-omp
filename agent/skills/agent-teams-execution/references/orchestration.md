# ATE Orchestration

Coordinator/lead only. Load shared coordinator runtime first. ATE coordinates research, design, disjoint execution, integrated review, proof, and QA; coordinator/lead never implement, research, or substitute their judgment for delegated evidence.

## Lifecycle and role control

Create/update `ate_active` before the first spawn and at each phase transition. Keep validated lineage, lane/assignment binding, ownership, and the project-understanding ledger current. An ATE marker remains through nested ECI work; only explicit shutdown, switch, cancellation, withdrawal, or root-scope replacement closes it. Apply [ECI fast path](../../explore-critique-implement/references/fast-path.md) within nested ECI tasks. Apply pause and stop recovery only on their predicates.

Use stable reusable producers, fresh special design/review roles, fresh blind critics, disjoint ownership except under the linked nested-ECI rule, and one expected event wait. Lead validates prompt artifacts, scope, skills, bindings, stop conditions, and evidence forwarding before a provider call. Status uses role trees and the redacted requirements registry; timeout or silence never authorizes a retry, re-spawn, or shutdown.

## Teardown and explicit closure

Teardown occurs only after explicit lifecycle closure. Preserve the marker, teammates, and unresolved ownership until then. Coordinator records closure evidence and closes or withdraws every active assignment before removing the marker.

## Design and FDR coordination

Coordinator supplies Designer with tagged research, requirements, criteria, lineage, related paths, prior findings, and style-source evidence. It obtains independent Design Reviewer scrutiny before durable execution and returns material premise, contract, ownership, security, or feasibility changes to design.

Fundamentals Design Reviewer receives independent issue brainstorming, investigation, and meta-review before a verdict. If that reviewer lacks agent tools, use Lead-Mediated Nested Delegation: FDR defines the child prompt, criteria, context, and output; lead materializes and spawns it; FDR retains the verdict.

## Root proof, review, and aggregate coordination

After known slices land, run root proof and the applicable root E2E. Coordinator then assigns fresh blind Critic A, Critic B, Critic C, and E2E for governed code; it validates packets/bindings and waits for every required report and evidence.

Pre-route findings by scope and impact. A root aggregate review starts only with the required proof and complete evidence. Required findings return as a grouped repair/proof cycle; valid debt/defer remains auditable without becoming required work. Record the resulting evidence and Git state, then rerun the necessary proof before QA.

## Critic C packet coordination

Before Critic C Packet 1, coordinator validates lineage, ledger/log anchors, prompt artifact, category/profile selectors, and fresh special identity. Packet 1 contains the narrow diff-intention request; the reviewer returns 2–4 `reconstructed intention:` bullets and stops. Packet 2 supplies the full review context. The first response never replaces the final review or post-write reconciliation.

## Debug, BRP, and cap coordination

Concrete bugs, failures, flakes, regressions, and QA rejections enter repro/RCA/critic/fix/review handling. Record the regression report before RCA, require a falsifiable cause chain, alternative or falsifying prediction, `regression: yes|no|unknown`, and real-path proof.

Use BRP only after normal handling fails or an approved limit is reached. Coordinator starts the required idea, exploration, and independent feasibility roles, preserves the attempt record, and does not treat a local block as mission closure. A root aggregate review has at most 10 REJECTED rounds; the next round creates a protocol-limit record and uses blocker-resolution-protocol before escalation.

## QA sequencing and closure

Test/spec admission precedes durable test work. Before QA, complete the required integrated proof, review, repairs, and rerun of applicable proof. QA applies each criterion with direct-versus-proxy evidence and reports defects to the matching owner.

QA approval is a verdict, not mission closure. Report its evidence to the user and wait for explicit lifecycle closure; preserve active teammates and unfinished work until that event.

## State, recovery, and enforcement

`pending` requires assignment/ownership; `submitted` requires tagged evidence and is not completion; only coordinator sets `complete` after required verification. Revalidate refs, paths, evidence hashes, and ancestry before state changes. Scope-creep debt consumes no critical-path capacity.

Staleness starts only after 30 minutes without assignment, output, owned file/Git, or observed-process activity; before then do not request status/checkpoint or interrupt. At the floor, inspect one roster snapshot, owned-file/Git changes, recorded proof/build activity, and provider terminal/cancellation/independent crash evidence; silence alone is insufficient. Send one checkpoint per unchanged silence episode through the correct role, then await its expected event; new assignment/output/file/Git/process activity resets the episode and floor. Preserve executor diff/status before closure or re-spawn; confirmed crash may re-spawn the same semantic role at most twice, then escalate to the user. Do not accept an untagged claim, reviewer edit, missing ownership/admission/evidence, or a claim without independent support.

## Coordination red flags

- Coordinator/lead performs a producer or reviewer role instead of delegating it.
- A phase, marker, or user-facing closure bypasses required evidence.
- A required review, proof, or QA finding is relabeled as optional work.
- A cap, timeout, or local block ends the mission while an internal route remains.
