# Requirement lineage

Requirement lineage is readable coordination context, not a permission system.
Assume bots are non-malicious. Use it to prevent accidental scope drift, not to
make normal work depend on hashes, receipts, exact schemas, or user-created
authorization artifacts.

## Record useful context

Keep the current `project-understanding.yaml` ledger readable enough to answer:

| Record | Purpose |
| --- | --- |
| User requirement | What outcome the user asked for, in concise faithful wording. |
| Lane | What bounded work is being done. |
| Reason | Why the lane helps that outcome. |
| Parent or prerequisite | Earlier lane when it materially explains the relationship. |
| Owner and evidence | Who owns it and the useful current proof or finding. |

Use stable IDs or aliases when they make a multi-lane report easier to follow.
Do not create an ID, registry, graph, hash, receipt, or exact record layout
solely to proceed with ordinary exploration, implementation, testing, review,
or status reporting.

## Trace material work

For a material ECI task, record `exact user source → faithful requested outcome
→ bounded scope`. Cite the source message and preserve the outcome's meaning;
a reason, discovery, or inferred safeguard is never a substitute user
requirement.

A repair is current-lane work when the record shows it is necessary to meet or
prove that outcome. A discovered concern whose remedy
serves an unadmitted separate outcome is an observation or follow-up suggestion, not a user
requirement, current lane, assignment, code change, review, forecast, deadline,
or proof program.

Authorized decomposition or related work may receive a child Job under the [recursive Job contract](../explore-critique-implement/references/task-orchestrator.md#recursive-child-admission-and-closure). Preserve its authorization source, stable parent chain, distinct outcome, and upward reporting route; no renewed user request is needed within that mandate. A discovery alone never supplies authorization.

Pressure check: a user requests useful diagnostics; investigation reveals an
unrelated potential secret/log concern. Preserve the diagnostics work and record the concern as a post-ECI
observation or follow-up; do not create a redaction lane, agent assignment,
code change, review, deadline, forecast, or proof program.

## Use lineage while working

- Record known outcome, scope, owner, and verification when they are available.
  Label unknown context and reconcile it in parallel; never delay a safe bounded
  handoff. Link the requirement context when known.
- Missing or stale lineage is recorded as `lineage unavailable—reconcile`; it
  does not stop bounded work, status reporting, or a safe assignment.
- Reconcile missing context in parallel with safe work. For example, assign an
  explorer to recover the connection between an existing user requirement and
  a current implementation lane.
- Ask the user only when the proposed work introduces a genuinely unauthorized new material
  outcome or a material ambiguity that cannot be resolved from the current
  request and ledger. Do not ask merely because an identifier or record is
  absent.
- Preserve historical decisions in `high_level_log.jsonl` when they change the
  current scope or rationale. The ledger remains the current snapshot.
- Correct false current scope in the ledger and status report, and log the
  correction. Cancel or reassign only unrooted current work. Do not
  destructively revert already-made work without user direction.

## Scope boundary

Stop or route only a concrete accidental scope mistake: for example, a worker
is about to modify another active lane's file, or a proposed feature is outside
the user's requested outcome. Name the resolved target or new outcome and use
the narrowest safe route.

Do not stop ordinary work for a missing lineage field, stale alias, unreadable
registry, absent log prefix, parser uncertainty, role label, command spelling,
or receipt mismatch. Those are coordination observations to repair or report.
