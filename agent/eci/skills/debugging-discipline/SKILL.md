---
name: debugging-discipline
description: Use when debugging and needing falsifiable hypotheses, alternative explanations, or stronger root-cause discipline
---

# Debugging Discipline

This skill supplements `systematic-debugging`; for established incorrect local behavior with a clear cause and repair, its direct-fix path takes precedence over that skill's full phased procedure, while uncertain cases retain the full procedure and the additional rigor here.

Mitigation is not a fix: lowering failure probability is containment until the cause chain is repaired.
For an uncertain cause, require explicit causality: trigger -> mechanism -> failure -> repaired link. Missing links require investigation; an obvious local defect with a clear cause and repair needs only proportionate rationale and validation.

## Required Procedure

Choose the path from evidence. If incorrect local behavior is established and its cause and repair are clear, fix it directly with before/after verification and checks proportionate to regression risk. A real defect encountered during isolation may be fixed even when its relation to the original incident is unproven or it may mask that incident. This does not authorize an unrelated bug hunt. Do not invent alternative causes, falsification exercises, or a bisect solely to justify that obvious repair. If behavior, cause, or repair is uncertain, use the RCA pipeline:

```text
loop(loop(RCA, critic), repro), loop(fix, review)
```

Core rule: keep the whole edit-to-result loop fast; use the shortest repro that establishes incorrect behavior under its isolated conditions and rerun it constantly. Iterate on that defect without requiring production-equivalent ordering or inputs first. Similar symptoms do not establish a shared cause. After each probe, logging change, or fix, check the isolated failure; after fixing it, retry the original path. If the original failure remains, re-isolate and repeat; use E2E for required user-path proof.

- **Role-neutral first response:** Any agent that hits a bug, including QA, deployer, reviewer, or observer roles, must do basic investigation and feasible first fixes before reporting failure. Escalate only after documenting repro/evidence, attempted fixes, results, and the blocker or out-of-scope boundary.
- **Repro loop:** Keep a minimal, fast repro command/path ready. Establish incorrect behavior under its stated conditions before each RCA pass and rerun it after each small probe, logging change, or fix. If reproduction is slow or inconsistent, first shrink/stabilize it with evidence, logging, or tests until the failure mode is observable. Explain why the behavior is wrong under those conditions; resemblance to the original symptom alone is insufficient.
- **Iteration latency:** When practical, aim for the whole edit/build/setup/repro/evidence loop to take less than a few seconds; this is a soft aspiration, not a gate. Once responsive, focus iterations on any remaining isolation and causal questions. When delays limit that work, time build, setup/deploy, repro, and evidence collection; investigate the dominant cost. A minutes-long build warrants inspecting timing/logs and build configuration for avoidable work, such as unnecessary clean/full rebuilds, dependency invalidation, cache misses, or resource contention. Try the smallest evidence-backed improvement within the debugging task. Bound the effort by expected savings over the remaining iterations; return to diagnosis when further tuning will not repay that effort. Compare equivalent before/after runs. Verify the faster path incorporates current edits and preserves assertions and required final user-path proof. When changing the repro, establish the isolated defect on a failing revision before using it to evaluate its fix; original-incident equivalence is not a pre-fix gate. Record the measured reason for unavoidable delay.
- **Regression check:** Every RCA classifies `regression: yes/no/unknown`. Check prior known-good behavior from commits, releases, configs, dependency/data states, previous test/run artifacts, CI history, user reports, and recent changes. If unknown after feasible checks, record why and what evidence is missing.
- **Live/prod proof gate:** Before waiting on live/prod, record {question, cheapest faithful environment, rejected cheaper-environment reasons, active owner}. Run the proof now; if out-of-role, route it to an active owner. Live/prod is allowed only for prod-only env/config/network proof, final confirmation after cheaper proof, or capture of a currently observable prod-only failure.
- **RCA/critic loop, when needed:** Apply to uncertainty about the local behavior, cause, or repair, not merely an unknown relation to the original incident. Gather evidence first: inspect the failing path, relevant code, logs, tests, recent changes, and adjacent systems. State an informed RCA hypothesis from that evidence, not a guess. The critic must identify alternative explanations, missing evidence, and predictions that could falsify the hypothesis. Repeat until the critic has no unresolved objections.
- **RCA instrumentation:** RCA may add any scoped diagnostic code needed to gather strong evidence for or against a root-cause hypothesis: logs, traces, counters, assertions, probes, tests, scripts, data captures, or temporary instrumentation. Keep probes targeted and reversible; final review decides what to keep, remove, or convert into regression coverage.
- **Bisect during RCA:** If the required behavior worked at a known commit, release, config, dependency version, or data state, bisect from known-good to current-bad before broad speculation. Run the fast repro at every step. If only a rough timeframe exists, first establish known-good and known-bad anchors.
- **Fix/review loop:** Explain the established local defect and why the diff repairs it; review before/after evidence and regression risk. For uncertain cases, include the critic-approved cause chain and RCA evidence. Unknown "why" or symptom-only change requires investigation unless containment was explicitly requested. Repeat until required review finds no blocking issue.

