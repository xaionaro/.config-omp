---
name: context-ledger
description: Use when writing or verifying project-understanding ledgers, context ledgers, ECI/ATE session ledgers, handoff context, or stop-hook ledger updates — keeps the ledger a current-state snapshot and the high-level log an append-only history, side by side
---

# Context Ledger

Three required records, one conditional current forecast record, and one audit-only history:

| File | Role | Edit mode |
|------|------|-----------|
| `project-understanding.yaml` (the ledger) | Current project/task records and forecast locators | Selective in-place `yq4` edits; stale records deleted |
| `high_level_log.jsonl` (the log) | Append-only history of every material change | Append only; never edit, never delete past entries |
| `forecasts.yaml` | Sole current source for lane/root forecast records; create when a forecast first applies | Selective in-place `yq4` edits; never copy its values into the ledger |
| `latest-status-report.yaml` (the report) | Latest structured status projection per `writing-status-reports` | Update stale or changed YAML fields; preserve unaffected content. Write the whole file only on first creation. |
| `forecast-target-history.tsv` | Audit-only root-task forecast-target history | Append only; corrections add a new target row |

The ledger answers what is true about the project and task now. `forecasts.yaml` answers what the current forecasts are. The log answers what changed, in order, and why. The report projects the latest state for the user; it is not another source of truth. The TSV audits root-target transitions and does not define current forecast state.

## Core Rule

A fresh agent reading the ledger and its `forecasts.yaml` locators, without transcript or memory, must reach the same current understanding you have. Record every project/task fact that could affect planning, implementation, risk handling, assignment, command choice, verification, or the final answer.

Keep current project/task facts in `project-understanding.yaml` and current forecast values in `forecasts.yaml`. The ledger may link to forecast records but never copy their targets, baselines, recalibrations, base-case/downside estimates, scenario ranges, forecast statuses, or endpoints. Binding user deadlines and actual completion timestamps are project facts and may remain in the ledger where useful; estimated completion dates belong only in `forecasts.yaml`.

Err on exhaustive useful detail for current state. Do not omit a detail because it seems obvious from transcript, local state, prior agent memory, or project familiarity. Equally, do not retain a detail because it was true earlier. Exhaustive on current state; zero on superseded state.

For ECI, record stage `normal` as a context fact. Track main and fast progress under the same task: owners, provisional evidence, current ECI decision, and needed adaptation. [ECI fast path](../explore-critique-implement/references/fast-path.md) defines their relationship; these are paths, not separate stages or automatic lanes. Reconcile missing or stale metadata alongside safe work; records never grant permission.

For material ECI work, keep `exact user source → faithful requested outcome →
bounded scope` as readable context. A repair stays in its lane when it is
necessary to meet or prove that outcome. An unadmitted separate-outcome concern is only a
post-ECI observation or follow-up, never current lane, assignment, code change,
review, deadline, forecast, or proof program. Reconcile missing or stale
lineage alongside known work; do not block it.

