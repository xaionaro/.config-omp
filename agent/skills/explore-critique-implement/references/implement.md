# ECI Step 3 — Implement (Producer)

Producer-only. The Implementer is the Producer for its assigned outcome. Treat every message as fresh, reread each intended target, and change one approved iteration/diff at a time within that outcome and its home Job. Other Job owners may advance independently.

Follow the [shared-tree priority](fast-path.md#shared-tree-and-eci-priority) and [adoption boundary](fast-path.md#adoption-review-and-closure) when integrating retained Fast owner work. Material fast evidence uses [design reopening](fast-path.md#evidence-can-reopen-design).

Apply [main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility): bring retained Fast code to the selected winner's quality standard; retain qualifying code and revise or replace deficient parts within that winner.

Follow the normative [post-Fast completion sequence](fast-path.md#post-fast-completion).
For post-Fast completion, validate retained code and make justified repairs under the new winner. An unchanged winner needs checks, not a redundant edit or empty commit.

Receive the Step 2 design-winner recommendation and the Supervisor's final per-item disposition/treatment. Implement only findings routed as `treatment: now`: fix each routed finding and implement Supervisor-applied `revise`/`replace` changes. Provide evidence, not implementation, for other dispositions. Also apply the TODO rule in [Main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility) to each already-discovered, unfixed lower-priority code issue in this assignment. For an unadmitted separate-outcome issue, keep it post-ECI; make only the TODO change that rule permits, and do not investigate or repair it. Include any TODO change in ordinary current-diff review. Do not otherwise implement an unadmitted separate-outcome concern, deadline-driven cleanup, or `ignored-contradictory` directive. Report a material scope or style conflict before the next affected write; keep unaffected bounded work moving.

A missing record, receipt, hash, marker, or coordination detail does not deny a bounded in-scope write. Reconcile useful context alongside the work. Stop or reroute only a concrete wrong target, destructive effect, or separate requested outcome.

For code/debug work, load test-driven-development, debugging-discipline, and every matching coding skill. Repair the causal mechanism, not timing/visibility/blast radius unless containment was requested. Apply debugging-discipline's direct-fix or RCA path and proportionate evidence/reporting requirements; explain why the diff repairs the established local defect. Every factual submission claim has a T1–T5 tag.

Before each submission, perform the iteration's required focused unit/proof checks. Run early E2E only under the central policy; the implementer owns the required final E2E on the stabilized final cumulative state. If required final E2E is unavailable, report the exact missing resource and shortest faithful evidence attempted; do not claim equivalent proof.

E2E triggers, cadence, scope, and timing: [ECI E2E policy](../SKILL.md#e2e-cadence-scope-and-timing).

## Write boundary and submission

Before each durable write, reread the intended target, requested outcome, approved winner, and changed-file context. Route a concrete wrong target or separate outcome before writing. One change/one diff per assignment; do not broaden a winner through “helpful” cleanup.

The handoff names changed files, the applied winner/fix, checks run, repair rationale, RCA/regression explanation where applicable, factual evidence, exact exclusions, and limitations. Commit the checked agreed scope during handoff; report its commit ID and immutable parent-to-commit range. Before dependent review or work, the Supervisor independently verifies that range. If a governed scope changes, report it before the next affected write. Continue unaffected work only; a local correction returns to Step 2 and substantive drift returns to Explore/Step 2. An unsupported load-bearing claim, missing required final E2E, unknown repair rationale, or symptom-only fix needs correction before acceptance; labels and coordination notes alone never decide it.

## Producer, Worker, and Helper boundaries

Use the active-writer, direct conflict-agreement, pending-index, and complete staged-result rules in [active-writer rows and shared-index coordination](task-orchestrator.md#active-writer-rows-and-shared-index-coordination), plus the [Producer handoff and joint-checkpoint rules](task-orchestrator.md#producer-handoff-commits-and-joint-checkpoints). Commit only your own checked contribution. The Supervisor supplies matching identity/scope/index facts but does not assign contested content or commit order.

You may delegate bounded support recursively to Helpers within your inherited assignment, authorization, and read/write limits. Keep each Helper’s exact runtime identity paired with its write scope in your active-writer row. Helpers return changes and evidence through their parent; integrate and check their work before including it in your commit. Helpers do not commit.

## Test and debugging discipline

For code work, test-driven-development governs production code: write a failing behavior test where applicable, observe the expected RED cause, implement minimal repair, verify GREEN, then refactor only while green. For debugging, debugging-discipline governs repro/hypothesis/RCA; do not claim root cause merely because a patch reduces frequency. If test/E2E infrastructure is unavailable, report the exact missing resource and the shortest faithful evidence attempted; do not silently substitute proxy evidence for a required real path.

Commit your checked scoped work during handoff. Do not declare the outcome accepted/complete, publish a manifest, or tear down ECI. The current Job owner assigns independent Step 4 review; the Supervisor verifies the immutable range first and retains acceptance.
