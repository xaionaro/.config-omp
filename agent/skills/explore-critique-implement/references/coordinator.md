# ECI Supervisor

Supervisor-only ECI lifecycle. Load the shared coordinator runtime before this module. Load blocker-resolution-protocol only after ordinary ECI issue handling fails.

## Engage and route

- Apply [concurrent task scheduling](../../../eci-binding-contract.md#concurrent-tasks): admit independent user requests under the active lifecycle, give each root package a Job, retain outcome-specific Producer identities, and scope waits to affected outcomes. Use the [Job Orchestrator contract](task-orchestrator.md) for ownership and handoffs.
- Enforce the [active Job compliance checks](task-orchestrator.md#active-job-compliance). Accumulate current Job rosters and critically reconcile owners' findings with the reports and relevant evidence; explicitly reconfirm that each owner checks Workers and follows through on corrections. Check each Job owner's compliance and separately check your own enforcement. Use existing coordination updates and session records.
- Follow the [ledger update priority](../../../eci/skills/context-ledger/SKILL.md#update-points): answer the user when due, propagate changed binding facts before dependent actions, and advance ready independent work without waiting for record maintenance. Reconcile records in the same turn by the defined checkpoints. Use lineage to explain ownership and handoffs; do not make normal work wait on a lineage artifact, hash, receipt, or schema shape.
- Follow the native goal lifecycle before Step 1. Assign every new tracked repository contribution, including code, policy, documentation, and administrative deliverables, to a bounded Producer to author, check, and commit. The Supervisor and Orchestrators author no new tracked contribution. Preserve any pre-existing authorized Supervisor-authored content with its original author, exact scope, and provenance, then hand it to a Producer for integration, checking, and commit. Do not disengage merely to change routing.
- ECI has reusable Explorer, Implementer Producer, and Fast Owner Producer identities. Publish one Orchestrator per active Job before its first Worker spawn, including J = 1 ([Initial admission](task-orchestrator.md#publication-transfer-and-report-drain)). Each Step 2 critic, Critic A/B/C, E2E, brainstormer, feasibility validator, and loop-breaker is a fresh isolated identity. Producer and critic identities never overlap.
- Each packet states exact scope, target/change/verification when it assigns implementation, expected output, claim tags, and verification duties. Apply [Work estimates](../../../eci-binding-contract.md#work-estimates) to assignments and execution. Missing coordination detail is repaired by a concise handoff or clarification; it does not block harmless work.
- Treat records, hashes, receipts, packet shape, and command spelling as context or audit, never as permission checks. Route only a concrete accidental wrong-target, cross-scope, or destructive effect.
- For every relevant Supervisor-to-user ECI progress update, report this standalone line for each executing lane:
  - `Forecast deadline: <named lane/task outcome> will be finished by <UTC ISO8601>.`
- For each unrepresented active root-task outcome omitted by lane reports, include this standalone line:
  - `Root completion forecast: <named active root-task outcome> will be finished by <UTC ISO8601>.`
- For material ECI work, retain `exact user source → faithful requested outcome → bounded scope`.
- A repair needed to meet or prove that outcome stays in its current lane; a
  unadmitted separate-outcome concern is only a post-ECI observation or follow-up, never
  current work or a forecast.
- Reconcile missing or stale lineage alongside known work; it does not block progress.
- When scope or its meaning is corrected, distinguish wording-only edits from changes to entity meaning, ownership, capability, lifetime, or acceptance. For semantic corrections, reassess affected design/interfaces, implementation, assignments, criteria, and proof. Resolve affected consequences through verified repairs or source-backed evidence that the current result already satisfies the corrected meaning; route necessary repairs through existing ECI gates. Refresh affected acceptance even when files have not changed. Wording-only corrections do not reopen semantic acceptance; preserve checks required for actual edits. Propagate binding changes before dependent work; continue unaffected ready work and preserve its valid evidence. Correct ledger/status and log the correction. Cancel or reassign only unrooted current work; do not destructively revert already-made work without user direction.
- The forecast line itself names the finished outcome using the [canonical forecast outcome wording](../../../eci/skills/context-ledger/SKILL.md#forecast-outcome-wording); a separate `Lane` or `Next milestone` does not substitute, and it never names a critic, reviewer, actor, or stage. Root completion is full root completion, not a child sum or stage.
- Use the two templates above exactly: named lane/task or root outcome and UTC deadline only. Do not append text.
- For a changed lane or root forecast, restate its current canonical line in the same update, then state Forecast recalibration: <prior UTC ISO8601> → <current UTC ISO8601>; why moved: <why>; supporting evidence: <evidence>.
- For every material ECI forecast update, report a separate base-case and
  downside-scenario spread block per the [canonical ledger rule](../../../eci/skills/context-ledger/SKILL.md#eci-scenario-forecasts).
  Keep the standalone forecast and recalibration lines above exact; `forecasts.yaml` owns current scenario selection and stored endpoints.
- If any forecast is missing or stale, say so and reconcile it alongside safe work without delaying the update.
- Forecasts are advisory. They never gate work, grant or deny permissions, require artifacts or receipts, create blockers, require parsers, or require per-command ceremony.
- Use the [`forecast-target-history.tsv` audit contract](../../../eci/skills/context-ledger/SKILL.md#forecast-target-history) for every root-target transition. It is audit-only and never a gate.
- Assign new tracked repository contributions, including policy and documentation, to a Producer for authorship, checks, and commit; do not author or commit them as Supervisor. Preserve existing authorized Supervisor-authored tracked content with exact provenance and transfer its bounded scope to a Producer for integration, checking, and commit. If no Producer is free, queue the assignment and continue other admitted work; capacity alone is not a user blocker. The Supervisor may maintain session-local coordination records, ledgers, plans, status reports, handoffs, and proof notes outside the tracked repository. Resolve routing edge cases through explicit session coordination while hook migration is deferred. Do not call retired self-edit helpers; Producer ownership and index rules still apply.
- Apply [ECI fast path](fast-path.md) at task launch and throughout shared-tree coordination, evidence rerouting, review, and closure.
- Follow the shared pause-all-work and stop-recovery modules only on their exact predicates. Load policy-pressure-tests only for workflow-policy changes.

## Native goal lifecycle

The Supervisor owns native goal calls; Workers and shell hooks do not create or complete goals. Native goals provide continuation. Do not create an ECI boolean marker, rename one as active state, or add a shell goal adapter. Keep ordinary session coordination records for ownership, scope, review, and teardown.

- On ECI activation, before Step 1, call `get_goal`. If no unfinished goal exists, call `create_goal` with the full requested ECI outcome as objective. Omit `token_budget` unless explicitly requested. Record the returned goal and its owning lifecycle in session coordination state. Repeated activation preserves that ownership; create at most one goal per ECI lifecycle.
- Preserve any other unfinished goal. Associate ECI with an enclosing goal only when its objective covers the requested outcome; association does not transfer ownership. For an unrelated goal, report that native creation is unavailable while it remains unfinished and continue authorized ECI work without modifying it.
- Child Jobs, additive outcomes, iterations, and ECI nested within an existing ECI lifecycle reuse that lifecycle's association. ECI nested under ATE follows the activation rule for its bounded ECI scope: reuse a covering outer goal without completion authority, or create an ECI-owned goal when no unfinished goal exists. Include every admitted outcome at every descendant depth in the owning lifecycle's completion check.
- After root clean-pass teardown succeeds, call `get_goal` again. Call `update_goal` with `status: complete` only for this lifecycle's still-matching owned goal, when its entire objective is achieved, every owned Job is closed, every outcome required to achieve that objective is accepted, and all task-owned Writers are stopped. If budgeted, report final token usage returned by the tool. A child clean pass, Fast completion, idle Worker, or zero running Workers alone never completes the goal.
- Cancellation, withdrawal, or replacement does not itself establish goal achievement. If the goal objective remains unmet, preserve the unfinished goal and report its state; apply native pause/block rules only when independently satisfied. If the current goal no longer matches the owned goal, leave it unchanged. Do not mark a goal complete to enable a successor.
- If native goal tools are unavailable or fail, report the limitation and continue safe ECI work. Do not substitute a shell hook, fabricate success, or change another goal.

## Step 4 — Review coordination

Each Producer commits its checked scope during handoff. Before Step 4 or dependent work in another implementation iteration, the Supervisor independently verifies the exact immutable parent-to-commit range. The review packet gives each reviewer that range and explicit exclusions; excluded baseline and later ambient changes remain outside review scope. A Producer checkpoint is not acceptance.

Apply the [fast-path adoption boundary](fast-path.md#adoption-review-and-closure) for write-yields, additional cumulative review targets, and final-state coverage. An intermediate review does not complete the normal path; schedule the mandatory post-Fast sequence after Fast finishes and its write-capable tools stop.
Follow the normative [post-Fast completion sequence](fast-path.md#post-fast-completion).
After the Step 2 recommendation, the Supervisor owns final disposition application/treatment and applies exactly one canonical disposition per in-scope inventory item. Step 2 authority is limited to design-winner selection; it does not apply treatment.

The current Job owner alone assigns fresh Step 2, A/B/C, and required independent E2E reviews for each outcome under the [Job Orchestrator contract](task-orchestrator.md#worker-and-helper-boundaries). The Supervisor verifies each Producer range before dependent review/work and retains final impact/treatment/disposition adjudication, acceptance, and protected operations. E2E follows the [central cadence, scope, and timing policy](../SKILL.md#e2e-cadence-scope-and-timing), including its early-run condition and final independent run.

E2E triggers: [Configuration E2E contract](../SKILL.md#configuration-e2e-contract) and [Runtime E2E policy](../SKILL.md#runtime-e2e-policy).

Name at least one critic in every critic round to check least restriction: bots are non-malicious; controls catch concrete accidental mistakes without turning normal work into permission ceremony.

Before dispatch, verify the assignment still names the right work, current requirements, named review range and exclusions, and supplied evidence. Missing or stale coordination evidence prompts refresh, reassignment, or an additional review; it does not stop ordinary exploration, implementation, or harmless verification.

Wait for each gate's required reviews and focused proof, plus E2E evidence when the central cadence calls for it. Final acceptance also requires the final E2E pair when a trigger applies. Pre-route every finding with the review policy. Send substantive `now` findings or design/API uncertainty back as one complete Steps 1–2 repair batch; send a trivial `now` finding once to the Implementer Producer. Preserve auditable debt/defer/ignored-contradictory records without treating them as a clean result. Use the shared Supervisor/runtime policy for repair cycles, clean-pass, and limits.

Before final acceptance, compare the actual cumulative result and proof with the original directive and applicable requirements under [coding-policy enforcement](../../references/workflow-runtime/coding-style-admission.md). Check source-backed violations and their resolutions yourself; review counts and passing tests do not establish compliance. For a workflow-failure repair, verify the original and different-case evidence required by [policy failure investigations](../../references/review-investigation.md#policy-failure-investigations); symptom discovery alone does not meet that outcome.

## Blockers, bugs, and limits

Concrete bug/failure/flake/performance/incorrect behavior uses debugging-discipline's direct-fix or uncertain-case RCA path. A verified local defect with a clear cause and repair follows the ordinary Producer and required ECI review path; delegate repro/RCA/regression work when local behavior, cause, or repair is uncertain. Unknown relation to the original incident alone does not require RCA. Preserve required Steps 1–4, independent reviews, and the central E2E policy on either path.

After a first Step 2 all-REJECT, route the verbatim findings to one Explorer revision, then assign a fresh blind Step 2 critic. A second all-REJECT enters the ordinary blocker route.

Use blocker-resolution-protocol only after normal handling cannot resolve a stall, after Step 2’s bounded all-REJECT path, or after a gate/cycle limit. A subagent blocker is not mission blocker. BRP uses a primary explorer and separate feasibility validator; a fresh idea-only brainstormer runs for genuine stalls. At the configured gate/cycle cap, invoke one fresh loop-breaker. It may ACCEPT only a demonstrated clean pass, RETRY exactly once with guidance, or BLOCKED into BRP. Hard escalation reports the original problem, attempts, final issue, and next-best option while ECI stays active.

## Clean pass and teardown

Complete each outcome's normal path and submit it to the Supervisor for acceptance only after its post-Fast sequence and required evidence pass. Keep the root lifecycle and unfinished Jobs active after an individual outcome clean pass or cancellation; apply [Job-owner closure and report drain](task-orchestrator.md#publication-transfer-and-report-drain), and run root teardown only when every owned outcome is accepted or explicitly cancelled and its Writers have stopped.

An iteration is Step 1 explore → Step 2 critique → Step 3 implement → Step 4 parallel review. Do not advance the change until its gate is clean. Each iteration needs focused proof and independent review; E2E follows the central cadence. Final acceptance needs every original criterion, final applicable E2E evidence, and no remaining `now` REJECT/CONDITIONAL.

On root clean pass or root user closure: apply [both-producer closure](fast-path.md#adoption-review-and-closure); write the disengage report; request/observe final implementer confirmation; record role state without closing terminal agents; then complete ordinary teardown and [native goal synchronization](#native-goal-lifecycle). The report contains exactly one `clean-pass:` or `user-closed:` certificate, Stop-checklist walkthrough, and incomplete-compliance analysis. Teardown failure leaves the lifecycle unfinished.

Status uses human-readable role/lane names, parent-child trees for nested Helpers, and a nearby redacted-verbatim requirements registry for every non-empty canonical `Lane requirement refs`; ECI records `Stage: normal` with main-path and Fast progress under the same outcome and Job. The ledger carries the full edge chain. Direct work with inactive lineage does not fabricate refs. Use `<role label> (<runtime name>)` in every status, wait, or close update. Lineage failures are routing risks, never fake `PAUSED`/`BLOCKED` states.

## Provider coordination

Agent coordination uses only subagent spawning, `write agent://<id>` (messages a live agent), `send_message`, `wait` (blocks on own background jobs; normally bounded by the next [active Job review](task-orchestrator.md#active-job-compliance) when due), cancellation-only `interrupt_agent`, and `read proc://` for roster maintenance only. If standard tools are unavailable, hard-escalate rather than shell-launch an omp agent. Spawning a subagent starts a new role; `write agent://<id>` starts a fresh turn when the reusable role is idle and delivers at a message boundary when it is running; `send_message` is bounded in-turn delivery. The Supervisor follows an Orchestrator only under the [publication and report-drain rules](task-orchestrator.md#publication-transfer-and-report-drain). Label every spawned/resumed role and immediately update the roster as `<role label>: <runtime name> [type]`.

Before Step 1, follow [native goal lifecycle](#native-goal-lifecycle). While engaged, assign every new tracked repository contribution to a Producer for authoring, checks, and commit. The Supervisor may directly maintain session-local coordination records outside the tracked repository; ordinary routing coordination does not override Producer ownership or authorize staging, index mutation, or commit. Keep ECI active through blocker work, nested paths, review, and acceptance; user cancellation/withdraw/replacement/ATE switch is user closure only after completing the successor handoff or scope removal. A PostCompact signal requires the Supervisor to reread the full router and then its exact assigned modules before the next decision.

Every Producer assignment says fresh-target treatment: Explorer rereads every referenced file; the Implementer Producer rereads every intended target. Every report/submission tags factual claims; E2E evidence identifies exact tool output/log/screenshot/state rather than bare “green.” Code/debug submissions follow debugging-discipline's proportionate evidence and reporting requirements and explain why the diff repairs the established local defect; unknown repair rationale is not submittable. The Supervisor independently verifies every committed range before routing it as evidence or starting dependent review/work.

## Exact team separation

Use stable Explorer and implementer identities across iterations, plus the Fast owner lifetime defined in [ECI fast path](fast-path.md#start-and-lifetime). Every invocation of Step 2 critic, Critic A, Critic B, Critic C, E2E, brainstormer, BRP feasibility validator, and loop-breaker is a distinct blind identity; no producer acts as critic. A blind critic receives a self-contained prompt with role, original requirements, files/scope, sources to reread, expected output, and all review rules. Critic C code Packet 1 remains the shared runtime’s narrow diff-only exception, never an omission of quality checks. Reuse does not imply trust: a reusable producer still treats every turn as fresh.

## Bug and blocker packet rules

For a concrete bug, capture its statement, repro, available evidence, and missing evidence in a readable current coordination note or report. Apply debugging-discipline's direct-fix or RCA proof and reporting requirements, including regression evidence when RCA is needed. Do not make a fixed file path, artifact, or report schema a prerequisite for investigation.

An isolated disposable repro/PoC may run before style review. Normal issue handling precedes BRP. For a genuine stall, record the useful attempts and choose an Explorer, brainstormer, or feasibility validator as needed; do not make a fixed blocker artifact or role sequence a prerequisite for continued ordinary work.

## Brainstormer, loop-breaker, and caps

Brainstormer is fresh, blind, and idea-only: original problem, attempts, paths, and “generate as many distinct ideas as possible; no filtering, feasibility judgment, negatives, or winner.” It never directly decides or edits. It fires after zero viable documented options, Step 2 bounce cap, or genuine implementer stall, together with BRP fact gathering/validation.

Use one fresh loop-breaker per change when three gate retries in a cycle, three failed full cycles, or the post-brainstorm Step 2 all-REJECT condition reaches its limit. Its self-contained packet includes original problem, all attempts/failures/issues, current paths, pre-routing, gate/E2E/proof evidence, and clean-pass assessment. It returns exactly `ACCEPT`, `RETRY`, or `BLOCKED`: ACCEPT only when clean pass already holds and waives nothing; RETRY grants exactly one matching retry with guidance; BLOCKED creates a protocol-limit record and enters BRP. Failed retry or exhausted limit never silently settles for good enough.

## Teardown certificate

The disengage report has `## ECI completion certificate` with exactly one `clean-pass:` or `user-closed:` evidence line; `## Stop checklist walkthrough` covering Questions, Git, Completion, Root cause, Adversarial self-critique, Assumed blockers, Rule-compliance self-audit, Project understanding ledger, and Testing; and `## Incomplete compliance` with each gap/impact or explicit `fully-compliant:` rule-by-rule explanation. It may instead include full omp sections `Summary`, `Verification`, `Requirements`, `Root Cause`, `Claim Inventory`, `Pre-Mortem`, `Adversarial Critique`, `Rule-Compliance Self-Audit`, and `Gaps`; either form needs substance, not boilerplate. After it, request/observe implementer final confirmation, record each role completed/idle/cancelled/still-running, complete ordinary teardown, then follow [native goal synchronization](#native-goal-lifecycle). A timeout/silence is nonterminal; never close a terminal agent to finish teardown.

## Coordinator red flags

- Two dependent changes in one task are implemented before a new critique, or a main-path iteration skips any of Steps 1–4.
- A critic prompt lacks lineage/full chain, original requirements, concrete winner text, or applies a defer/debt as implementation.
- A fresh critic is replaced by a producer, a same-role followup, or serial review.
- A known bug is sent to BRP before debugging discipline/attempt record.
- A stable reusable producer is replaced while idle, a `send_message` is used to start its turn, or a special/fresh critic is upgraded by followup.
- A status report uses task/iteration numbers or flattens nested work, or a lane packet omits the full chain outside Critic C Packet 1.
- An ordinary repository-code edit reaches any denial path instead of an automatic bounded Producer handoff or queue.
- A normal command or routing step waits on an artifact, receipt, hash, allowlist, raw spelling, or shell punctuation without a concrete accidental destructive effect.
