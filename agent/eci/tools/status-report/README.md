# status-report

Run `status-report validate [FILE]` or `status-report render [FILE]`; omit FILE or use `-` for stdin. The launcher requires Go and module dependencies. Both commands validate the YAML; rendering uses only that YAML and derives the progress bar. Validation checks structure and references, not factual truth or reviewer independence.

Required top-level fields are `timestamp` (RFC3339 UTC, `Z` or `+00:00`), `state`, lists `progress`, `decisions`, `blockers_risks`, `verification`, `tasks`, `top_priorities`, `next_focus`, `scope_complete` (boolean), `priority_estimates`, and `completion_estimate`. Use empty lists as `[]`. Optional fields are `impact_scale`, `priority_order`, `lanes`, `root_forecasts`, `roster`, `timeline`, `timeline_state`, `reviews`, and the extensible mapping `details`. Unknown fixed fields are rejected; `details` is rendered in full.

Tasks have unique `id`, optional `parent_id`, `title`, `priority`, `impact`, `effort`, `status`, optional `owner`, `context`, `evidence`, and `indivisible_reason`. Task IDs and parents define the hierarchy; a dotted ID's `parent_id` is its exact prefix before the final dot, and a root ID has no parent. Task `owner` executes the task. `priority` labels group completion estimates; the legacy `priority_order` is accepted but does not rank tasks. Optional `impact_scale` declares the common impact scale, and each task may supply finite nonnegative `impact_points` and an `impact_basis`. When the scale, points, and basis are supplied and effort is `work` with a positive maximum, calculated priority is points divided by maximum remaining work minutes. Siblings, top-priority references, and lanes sort by this score descending, with stable ties; unscored positions remain fixed and display the reason. Zero points is valid; a ratio beyond the numeric range is unscored. Estimates and their bases must be labeled as such. Parent and top-priority references must exist and the tree must be acyclic. `effort` has finite ordered nonnegative `min_minutes` and `max_minutes`, `kind` (`elapsed` or `work`), and `basis`. Build the task tree under [Work estimates](../../../eci-binding-contract.md#work-estimates); a leaf upper bound above 30 requires `indivisible_reason`. Status is `NEW`, `IN PROGRESS`, `PAUSED`, `BLOCKED`, `CLOSED`, `unknown` or null. CLOSED requires acceptance evidence. The progress bar counts completed known leaf packages / known leaf packages, labels this an equal-package estimate, excludes parents and marks unknown statuses and incomplete scope as partial. It is not elapsed-time progress.

Provide one `priority_estimates` entry for each priority present in the tasks, including `unknown`, and one overall `completion_estimate`. Each has a UTC `as_of`, finite ordered `min_minutes` and `max_minutes` of elapsed time, and nonempty `basis`, `assumptions`, `dependencies`, and `critical_path`; priority entries additionally have `priority`. The renderer displays `as_of` and derives endpoints by adding the durations to it; do not supply `earliest` or `latest`. Label educated guesses as such. The producer determines the critical path and latest parallel endpoint; the tool never adds parallel durations. Refresh estimates when affected work changes.

Lanes have `task_id` (an existing task), `outcome`, `required_environment` (`implementation`, `test`, or `prod`), and distinct `implementation_status`, `test_status`, `prod_status` (`NEW`, `IN PROGRESS`, `PAUSED`, `BLOCKED`, `CLOSED`). Optional fields: `owner`, `stage`, `next_milestone`, `completed_at`, `forecasted`, `blocker`, `next_action`, `context`, `root_task_status` (`FOLLOWUP`, `FOLLOWUP_PAUSED`, or null), `forecast`. Lane `owner` coordinates the lane; its referenced task's `owner` executes the task. Lane hierarchy comes from that task, not separate lane IDs or parent aliases. A `completed_at` UTC value requires the selected required-environment status to be CLOSED and no active forecast. Every lane whose selected status is CLOSED requires actual `completed_at`; a CLOSED selected status cannot retain an active forecast. Task status remains independent of lane readiness; a CLOSED source package can have an active test or production lane.

Enum fields must be YAML string scalars with an allowed value; mappings, lists, numeric or boolean scalars are invalid. Task `status` and optional `timeline_state` and `root_task_status` also accept YAML null as unknown; required enum fields reject null.
Canonical YAML authoring disallows anchors, aliases, and merges; validation checks the resolved values.

