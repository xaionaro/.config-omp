# Review Policy

Use this for independent review, impact routing, claim discipline, and design-versus-implementation classification. Reviewers report; writers repair.

ECI-specific routing below governs ECI outcomes. For nested ECI, the owning outer Coordinator/Lead retains outer-workflow lifecycle, review assignments, escalation, and acceptance.

## Evidence and severity

Apply [impact-first delivery](../../explore-critique-implement/references/fast-path.md#impact-first-provisional-delivery) and [security follow-ups](../../../eci-binding-contract.md#decisions) before severity, `now`, defer eligibility, or gate evaluation. Security follow-ups override the generic only-trivial defer rule below.

Tag factual claims when precision matters and name their source/confidence. Verify load-bearing source-backed claims independently; no peer output is trusted by default. An omitted tag is a review gap to clarify, not a permission gate. A reviewer’s first response to another agent’s input identifies a concern, gap, or question.

Evidence tests the result; a record, receipt, hash, or packet shape never permits or blocks ordinary work. Unsupported evidence can invalidate the conclusion that relies on it, not unrelated bounded work.

| Code | Meaning |
| --- | --- |
| REJECT / REJECTED | Wrong, unsafe, contradictory, or needs redesign; fix now. |
| CONDITIONAL | Viable but needs concrete bounded text/fix; route by impact. |
| NIT | Preference only; may be ignored. |

Review every applicable root-cause rationale and regression explanation. Unknown cause or symptom-only mitigation is REJECT unless containment was explicitly requested. Governance/prompt/hook/protocol/reviewer changes also audit mechanism/predicate, emitted wording, strongest supported wording, and a boundary counterexample; reject claims beyond evidence.

## Impact-proportional routing

Screen scope before severity. A remedy is `now` only when it is necessary to meet or prove the original user outcome; labels such as acceptance criteria, security, correctness, specification, contract/interface, persistence, concurrency, admission, TDD, proof, regression, verification, or required test do not independently make it current work. A repair with a clear link to meeting or proving that outcome is a current-lane repair, not a substitute requirement. Within ECI, maintain one TODO under [Main ECI quality responsibility](../../explore-critique-implement/references/fast-path.md#main-eci-quality-responsibility) for each already-discovered lower-priority code issue that remains unfixed: reuse a covering TODO, update it if stale or incomplete, and add one only if absent. It changes no scope, priority, or in-scope disposition, and never replaces a required in-scope repair. An unadmitted separate-outcome issue remains post-ECI; its TODO is the only code change directed at it, with no separate issue-specific investigation, repair, lane, blocker, deadline, forecast, proof, or review. Keep the TODO in the bounded Producer assignment and full current-diff review, followed by the usual independent verification and checkpoint; if no assignment covers it, route a bounded TODO-only Producer assignment under the linked rule. Do not limit review to the TODO hunk. Split mixed remedies.

Apply [coding-policy enforcement](coding-style-admission.md) when screening governed contributions. A concrete applicable requirement violation in the selected design or contribution supplies the outcome link; no independent runtime failure or harm threshold is required. This does not admit unrelated baseline cleanup.

After scope-screening, for ECI work (including ECI nested in ATE), order in-scope findings and remedies by [Main ECI quality responsibility](../../explore-critique-implement/references/fast-path.md#main-eci-quality-responsibility). This changes work order only; it does not alter `now` or defer eligibility.

Only an in-scope, non-hard, impact-trivial, isolated finding may defer. Require evidence supporting each eligibility condition (in-scope, non-hard, impact-trivial, and isolated), plus a technical reason and revisit trigger. Missing evidence invalidates only that defer conclusion; it never waives criteria or gates unrelated bounded work. Deadline, fatigue, sunk cost, authority, completed work, and calendar date never qualify. Mutually exclusive remedies for the same criterion from different decisions are `ignored-contradictory`; do not pretend the unmet criterion is resolved.

## Design-versus-implementation boundary

Critics report implementation-level findings as well as design findings; do not discard a detail merely because the approved design can survive it. Classify by blast radius and repair nature. A design-level finding is REJECT-worthy when substantial scale-up would amplify the problem and fixing it requires a new design decision, model, contract, trust boundary, or feasibility assumption; semantic/model/contract errors are normally in this class. A local syntax/style/wiring/mechanical defect with contained blast radius that can be repaired without changing the approved design is an implementation detail. A critic still reports every real finding under its own adversarial rubric and may label it REJECT when its evidence supports that label; do not bias or silence the report. Within ECI, a fresh Step 2 critic alone decides final impact and may downgrade a contained implementation detail to CONDITIONAL or tech-debt, so it is never a design REJECT after that adjudication. Other workflows use their owning Coordinator/Lead for adjudication. The implementation loop still resolves or records every such finding in its acceptance/debt ledger. Do not silently drop a fixable detail, and do not downgrade a design change to polish.

Classification or tech-debt recording does not waive [coding-policy enforcement](coding-style-admission.md) or its final-acceptance consequence.

Examples: a changed persistence model, ownership rule, public contract, or data/side-effect boundary is design-level; a typo, local adapter wiring error, or mechanical call-site correction that preserves those decisions is implementation-level.

## Three-critic review

For ECI outcome-level review dispatch and evidence coordination, follow the [Job Orchestrator contract](../../explore-critique-implement/references/task-orchestrator.md#worker-and-helper-boundaries). The current Job owner dispatches required reviews for each outcome; a fresh Step 2 critic decides ECI finding treatment, and the ECI Supervisor retains acceptance. The outer Coordinator/Lead retains review assignments and acceptance for the outer workflow.

For governed code, use fresh blind Critic A (style), Critic B (correctness/fidelity), and fresh special Critic C (long-term health) in parallel. Name at least one critic to check that controls prevent accidental mistakes without treating bots as malicious. ECI E2E follows the [router's cadence, scope, and timing policy](../../explore-critique-implement/SKILL.md#e2e-cadence-scope-and-timing).

ECI E2E triggers: [Configuration E2E contract](../../explore-critique-implement/SKILL.md#configuration-e2e-contract) and [Runtime E2E policy](../../explore-critique-implement/SKILL.md#runtime-e2e-policy).

All report only. A/B/C independently classify findings and state impact. Critic A checks applicable style guidance and never lets style downgrade a hard contract. Critic B guards concrete behavior, accidental-harm boundaries, interfaces, tests/proof, and fidelity. Critic C checks long-term health, architecture, actual scope/guidance reconciliation, and final-state clarity. Withhold each iteration gate until its required reports and focused proof arrive; apply E2E only at the cadence the ECI router specifies.

## Review packet and finding contract

Include the shared [review investigation responsibility](../review-investigation.md) in reviewer packets.

Normal reviewer packets contain original user requirements, exact target/diff, objective/criteria, readable lineage context, available evidence, and scrutiny rules. For a checkpointed review, the packet names the checkpoint and states explicit exclusions. The checkpointed `current diff` is the named parent-to-checkpoint range. Respect explicit exclusions and exclude later ambient worktree changes. This is review scope, not admission proof. Every finding attaches to a specific option/target with severity, impact (`trivial` or `substantive`), one-line rationale, evidence location, and concrete fix direction. Missing presentation detail is repaired in the review report; it is not a work-admission test.

ECI packets also cover retained fast changes through the [adoption boundary](../../explore-critique-implement/references/fast-path.md#adoption-review-and-closure); report missing cumulative coverage or stale final-state evidence.

Critic A loads each matching installed coding skill and checks actual adherence under [coding-policy enforcement](coding-style-admission.md). Critic B independently verifies stated purpose and interface fulfillment before quality. Critic C judges final state, not change-history defense, and flags coupling, hidden dependencies, wrong layer, unclear names, duplication, missing/premature abstraction, or architectural mismatch supported by an applicable requirement or concrete harm. Discretionary taste is NIT; “would refactor someday” alone is not a finding.

Required E2E uses the shortest faithful real path that proves the original criteria and checks relevant regressions; run the full suite only when it supplies required coverage. Cite command and output/state/screenshot. A missing required final E2E returns to the implementer before acceptance, not to a false approval.

## Gate evaluation

After all required reports, pre-route findings; within ECI, a fresh Step 2 critic makes the [finding decisions](../../explore-critique-implement/SKILL.md#step-handoffs) from the unchanged reports. Each finding gets one treatment: `now`, valid defer, `ignored-contradictory`, post-ECI observation/follow-up for an unadmitted separate outcome, `user decision` for a decision the critic names as the user's, or `no change` with its reason for a NIT not taken or a finding that cited evidence refutes or shows resolved or superseded; a finding with separable parts counts as one finding per part. At least one substantive `now` REJECT/CONDITIONAL, or design/API-uncertain E2E failure, becomes one design-revision issue batch for re-exploration/critique. A trivial `now` REJECT/CONDITIONAL or trivial E2E failure returns in one implementer repair batch. A clean iteration gate has no remaining `now` issue and all required same-run focused proof; final acceptance also needs the final E2E pair when triggered. Treatments other than `now` and `user decision` are not clean-pass defects. A `user decision` holds final acceptance until a fresh Step 2 critic decides the finding again from the user's quoted answer. Admitted child outcomes retain independent acceptance under the Job contract.

The design-revision batch groups all remaining issues by affected artifact/API/contract and includes source reviewer, severity, impact, evidence location, exact issue text, relevant debt/defer reference, and acceptance criteria from the original requirements and findings. Never patch substantive gate issues one by one without the design loop. Review caps and loop-breaker/BRP escalation remain owned by the outer workflow.

## Independent-review discipline

Review independently before reading sibling findings. Do not praise or rubber-stamp Producer/peer output. For multiple non-execution reviewers partition primary lenses (correctness/edge, accidental-harm and resolved-target safety under the non-malicious model, design/semantic/naming) while still reporting real out-of-lens issues. A minority dissent needs counter-evidence to override. Higher evidence tier wins contradiction. Reviewers never edit their target; dispute one evidence-backed exchange, then a fresh Step 2 critic decides ECI findings, or the owning Coordinator/Lead adjudicates findings in another workflow.
