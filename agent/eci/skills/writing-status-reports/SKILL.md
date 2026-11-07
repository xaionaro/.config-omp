---
name: writing-status-reports
description: Use when giving a concise status report, sitrep, or quick status update about work in progress or recently completed work, including progress, decisions, blockers, risks, verification, or next focus.
---

# Writing Status Reports

Core rule: Report changed state, not activity logs. Be concise, specific, and explicit about progress, decisions, blockers, verification, and next focus.

## When to Use

- Work is in progress and the user asks for status, sitrep, progress, or a quick update.
- Work just finished and the user needs a concise completion report.
- A plan, review, handoff, or checkpoint needs current state without an action log.

## When Not to Use

- The user asks for full logs, raw command output, a transcript, or detailed reasoning.
- The work has not started; give the planned first step instead.
- The task requires a formal handoff; use `writing-handovers`.

## Coverage Categories

| Category | Include when relevant |
| --- | --- |
| State | Current overall state in one line. |
| Progress | Outcomes and changed state, not actions taken. |
| Decisions | Chosen path plus reason. |
| Blockers/Risks | Impact, owner, exact unblock action, and target artifact/path when applicable. |
| Verification | Tests, commands, observed behavior, or "not verified yet". |
| Next Focus | Next concrete work area, not "continue". |

Progress reports changed state and completed outcomes, not files read, commands run, or agents contacted unless those actions are verification evidence.

## Format Rules