A supplied lane forecast or `root_forecasts` entry requires `outcome`, `target`, and `assessment`; use `unknown` for an unsupported target and quote UTC values. Optional fields are `baseline`, `prior`, `reason`, `evidence`, `dependencies`, and `scenarios` (list with `case`, `trigger`, `affected_work`, `duration_basis`, `evidence_assumptions`, `endpoint`, `evidence_gap_next_action`). Every scenario field must be nonempty; use `unknown`, `none`, or `—` where appropriate. `baseline` and `prior` must be UTC timestamps or `unknown`. A supplied prior equal to the current target is rendered as unchanged without claiming a move; an unknown endpoint leaves movement unknown. When no prior is supplied, equal baseline and target instants permit the canonical unchanged-baseline line while intermediate history remains unknown; unequal or unknown endpoints never imply an unchanged forecast. Forecasts project upstream records; the tool does not calculate them. Root forecasts cover roots omitted by lane forecasts.

Timeline entries have `job`, `parent_chain`, `start`, `end`, `fast`, `normal`, optional `evidence`, `uncertainty`. Start and end are UTC timestamps defining one of the eight consecutive half-hour bins anchored to report time T over [T−4h, T); intervals can cross midnight. A job may appear once per bin; different jobs can share a bin. Rows render chronologically within each job. All rows for a job must have the same parent chain. `timeline_state` may be `verified_empty`, `unavailable`, `present`, null, or omitted for unknown. Unavailable with rows is rendered as incomplete history.

`reviews` is a list of mappings with `task_id` (existing task), `reporter`, `critic`, `rounds` (1–5), `estimate`, `impact`, `priority`, and `unresolved` (`none` if resolved). The reporter requests independent review only for newly added time-estimated queue items; the tool does not dispatch critics or establish independence. At most five rounds. A report refresh, compaction, or recalibration of an existing item alone does not trigger this review.

```yaml
timestamp: '2026-11-02T15:24:00Z'
state: Work is underway.
progress: [Implementation started.]
decisions: [Use the existing contract.]
blockers_risks: []
verification: [Checks pending.]
next_focus: Complete checks.
scope_complete: false
tasks:
  - id: '1'
    title: Finish the work
    priority: high
    impact: Required outcome is incomplete.
    effort: {min_minutes: 20, max_minutes: 30, kind: elapsed, basis: educated guess}
    status: IN PROGRESS
    owner: implementation executor
top_priorities: ['1']
priority_estimates:
  - {priority: high, as_of: '2026-11-02T15:24:00Z', min_minutes: 20, max_minutes: 30, basis: educated guess, assumptions: review available, dependencies: none, critical_path: latest parallel endpoint}
completion_estimate: {as_of: '2026-11-02T15:24:00Z', min_minutes: 20, max_minutes: 30, basis: educated guess, assumptions: review available, dependencies: none, critical_path: latest parallel endpoint}
lanes: []
roster: {}
timeline: []
reviews: []
```

## Source reconciliation

`validate` and `render` reconcile the report against session sources before producing output. For a file named `latest-status-report.yaml`, sibling `project-understanding.yaml` and, when applicable, `forecasts.yaml` are used. For stdin or another filename, supply `--session-dir DIR` after the command. Without usable session context, rendering still produces the report with an `INCOMPLETE` notice and partial progress; validation exits nonzero. Structural report errors still prevent rendering. Reconciliation is advisory and does not prevent work.

Each task may supply `work_refs`, a list of `project-understanding.yaml#/records/<semantic-id>` references identifying work covered by that package. These are coverage links: package status and owner need not match the linked work's state and owner. Each lane supplies `work_ref` to its declared work record. Each lane or root forecast supplies `forecast_ref` to its canonical `forecasts.yaml#/records/<id>`. Work records explicitly declare `reporting_roles: []`, `[lane]`, `[root]`, or `[lane, root]`; the empty list means direct work without a lane or root forecast. Active or blocked work needs task coverage, each declared lane needs a report lane, and each open lane or unrepresented active root needs its active forecast. No forecast file is required when no forecast applies. Legacy records without roles remain unverified rather than being classified by title.

Reconciliation identifies missing or malformed sources, invalid references, omitted work, lanes or forecasts, forecast mismatches, and completion estimates with future as-of times or elapsed endpoints while work remains open. For a lossless forecast projection, supply `source_projection` under the report forecast containing the canonical `target_basis`, `baseline_evidence`, `base_case`, and `scenarios` fields present in the source. These nested values are rendered and compared recursively, including scenario event times and bases. Missing, changed, or unsupported fields are marked incomplete. UTC instants are compared semantically. The check cannot establish source freshness, factual truth, or reviewer independence; an absent freshness marker is unverified, not proof of staleness. It does not infer lane readiness from a task's status or compare work effort with elapsed duration.

