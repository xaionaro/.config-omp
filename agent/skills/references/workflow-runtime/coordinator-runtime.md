# Coordinator Runtime

Use this module as the ECI Supervisor or the owning outer-workflow Coordinator/Lead. Ordinary Workers load only the local module named in their assignment. ECI-specific rules below govern ECI outcomes; for nested ECI, the outer Coordinator/Lead retains outer-workflow lifecycle, review assignments, escalation, and acceptance. This reference routes work and review; it is not a permission system. Assume bots are not malicious. Prevent concrete accidental target or scope damage. Do not stop normal work for an artifact, receipt, hash, allowlist, raw command spelling, parser shape, model label, or missing coordination field. When a lower rule appears stricter without a concrete resolved accidental effect, this rule wins.

## Admission and agent lifecycle

- Start an outer workflow only when omp selects it. A nested ECI remains owned by outer ATE.
- Apply [concurrent task scheduling](../../../eci-binding-contract.md#concurrent-tasks) to ECI tasks within the active lifecycle. Stage, checkpoint, and review waits follow the affected task's dependencies; root teardown covers all owned tasks.
- Before a durable spawn, followup, lane-changing message, or write, identify task, scope, owner, and enough target detail to avoid an accidental cross-scope change. For material ECI work, carry `exact user source → faithful requested outcome → bounded scope`. Missing lineage or a stale label calls for a concise clarification or reassignment; it does not block harmless work.
- Keep a repair in its current lane when the record shows it is necessary to meet or prove the requested outcome. Do not relabel that repair as a substitute user requirement.
- Record a discovered concern whose remedy serves an unadmitted separate outcome as an observation or follow-up suggestion after current ECI. It does not create current lane, assignment, code change, review, deadline, forecast, or proof work.
- Keep prompts and handoffs as readable coordination records. They may support review, but no proof-directory artifact, hash, receipt, ledger prefix, or serialized field list is a prerequisite for normal spawn, followup, exploration, implementation, or verification.
- Spawning a subagent creates a role; `write agent://<id>` messages a live agent and starts a new turn when the reusable role is idle; `send_message` supplies bounded information to a running turn; `interrupt_agent` cancels exact active work only. Never shell-launch agents or close terminal agents.
- Name reusable ordinary producers by stable semantic role. Every blind critic and every special role uses a fresh subagent spawn (`fork_turns:"none"`) with a unique transport name. A `write agent://<id>` message never upgrades an ordinary role to special.
- Keep at most one outstanding `wait` per undelivered expected completion. A timeout is non-terminal: do not poll, retry, or infer a crash. Use `read proc://` only for roster maintenance or documented crash recovery.
- Include scoped verification and recovery duties in every subagent prompt.

## Role and model admission

Every assignment and roster entry names one category and one semantic role. State the work, target/change/verification for implementation, and owner. Use plain readable records; canonical JSON, hashes, receipts, and exact field order are optional diagnostics, never admission requirements.

| Category | Authority/model | Permitted roles and work |
| --- | --- | --- |
| `exploration-only` | non-authoritative / ordinary | Fact gathering, options, evidence; never authoritative design/admission. |
| `design` | authoritative / special | Designers, design reviewers, ECI Step 2/Critic C, ATE long-term-health reviewers. |
| `mixed` | combined / special | Only explicit `combined fact-and-authority`; never infer it. |
| `implementation` | non-authoritative / ordinary | Producers (Implementers and Fast Owners), Helpers, executors, test/QA, Critic A/B, correctness reviewers. |

Use stable role labels where available. An unfamiliar label or changed profile prompts a quick clarification or a reasonable available assignment; it does not block normal work or require profile hashing. Request the intended model and effort when the provider exposes them, and record unavailable selector fields plainly.

Send exposed selectors when they materially help the assignment. Treat unavailable or conflicting telemetry as a note for review, not a reason to halt, manufacture evidence, or reject ordinary worker progress.

### Closed role map

`fast-owner`→`Fast Owner Producer` is a reusable ECI Producer governed by [ECI fast path](../../explore-critique-implement/references/fast-path.md), including its scoped shared-tree ownership exception. `task-orchestrator`→`Orchestrator` is an ordinary coordination role governed by the [Job-owner contract](../../explore-critique-implement/references/task-orchestrator.md); it does not load this Supervisor-only runtime.

Within ECI, the global lifecycle role is the **Supervisor**. Exactly one published **Orchestrator** coordinates each active **Job**, including a single Job. N counts outcomes for lifecycle and acceptance only and never publishes Orchestrators. Each Worker assignment names its Job, outcome, Supervisor, and current Job owner.

Common labels include `explorer`→`Explorer`; `researcher`→`researcher`; `brainstormer`→`Brainstormer`; `critic-step2`→`ECI critic-step2`; `critic-A`→`ECI Critic A`; `critic-B`→`ECI Critic B`; `critic-C`→`ECI Critic C`; `e2e-gate`→`E2E gate`; `implementer`→`Implementer Producer`; `executor`→`Executor`; `qa`→`QA`; `fdr-reviewer`→`FDR reviewer`; `fdr-meta-reviewer`→`FDR meta-reviewer`; `ate-design-reviewer`→`ATE Design Reviewer`; `ate-meta-reviewer`→`ATE meta-reviewer`; `execution-reviewer-correctness`→`Execution Reviewer: correctness/fidelity`; and `execution-reviewer-long-term-health`→`Execution Reviewer: long-term-health`. Normalize when useful; unknown labels ask for clarification rather than block.

- Use role categories as routing defaults: exploration gathers facts; design reviews consequential design choices; implementation changes and tests code. If the exact role is unavailable, use a reasonable available role and schedule independent review where it matters. Do not make a special model, field shape, or role label a normal-work gate.

If work needs a different role, reassign it clearly and use a fresh reviewer where independence matters. Special roles normally use a fresh unique subagent spawn (`fork_turns:"none"`); ordinary Explorer, implementer, and ordinary correctness/fidelity slots may be reusable where their outer workflow permits. Do not create an artifact or model-routing blocker merely to record the reassignment.

`PostCompact` is the authoritative compaction refresh signal. Immediately after it, the coordinator/lead re-reads the active outer skill before another decision or tool call. `SessionStart` `startup|resume|clear` is only a best-effort resume/clear reminder. The `PostCompact` hook is read-only; it is a reminder, not proof of rereading.

## Required-critic runtime

The ECI Supervisor keeps global ECI review state visible while ECI is active. A manifest is a useful summary, not a publication or command-admission monopoly; Workers may provide ordinary reports and the current Job owner reconciles outcome-local evidence.

For each target, record its path or scope, current diff/revision when useful, assigned reviewers, required verification, and current verdict. Keep enough context to notice that the target changed; do not require a prescribed header, hashes, or artifact paths before work can proceed.

At review and final acceptance, obtain the independent checks and E2E each outcome needs. Within ECI, the current Job owner dispatches required reviews under the [Job Orchestrator contract](../../explore-critique-implement/references/task-orchestrator.md), and the ECI Supervisor retains outcome acceptance. Nested ECI does not transfer outer-workflow review assignments or acceptance from the outer Coordinator/Lead. Use the [router's E2E cadence, scope, and timing policy](../../explore-critique-implement/SKILL.md#e2e-cadence-scope-and-timing). A changed target or missing review routes fresh review before claiming acceptance; it does not freeze exploration, implementation, ordinary verification, or targeted commits behind row schemas, receipts, or hashes.

At acceptance, compare the actual current target and diff with the review scope. A meaningful change triggers fresh appropriate review. Preserve historical notes when useful, but missing or malformed ledgers, receipts, hashes, or anchors are repairable coordination gaps, not a reason to reject normal work.

**ECI adjudication:** The ECI Supervisor preserves each critic's substantive verdict and explains any disagreement. Use readable reports; do not make terminal markers, byte format, bounded text rules, or report hashes prerequisites for progress.

The optional Critic C prewrite note may help a selected skip-design route, and a post-write reconciliation can improve final review. Neither is a prerequisite for the first production write. Nested ECI never removes the outer ATE marker.

## Acceptance and Git boundary

Only governed target paths need assigned review; leave unrelated changes out. Within ECI, the ECI Supervisor maintains global review state, and the current Job owner maintains outcome-local evidence. A document path, writer spelling, redirection, or generic file operation is not a permission boundary; protect only real active control records and other sessions' control state.

Before a destructive Git action, resolve the repository and concrete paths. Stop only a broad, unresolved, or unrelated destructive action and offer the narrow safe route.

Within ECI, Producers own and author new tracked repository contributions, check their complete scope, and make their own commits. This includes documentation, configuration, analysis, and administrative output; a Producer integrates delegated Helper work. Preserve any pre-existing authorized Supervisor-authored repository change with its original author, exact scope, and provenance, then explicitly hand it to a Producer for integration, checking, and its own checkpoint. Session coordination grants no Supervisor authority to author a new tracked contribution or stage, mutate the index, or commit it.

Within ECI, an explicitly authorized Helper may perform bounded staging or index operations only after its own exact runtime identity and write scope have a live pending intent and a fresh same-index lookup. Its responsible Producer routes that intent and lookup, then agrees order directly with matching Producers; the Helper follows that order and never commits. Supervisors and Orchestrators retain live-control, coordination, and verification duties but do not author new tracked contributions or stage, mutate the index for, or commit repository contributions. They relay matching identity, scope, and index facts; they do not assign contested content or Git turns. Follow the [active-writer, shared-index, staged-result, and joint-checkpoint rules](../../explore-critique-implement/references/task-orchestrator.md) for shared-index order. Preserve other authors’ staged and unstaged content. The ECI Supervisor independently verifies each Producer’s exact immutable parent-to-commit range before dependent review or work. Reviewers receive that range and explicit exclusions; exclude later ambient changes. This is review scope, not admission proof.

For ECI fast contributions, apply the [adoption boundary](../../explore-critique-implement/references/fast-path.md#adoption-review-and-closure), including cumulative review beyond the iteration range and both-producer closure. Normal-path completion requires the post-Fast sequence, even after a clean intermediate review.
Follow the normative [post-Fast completion sequence](../../explore-critique-implement/references/fast-path.md#post-fast-completion).

After ECI `off`, record a concise teardown summary and the actual current repository state. If a historical coordination note disagrees, reconcile it; do not make hashes, receipts, or byte-exact records a prerequisite for normal teardown.

## Shared records

Use readable current-state records for current requested work. A concern whose
remedy serves an unadmitted separate outcome is only a post-ECI observation or follow-up
suggestion, never a current queue or replay record. It never waives an original
criterion.

## Routing packet guidance

Before every spawn, followup, lane-changing `send_message`, or durable execution, carry enough context to identify the task, scope, owner, target/change/verification when implementation is involved, and reason for any reroute. Use stable IDs and lineage where helpful. Missing metadata routes a concise clarification or queue; it does not block harmless work or create an admission failure state.

Every prompt states semantic role, exact scope/files/ownership, expected output, fresh-assignment reread rule, matching skills, concurrent-edit warning, relevant verification, and scoped verification and recovery duties. Reviewer/verifier/QA packets also state objective/criteria and the current target/diff. Use concise readable context; no prompt artifact, hash, or ledger prefix gates dispatch.

Prompts and selector records aid review. They do not prove effective model behavior and do not gate ordinary work. Record a limitation when relevant, then continue with the available provider.

### Stable roles and fresh identities

Use the closed role map above. Reusable ordinary producer slots are Explorer, Implementer Producer, Fast Owner Producer, ordinary Executor, and ordinary correctness reviewer only where the outer workflow says so. An Orchestrator identity remains reusable only while the Supervisor keeps its Job ownership published; later delegation follows the fresh-candidate rule in the [Job Orchestrator contract](../../explore-critique-implement/references/task-orchestrator.md#publication-transfer-and-report-drain). Step-2 critics, Critic A, Critic B, Critic C, E2E, brainstormer, BRP validator, loop-breaker, Design Reviewer, FDR reviewer/meta-reviewer, and every special semantic role are fresh identities as their owning workflow requires. An ordinary followup never upgrades category or model class.

## Critic-manifest currentness

A manifest is a current, readable review summary. Compare it with the actual target and diff before final acceptance. If they differ materially, obtain fresh appropriate review. Do not demand a header, canonical byte stream, hash, receipt, lock, symlink policy, or fixed A/B/C row count before continuing normal work.

Keep prewrite/postwrite notes only when they help a chosen review route. They never create a universal write firewall or make a reviewer report artifact a prerequisite for an ordinary edit, command, or targeted commit.

Preserve substantive critic findings. A stale or missing summary is a coordination repair, not an access denial.

### Code-diff and nested boundaries

The optional Critic C prewrite route may be used for selected skip-design work before its first production write. It informs review, not command admission. After write, a separate critic may reconcile actual scope, approved deltas/deviations, and tool evidence when that review is useful.

Nested ECI records outer owner/session/target and does not remove the outer ATE marker. Resolve actual competing writes or teardown targets; do not block nested work solely because a receipt or lock record is absent or malformed.

## Guarded command and completion behavior

Evaluate commands by their resolved effect and target. Unknown syntax, interpreters, Make, aliases, options, substitutions, wrappers, environment expansion, parser limitations, or shell punctuation are not reasons to deny normal work. Resolve and stop only an actual broad, unresolved, cross-scope, or destructive effect; explain the concrete mistake and offer a narrow safe route. Locks and records coordinate concurrent work but never become proof or a prerequisite for harmless work.

Before ECI Supervisor or outer Coordinator/Lead completion, independently re-read changed lines, run relevant checks, inspect current diff/status, and verify every original requirement. An ECI Producer handoff is an unreviewed PR: the ECI Supervisor verifies its immutable range before dependent ECI review/work or ECI acceptance. Preserve unrelated dirty paths as explicit exclusions; never bundle them into a critic target or commit.