- These formatting rules, including exact forecast lines and tables, apply to rendered user-facing reports. The persisted YAML projection carries equivalent structured fields as described in [Latest Status Report](../context-ledger/SKILL.md#latest-status-report).
- Keep ordinary progress commentary short: one tight paragraph or relevant coverage categories as bullets. For a requested status report, use the applicable detailed bundle below by default.
- Lead with state, then include only categories that changed or matter now.
- Name exact affected area, requirement, command, file, or decision when relevant.
- Use "not verified yet" instead of implying unrun checks passed.
- For blockers/risks, state impact, owner, and exact unblock action; user blockers must name the user's action and target artifact/path when applicable, e.g. `review docs/design.md`.
- Preserve parent/child work: use a tree, or include `Task ID` and `Parent ID` columns; do not flatten children into peer lanes.
- If task IDs exist, make them hierarchical, e.g. `1`, `1.3`, `1.3.2`, and sort children under their parent.
- In material ECI status, show `exact user source → faithful requested outcome → bounded scope` in readable form. Do not broaden the outcome through a generic paraphrase or relabel a reason, discovery, or inferred safeguard as a user requirement.
- Keep a repair in its lane when it is needed to meet or prove that outcome. Put a discovered concern serving an unadmitted separate outcome under Risks as an observation or follow-up suggestion, not as a lane, assignment, code change, review, deadline, forecast, or proof program. Report admitted child Jobs with their stable parent chain, current owners, and distinct outcome evidence under the [recursive Job contract](../../../skills/explore-critique-implement/references/task-orchestrator.md#recursive-child-admission-and-closure).
- When requirement context is missing or stale, write `lineage unavailable—reconcile` beside the lane and continue the status report and safe bounded work. Do not invent a registry or turn missing context into a blocker.
- Known in-scope work remains in scope while lineage is reconciled; do not infer a new outcome from a missing record.
- Workflow activity is overhead, not progress.
- In direct work, include requirement context only when it helps the reader.

## Active work and progress

For a requested status report, include the applicable active-work, timeline,
and progress bundle below alongside the relevant coverage categories. Show the
full known in-scope task tree, including descendants at every priority and their
assigned agents or `unowned` and parent/requirement context. Calculate priority
as impact points on a declared shared scale divided by maximum estimated
remaining work minutes; order siblings by descending ratio. This compares
expected benefit per unit of remaining work. State score and estimate bases;
use labeled evidence-backed guesses for missing inputs or mark the item
`unscored` with a reason. Elapsed duration is not comparable to work effort;
zero work minutes produces no finite priority. Identify missing task evidence
rather than implying the tree is complete.

When newly time-estimated items enter the queue, have an independent critic review only those items' estimates, impact, and priority. The reporter repairs findings and requests another review as needed, for at most five reporter–critic rounds. Expose unresolved findings or unavailable review honestly. A report refresh, compaction, or recalibration of existing items alone does not trigger this review; do not critique the whole report under this rule.

Present a separate top-priority list and the full task tree. Both refer to the same task records. Reconcile the structured YAML against current ledger and forecast evidence, validate it, then render and publish the full rendered result using the status-report tool as described in [Latest Status Report](../context-ledger/SKILL.md#latest-status-report). Validation may report incomplete evidence without preventing rendering. Correct missing known sections in the YAML and rerender before publication. Label missing, stale, inconsistent, or incomplete evidence and affected coverage incomplete. If structural failure prevents rendering, answer directly with supported facts, label the response incomplete, and repair the report alongside safe work.

For an active multi-agent status report, show the recursive roster of actually
active agents under their parent agents, including each agent's current task, owner or
role, model, and reasoning effort. Include the top-level tasks and their
owners. Use live roster and assignment evidence; mark unavailable fields
`unknown`. Exclude idle agents from the active roster even when they have assigned
tasks; retain those assignments in the task list. If runtime
telemetry is unavailable, label launch settings as requested, not observed. State
when no agents are active or roster evidence is unavailable. Show an inactive
ancestor only as labeled ancestry context, never as an active roster entry. Do not infer
model or effort from a parent or a role.

In every task list include priority,
impact, and effort columns. Distinguish assigned impact points from calculated
priority (impact points divided by maximum remaining work minutes). Show the
calculated priority and its input values;
include concrete impact and its score points. Priority labels group forecasts
only; they do not rank work. Separately,
give the estimated remaining work effort or elapsed duration with units and a
range, explicitly identifying which it is and distinguishing it from agent
reasoning effort. For every priority, attempt to decompose remaining work into
packages estimated at 30 minutes or less, including required review, checks,
repairs, and acceptance. If a package cannot reasonably be decomposed further,
say why and retain its estimate. Estimate each package from evidence or a
labeled educated guess. Report a completion estimate for each priority present,
including unscored items, and an overall completion estimate for all
remaining in-scope work. Give each estimate's basis, material assumptions,
uncertainty, and dependencies, including external waits. Combine serial
durations and use the latest required parallel endpoint on the critical path;
do not sum parallel durations or double-count shared work. Follow
[Work estimates](../../../eci-binding-contract.md#work-estimates) and,
for ECI, the [scenario forecast rule](../context-ledger/SKILL.md#eci-scenario-forecasts).
Use evidence-backed guesses for missing impact and work inputs, labeled as
estimates, or leave priority unscored with a reason. Never invent
measurements or omit the completion estimate.

At report time T, for every Job with evidenced progress in [T−4h, T), including
Jobs completed within that window and descendant Jobs, show its parent chain and
one timeline with exactly three columns: `UTC interval`, `Fast`, and `Normal`.
Divide the window into eight half-hour, half-open intervals; use event occurrence
time where known. Omit empty intervals and Jobs
with no evidenced progress. Summarize evidenced progress in its path column and
include relevant commit short hashes and subjects there. Attribute commits only
where Job, path, and time are supported; label uncertain attribution. State
when history is incomplete or unavailable. Use `—` for a path verified empty
in a displayed interval and `unknown` when its history is unavailable or
incomplete; omission does not prove no work occurred. State when no Job has
evidenced progress, distinguishing verified empty history from unavailable
history. Never
invent events or treat a commit as proof of acceptance. Timeline-only reports
follow the forecast exception below.

Calculate progress from the report YAML's leaf task statuses using the status report tool. Each known leaf is one estimated package from the best-effort half-hour breakdown; only `CLOSED` with acceptance evidence is complete. Parent tasks are excluded. Flag leaves whose upper estimate exceeds 30 minutes and explain why they cannot be split. Label the result an equal-package estimate, not elapsed-time progress; rebaseline when scope is added or split. Unknown or null statuses are unscored. If scope or any leaf status is unknown, label the result partial and identify coverage and unscored tasks. With no scored leaves, show unknown without a percentage or filled bar. Never store a separate progress percentage or bar or infer an ETA from it.

## Lane forecasts

A lane is an independently advancing workstream, not an ECI step. Serial implement→review→repair→review→implement stays one lane with one critical path. Create distinct lanes only for independently advancing work with separate ownership or synchronization.

Only a material changed-state ECI update emits forecast lines. In one such response, emit each canonical forecast once. Preparation-only commentary, pure explanation, roster/wait, and timeline output omit forecast lines unless they also report a material change.

Use these labels when a material changed-state report includes the corresponding field.

| Field | Record |
| --- | --- |
| Next milestone | `Next milestone: <named outcome>` |
| Forecast deadline | `Forecast deadline: <named lane/task outcome> will be finished by <UTC ISO8601>.` |
| Root completion forecast | `Root completion forecast: <named active root-task outcome> will be finished by <UTC ISO8601>.` |
| Forecast assessment | `Forecast assessment: complete` or `Forecast assessment: provisional — <material risk/evidence gap>; target conditional.` |
| Forecast recalibration | `Forecast recalibration: <prior UTC ISO8601> → <current UTC ISO8601>; why moved: <why>; supporting evidence: <evidence>.` |
| Initial forecast evidence | `Forecast recalibration: unchanged — baseline <UTC ISO8601>; supporting evidence: <evidence>.` |
| Dependencies / critical path | `Dependencies / critical path: <none, named dependency + owner/resume, or critical path>` |

In a material changed-state ECI update, each non-`CLOSED` lane with a
user-requested outcome names its next milestone and one canonical forecast line.
For each unrepresented active root-task outcome omitted by its lane reports in
that update, include this standalone line once:

`Root completion forecast: <named active root-task outcome> will be finished by <UTC ISO8601>.`

The forecast line itself names the finished outcome using the [canonical forecast outcome wording](../context-ledger/SKILL.md#forecast-outcome-wording). A separate `Lane` or `Next milestone` does not substitute, and the outcome never names a critic, reviewer, actor, or stage. Root completion is full root completion, not a child sum or stage. In that update, emit each
lane/root canonical forecast once. For
a changed lane or root forecast, restate its current canonical line, then state
Forecast recalibration: <prior UTC ISO8601> → <current UTC ISO8601>; why moved:
<why>; supporting evidence: <evidence>.
Beside each lane/root canonical forecast line, state `Forecast assessment:
complete` or `Forecast assessment: provisional — <material risk/evidence gap>;
target conditional.` Keep the canonical line exact.
A `CLOSED` lane records `Completed: <UTC ISO8601>; no active forecast deadline.`
A `CLOSED` lane records completion; do not invent or revive a forecast deadline
or recalibration. For an initial forecast, write `Forecast recalibration:
unchanged — baseline <UTC ISO8601>; supporting evidence: <evidence>.`

State dependencies and parallel work. For parallel children, report the single
critical-path deadline; child deadlines remain parallel; never add or sum
parallel child deadlines into a parent, root, or mission deadline.

#### ECI forecast scenario spread

For each material ECI forecast update, including nested ECI, add a separate
report-only block beside the canonical forecast line. Keep the forecast line
exact and put its assessment beside it using the labels above. Show the base
case and every named material downside scenario, including any whose timing is
not supported; identify the lane or root in each row:

| Lane/root and case | Trigger/evidence | Affected work/dependency | Added duration/range and basis | Evidence assessed; assumptions/limits | Endpoint/range | Evidence gap / next fact-finding action |
| --- | --- | --- | --- | --- | --- | --- |
| `<lane/root> — base case` | No listed downside scenario | Remaining authorized in-scope critical-path work | No generic buffer | `<current work facts and progress used; assumptions/limits>` | `<UTC>` | — |
| `<lane/root> — <scenario or conditional combination>` | `<trigger/evidence>` | `<work/dependency>` | `<estimated duration/range; evidence or labeled educated-guess basis>` | `<relevant facts reviewed and assumptions/limits>` | `<estimated UTC endpoint/range; label conditional guesses>` | `<specific evidence gap and next fact-finding action; — when resolved>` |

Apply the canonical [ledger scenario rule](../context-ledger/SKILL.md#eci-scenario-forecasts)
for decomposition, educated guesses, external-wait assumptions, evidence gaps,
duration combinations and critical-path endpoints. Always report an overall
completion estimate; a bounded probe supplements it. Keep guessed timing
provisional and conditional until evidence supports or resolves the risks.
Summarize the basis, assumptions, limits and next fact-finding action; never
present guesses as observations, guarantees, external commitments or hard caps.
Include every material scenario and its conditional endpoint in the spread;
the latest estimated endpoint is no absolute worst case. Never invent
probabilities.

Forecasts are advisory. They never gate work, grant or deny permissions, require
artifacts or receipts, create blockers, require parsers, or require per-command
ceremony. Missing or stale forecasts are planning-quality defects. Reconcile
them alongside safe work without delaying the update.

| Pressure | Correct report |
| --- | --- |
| Material changed-state report omits a milestone or forecast | The report is corrected alongside safe work. |
| Forecast lacks current evidence | A changed lane/root forecast without its restated current canonical line, prior/current UTCs, why moved, and supporting evidence is stale. |
| Material risk has no supported duration or endpoint | Decompose work and give labeled educated duration/endpoint guesses, including explicit external-wait assumptions; retain the evidence gap and next action, provisional assessment and conditional overall target/spread. |
| Parallel children | For parallel children, report the single critical-path deadline; child deadlines remain parallel; never add or sum parallel child deadlines into a parent, root, or mission deadline. |
| Dependency is paused | A paused dependency names its owner and resume condition; it is not a user blocker. |

## Multi-Lane Mission Status

For “where are we on each lane?” or “who works on each lane?”, report every in-scope lane known from the active plan, ledger, or test matrix. Review, deploy, and proof are current work within a lane, not automatically separate lanes. Include idle, waiting, and paused lanes; create a distinct lane only when work independently advances with separate ownership or synchronization.

Use three separate status columns so source readiness cannot be mistaken for E2E completion.

When work is flat and has no task IDs, omit `Task ID` and `Parent ID`. Keep `Lane` followed by `Lane requirement context` when that context would help the reader. Use a known readable reference or `lineage unavailable—reconcile`.

| Task ID | Parent ID | Lane | Lane requirement context | Priority | Impact | Effort | Stage | Owner | Implementation Status | Test Status | Prod Status | Blocker | Next milestone | Forecast deadline / recalibration | Dependencies / critical path | Next proof/action |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `1.3.2` or `none` | `1.3` or `none` | `<human-readable lane result wanted>` | `<known requirement or lineage unavailable—reconcile>` | `<impact points / maximum work minutes = ratio, or unscored with reason>` | `<concrete impact, score points and basis>` | `<remaining work effort range in minutes and basis; elapsed duration separately>` | `normal` for ECI | `<person/agent or unowned>` | `NEW` / `IN PROGRESS` / `PAUSED` / `BLOCKED` / `CLOSED` | `NEW` / `IN PROGRESS` / `PAUSED` / `BLOCKED` / `CLOSED` | `NEW` / `IN PROGRESS` / `PAUSED` / `BLOCKED` / `CLOSED` | `none` or `PAUSED: <dependency lane; impact; owner; resume condition>` or `BLOCKED: <exact user input/decision; impact; owner: user; exact unblock action; target artifact/path>` | `Next milestone: <named outcome>` | `Forecast deadline: <named lane/task outcome> will be finished by <UTC ISO8601>.`<br>`Root completion forecast: <named active root-task outcome> will be finished by <UTC ISO8601>.`<br>`Forecast recalibration: <prior UTC ISO8601> → <current UTC ISO8601>; why moved: <why>; supporting evidence: <evidence>.` | `Dependencies / critical path: <none, named dependency + owner/resume, or critical path>` | `<next evidence/action>` |

For ECI, record `Stage: normal`. Show main and fast progress under the same task,
with each owner, provisional evidence, current ECI decision, and adaptation.
These are paths, not separate stages or automatic lanes; apply [ECI fast
path](../../../skills/explore-critique-implement/references/fast-path.md) for their relationship.
Missing, stale, or unknown stage metadata is reported and reconciled
without pausing harmless work. Stage records never authorize work or change
the implementation/test/production status meanings below.

After coordinator-confirmed Fast completion, report one `Root-task status` per
assigned outcome, including child-Job outcomes, on a separate line outside the lane-readiness table. The legacy field name does not imply root-only ownership. Use
`Root-task status: FOLLOWUP` or `Root-task status: FOLLOWUP_PAUSED`. Omit this
field before genuine Fast completion and after coordinator acceptance or
explicit user closure. Keep these root-task statuses separate from the existing
Implementation, Test, and Prod status columns and their vocabulary.
For reports covering multiple outcomes, put each status on its own line and
identify it by Job/parent chain and outcome, e.g.
`Root-task status [1.2 / checkout flow]: FOLLOWUP_PAUSED`.

| `Root-task status` | Use |
| --- | --- |
| `FOLLOWUP` | Genuine Fast completion is confirmed and required post-Fast work remains unaccepted, unless the next required post-Fast action cannot advance because it awaits a named in-scope dependency. |
| `FOLLOWUP_PAUSED` | The next required post-Fast action cannot advance because it awaits a named in-scope dependency. Report the dependency, impact, owner, and resume condition; return to `FOLLOWUP` when it clears. |

These statuses cover only the mandatory post-Fast sequence, not separate-outcome
observations. A user input or decision remains `BLOCKED` in the affected lane,
with the exact input, impact, owner (`user`), unblock action, and target; do not
use it as the reason for `FOLLOWUP_PAUSED`. While the sequence remains
unaccepted, keep the root-task status `FOLLOWUP` unless the next required
post-Fast action cannot advance because it awaits a named in-scope dependency.
End either root-task status on coordinator acceptance or explicit user closure.

Use a short `Requirements` list near the table only when it makes the report
clearer. It is a readable projection of the ledger, not an admission artifact.
Never delay a report to construct aliases, hashes, receipts, or verbatim
registries.

| Rule | Behavior |
| --- | --- |
| Status vocabulary | In each status column, use only `NEW`, `IN PROGRESS`, `PAUSED`, `BLOCKED`, `CLOSED`. |
| Stage vocabulary | ECI uses `normal`; main/fast progress is path context within the task. Missing/stale stage metadata is a report-quality issue to reconcile, never a work gate. |
| Implementation Status | Covers exploration, RCA, design, code changes, code review, build checks, unit/component/integration auto-tests, and source-level readiness. `CLOSED` means source-level work is accepted with relevant automated checks. |
| Test Status | Covers E2E validation in the non-production test environment, including real devices, test services, UI manipulation, and mission/test-plan helpers. `CLOSED` means test-environment E2E passed or was explicitly waived. |
| Prod Status | Covers E2E validation in production, including deploy provenance, real production services/devices, UI manipulation where relevant, and user-visible behavior. `CLOSED` means production E2E passed or was explicitly waived. |
| Lane closure | A lane is finished only when the required highest environment column is `CLOSED`. For production-gated work, Implementation/Test `CLOSED` with Prod open is still not finished. |
| Evidence states | Put worker completions, reviews, source fixes, deploys, and partial proofs in `Next proof/action`, not by collapsing status columns. |
| RCA/fix closure | For bug/debug lanes, missing, failing, or not-runnable domain-required acceptance proof keeps RCA/fix open. Source approval may close Implementation only; wording must say source-only/progress, not fixed/closed. |
| `CLOSED` | Use only in the specific column whose required evidence is proven or explicitly removed from scope. |
| `PAUSED` | Use only when this lane's next required action is progress from another in-scope lane. No user input or decision is pending for this lane's next action. Name the dependency lane, impact, owner, and resume condition in `Blocker` or `Next proof/action`. If the dependency lane is `BLOCKED`, keep this lane `PAUSED` and mark the dependency lane `BLOCKED`. |
| `BLOCKED` | Use only when this lane cannot make any more progress until the user provides a named input or decision. Name the exact user input/decision, impact, owner (`user`), exact unblock action, and target artifact/path. Do not use `BLOCKED` for another lane's progress. |
| Coverage | Do not omit lanes because they are idle, waiting, or paused. Review, deploy, and proof remain within their lane unless they independently advance with separate ownership or synchronization. In a material changed-state update, each unrepresented active root-task outcome omitted by lane reports has one Root completion forecast. |
| Lane requirement context | Use known readable context when available. Otherwise state `lineage unavailable—reconcile`; missing context never fails or delays the report. |

## Pressure Scenario

Under time pressure, classify by the lane's next required action. For example: "PAUSED on API-contract lane [requirement context: checkout contract]; impact: checkout validation cannot make progress; owner: API-contract lane; resume: lane 2 publishes docs/api-contract.md." Use `BLOCKED` only for a real user input: "BLOCKED on user decision: choose required API fields; impact: checkout validation cannot make further progress; owner: user; unblock: record the choice in docs/api-contract.md." If context is not yet known, say `lineage unavailable—reconcile` and report the next safe action.

## Common Failures

| Failure | Fix |
| --- | --- |
| Action log: "Read files, ran tests, asked agent." | Report resulting state: "Validation path is mapped; unit tests are the remaining gap." |
| Vague progress: "Made progress." | Name the changed state. |
| Decision without reason. | Add the tradeoff or constraint that drove it. |
| Blocker without actionable request. | Add stalled impact, owner, exact unblock action, and target artifact/path when applicable. |
| Verification implied. | Cite evidence or say "not verified yet". |
| Next focus is "continue". | Name the next concrete work area. |

## Checklist

| Check | Pass condition |
| --- | --- |
| State | One-line current state is clear. |
| Progress | Describes outcomes, not effort. |
| Decisions | Includes reason for chosen path. |
| Blockers/Risks | Includes impact, owner, exact unblock action, and target artifact/path when applicable. |
| Verification | Evidence is cited or absence is explicit. |
| Next Focus | Names the next concrete work area. |
| Concision | No raw activity log or filler. |
| Hierarchy | Parent/child work is shown as a tree or with `Task ID` + `Parent ID`; nested work is not flattened. |
| Task IDs | Existing task IDs use hierarchical form such as `1.3.2`. |
| Multi-lane coverage | Every in-scope lane is listed, including idle/waiting/paused lanes; review/deploy/proof remain current lane work unless independently advancing. |
| Root coverage | In a material changed-state update, each unrepresented active root-task outcome omitted by lane reports has one Root completion forecast. |
| Requirement context | Known context is readable and accurate. Missing context is labeled `lineage unavailable—reconcile`; it never fails or delays the report. |
| Lane statuses | Each Implementation/Test/Prod status is exactly `NEW`, `IN PROGRESS`, `PAUSED`, `BLOCKED`, or `CLOSED`. |
| PAUSED semantics | A lane waiting for progress from another in-scope lane, with no user input/decision pending, is labeled `PAUSED`, not `BLOCKED`. |
| BLOCKED semantics | A lane unable to make more progress until the user supplies a named input/decision is labeled `BLOCKED` with `owner=user` and an exact unblock action; dependency-only waiting is not `BLOCKED`. |
| Dependency propagation | If a dependency lane is `BLOCKED` on user input, the dependent lane remains `PAUSED` until its own next required action needs user input. |
| Status separation | Source-level completion, test E2E, and production E2E are never merged into one status. |
| Lane forecasts | A material changed-state update names each active lane's milestone and one named-outcome forecast with critical-path treatment. Changed lane/root forecasts restate their current canonical line with prior/current UTCs, why moved, and supporting evidence. Closed lanes record a completion timestamp instead of a deadline or recalibration. Parallel child deadlines remain parallel under one critical-path deadline. |