After each successful high-level log append, `eci-active` invokes `status-report refresh SESSION_DIR EVENT` while holding the ECI mutation lock. Refresh reads the current ledger and applicable forecasts, projects supported linked forecast fields, and records reconciliation issues in `blockers_risks`. A `requirement_change` also marks `scope_complete` false and adds a scope-review diagnostic, even when existing links match. An owner must review coverage and remove resolved diagnostics before declaring scope complete; refresh does not clear them automatically. Unsupported edits are reported for manual refresh rather than discarding comments or inventing facts. The append is preserved if refresh fails; its warning includes a retry command that does not append the event again. Direct work with no forecast reference needs no forecast file.

Run the Go refresh command under the existing ECI mutation lock; `eci-active status-refresh --event EVENT` takes that lock for a retry. All other report and source writers must serialize with it. The before-publication change check detects some uncoordinated edits but is not a compare-and-swap and does not replace locking. Rendering and validation still reconcile sources as a backstop.

### Log-driven report updates

Producers can include an explicit `details.status_update` object in a high-level log event. Its `updates` and `additions` are arrays; omit either when unused. Each update has a `target` and stable `id`; `fields` supplies nonempty string values, and the typed operations below use `values` or `clear`. Supply known facts and evidence: prose elsewhere in the event is not interpreted as a report update. This payload closes a task and supplies its acceptance evidence in the same update:

```json
{"updates":[{"target":"task","id":"1","fields":{"status":"CLOSED","evidence":"Acceptance check passed: checks.out"}},{"target":"verification","id":"acceptance-check","fields":{"text":"Acceptance check passed.","evidence":"checks.out"}}]}
```

Place that object under `details.status_update` in a valid log event. Supported updates are:

- `task`: identify by task `id`; fields are `status`, `evidence`, `owner`, `context`, and `lifecycle`. `CLOSED` requires accompanying `fields.evidence`. Cancel a task with `fields` containing `lifecycle: "cancelled"`, `status: "unknown"`, and nonempty `evidence` together. This removes that task from `top_priorities` and preserves its descendants.
- `lane`: identify by `id` (the lane's `task_id`), `work_ref`, and `required_environment` (`implementation`, `test`, or `prod`). Fields are `owner`, `implementation_status`, `test_status`, `prod_status`, `blocker`, `next_action`, `next_milestone`, `context`, `completed_at`, `lifecycle`, and `root_task_status` (`FOLLOWUP` or `FOLLOWUP_PAUSED`). To close the required environment, supply its `CLOSED` status and `completed_at` together, plus nonempty update-level `evidence`. The canonical work must be completed at the same time and the report lane must have no active forecast. Cancellation uses `lifecycle: "cancelled"`, update-level evidence, and canonical work in the cancelled state.
- `progress`, `decisions`, `blockers_risks`, and `verification`: identify a subject with `id`; fields are `text` and `evidence`. New subjects need `text`; the report stores the ID as `subject_ref`.
- `report`: use `id: "current"`; fields are `state` and `next_focus`.

For example, a lane completion update after the canonical work and forecast are reconciled is:

```json
{"updates":[{"target":"lane","id":"1","work_ref":"project-understanding.yaml#/records/work:example","required_environment":"test","evidence":"acceptance.out","fields":{"test_status":"CLOSED","completed_at":"2026-11-02T15:24:00Z"}}]}
```

`additions` accepts `{"target":"task","record":{...}}` or `{"target":"review","record":{...}}` with a complete report-schema record. IDs must be unique; a review uses `task_id`. When a task introduces a new priority, add its complete `priority_estimate` record in the same event; the task and estimate are inserted together. These operations do not derive impact points: the producer assigns supported points and basis on the shared scale, and the renderer calculates priority from those points and remaining work.

An existing task can similarly change `priority` in `values` in the same event that adds the new `priority_estimate`. To retire the old estimate when this is its last task, include `retire_priority` naming the old priority and `expected_prior` containing its complete existing estimate on the new estimate addition. A mismatched prior remains pending.

Additions also accept complete `lane`, `root_completion`, and `root_forecast` records. Their work, reporting role, outcome and forecast must agree with canonical sources; terminal additions require addition-level `evidence`. For a terminal lane update, the canonical work must match and any referenced canonical forecast must be inactive before its report forecast can be retired. Reopening a lane clears its `completed_at`.

To replace an existing root forecast with a completion, the `root_completion` addition supplies `retire_forecast: true`, `expected_prior` containing the complete existing forecast record, and evidence. To reopen a completed root, the `root_forecast` addition supplies `reopen_work_ref`, `expected_prior` containing the complete existing completion record, and evidence. The prior record must match exactly and canonical work and forecast states must support the transition. Comments or conflicting edits can leave the transition pending.

To correct an existing root completion, append a `correction` event identifying the mistaken entry in `details.corrects` and provide a `root_completion` addition with evidence, the exact existing record as `expected_prior`, and the corrected `record`. The canonical completed work and its outcome and completion time must support the corrected record. For example, `details.status_update` can contain:

```json
{"additions":[{"target":"root_completion","evidence":"corrected","expected_prior":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:00:00Z"},"record":{"work_ref":"project-understanding.yaml#/records/w","outcome":"Result","completed_at":"2026-11-02T15:05:00Z"}}]}
```

To retire a cancelled root's forecast without a completion, use an update whose `id` is the forecast reference and whose `work_ref` identifies the canonical work. Supply evidence, the complete existing report forecast in `values.expected_prior`, and `clear: ["record"]`:

```json
{"target":"root_forecast","id":"forecasts.yaml#/records/f","work_ref":"project-understanding.yaml#/records/w","evidence":"owner cancelled","values":{"expected_prior":{"forecast_ref":"forecasts.yaml#/records/f","outcome":"Result","target":"2026-11-02T16:00:00Z","assessment":"complete"}},"clear":["record"]}
```

The canonical work must be cancelled, have the root reporting role and reference that forecast; the canonical forecast must be an inactive root forecast, and the recorded outcome must correspond to the work. The supplied prior must match the report record. Conflicting edits or comments can leave the removal pending.

Typed updates use `values` for supplied JSON values and `clear` for explicit removal of supported fields. Supported typed targets and fields are:

- `task` by task ID: `effort`, `impact`, `impact_points`, `impact_basis`, `priority`, `work_refs`, `indivisible_reason`, `title`, `parent_id`.
- `priority_estimate` by priority: `min_minutes`, `max_minutes`, `as_of`, `basis`, `assumptions`, `dependencies`, `critical_path`.
- `review` by task ID: `reporter`, `critic`, `rounds`, `estimate`, `impact`, `priority`, `unresolved`.
- `report` with ID `current`: `completion_estimate`, `top_priorities`, `roster`, `priority_order`, `impact_scale`.

For example, `{"target":"task","id":"1","values":{"impact_points":5,"impact_basis":"educated guess"},"clear":["indivisible_reason"]}` supplies points and explicitly removes the optional reason. Resulting reports still must validate; a clear does not invent a replacement.

Task updates can also clear `lifecycle`, `context`, `owner`, or `evidence`; clearing `lifecycle` requires an explicit non-unknown status in `fields`. Lane updates can clear `blocker`, `next_action`, `lifecycle`, `completed_at`, or `root_task_status` alongside scalar `fields`. Clearing lane `lifecycle` requires event evidence and canonical work in active or blocked state. Supply readiness changes as needed for a valid report; clearing `completed_at` while the lane remains closed is invalid. A field cannot be supplied and cleared in the same update.

A complete timeline snapshot can be supplied as `details.status_update.timeline` with UTC `as_of`, `state` (`present` or `verified_empty`), and `entries`. A present snapshot supplies all eight consecutive half-hour bins for every included job, with both `fast` and `normal` values; `verified_empty` has no entries. The snapshot replaces the timeline and its anchor as a unit.

For example, this payload adds a task and its review, using supplied estimates and review facts:

```json
{"additions":[{"target":"task","record":{"id":"2","title":"Check result","priority":"high","impact":"Acceptance remains open","effort":{"min_minutes":10,"max_minutes":20,"kind":"work","basis":"educated guess"},"status":"NEW"}},{"target":"review","record":{"task_id":"2","reporter":"owner","critic":"reviewer","rounds":1,"estimate":"10–20 minutes","impact":"Acceptance remains open","priority":"high","unresolved":"none"}}]}
```

Sync consumes events in append order and applies each target update as an ordered transaction. A correction is a new `correction` event whose update includes `corrects`, the one-based log line containing the latest update for each corrected field. If that version is not the first update on its line, also supply `corrects_ordinal`, its zero-based position in that line's `updates` array; omission means zero. Keep earlier entries unchanged. The report stores its sync cursor and pending state in `status_sync`; do not edit it as a substitute for an event. Conflicting owner edits, missing targets, invalid records, and unsupported or unsafe changes may remain pending or produce diagnostics for manual reconciliation. Sync preserves unrelated content and owner edits it detects; it does not infer missing facts. Review diagnostics and reconcile sources before claiming completion. If refresh fails after append, use the emitted retry command without appending the event again. A typed event can advance the report timestamp; an existing timeline keeps its original anchor in `timeline_as_of` unless a new snapshot is supplied.

Old field-wise pending corrections that reference a source update with typed values or explicit clears remain pending for manual reconciliation. Their saved state cannot represent the complete transaction, so refresh does not apply a scalar field separately from its companions.

A log is optional before any events have been consumed; if a saved cursor refers to a missing log, refresh fails until the history is restored. A complete final JSON entry is accepted without a trailing newline. An invalid unterminated tail is diagnosed and left unconsumed until it is completed or terminated; a malformed newline-terminated status update is diagnosed as invalid.

For example, when line 12 was the latest update to task 1's owner, a correction event can include `details.corrects` identifying that mistaken entry and `details.status_update` containing:

```json
{"updates":[{"target":"task","id":"1","corrects":12,"fields":{"owner":"actual owner"}}]}
```

To retract a sync-managed narrative subject, append a `correction` event with an update such as `{"target":"progress","id":"subject","corrects":12,"retract":true}` and no `fields`. `corrects` must identify the latest text or evidence update for that subject. Retraction checks the managed text and evidence and preserves owner edits; a conflict requires reconciliation.

A linked active or blocked work record needs a remaining non-CLOSED leaf in the linked task subtree, or an explicitly open acceptance lane. Closed source packages may coexist with an open test or production lane. If all scored leaves are CLOSED but an open lane still has acceptance work, the package ratio is partial. Cancelled lanes may be omitted; a retained cancelled lane uses `lifecycle: cancelled` and must not claim completion, and any linked canonical forecast must be inactive.

A completed root without a lane is represented in `root_completions` as `{work_ref, outcome, completed_at}`. The outcome must match an outcome record linked by that work record, and the timestamp must match the ledger's actual completion instant. Forecasted completed roots also require an inactive canonical forecast. Root completions render as completion records, without an active deadline.

A lane's `work_ref` must belong to its `task_id` scope: the reference may occur on that task, an ancestor, or a descendant within its aggregate subtree. A reference on an unrelated branch does not establish coverage. Completed or cancelled source work cannot retain `FOLLOWUP` or `FOLLOWUP_PAUSED` in its lane.

Each validate, render, or refresh invocation captures one UTC evaluation time. An active forecast target or relevant open-work estimate whose endpoint has elapsed by that instant is incomplete, even when the saved report predates the endpoint. A future report timestamp or estimate `as_of` is incomplete; an older estimate is not stale solely because it is old. Completed work is exempt from elapsed-endpoint checks.

Active work must link `outcomes` to existing ledger outcome record IDs. Forecast ownership compares complete linked outcome-ID sets; unrelated roots cannot share one forecast. A lane's label and its forecast's outcome remain separate prose. If linked canonical outcome descriptions do not establish their equivalence, the report is marked incomplete without inventing a match.

Displayed lanes, root forecasts and root completions require source-backed outcome correspondence. The complete set of linked canonical outcome records must support the displayed claim; a matching child alone cannot establish a composite claim. Unmatched or ambiguous prose is marked unverified without rewriting it. Completed and cancelled work needs valid outcome links when its result is displayed. Actual completion timestamps later than the invocation's captured evaluation time are incomplete, including when session sources are unavailable; future forecast targets remain valid.

The optional lane `lifecycle` is a string: omit it or use an empty string for no marker, or use `cancelled`. Null, non-string values, custom YAML tags and other labels are invalid, including through YAML merges.

For an active forecast with a base case or scenarios, the target must match the latest comparable endpoint across the complete supplied spread. Endpoints may be a UTC instant or an ordered `UTC–UTC` range; the range's upper endpoint is used. A known endpoint beyond the target is incomplete even if another endpoint is unknown. Missing base cases, unknown endpoints, unsupported qualifiers or malformed ranges leave spread consistency unverified. A forecast with no spread remains valid; inactive history is not assigned a deadline.

Every scored leaf, including a completed package, must resolve at least one canonical work reference on itself or an ancestor. A sibling's reference does not cover it. Estimate expiry considers non-CLOSED report leaves as well as open canonical work and acceptance lanes; it uses each leaf's priority for priority estimates and all open leaves for the overall estimate.

A native `source_projection` with a base case or scenarios renders a seven-column forecast-spread table for each lane or root. Table cells use supplied values and show `unknown` for missing fields; the full projection remains visible so package details, event times and other supplied fields are retained. When both are supplied, native rows appear first and legacy rows appear in a labeled unverified appendix. A provisional assessment uses its supplied target basis or evidence gap, or states `unknown risk`, and labels the target conditional.
