---
name: sentry-iterate
description: Use only when the user explicitly asks to run sentry-iterate or invokes $sentry-iterate. Mentioning Sentry, explaining this skill, or editing it alone does not start an iteration.
---

# Sentry Iterate

Run one bounded iteration over all Sentry issues in the user's scope. Fix every unresolved issue that can be fixed or mitigated. Insufficient understanding calls for useful observability; only a fundamentally unmitigable external cause qualifies as `actionability:external`.

Start only on explicit invocation. Invocation requests the repairs, observability, issue comments, and justified classification described here, within the user's scope and existing permissions. It does not authorize deployment, pushing, deleting issues, changing access, or unrelated configuration. Creating, reviewing, or explaining this skill alone performs no live Sentry actions.

## Collect the complete inventory

1. Resolve the intended Sentry organization(s), projects, environments, and associated repositories from the request and available context. Discover the actual connector, API, or UI capabilities and permissions. Continue answer-independent work when a scope or access gap needs clarification.
2. Gather **all issue statuses**, then derive the unresolved queue from verified issue state. Override default unresolved-only queries and hidden project, environment, saved-view, and time filters. The [organization issues API](https://docs.sentry.io/api/events/list-an-organizations-issues/) uses an empty `query` for all statuses. Verify the chosen interface's date defaults; an omitted period or a default 14-day window does not establish all-history coverage. Use supported unbounded retrieval or explicit windows covering all accessible retained history. Distinguish retained-history limits from complete historical knowledge.
3. Enumerate every page for every scoped organization/project. For REST, follow [pagination](https://docs.sentry.io/api/pagination/) while the next link has `results="true"`; a page limit is not a total. Deduplicate by organization and issue ID. Record query/window, collection cutoff, project/environment coverage, pagination completion, and inaccessible or truncated portions. A capped tool response requires a capable retrieval route or an explicit incomplete-inventory result.
4. Maintain the inventory in existing task tracking. Preserve resolved/ignored issues as inventory context; create the work queue from unresolved issues, including ones previously marked external or awaiting diagnostics. Recheck state before acting on stale results. Collect remaining pages while independent ready issue work advances. Do not claim "all issues gathered" while coverage remains unverified.

## One ECI root task per unresolved issue

Create each unresolved issue as its own ECI root task, identified by its issue URL/ID, with scope, owner, dependencies, evidence, and current-iteration acceptance criteria. Here "root task" means an independently owned issue outcome under the **one active ECI/ATE lifecycle**, not a separate top-level lifecycle for each issue; preserve normal bounded ECI lifecycle under active ATE. Do not reduce issue tasks to list items inside one aggregate implementation task. Reuse an existing task for that issue. Preserve active ATE and route bounded issue work through normal ECI; otherwise use ECI. Do not infer ATE from queue size.

Check prior observability and new-build evidence before scheduling solving work. A verified waiting issue keeps its independent task record with a checked skip disposition for this iteration; do not launch another repair/instrumentation effort or a polling lifecycle for it.

Use [explore-critique-implement](../explore-critique-implement/SKILL.md) and its [Fast contract](../explore-critique-implement/references/fast-path.md) for each issue needing investigation or changes. Start main ECI and its reusable Fast owner concurrently in the same tree, with main priority for conflicting writes. Independent ready issue tasks advance concurrently within capacity; name dependencies, conflicts, or capacity when queuing. Shared fixes retain per-issue evidence and integrated verification.

Fast success is provisional. Require ECI's [post-Fast completion](../explore-critique-implement/references/fast-path.md#post-fast-completion): actual Fast completion and stopped tools, then new exploration and fresh design/quality acceptance of the cumulative state. Follow that contract for implementation, E2E, resumed writes, and closure; earlier reviews or a write-yield cannot substitute.

## Decide and act within each task

Read issue details, representative events, stack traces, breadcrumbs/logs, comments, prior fixes, and relevant code. Separate causes from triggers and test competing explanations. Inspect earlier observability before adding more.

| Evidence | Current-iteration action |
| --- | --- |
| A local fix or mitigation is feasible | Reproduce, implement, and verify it through ECI. Include useful mitigation for externally triggered failures. Record code/test evidence and deployment status separately; local acceptance alone does not prove production resolution. |
| Cause remains unclear; adequate observability is absent | Add and verify the specific observability needed in the affected tooling, then comment on the issue with what changed and what evidence the next iteration needs. Preserve the unresolved state and defer diagnosis until that evidence exists. |
| Adequate observability was already added, but no qualifying events from software containing it exist | **Skip this issue for this iteration** and move to the next issue. Retain its evidence request. Do not duplicate instrumentation, comments, or polling. Silence is neither a fix nor proof that diagnostics failed. |
| Events from a proven instrumented build contain the needed evidence | Resume diagnosis now and fix or mitigate when possible. A previous waiting disposition does not defer new actionable evidence. |
| The cause is fundamentally external and cannot be fixed or mitigated locally | Classify `actionability:external` only after the external-cause test below. Record supporting evidence and apply the annotation through a verified supported interface. |
| Access, capabilities, or build provenance remain unresolved | Record the precise missing capability/evidence and complete independent work. This is a blocker or uncertainty, never evidence for `actionability:external`. |

### Evidence needed before waiting

Verify the existing diagnostic change in source/history and its coverage of the missing information. Map its commit or equivalent source identity to the actual build/release and target environment; inspect deployment evidence where available. Inspect event release/build identifiers and diagnostic signatures. A newer timestamp, greater version string, staging event, or "logging added" comment alone does not prove that the relevant code ran.

If provenance is unknown, investigate it; unresolved build mapping remains an evidence gap, not a verified waiting disposition. For verified instrumentation, check issue events in the relevant environment from builds proven to contain the change, including later builds. A qualifying event is enough to resume diagnosis. To establish absence, use supported release/build filters and sufficient time/pagination coverage of that cohort; representative old events alone are insufficient. A failed, truncated, or insufficiently scoped query is unknown coverage, not evidence of absence. Once no qualifying events are verified, record whether instrumentation awaits deployment or events and skip this iteration. Do not deploy merely to unblock this workflow. If instrumented events arrive but diagnostics are inadequate, improve the specific missing observation and explain the new evidence request.

Observability must distinguish the remaining hypotheses: for example, operation identity, sanitized failure stage, relevant state transition, build/environment, and correlation to the failing request. Test that the affected failure path emits usable diagnostics without secrets or excessive noise. Add observations in the relevant tooling, not merely a TODO or a request for more logs.

### External-cause test

Establish both the external cause and why no local repair or mitigation can change the outcome or impact. Consider applicable timeout/retry safety, recovery, fallback, validation, durable state, and user-facing handling. A third-party outage, OS error, missing permissions, limited time, or inability to reproduce is insufficient. Reassess prior external classifications against new evidence. Never mark a mitigable local defect external to reduce the queue.

### Issue annotations and comments

Inspect the actual supported write schema before mutating Sentry. The [issue update API](https://docs.sentry.io/api/events/update-an-issue/) does not document an arbitrary-tag or comment body field; do not invent one or assume event tags are editable issue labels. Use a verified supported connector/API/UI route with existing authority. Re-read issue history before posting to avoid duplicates.

When observability changes, post a concise issue comment containing the unresolved question, diagnostic change and source reference, known build/release/environment or explicit unknowns, verification, and the event evidence needed next. Post another comment only for material new information. An unchanged waiting issue needs no repeated comment.

If the external annotation or required comment cannot be written, retain the exact intended annotation/comment, mark that write **pending**, explain the capability/access gap, and continue other issues. A comment mentioning `actionability:external` is not an applied classification. Never claim a write succeeded without confirmation, and do not invent resolve/archive operations as a substitute.

## Finish this iteration

Finish the iteration when inventory coverage is established and every queued issue has a verified current-iteration outcome. If collection or a required write remains blocked, report the iteration as incomplete with the precise gap and completed independent work. Waiting means the **next invocation**, not an endless poll or automatic rerun. Re-fetch issue state and new-build evidence on that invocation.

Report inventory coverage/counts and each issue's outcome: fix/mitigation with verification and release status; observability added with comment confirmation; skipped awaiting the identified build/events; justified external classification with write status; or blocker with next action. Keep pending writes visible. An iteration disposition can be accepted while the Sentry issue remains unresolved; do not describe deferred diagnosis, an unapplied annotation, or an undeployed fix as production resolution. Complete required ECI reviews and stop all task-owned writers before normal lifecycle closure; retain unfinished siblings.
