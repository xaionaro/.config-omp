# ECI Step 3 — Implement (Producer)

Producer-only. The Implementer is the Producer for its assigned outcome. Treat every message as fresh, reread each intended target, and implement your assigned part of the current iteration's approved design within that outcome and its home Job. Other Job owners may advance independently.

Follow the [shared-tree priority](fast-path.md#shared-tree-and-eci-priority) and [adoption boundary](fast-path.md#adoption-review-and-closure) when integrating retained Fast owner work. Material fast evidence uses [design reopening](fast-path.md#evidence-can-reopen-design).

Apply [main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility): bring retained Fast code to the selected winner's quality standard; retain qualifying code and revise or replace deficient parts within that winner.

Follow the normative [post-Fast completion sequence](fast-path.md#post-fast-completion).
For post-Fast completion, validate retained code and make justified repairs under the new winner. An unchanged winner needs checks, not a redundant edit or empty commit.

Read the Step 2 critic's full output as the authoritative winner, disposition, and treatment; a summary does not replace it. After a Step 4 round without a finding critic, read each CONDITIONAL routed to you in its reviewer's full report; [Step 4](../SKILL.md#step-handoffs) gives it `treatment: now`. When a fix direction changes a rule, apply that change at every site that states the rule. Implement only findings routed as `treatment: now`: fix each routed finding and implement routed `revise`/`replace` changes. Provide evidence, not implementation, for other dispositions. Also apply the TODO rule in [Main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility) to each already-discovered, unfixed lower-priority code issue in this assignment. For an unadmitted separate-outcome issue, keep it post-ECI; make only the TODO change that rule permits, and do not investigate or repair it. Include any TODO change in ordinary current-diff review. Do not otherwise implement an unadmitted separate-outcome concern, deadline-driven cleanup, or `ignored-contradictory` directive. Report a material scope or style conflict before the next affected write; keep unaffected bounded work moving.

A missing record, receipt, hash, marker, or coordination detail does not deny a bounded in-scope write. Reconcile useful context alongside the work. Stop or reroute only a concrete wrong target, destructive effect, or separate requested outcome.

For code/debug work, load test-driven-development, debugging-discipline, and every coding skill matching changed prod code. Repair the causal mechanism, not timing/visibility/blast radius unless containment was requested. Apply debugging-discipline's direct-fix or RCA path and proportionate evidence/reporting requirements; explain why the diff repairs the established local defect. Every factual submission claim has a T1–T5 tag.

Run early E2E only under the central policy; the implementer owns the required final E2E on the stabilized final cumulative state. Continue or abort the final E2E under [Prod-code focus](../../../eci-binding-contract.md#prod-code-focus); report what is observed, what is missing, and the actual product result, if established.

E2E triggers, cadence, scope, and timing: [ECI E2E policy](../SKILL.md#e2e-cadence-scope-and-timing).

## Write boundary and submission

Before each durable write, reread the intended target, requested outcome, approved winner, and changed-file context. Route a concrete wrong target or separate outcome before writing. One change/one diff per assignment; do not broaden a winner through “helpful” cleanup.

The handoff names changed files, the applied winner/fix, checks run, repair rationale, RCA/regression explanation where applicable, factual evidence, exact exclusions, and limitations. Report your commit IDs with their parent-to-commit ranges. If a governed scope changes, report it before the next affected write. Continue unaffected work only; evidence against the winner returns to a fresh Step 2 critic under [Step 2](../SKILL.md#step-handoffs). An unsupported load-bearing claim, missing required final E2E, unknown repair rationale, or symptom-only fix needs correction before acceptance; labels and coordination notes alone never decide it.

## Producer, Worker, and Helper boundaries

Commit under [Commits](task-orchestrator.md#commits): your own checked lines, whenever you choose; resolve conflicts directly with the other Producer.

You may delegate bounded support recursively to Helpers within your inherited assignment, authorization, and read/write limits. Helpers return changes and evidence through their parent; integrate and check their work before including it in your commit.

## Test and debugging discipline

Apply the [test-efficiency rules](../SKILL.md#test-efficiency) to each Producer's focused checks and the combined iteration proof.

Implement and prove your complete assigned part as one piece under the [iteration rule](../SKILL.md#iteration-rule), never one pending defect at a time. First choose its proof under [Prod-code focus](../../../eci-binding-contract.md#prod-code-focus): the shortest faithful evidence of every required behavior, such as existing suites, new or changed tests, or direct real-path runs. Write only the new or changed tests you selected before implementing the complete part; apply test-driven-development's [Batch Order](../../test-driven-development/SKILL.md#batch-order) RED/GREEN to those tests, with your part as the batch. Confirm the build as Batch Order requires whether or not you write tests. Then run the chosen proof over all of it. Repair scoped implementation defects together. Repair unfaithful proof or replace it with equally faithful evidence; record which. Rerun the whole-part proof until it passes. A WIP commit is not a handoff.

When several Producers share the iteration's Step 3, they agree which one hands off last. That Producer waits for every other handoff, runs the iteration's focused proof over all their parts, and hands off with that proof. Repair scoped implementation defects together, in any part. Repair unfaithful proof or replace it with equally faithful evidence; record which. Rerun the combined proof until it passes.

For debugging, debugging-discipline governs repro/hypothesis/RCA; do not claim root cause merely because a patch reduces frequency. If focused-proof infrastructure fails, repair or replace the method with equally faithful evidence. Continue E2E under [Prod-code focus](../../../eci-binding-contract.md#prod-code-focus); do not substitute proxy evidence for a required real path.

Do not declare the outcome accepted/complete, publish a manifest, or tear down ECI. The current Job owner assigns independent Step 4 review; the Supervisor retains acceptance.