Record authorized child Jobs with stable parent edges, their own outcomes and full ECI evidence, and upward owner routes under the [recursive Job contract](../explore-critique-implement/references/task-orchestrator.md#recursive-child-admission-and-closure). A parent's finished home outcomes do not close unfinished descendants. Admission within inherited authorization does not invent a user requirement.

Ground each progress item in `high_level_log.jsonl` and `latest-status-report.yaml` in a
named or linked user-stated requirement. Explain how the change advances or
verifies the requested outcome; for a blocker, state its effect on that outcome.
For supporting work, state what it enables and whether the requested outcome
remains open.

Apply [Project requirements](../../eci-binding-contract.md#project-requirements) before classifying material input. Preserve exact attributed user passages and distinguish project-specific changes, instruction corrections, hypotheses and derived work using existing record types and links. Reuse the affected outcome for corrective work. Keep applicable instruction sources and unresolved inquiries with their owner and next action; do not attribute agent-derived checks to the user.

Example: ‘The test harness now exercises the requested production behavior,
making the fix verifiable; the production fix remains open.’

## Storage

For ECI/ATE, these records live at:

```text
$HOME/.agent/context/sessions/$SESSION_ID/project-understanding.yaml   # the ledger
$HOME/.agent/context/sessions/$SESSION_ID/high_level_log.jsonl          # the log
$HOME/.agent/context/sessions/$SESSION_ID/forecasts.yaml                # current forecasts, when applicable
$HOME/.agent/context/sessions/$SESSION_ID/latest-status-report.yaml    # the report
$HOME/.agent/context/sessions/$SESSION_ID/forecast-target-history.tsv # target history
```

Do not store any of these files in the project/repo. Keep the canonical filenames above; session-snapshot pruning ignores directories younger than 30 days.

Create `project-understanding.yaml`, `high_level_log.jsonl`, and `latest-status-report.yaml` once. Create `forecasts.yaml` once when the first forecast applies, then update current records in place. Create `forecast-target-history.tsv` with its header at the first `none → A` transition. Follow each file's edit mode above; never delete or recreate these records.

If an active session has only `forecasts.md`, create `forecasts.yaml` from its
current records at the next forecast maintenance, preserving all forecast and
scenario meaning, and update ledger locators to the new record IDs. Preserve
`forecasts.md` unchanged as a historical snapshot; only YAML is current
thereafter. Never overwrite existing YAML from Markdown or bulk-convert sessions.

The only canonical understanding snapshot is `project-understanding.yaml`. Start
fresh from current authoritative facts; do not import or convert other caches.
No alternate filename, compatibility alias, dual read/write, or migration.

## High-Level Log

Append-only history. Every material change to the ledger or `forecasts.yaml` gets a corresponding entry appended to the log in the same turn.

| Rule | Detail |
|------|--------|
| Append only | Never edit, reorder, or delete past entries. Wrong entries are corrected by a new appended entry referencing the prior one. |
| Material essence | Lead with what is true now, what to do next, why it matters, and evidence. Add changed-state context or provenance when it explains a material change. |
| Reflect all details | Capture the change, prior state, new state, reason, source/evidence, and agent/turn. |
| Chronological | Newest entries at the bottom. Each object has a UTC `timestamp`. |
| Same-turn pairing | Every material ledger or forecast update has at least one log entry from that turn. A current-state diff with no log append is defective. |
| No synthesis | The log records what changed; it does not duplicate the ledger's current-state synthesis. Cross-reference record IDs instead. |

Treat bytes already present in `high_level_log.jsonl` as immutable; permit only
an EOF append to that same canonical file. Never follow a symlink alias.

Use the command below with explicit `ECI_SESSION_ID` (or `ECI_THREAD_ID`
when unset), from the intended CWD. A missing canonical JSONL starts empty through normal
bootstrap; append current events without importing or converting other files.
Existing JSONL remains append-only. The CLI preserves concrete target safety,
detaches shared log inodes, and refreshes `high_level_log.anchor` advisory metadata.

All new canonical entries are one compact JSON object per line, including
direct EOF appends. The only canonical log is `high_level_log.jsonl`; no text
fallback, alternate filename, alias, tolerant parser, sidecar, or conversion.
This is a logging contract, not a syntax/content gate; direct append admission
and advisory anchor reconciliation remain unchanged.

Use exactly `"$HOME/.omp/agent/eci/bin/eci-active" ledger-append --json '<event-object>'`. It validates exactly one object
before log mutation and sets `schema: "eci-high-level-log/v1"`, the actual UTC
`timestamp`, and selected log `session_id`, overriding supplied envelope values.
Preserve event-specific payload in `details`. Direct append callers supply this
same envelope themselves.

| Field | Contract |
|-------|----------|
| `event` | One name from the vocabulary below, chosen by material change, not stage or command. |
| `summary` | Nonempty string describing the changed result and whether supporting work leaves the requested outcome open. |
| `actor` | Performer `{id, role, turn?}`. Use the exact stable runtime worker id from assignments/reports; role changes keep that id. `id`, `role`, and available `turn` are nonempty strings. |
| `recorded_by` | Optional recorder identity in the same form when a coordinator records another worker. Keep the worker in `actor`; the root log session is not the performer. |
| `requirements`, `evidence` | For `worker_progress`, nonempty arrays of nonempty strings naming/linking user requirements and evidence references. |
| `change`, `reason` | For `worker_progress`, `{before, after}` and `reason` are nonempty strings. Use `"unknown"` for an actually unknown prior state; do not fabricate it. Other material events retain prior/new state, reason, and evidence as applicable. |
| `next_action` | Nonempty string required by this guidance when work remains; omit when no action remains. The CLI checks its type when supplied. |
| `details` | Event-specific payload, such as verdicts, forecast targets, commit references, or `corrects` identifying a mistaken prior entry. |

For a material change that should also update the report, put an explicit
`details.status_update` payload in the appended event. Use the operation schema,
closure evidence rules, and examples in the [status-report README](../../eci/tools/status-report/README.md#log-driven-report-updates).
Describe only supplied facts; ordinary event prose does not authorize inferred report
changes. The producer assigns impact points and their basis; the renderer calculates
priority from those points and remaining work.

Append events in order. Correct a mistaken projected field with a new `correction`
event and its update's `corrects` set to the one-based line of the latest update
for that field; when that update was not first on its line, also supply its
zero-based `corrects_ordinal`. Never edit the earlier entry. Refresh tracks pending updates and
preserves detected owner edits and unrelated content. Reconcile diagnostics and
pending changes manually when needed. If automatic refresh fails after append, run
the provided retry command without appending again.

Use the documented cancellation update for a cancelled task and a correction
with `retract: true` to retract a sync-managed narrative subject. When adding a
task with a new priority, supply its priority estimate in the same event.

| Event | Material change |
|-------|-----------------|
| `worker_progress` | Worker discovery or milestone advancing a user requirement. |
| `assignment_change` | Ownership, dispatch, or reassignment. |
| `review_result` | Findings or review verdict. |
| `decision` | Selected alternative or changed design choice. |
| `verification_result` | Test, proof, or E2E evidence/verdict. |
| `requirement_change` | Binding requirement, source, or scope update. |
| `forecast_change` | Target or forecast recalibration. |
| `blocker_change` | Block, wait, resume, or recovery. |
| `checkpoint` | Commit or provisional checkpoint. |
| `lifecycle_change` | Launch, cancellation, closure, or teardown. |
| `correction` | Mistaken prior entry, referenced by `details.corrects`. |

Structured worker-progress example, recorded by its coordinator:

```sh
"$HOME/.omp/agent/eci/bin/eci-active" ledger-append --json '{"event":"worker_progress","actor":{"id":"/root/implementer","role":"Implementer","turn":"7"},"recorded_by":{"id":"/root","role":"Supervisor"},"summary":"Focused validation passes; requested outcome remains open.","requirements":["requirement:log:R7:worker-progress; project-understanding.yaml#/records/requirement:log:R7:worker-progress"],"change":{"before":"Structured append unverified","after":"Focused checks pass"},"reason":"Implemented structured serialization","evidence":["focused-checks.out"],"next_action":"Run the production check"}'
```

Extract one worker's v1 progress directly:

```sh
jq -r 'select(.schema == "eci-high-level-log/v1" and .event == "worker_progress" and .actor.id == "/root/implementer") | .summary' high_level_log.jsonl
```

## Latest Status Report

`latest-status-report.yaml` holds the single most recent status report as structured YAML. It is the handoff snapshot the next agent or user reads first. Render user-facing reports per `writing-status-reports`.

| Rule | Detail |
|------|--------|
| Coverage | Include UTC `timestamp`, `state`, `progress`, `decisions`, `blockers_risks`, `verification`, and `next_focus`; include hierarchical tasks, lanes, and forecast projections when applicable. Use mappings and lists for structured detail; do not wrap the report in an opaque Markdown body. |
| Refresh triggers | After every ledger update, after every material change, before user-waiting stops, before shutdown, after each ECI compaction, and whenever the user asks for status; apply [Update Points](#update-points) timing. |
| Forecast projection | Derive equivalent native fields from `forecasts.yaml`, including outcome, target, assessment, baseline or recalibration and evidence, dependencies, and applicable scenarios and completion. Render exact lines and tables only in user-facing reports. The report is not a second source of truth. |
| No copying the ledger | Report changed state plus next focus; do not duplicate ledger structure. Cross-reference instead. |

The executable contract and example live in [status-report README](../../eci/tools/status-report/README.md). Before a status response, reconcile the report with the current ledger and `forecasts.yaml`, including applicable work, evidence, and forecast projections. Mark missing, stale, inconsistent, or incomplete source evidence and affected coverage as incomplete; do not infer completeness from schema validation. Use `status-report validate FILE` to check schema and references, then `status-report render FILE` and publish its full output, including when validation reports incomplete reconciliation. Correct known missing sections in YAML and rerender before publication. Validation cannot establish factual truth or reviewer independence. If structural failure prevents rendering, answer the user directly with supported facts, label the response incomplete, and repair the report alongside safe work. Track task hierarchy and top-priority IDs; do not store a progress bar or percentage. Include queue review records when applicable. Forecast values remain projections from `forecasts.yaml`.

Apply the scalar and selective-edit rules in [YAML authoring and native edits](#authoring-and-native-edits) to this report, without the ledger's envelope, record schema, or record-specific ID/type checks. Select changed fields against the latest report and preserve unrelated content. Use the supported schema to retain the status skill's lane, dependency, root-task status, forecast and scenario detail. Record changed forecast prior and current targets, reason and evidence; record initial baseline and evidence; record actual completion timestamps for every lane whose required-environment status is closed, without an active deadline. Represent each applicable root-task status and every material scenario with its trigger, affected work, duration basis, evidence, assumptions, endpoint and next fact-finding action. Task `id` and `parent_id` preserve hierarchy: a dotted ID's parent is its exact prefix before the final dot, and a root has no parent. Lane `task_id` references that task. Task `owner` executes the task; lane `owner` coordinates the lane. The three lane status values retain distinct readiness meanings. Forecasts remain projections of `forecasts.yaml`.

```yaml
timestamp: '2026-10-26T12:00:00Z'
state: 'Validation remains open.'
progress:
  - 'Source changes pass focused checks.'
decisions:
  - choice: 'Use the existing service contract.'
    reason: 'It preserves compatibility with the test service.'
blockers_risks: []
verification:
  - 'Focused checks passed.'
next_focus: 'Run the integration check.'
scope_complete: false
tasks:
  - id: '1'
    title: 'Deliver the service integration.'
    priority: 'high'
    impact: 'Service integration remains unverified.'
    effort: {min_minutes: 20, max_minutes: 30, kind: elapsed, basis: educated guess}
    status: 'IN PROGRESS'
    owner: 'integration executor'
  - id: '1.2'
    parent_id: '1'
    title: 'Complete the service integration.'
    priority: 'high'
    impact: 'Service integration remains unverified.'
    effort: {min_minutes: 20, max_minutes: 30, kind: elapsed, basis: educated guess}
    status: 'IN PROGRESS'
    owner: 'integration executor'
top_priorities: ['1.2']
priority_estimates:
  - {priority: high, as_of: '2026-10-26T12:00:00Z', min_minutes: 20, max_minutes: 30, basis: educated guess, assumptions: test service available, dependencies: test service, critical_path: integration check}
completion_estimate: {as_of: '2026-10-26T12:00:00Z', min_minutes: 20, max_minutes: 30, basis: educated guess, assumptions: test service available, dependencies: test service, critical_path: integration check}
lanes:
  - task_id: '1.2'
    required_environment: test
    outcome: 'Complete the service integration.'
    owner: 'integration coordinator'
    stage: 'normal'
    implementation_status: 'CLOSED'
    test_status: 'IN PROGRESS'
    prod_status: 'NEW'
    next_milestone: 'Integration check passes.'
    forecast:
      outcome: 'The service integration passes its required checks.'
      target: '2026-10-26T13:00:00Z'
      assessment: 'provisional: test service availability is unverified; target conditional'
      baseline: '2026-10-26T13:00:00Z'
      evidence: 'Focused checks passed; integration check remains.'
      dependencies: 'Test service availability; integration owner; resume when available.'
      scenarios:
        - case: 'base'
          trigger: 'No listed downside scenario.'
          affected_work: 'Remaining integration check.'
          duration_basis: 'No generic buffer.'
          evidence_assumptions: 'Focused checks passed; test service assumed available.'
          endpoint: '2026-10-26T13:00:00Z'
          evidence_gap_next_action: 'Confirm test service availability.'
```

If a session has only `latest-status-report.md`, create the YAML projection from current authoritative records at its next required refresh. Preserve the Markdown unchanged as a historical snapshot; only YAML is current thereafter. Do not bulk-convert other sessions or overwrite YAML from legacy Markdown.

A ledger or material forecast update without its required same-turn log entry and report refresh is defective.

## Current State, Not History

| Case | Ledger Action |
|------|---------------|
| Mutable fact changes | Replace value in place; never append beside old |
| Hypothesis disproved, plan abandoned, decision reversed | Delete the obsolete entry; keep only the surviving conclusion |
| Old state explains a binding constraint or hazard | Keep only the needed history and why it still matters |
| Step finishes | Record verdict + resulting state + evidence link; drop the in-progress entry |
| Detailed report exists elsewhere | Link it; do not copy report body, substeps, transcripts, or bullet lists |
| Correction changes current understanding | Record the surviving fact, affected state, recurrence guard when useful, and source/evidence. For reaffirmed existing facts, use the `Existing ledger fact reaffirmed before action changes state` routing row. |
| False current scope | Correct the ledger/status and append the correction to the log. Cancel or reassign only unrooted current work; do not destructively revert already-made work without user direction. |
| Task/blocker resolved | Move to completed milestones with link, or delete |

Skip blow-by-blow history unless it prevents recurrence.

### Log, Ledger, and Forecasts

A current fact lives in one canonical record. Keep only a locator to a current forecast in the ledger; append material forecast changes to the log.

| Content | Ledger | Forecasts | Log |
|---|---|---|---|
| "14:22 - tried A, failed" | - | - | append |
| "Considered X, chose Y because..." | "Using Y. Why: <reason>." | - | append the consideration + decision event |
| "Thought bug was in M, found in N" | "Bug: N. Fix: <link>." | - | append the M->N correction event |
| "Step 1 done. Step 2 done. Step 3 WIP." | "Current: step 3 - <state>. Done: 1, 2 (links)." | - | append each step transition |
| Narrative of what each agent did | Current owner + last verdict + next action | - | append per-agent action when it produced a material change |
| Current lane/root forecast values | Rendered link only | Canonical current values | - |
| Material forecast update | Do not copy current values | Update current record in place | Append the material change |
| Existing ledger fact reaffirmed before action changes state | keep existing current fact | - | keep log as-is for pure reaffirmation; append material planning, risk, ownership, authority, or verification change |

Per-ledger-line test: true and load-bearing right now? No -> drop from ledger; if it captures something material that happened, append to the log instead.

### Lane forecasts

A lane is an independently advancing workstream, not an ECI step. Serial implement→review→repair→review→implement stays one lane with one critical path. Create distinct lanes only for independently advancing work with separate ownership or synchronization.

Record lane/root state and next action in `work` records, including owner and outcome links. Preserve dependencies, binding user deadlines and useful milestones as typed current records. Store each relevant forecast locator in `forecast_ref`; do not copy forecast values into the ledger or require rendered Markdown links inside YAML.

Store every current lane/root forecast in `forecasts.yaml`: named outcome, active/inactive status, assessment, target, original baseline, recalibration reason/evidence, ECI base case, and each material downside scenario with its evidence, range, and endpoint. Keep current records there in place. `latest-status-report.yaml` projects these values using `writing-status-reports`.

#### Forecast outcome wording

Write every lane and root forecast `Outcome` as a completed result in plain language: name the affected behavior or artifact and what will work or be delivered. The description must be understandable without a `Lane` heading, `Next milestone`, or transcript. Bare labels such as `UNPRICES` or `WITHDRAWN`, opaque identifiers, and vague phrases such as `forecast cleanup` are insufficient. Retain domain terms when the surrounding words explain the delivered result.

Example: `Outcome: Import validation that identifies unpriced records and explains how to correct them.`

**Forecast record fields in `forecasts.yaml`**

Use the scalar and selective-edit rules in [YAML authoring and native edits](#authoring-and-native-edits).
The top-level mapping has `schema: "forecasts/v1"` and `records`, a mapping
from stable IDs to forecast records. IDs use lowercase letters, digits, hyphens,
underscores, or colons; retain them when a forecast changes. A work record's
`forecast_ref` is `forecasts.yaml#/records/<id>` and identifies that exact key.
Use distinct IDs for distinct lane/root forecasts. The synthetic
[forecast example](examples/forecasts.yaml) shows the native shape.

Each record has `kind` (`lane` or `root`), `outcome`, `status` (`active` or `inactive`, whether the forecast still applies), `assessment` (`complete` or `provisional`, whether material timing is supported),
`target` while active, `original_baseline`, `baseline_evidence`, and `recalibration`. Recalibration
has `status` (`unchanged` or `changed`) and `evidence`; when changed it also
has `prior`, `current`, and `reason`. An initial forecast records its baseline
and evidence with `unchanged`; preserve baseline evidence on later changes.
A provisional record also has `target_basis`.
For ECI, record `base_case` and `scenarios` as native mappings and lists: keep
the package duration basis, endpoint, evidence, assumptions and evidence gaps
needed by the scenario rules below. Store durations and ranges with units and
UTC endpoints as quoted strings; retain full detail rather than collapsing
scenario data into a rendered report. Omit an active target when inactive.
`base_case` contains `endpoint` and a `packages` list; each package contains
`work`, `duration`, and `basis`. Each scenario contains `name`, `trigger`,
`affected_work`, `evidence`, `assumptions`, `added_duration`, `event_time`,
`endpoint`, and `basis`; include `evidence_gap` and `next_action` when timing
is unsupported. Add fields needed for combinations, external waits, or other
material facts; these fields do not limit the scenario rules below.

Set `assessment` to `complete` only when every material risk has evidence-supported duration and endpoint ranges, or current evidence resolves the risk. A materially guessed base-case package duration makes it `provisional`; retain each guessed duration and endpoint, exact evidence gap and next fact-finding action. Keep `status` independent of `assessment`. For a provisional forecast, `target_basis` describes the conditional guessed timing and assumptions; any finite target, base case, or displayed spread remains conditional while evidence gaps are open.

Every non-`CLOSED` lane has a current forecast record with its named outcome and target. For each unrepresented active root-task outcome omitted by lane reports, keep one root completion forecast with the full root outcome and target. Root completion is not a child sum or stage. Keep task dependencies and critical-path facts in the ledger; use them to calculate the forecast without copying forecast values back into the ledger.

When a lane or root forecast changes, update its current record in `forecasts.yaml` with the outcome and target, then record the prior target, current target, reason, and evidence. Preserve the original baseline. For an initial forecast, record `unchanged`, its baseline, evidence, and `assessment: complete` or `provisional` from the fact-based risk review. If provisional, retain each exact evidence gap and next action and label any finite target conditional. When a lane closes, mark its forecast inactive; actual completion time remains a project fact in the ledger.

#### ECI scenario forecasts

For ECI forecasts, including nested ECI, store a base case and every material
downside scenario alongside the canonical target, including risks whose timing
is not yet supported. Apply [Work estimates](../../eci-binding-contract.md#work-estimates):
decompose remaining work, use labeled educated guesses where evidence is
incomplete, and always give an overall completion estimate. Keep evidence gaps
visible; a probe supplements the estimate.

- Base case: completion UTC for the remaining authorized in-scope work on the
  current critical path, assuming no listed downside scenario. Estimate each
  package using evidence or a labeled educated guess with reasoning; add serial
  durations and use the latest required parallel endpoint. Add no generic buffer.
- Scenario: include every material downside risk that could affect completion
  of the requested ECI outcome. Record its name, trigger/evidence, affected work
  or dependency, evidence assessed, assumptions and limits, added
  duration/range and endpoint/range with evidence or labeled educated-guess basis, and the exact evidence gap and next
  fact-finding action. Assess all relevant current facts, applicable written
  commitments or service-level terms only when they cover this failure and
  measure its resolution, comparable history, current progress or throughput,
  documented lead times, direct timing evidence, and dependency/overlap
  evidence. Distinguish commitments from observed durations; do not present a
  commitment as a guaranteed resolution, hard cap, or worst case. Cite or
  summarize what was assessed; update as evidence changes. Derive a UTC
  endpoint from a stated event/base time and estimated duration. When timing
  lacks evidence, use a labeled educated guess and set the forecast assessment
  to `provisional`. For external waits, state assumed availability or wait duration,
  owner and resume condition; the conditional estimate is no commitment or cap
  on the external actor. Permanent access loss may prevent completion; state
  that condition alongside an ETA conditional on access recovery. A guessed
  material risk cannot coexist with a `complete` forecast. Keep any finite
  target, base case, or spread conditional while it remains open; continue safe
  work while gathering evidence.
- Record co-occurring delays as combinations when evidence supports their
  sequence, or label the assumed dependency/overlap used for a provisional
  estimate and request evidence from their owners. Do not present assumed
  relationships as facts. List mutually exclusive scenarios
  separately and count overlapping delay once. For root forecasts, use the latest required
  parallel lane/dependency endpoint, not a sum of parallel work.
- The single UTC target is the latest endpoint among the base case and estimated
  scenarios, including labeled educated guesses. Keep each scenario's endpoint/range in the
  `forecasts.yaml` record. If a material risk lacks supported timing, keep it in
  the record with its gap and next action, set `assessment: provisional`, and
  label the finite target/base case/spread conditional. A finite target is the
  latest estimated endpoint, not an absolute worst case. Never invent
  probabilities.
- `original_baseline` remains the original forecast target; it is not
  the scenario base-case endpoint.

Forecast scenarios describe planning uncertainty only. They do not change
scope, priority, blocker status, or forecast authority.

For every material forecast update, edit `forecasts.yaml` in place, append to `high_level_log.jsonl`, append a `forecast-target-history.tsv` row only when a root target changes, and refresh `latest-status-report.yaml`. A forecast-only update does not require rewriting `project-understanding.yaml`. Store forecast values only in `forecasts.yaml`, keeping locators in the ledger. On closure mark the canonical forecast inactive and retain history rows.

**Rendered status-report examples — not ledger storage**

In a material changed-state user-facing report, emit each canonical forecast line once using `forecasts.yaml`. The persisted YAML report carries equivalent fields as described in [Latest Status Report](#latest-status-report). Do not copy these values into `project-understanding.yaml`.

- Next milestone: `Next milestone: <named outcome>`
- Forecast deadline: `Forecast deadline: <named lane/task outcome> will be finished by <UTC ISO8601>.`
- Root completion forecast: `Root completion forecast: <named active root-task outcome> will be finished by <UTC ISO8601>.`
- Changed forecast: `Forecast recalibration: <prior UTC ISO8601> → <current UTC ISO8601>; why moved: <why>; supporting evidence: <evidence>.`
- Initial forecast evidence: `Forecast recalibration: unchanged — baseline <UTC ISO8601>; supporting evidence: <evidence>.`
- Dependencies / critical path: `Dependencies / critical path: <none, named dependency + owner/resume, or critical path>`
- `CLOSED` lane: `Completed: <UTC ISO8601>; no active forecast deadline.`

Every non-`CLOSED` lane names its next milestone and canonical forecast line in the rendered report. For each unrepresented active root-task outcome omitted by lane reports, include the standalone root completion line once. The forecast line itself names the finished outcome using the [forecast outcome wording](#forecast-outcome-wording); a separate `Lane` or `Next milestone` does not substitute, and it never names a critic, reviewer, actor, or stage. Root completion is full root completion, not a child sum or stage.

For a changed lane or root forecast, restate its current canonical line in the same rendered report, then state `Forecast recalibration: <prior UTC ISO8601> → <current UTC ISO8601>; why moved: <why>; supporting evidence: <evidence>.`

A closed forecasted lane/root requires actual quoted `completed_at` UTC in its work record and an inactive canonical forecast. Append material closure to the log and refresh the report with the completion timestamp and absence of an active deadline. Render the completion and initial forecast evidence using the exact lines in `writing-status-reports`. Small non-lane work timestamps are optional. Removing `forecast_ref` alone does not close a forecast. Do not invent or revive a forecast deadline or recalibration.

State dependencies and parallel work. For parallel children, report the single critical-path deadline; child deadlines remain parallel; never add or sum parallel child deadlines into a parent, root, or mission deadline.

Forecasts are advisory. They never gate work, grant or deny permissions, require artifacts or receipts, create blockers, require parsers, or require per-command ceremony. Missing or stale forecasts are planning-quality defects. Reconcile them alongside safe work without delaying the update.

Coordinator-to-user updates retain the exact standalone lines defined by the ECI coordinator module. Do not use report lines or the audit history as current forecast storage.

### Forecast target history

`forecast-target-history.tsv` is the canonical audit-only history for root-task forecast targets. It has exactly four columns:

```text
added_utc	root_task_id	new_target_utc	reason
```

Every row records the UTC addition time, root task ID, new UTC target, and a concise human-readable reason for setting or changing the target. Append only: never edit, delete, reorder, or reuse a row.

| Target transition | `high_level_log.jsonl` | `forecast-target-history.tsv` |
| --- | --- | --- |
| none → A | Append a material `high_level_log.jsonl` entry naming A, why, and evidence. | Append A row with reason. |
| A → B | Append a material `high_level_log.jsonl` entry naming prior A, new B, why, and evidence. | Append B row with reason. |
| A → A | No high-level-log entry or history row for mere reaffirmation. | No row. |
| close | Record material completion normally. | No date row. |
| correction | Append a correction naming the prior entry and corrected target. | Append the corrected-target row with reason; never rewrite earlier rows. |

This history is audit-only. It never gates work, grants or denies permission, creates a blocker, or delays ordinary work. It is not a required session record or work prerequisite. Reconcile a missing, stale, or malformed history alongside ordinary work.

## YAML Snapshot Contract

Use one UTF-8 YAML 1.2 document. Required envelope: `schema:
project-understanding/v1`, `sections` map and global `records` map. Each section
has a `title` string; each record has `section` (existing section ID), `type`,
`data`, and `evidence` (string array). Optional `links` maps named relationships
to arrays of existing record IDs. No mandatory timestamp, hash, source registry
or duplicate ID field.

Stable semantic IDs identify subjects, not states or locations:
`requirement:<job>:<source-id>:<facet>`, `work:<job>:<subject>`. Different Jobs'
R1 labels must not collide. Facets distinguish independently binding conditions
from one source. Search/reuse the existing subject before adding; changes to
value, owner, role or section retain its ID. Section title changes preserve IDs;
section-key changes update memberships and record deletion repairs incoming links.

### Predictable shared types

Common `data` values are objects except `context`/`constraint`, which allow
project-specific typed JSON-compatible values and arbitrary domain arrays.
Listed fields are required unless conditional/optional. Common string fields
are nonempty; common string-array fields contain strings. Required acceptance
and relationship arrays are nonempty; named links target the stated record types.

| Type | `data` fields | Required typed links |
|---|---|---|
| requirement | `source_text: string`, `acceptance: string[]` | None |
| outcome | `description: string` | `requirements` → requirement, `scope` → scope |
| scope | `include: string[]`, `exclude: string[]` | None |
| work | `owner: string`, `state: active\|blocked\|completed\|cancelled`; `next_action: string` while active/blocked; `forecast_ref: string` when forecast applies; actual `completed_at: UTC string` for completed forecasted lane/root work, optional for small non-lane work | `outcomes` → outcome |
| unknown | `question: string`, `answer: null`, `effect: string`, `resolve_by: string` | None |
| decision | `choice: string`, `reason: string`, `consequence: string` | None |
| guard | `rule: string`, `reason: string` | None |
| verification | `verdict: pass\|limited-pass\|fail\|unverified`, `scope: string`, `limitations: string[]`; `revision: string` when relevant | None |
| context, constraint | Project-specific typed values | None |

Keep independently changing or differently evidenced subjects in separate
records. Preserve useful architecture, configuration, API, ownership and domain
details, not just progress. Use named fields and explicit units such as
`{value: 1000000, unit: "Hz"}`; booleans/numbers/arrays remain typed. A concise
single-assertion string is allowed; pasted prose reports/chronology are not.
Absent knowledge is an `unknown`, never fabricated zero/false/empty. Provisional
inferences state their uncertainty, effect and resolution action in unknown records.

Retain exact attributed source text and acceptance in requirements; outcomes
link their requirements and bounded scope, work links outcomes. Evidence strings
resolve to accessible passages, retained exact user quotes or observed output;
relative file references resolve from the canonical session directory. Extract
the relevant current facts rather than only listing sources. Commands alone are
not observed proof: verification states scope, limitations and relevant revision.
Tier/confidence may accompany evidence when useful.

One mutable fact has one canonical slot. Work owns workflow state/owner/next
action; outcomes and context never repeat that state. Verification owns observed
verdict/limits. Keep still-binding requirements, exact sources, acceptance and
source→outcome→scope after completion; clear obsolete next actions/blockers.
Delete superseded requirements only when they no longer constrain the project;
preserve original source/change in append-only history, linking it from a guard
when it still explains a current hazard.

### Authoring and native edits

Use unique string keys, two-space indentation and JSON-compatible scalars.
Quote free text, dates, IDs and values resembling booleans/null/numbers. Use
native `true`/`false`, `null`, and finite decimal numbers for typed values.
Quote exact integers outside the read tool's safe range and exact decimals
outside its precision. Preserve exact multiline text with `|-` (no final
newline), `|` (one), `|+` (all trailing newlines), or quoted escapes. No
directives, explicit/custom tags, anchors, aliases, merge keys or multiple
documents. These are writing/quality rules, not runtime content permission gates.

Edit YAML in place with mikefarah `yq` v4 (`yq4`; `yq4 --version` reports
mikefarah v4): run `yq4 -i` on the canonical file and pass IDs and text through
`strenv()`. Do not edit a temporary copy and swap it in. Never write YAML with
the jq-based `yq` (version 3.x), which drops comments and quoting, or redirect a
tool's output onto its input. Use a native selective text edit only when `yq4`
is unavailable.

`yq4 -i` creates missing paths instead of failing. Before an update or deletion
(`del(.records[strenv(id)])`), check with `yq4 -e` that the ID exists and the
ledger record’s `type` or forecast record’s `kind` matches; before an addition,
check that the ID is absent and add a complete record. `yq4` keeps the quoting
of existing scalars but writes new keys and strings unquoted; set
`style="double"` on them. Preview each change as a diff before applying it: only
the intended records may change, apart from one-time layout normalization (line
wrapping, blank lines, comment spacing, sequence indentation). One canonical
session writer applies bounded worker proposals to the latest file.

```sh
cd "$HOME/.agent/context/sessions/$SESSION_ID"
f=project-understanding.yaml

# Update an existing work record.
export id='work:receiver:clock' owner='worker-b'
e='.records[strenv(id)].data.owner = strenv(owner)'
yq4 -e '.records[strenv(id)].type == "work"' "$f" && yq4 "$e" "$f" | diff -u "$f" -
yq4 -i "$e" "$f"  # after reviewing the preview

# Add a record with a quoted key and strings.
export id='guard:receiver:clock-source' rule='Keep a supplied setting distinct from a measurement'
e='.records[strenv(id)] = {"section": "proof", "type": "guard", "data": {"rule": strenv(rule)}, "evidence": []}
  | with(.records[strenv(id)]; (.. | select(tag == "!!str")) style="double" | key style="double")'
yq4 -e '.records | has(strenv(id)) | not' "$f" && yq4 "$e" "$f" | diff -u "$f" -
yq4 -i "$e" "$f"
```

Example fixture: [project-understanding.yaml](examples/project-understanding.yaml).
Its source/values are explicitly synthetic; adapt subjects and sections to the
project instead of treating it as a fixed domain template.

### Read-only queries

Use `yq4` to select and inspect records before a bounded edit; if it is
unavailable, use native text inspection. For example:

```sh
yq4 '.records | to_entries[] | select(.value.type == "requirement") | {"id": .key, "source": .value.data.source_text, "acceptance": .value.data.acceptance}' project-understanding.yaml
```

Node's existing `yaml` package can also parse for read-only queries; check
`node -p 'require.resolve("yaml")'`. Do not assume Node or the package is installed.
The following Bash example rejects parse/key/alias failures but does not certify
the full schema, allowed authoring subset, truth, freshness or completeness:

```sh
set -o pipefail
node -e 'const fs=require("fs"), YAML=require("yaml"); const value=YAML.parse(fs.readFileSync(process.argv[1],"utf8"),{uniqueKeys:true,stringKeys:true,maxAliasCount:0}); process.stdout.write(JSON.stringify(value));' project-understanding.yaml |
jq '.records | to_entries[] | select(.value.type == "requirement") | {id:.key,source:.value.data.source_text,acceptance:.value.data.acceptance}'
```

Parsing cannot detect semantic duplicate subjects or establish that useful
knowledge was omitted. Reconcile quality defects alongside safe work, never as
permission ceremonies. History/status/forecasts retain their separate contracts.

## Update Points

Update at work start, material state changes, findings/decisions/agreements, milestones, material user input, and existing QA/verdict, handoff, user-waiting stop, and shutdown checkpoints. Apply the dependency order below; these update points do not delay ready independent work.

Every material forecast change updates its current `forecasts.yaml` record in place and applies the same-update recalibration rule to that forecast.

Answer the user before ledger maintenance or coordination when a response is due.
Advance ready independent work without waiting for ledger, log, forecast, or report maintenance. Before a dependent action, correct the affected current-state records and communicate the material changed facts that action needs, including changed requirements, scope, ownership, controls, evidence, and verification results. Stop or redirect only work invalidated by that correction.

For assignments affected by material user input, use the [capture-and-prepare operation](requirement-assignment.md).

Reconcile the remaining records, append material changes to `high_level_log.jsonl`, and refresh `latest-status-report.yaml` in the same turn, before QA/verdicts, handoff, user-waiting stops, or shutdown. A status must reflect current facts; answering a due user response still comes first. A forecast-only change updates `forecasts.yaml` without rewriting the ledger. Keep this maintenance bounded to changed facts and their affected links; it never grants permission or creates a dispatch gate.

For a project/task fact change, use this three-pass ledger edit:

1. Stale pass. Check the affected subjects and linked records against current instructions, sources and reports. Replace/delete obsolete records and contradictory copied state.
2. Omission pass. Add missing facts for those subjects from authoritative sources, user instructions, current diffs/state, and this turn's agent reports.
3. Fit pass. Adjust their sections and memberships to fit current knowledge. Repair incoming references when deleting or moving records.

Review the whole snapshot at existing QA/verdict, handoff, and shutdown checkpoints. Between checkpoints, review affected subjects and linked contradictions rather than rescanning unrelated state for every event. Shared record contracts stay predictable while domain payloads and sections evolve. A purely additive update retaining stale state is a log-into-ledger defect: replace/delete in place.

Then append to `high_level_log.jsonl` one entry per material change made this turn. Every passed-around fact the stale pass deleted or rewrote becomes a log entry. For a forecast-only change, update its `forecasts.yaml` record and log it without rewriting the ledger.

Finally refresh `latest-status-report.yaml` using the edit mode above and the coverage in `writing-status-reports`; reflect the updated ledger and/or forecasts. Skip only when this turn produced no current-state or log change.

## Invalid Ledger

Reject the ledger if any holds:

- A fresh agent needs the transcript or unstated local memory to recover useful current project/task facts.
- An authoritative project/task source is named without extracting its relevant current-state facts. Resolve forecast details through the linked `forecasts.yaml` record; never extract forecast values into `project-understanding.yaml`.
- Binding requirements, acceptance criteria, material current-understanding corrections, useful recurrence guards, assumptions, risks, decisions, current state, or evidence are missing.
- Claims cannot be traced to sources, reports, commands, logs, screenshots, or commits.
- Obsolete states are retained as if current: stale plans, abandoned hypotheses, finished WIP, resolved blockers, superseded values.
- Entries are timestamped narrative or chronological "what happened next" prose, i.e. log-style.
- Multiple values for the same fact coexist instead of one current value.
- Activity logs or copied report bodies replace current-state summaries and links.
- The latest update is purely additive while sections that should have changed were left untouched.
- Facts are prose dumps instead of independently useful typed records under fitting sections.
- Canonical schema/shared record shape is absent, semantic subjects are duplicated, or section/link targets are missing or mistyped.
- An active lane lacks its next action or `forecast_ref` locator to its current forecast record in `forecasts.yaml`; the forecast lacks its named outcome, status, target, original baseline, or required recalibration; or a changed lane/root lacks equivalent outcome, target, assessment, and recalibration fields in the report projection.
- An unrepresented active root-task outcome omitted by lane reports lacks a full outcome and target record in `forecasts.yaml`.
- The ledger copies a forecast target, baseline, recalibration, base-case/downside estimate, scenario range, endpoint, or forecast status instead of linking to its `forecasts.yaml` record.
- Completed forecasted lane/root work lacks actual `completed_at` UTC, its forecast remains active, or the report omits the completion timestamp and absence of an active deadline.
This is a planning-quality defect: reconcile it alongside safe work without delaying the update.
- Section titles or memberships no longer fit the content.
- The high-level log is missing, was edited or truncated in place, lacks entries for material ledger changes or material forecast-only updates made this session, or duplicates current-state synthesis.
- The latest status report is missing, lacks a UTC timestamp, predates the last ledger update or material forecast-only update, fails `writing-status-reports` coverage (state, progress, decisions, blockers/risks, verification, next focus), or duplicates ledger structure instead of summarizing changed state.
- Secrets, credentials, or unnecessary personal data are recorded.
