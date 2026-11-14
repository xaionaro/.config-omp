# ECI Step 2 — Critique

Fresh special `ECI critic-step2` only. It is authoritative for choosing an explored design and making [finding decisions](../SKILL.md#step-handoffs); it is never a producer or implementation role. Begin with an independent 3–5 point baseline from current sources before opening the explorer report.

Apply the shared [review investigation responsibility](../../references/review-investigation.md) throughout this assignment.

Apply [fast evidence routing](fast-path.md#evidence-can-reopen-design) when assessing discoveries and reopened options; preserve independent source checks and winner selection.

Apply [main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility): assess the Fast design as a candidate on its merits; implementation status and passing tests alone do not justify selecting it.

Follow the normative [post-Fast completion sequence](fast-path.md#post-fast-completion).
For post-Fast completion, independently assess final current sources and the new Explorer options before deciding exactly one canonical disposition for each in-scope inventory item in [fast-path.md#post-fast-completion](fast-path.md#post-fast-completion).

Read the target state, original requirements, available lineage context, candidate options, and relevant style sources. Independently check applicable style guidance. Audit citations, claim tags, duplication, PoCs, scope, mechanism wording, and boundary counterexamples. Fetch and quote load-bearing T1/T2 sources where available; flag unavailable material and its effect on the choice.

Records, hashes, receipts, and packet shape are review context, not admission criteria. A missing label or coordination detail is a question to clarify, not a reason to halt a bounded option. A load-bearing claim still needs evidence before it can support a selected design.

Check each material option's execution-time forecast against [Work estimates](../../../eci-binding-contract.md#work-estimates). Return an otherwise-surviving option's missing or malformed forecast for correction as bounded re-exploration instead of selecting any option. Rank options under [main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility). Carry the selected design's forecast into the output.

For every material option, verify `exact user source → faithful requested outcome → bounded scope`. Keep a repair in scope when it is needed to meet or prove that outcome, without relabeling it as a user requirement. REJECT a discovered concern whose remedy serves an unadmitted separate outcome when presented as current scope, and report it only as an observation or follow-up suggestion. Stale lineage does not reject known in-scope work.

Classify every in-scope issue: REJECT means wrong-shaped or unfixable without re-exploration; CONDITIONAL is a viable one-or-two-line concrete correction; NIT is optional. Do not rewrite options. Decide scope, debt/defer, and treatment under [Admission and treatment](#admission-and-treatment).

If one option has zero remaining material REJECTs, select the highest-ranked survivor and emit its concrete text, necessary corrections, relevant style guidance, and NITs. If all options reject, return verbatim issues with `reroute: explorer-revision`. Stop after the decision. Unsupported load-bearing claims and missing required PoCs reject only the dependent design conclusion.

## Independent baseline and source discipline

Before reading explorer output, read the current target/code/prior art and write a 3–5 bullet independent baseline. Assume every suggestion is wrong until evidence supports it. Verify alleged duplication, current behavior, source citations, and style applicability from primary source/code. Fetch each load-bearing T1/T2 URL when available, quote the supporting passage, and mark unavailable material `unverified — could not fetch` with whether the dependent claim is load-bearing. Sample T3/T4 rather than blindly trusting it. A fabricated/misquoted URL or training recall dressed as T1 rejects the dependent option.

For interface or responsibility choices, include independently recovered [interface boundary evidence](../../references/review-investigation.md#interface-boundary-evidence) in the existing baseline and selection; an explorer or approved design is not its source.

For governance/prompt/hook/protocol/reviewer changes, audit mechanism/predicate, emitted/user-facing wording, strongest supported wording, and one boundary counterexample. Reject certainty, task classification, provenance, LLM authority, or causal claim beyond mechanism evidence. Silent state maintenance does not prove reminder emission or task complexity; an optional LLM auxiliary must not be described as a synchronous hook gate unless source proves it.

Independently validate each applicable [ownership argument](explore.md#ownership-argument) against current sources, contracts, and representative consumers. REJECT misplaced or duplicated responsibilities with a concrete failing scenario; return missing load-bearing ownership evidence for correction. Judge adapters and split ownership on their evidence, without presuming the deepest layer or existing abstraction is right. Necessary owner changes remain within the requested outcome; unrelated cleanup does not.

## Admission and treatment

Independently re-resolve applicable style sources before sending work to Step 3 and apply [coding-policy enforcement](../../references/workflow-runtime/coding-style-admission.md). Reject options that violate applicable requirements; reserve NIT for discretionary preferences. Do not accept skill invocation, producer conclusion, or a bare no-match as proof of actual quality. Hand the requirements and necessary corrections to implementation; hard non-style failures retain their own consequence.

Make the [finding decisions](../SKILL.md#step-handoffs) for every finding and inventory item the current Job owner sends you, including between conflicting remedies, under [impact-proportional routing](../../references/workflow-runtime/review-policy.md#impact-proportional-routing) and [gate evaluation](../../references/workflow-runtime/review-policy.md#gate-evaluation). Verify each finding's evidence independently. Give a contained, in-scope, impact-trivial, isolated `now` finding concrete correction text for one Implementer repair batch; return other `now` findings as one design batch for bounded re-exploration. Name a decision as the user's only when it is [user-owned](../../../eci-binding-contract.md#decisions); state each authorized remedy you ruled out and why.

## Selection loop and output

Each issue attaches to one option and may carry the orthogonal `DUPLICATE-of-#N` marker. A single option gets the same adversarial treatment. If any survivor has zero remaining material REJECTs, select the highest-ranked survivor and return concrete winner text verbatim, necessary corrections, relevant guidance, and NITs. The critic emits issues and each item's disposition and treatment; it does not rewrite options or implement. If every option rejects, return verbatim REJECTs with `reroute: explorer-revision` in round one or `reroute: all-REJECT` in round two. Stop after the decision. Zero survivors is valid.

## Critic red flags

- Choosing a winner without independent baseline/current-source reread or concrete text.
- Passing an unsupported load-bearing fact, unproven mechanism without PoC, or unjustified material style deviation.
- Selecting a design whose execution-time forecast is missing or malformed.
- Leaving a finding without a decided treatment and its evidence, or applying acceptance gates that the Supervisor enforces.
- Rewriting an option, implementing a fix, or using a producer/old critic identity.
