# ECI Step 4 — Review

Apply the shared [review investigation responsibility](../../references/review-investigation.md) throughout this assignment.

This module is for one independent reviewer. The `current diff` consists of the parent-to-Producer-commit ranges named in the Producers' handoff reports. Respect explicit exclusions and exclude later ambient worktree changes. This is review scope, not admission proof. Work only from the assigned original requirements, intended scope, and supplied evidence. Do not read sibling reports before submitting your own. Report findings to the current Job owner only, except for the routing exception under [violation re-read escalation](../SKILL.md#violation-re-read-escalation). Do not edit the reviewed target, decide a gate, or perform lifecycle actions. Bounded Helper support is permitted under the Worker and Helper boundaries below, but it does not transfer required review responsibility.

Also review any cumulative retained-fast target supplied under the [adoption boundary](fast-path.md#adoption-review-and-closure). Report uncovered retained hunks or material late changes to the current Job owner, including discoveries relevant to [design reopening](fast-path.md#evidence-can-reopen-design).

Apply [main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility): assess retained Fast work with the same correctness, style, and maintainability scrutiny as main-path changes; passing producer tests or an existing checkpoint does not resolve quality findings.

Follow the normative [post-Fast completion sequence](fast-path.md#post-fast-completion).
Review the final cumulative scoped state after the Step 2 critic decides exactly one canonical disposition per in-scope inventory item.
For post-Fast completion, independently review the final cumulative scoped state after the new exploration and design disposition, including unchanged retained code. Report missing final-state coverage; earlier reviews alone do not satisfy this assignment.

Tag every factual claim. An untagged claim or unpromoted T5 is not review evidence. For every finding, state one [classification](../../references/workflow-runtime/review-policy.md#evidence-and-severity) (`REJECT`, `CONDITIONAL`, `NIT`, or `PASS`), its impact, exact location, concrete evidence or repro/test output, and the condition that would resolve uncertainty. A behavior/interface mismatch is a hard finding, never a style deviation. For each CONDITIONAL, also state its scope: in scope or follow-up under [impact-proportional routing](../../references/workflow-runtime/review-policy.md#impact-proportional-routing). On a round without a finding critic, [Step 4](../SKILL.md#step-handoffs) routes each CONDITIONAL by the scope you state.

The current Job owner—its Orchestrator—assigns the Job's required workflow reviews. The Supervisor retains acceptance. A reviewer may use Helpers for bounded support, but personally establishes its independent baseline, verifies evidence, supplies its required verdict, and performs its assigned required E2E run. A Helper cannot replace any of those duties or carry conclusions into another independent review.

## Critic A — coding requirements

Check applicable coding skills, formatter/linter/config anchors, actual scope, exceptions, and post-write tool evidence under [coding-policy enforcement](../../references/workflow-runtime/coding-style-admission.md). Report every source-backed applicable requirement violation; reserve `NIT` for discretionary preferences. Apply it to prod code only ([Prod-code focus](../../../eci-binding-contract.md#prod-code-focus)); test-only code style is at most `NIT`.

Check in-scope tests and the test runs used as proof against the [test-efficiency rules](../SKILL.md#test-efficiency), even when no test files changed. Check each proof suite’s command and elapsed time against the underlying evidence. Report test-only slowness as a proof gap only when it makes the proof untrustworthy; report other test-only speed work, including a missing speed review or record, as a follow-up. Distinguish a concrete violation from missing evidence.

## Critic B — correctness and fidelity

Check correctness, safety, concrete requirements, interfaces, non-style boundaries, and root-cause/regression rationale. Check scope fidelity and least restriction against `exact user source → faithful requested outcome → bounded scope`. Distinguish a repair needed to meet or prove that outcome from an invented separate outcome; report the latter as `REJECT` without treating stale lineage as a work gate. Verify that named functions, types, and interfaces actually provide their claimed behavior. Check that cited proof is faithful and discriminating: a check that cannot fail, a proxy for a required real path, or a missing required observation is a proof gap. A prod performance regression is a correctness finding.

Verify [clean system and repair](../../../eci-binding-contract.md#clean-system-and-repair): repairs reach original consumers without creating competing authority. Check relevant duplicates within authorized scope, consumer routing and behavior verification before retirement, justified copy lifecycles, and preservation of unrelated work. Report other duplicates as follow-ups.

Assume bots are non-malicious. Check that each control catches a concrete accidental broad, destructive, wrong-target, wrong-owner, or cross-scope effect, or a bypass of a user-required route, without treating unfamiliar syntax, wrappers, metadata, receipts, hashes, or shell punctuation as an error by itself. Require every enabled gate to name a bounded legitimate-work escape path; if it has none, recommend disabling it. Preserve the legitimate owner and session; do not recommend rerouting owner-scoped dependency work.

## Critic C — long-term health

Check maintainability harm from debt, coupling, hidden dependencies, code smells, layer/module fit, naming, abstraction, architecture, and self-explaining intention. If assigned a pre-write intention check, return `reconstructed intention:` followed by 2–4 bullets and stop; a later full-context review remains independent.

For an applicable [ownership argument](explore.md#ownership-argument), reconstruct the shared domain question from the original requirements, current contracts, and representative consumers. Independently revalidate the selected argument’s premises before checking implementation conformity. User examples and accepted designs do not alone establish a shared taxonomy. Review scope bounds the changed contribution, not the contextual reads needed to judge its contracts. For interface or responsibility changes, include independently recovered [interface boundary evidence](../../references/review-investigation.md#interface-boundary-evidence) in the architecture conclusion, including a no-finding verdict. When a shared category names a platform or implementation, test substitution: would another supported implementation of the same capability require changing the shared contract, and do its consumers need that distinction? State the domain question, concrete supported substitution (or evidence none applies), and consumer evidence in the report; report material ambiguity if that evidence does not resolve the category. Retain implementation identity where the contract requires it. Report misplaced responsibilities, duplicated policy, or ownership drift with a concrete scenario and source evidence through the existing finding format. Route material findings under the existing [Producer handoff](implement.md#write-boundary-and-submission).

## E2E

For the final independent E2E, run a fresh check on the stabilized final cumulative revision under the central policy. Use the shortest faithful real path, preserve required criteria and relevant regression coverage, and cite command and output/state/screenshot. Proxy evidence alone is insufficient. Continue or abort the run under [Prod-code focus](../../../eci-binding-contract.md#prod-code-focus); report incidents, missing observations, and observed product defects separately. Incomplete evidence alone is not a product REJECT.

E2E cadence, scope, timing, and triggers: [ECI E2E policy](../SKILL.md#e2e-cadence-scope-and-timing).

## Review red flags

- A reviewer shares producer identity or relies on a sibling result.
- A report lacks claim tags, an impact rationale, precise evidence, or an honest uncertainty.
- A reviewer changes the work instead of reporting the finding.
- An intention reconstruction is treated as a full review or permission for a write.
