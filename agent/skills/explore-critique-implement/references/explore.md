# ECI Step 1 — Explore

Explorer-only. Treat each assignment as fresh: reread every referenced target and do not trust prior-turn summaries.

Consume Fast owner discoveries under [evidence can reopen design](fast-path.md#evidence-can-reopen-design); independently check them against current sources when developing options.

Apply [main ECI quality responsibility](fast-path.md#main-eci-quality-responsibility): derive options from original requirements and quality needs; existing Fast code does not restrict exploration to its design.

Follow the normative [post-Fast completion sequence](fast-path.md#post-fast-completion).
For post-Fast completion, begin a new exploration after Fast has finished and its write-capable tools have stopped. Reread final shared scoped code and relevant context; an earlier exploration does not satisfy this assignment.

The packet must supply the current iteration, exact problem, original requirements, validated lane/assignment/full chain, related paths, prior attempts/gate findings, governed-scope style-source facts, and a bounded word cap. Reject an untraceable material option presented as current scope; reconcile missing lineage alongside known work.

Return ranked options. Each states what changes, why, where, cost, tradeoffs, relevant facts with T1–T5 tags, and the needed Style Brief/Tool route/No-source proposal for any governed scope. Do not make admission or author authoritative design.

For an unproven core mechanism, attach a minimal isolated PoC that exercises one real input and reports observable behavior. Strip production polish and do not reuse the PoC before final-scope admission. A known/codebase-shipped/documented mechanism may instead state its evidence.

If a bug is assigned, load debugging-discipline and apply its direct-fix or uncertain-case RCA path: establish incorrect local behavior and assess the repair rationale; develop falsifiable repro/RCA evidence and regression status when local behavior, cause, or repair is uncertain. Return facts/options rather than a production fix.

## Required assignment packet

The coordinator gives the exact problem for this iteration in full context; active canonical requirement refs and alias map; register/state version; immutable lane and mutable assignment; complete path/edge evidence; derivation reason; admission binding; expanded requirement chain; existing related paths to reread; and all prior attempts, gate findings, changed files, and rejected reasons verbatim where applicable. Reject a proposed new material outcome that does not reach an active user requirement; reconcile missing lineage alongside known work.

Trace each material option as `exact user source → faithful requested outcome → bounded scope`. A repair needed to meet or prove that outcome stays in the lane and is not a new direct requirement. Return a concern whose remedy serves an unadmitted separate outcome only as a tagged observation or follow-up suggestion; do not turn it into an option or lane. Known in-scope work remains nonblocking while stale lineage is reconciled.

For each option return `{what, why, where it applies, cost, tradeoffs}` and distinguish fact from inference with the required claim tag. State what is proven in the codebase/documented vendor behavior versus what requires a PoC. Do not treat an assumption, training recall, or an unverified URL as T1. Keep the bounded report focused on this iteration; do not smuggle unrelated future work into an option.

## Ownership argument

For a new capability/invariant or substantive responsibility change, include a concise source-backed ownership argument in each material option. Identify who owns its meaning, required information, enforcement, and lifecycle; these owners may differ. Trace relevant contracts and representative consumers. Compare extending an existing abstraction, changing the boundary, and an adapter where viable. Explain the chosen placement, what callers must know, and any duplicated policy. Give one concrete counterexample that would falsify the placement. Neither the deepest layer nor the existing abstraction is automatically correct; valid adapters and split ownership remain options.

Apply [clean system and repair](../../../eci-binding-contract.md#clean-system-and-repair). For applicable resources, identify the maintained source and owner, effective configuration precedence, selected executable and installation owner, and service manager, instance, and data. Trace actual consumers. Compare reuse or repair against any proposed new authority; explain necessary copies and their lifecycle.

Include owner changes needed to meet or prove the requested outcome within its bounded scope. Exclude unrelated cleanup.

## Style-source proposal

For every governed coding scope, resolve applicable governing source facts and propose only the needed Style Brief, Tool route, and/or No-source verdict. The explorer may gather exact clauses/config anchors/skill matches/exclusions, but does not admit the scope. The later independent critic re-resolves and either admits, corrects, or rejects it. A disposable PoC/repro is evidence only; after final-scope admission reuse is limited to what that admission permits.

## PoC validation

An option’s core mechanism needs a PoC when it is neither a well-known pattern, already shipped in this codebase, nor documented vendor API used as documented. The PoC is minimal, real-input end-to-end, and attached to the option with observable output. No error-handling/polish/scaffolding disguises a non-working mechanism. Missing PoC makes that unproven option Step 2 REJECT. An exemption explicitly says `proven by <link/citation>` and identifies the source.