When ECI or ATE owns the debugging work, preserve that protocol's required roles and reviews; the direct-fix path does not waive them. Outside ECI/ATE, when subagents are explicitly authorized and RCA is needed, use one persistent `repro/RCA/fix` subagent for all three phases so reproduction and RCA context carry into the fix. Use separate subagents for `critic` and `review`. Prefer fresh `critic` and `review` subagents each iteration; close or replace them after each pass so prior conclusions do not anchor the next critique. Pass only a compact evidence packet: problem statement, repro steps, relevant logs, current RCA or diff, constraints, and open questions.

Report fix proposal and confirmation to the coordinator; when RCA is needed, also report suggested and critic-approved RCA. Direct fixes need no invented RCA milestones.

- Label state as `hypothesis`, `accepted-for-fix`, `fix-submitted`, or `confirmed-fixed`.
- Only `confirmed-fixed` may say RCA/fix is closed for its named defect. It requires domain-required acceptance proof on that defect's failing path; original-incident closure requires its original/user-path proof.
- Source/unit proof alone is source readiness when required E2E/integration proof for the claimed scope remains open.
- Name the evidence scope: an isolated defect may be fixed while the original incident remains unconfirmed. Preserve its causal proof and report the original-path retry separately; do not close the original incident solely from the isolated result.
- Include the local defect, repair rationale, and validation evidence. For RCA, include cause chain, falsifying prediction tested or still needed, repaired link, unresolved alternatives, and regression status. If `regression: yes`, report the regression explanation alongside the RCA: prior working evidence, introducing change/state, why existing tests/runs/guards did not catch it earlier, and the mechanism that changed old-good into current-bad.
- Coordinator owns current root-cause state and passes it into the next critic/review packet.

## Reproduce first

Establish the defect under the repro's stated conditions before causal investigation and repair. A reduced repro need not match production ordering or inputs before its own defect can be fixed. Slow repros block learning; shrink or automate them before expanding scope, then retry the original path after the isolated fix.

## Hypothesis Discipline

Apply these rules when the cause is uncertain; clear local defects use the direct-fix path above.

- Label every potential cause as HYPOTHESIS until falsified — saying "root cause identified" prematurely leads to wasted effort on wrong fixes.
- An RCA hypothesis without cited evidence and explored code/log/test context is a guess. Gather more evidence before testing fixes.
- Before testing a hypothesis, state at least one alternative explanation. If you can't, you don't understand the problem yet.
- A hypothesis becomes "confirmed root cause" only when you have tested a prediction that would have DISPROVED it if wrong, and it survived.

## Observability and error descriptions

- Fix concrete observability gaps and inadequate error descriptions found during debugging before closing the fix, even after the bug is understood or functionally fixed. Leave sufficient diagnostics unchanged.
- Use the smallest lasting diagnostic improvement needed: logs, traces, metrics, or other instrumentation. Removing temporary probes must not reopen the gap; regression tests alone do not replace needed diagnostics.
- Improve errors/logs with the failed operation, relevant context/IDs, and known cause; preserve causes when wrapping. Redact sensitive data while keeping useful detail.
- When diagnosis lacks evidence, add targeted instrumentation and automated repro tests. Exercise the failure path to verify improved diagnostics.
