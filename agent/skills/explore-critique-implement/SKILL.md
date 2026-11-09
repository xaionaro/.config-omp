---
name: explore-critique-implement
description: Use when a non-mechanical task has decision uncertainty, meaning two reasonable resolutions materially change work, outcome, consequential risk, or acceptance; also when active ATE routes bounded work here
---

# Explore-Critique-Implement

Separate exploration, authoritative critique, implementation, and independent review. The builder never critiques its own output.

## Activation and invariants

Start only when omp selects ECI or active ATE explicitly routes bounded work through it. Loading this router alone does not start ECI. Use ECI for non-mechanical work with uncertainty, future behavior/routing/protocol risk, or two plausible approaches; classify by decision complexity and risk, not diff size. A one-line/local change is still non-trivial when it changes instructions, prompts, routing, protocols, public contracts, security, persistence, concurrency, architecture, or reviewer/agent behavior. Skip only a mechanical answer whose consequences are obvious, directly verifiable, and carry no future behavior or routing risk.

On activation and closure, the Supervisor follows [native goal lifecycle](references/coordinator.md#native-goal-lifecycle).

- Maintain requirement lineage and a project-understanding ledger. The active ECI/ATE lifecycle owns normal work; ATE may contain normal ECI. Keep forecast values in `forecasts.yaml`; project-understanding may link to it but must not duplicate them.
- Apply [concurrent task scheduling](../../eci-binding-contract.md#concurrent-tasks) to new Jobs, independent progress, outcome-local gates, and root closure. Each outcome keeps its scope and Producer identities; ownership follows the [Job Orchestrator contract](references/task-orchestrator.md).
- For material ECI work, keep `exact user source → faithful requested outcome →
  bounded scope`. A repair necessary to meet or prove that outcome stays current-lane work.
  For each already-discovered lower-priority code issue that remains unfixed, add the TODO defined by [Main ECI quality responsibility](references/fast-path.md#main-eci-quality-responsibility). It does not change an in-scope disposition or replace its required repair; for an unadmitted separate-outcome issue, its TODO is the only code edit and the issue remains post-ECI.
  Missing or stale lineage never blocks known in-scope work.
- Derive interface scope and lifetime from the requested capability, affected entities’ meaning and ownership, contracts, and representative consumers. An immediate example alone does not establish a narrower boundary. Preserve explicitly authorized narrow or staged scope; add no speculative capabilities.
- Apply [coding-policy enforcement](../references/workflow-runtime/coding-style-admission.md) to governed work. For workflow or review-failure repairs, apply [policy failure investigations](../references/review-investigation.md#policy-failure-investigations) before adopting scope, selecting a remedy, or accepting its proof.
- Every admitted outcome starts main-path ECI and one [Fast Owner](references/fast-path.md) concurrently within its home Job. That module owns launch, lifetime, shared-tree priority, evidence feedback, adoption, and closure rules.
- Apply [impact-first provisional delivery](references/fast-path.md#impact-first-provisional-delivery): deliver verified useful improvements over a broken baseline while the full quality and acceptance pipeline continues.
- Jobs may recursively admit bounded authorized child packages under the [recursive Job contract](references/task-orchestrator.md#recursive-child-admission-and-closure). Each child owns the full pipeline and reports through its parent owner; unadmitted separate-outcome restrictions do not prohibit authorized child work.
- Follow the normative [post-Fast completion sequence](references/fast-path.md#post-fast-completion).
- Intermediate iteration progress remains concurrent.
- Record `Stage: normal` for ECI; show main-path and Fast progress within each outcome, not as separate stages or automatic lanes.
- A lane is an independently advancing workstream, not an ECI step. Serial implement→review→repair→review→implement stays one lane with one critical path. Create distinct lanes only for independently advancing work with separate ownership or synchronization.
- Every normal ECI Worker reads this router plus the exact module(s) useful to its assignment. Report an unknown role, predicate, or link to the Supervisor and resolve it while safe bounded assigned work continues; it does not itself deny or stall normal work.
- Enforce ECI duties for your actual activity, including delegated work. When work changes, recheck role authority, model selection, and applicable ECI dependency skills and their required methods under their role/activity predicates. Report significant movements (activity/phase changes, Helper dispatch/completion, material findings or verification) and any unmet duty through the existing report route, with evidence and the next correction. A retained role label or a loaded skill does not establish compliance.
- Own your assigned outcome through successful completion, required evidence, and complete handoff. Resolve recoverable obstacles with safe, authorized in-scope actions; use and verify available recovery paths before asking for help (for example, use authorized ADB recovery to unlock the task device). If no authorized in-scope path is available, or attempted recovery leaves the outcome blocked, report why, what you tried (where possible), and the exact missing input, access, or approval. Keep scope bounded; the Supervisor retains ECI lifecycle and final acceptance.
- The Supervisor alone loads lifecycle, blocker, pause, stop, teardown, and pressure-policy modules. The current Job owner—an Orchestrator—loads shared [review policy](../references/workflow-runtime/review-policy.md) only for outcome-level review dispatch and evidence coordination under the [Job Orchestrator contract](references/task-orchestrator.md). It sends findings, evidence, and disposition recommendations to the Supervisor. The Supervisor retains final adjudication, commit-range verification, acceptance, and protected operations.
- Each normal iteration is Explore → Critique → Implement → parallel Review. A producer never acts as critic.
- Bugs with hard uncertainty or a material competing diagnosis/approach use `debugging-discipline` on either path; retain permitted basic investigation and Fast's deferred main-path sequence.

## Configuration E2E contract

Every configuration change, including configuration-only work, requires the final implementer-owned and fresh independent E2E pair on the stabilized final cumulative state described below. This requirement may not be waived.

## Runtime E2E policy

Code/debug work affecting runtime behavior reachable through a UI, API, device, or CLI requires the final E2E pair below. Docs, prompts, design-only changes, tests-only changes, and pure refactors do not require E2E under this policy.

## E2E cadence, scope, and timing

Each implementation iteration still gets focused tests/proof, a checked scoped Producer commit during handoff, independent Supervisor verification of its immutable range, and independent code review. Do not run routine E2E between iterations. An early E2E is allowed only to investigate a concrete failure or integration uncertainty; run the shortest faithful real-path scenario that can resolve it. Early evidence does not replace final E2E.

For active ECI, this router controls E2E applicability and cadence within ECI iterations. `testing-discipline` still governs focused-check and required-E2E quality, but its generic per-modification E2E default does not add routine E2E between ECI iterations. This ECI-local cadence does not replace separately applicable outer-workflow acceptance evidence, such as ATE root gates.

Once main-path implementation, post-Fast findings/dispositions, and any resulting repairs are complete, run one final pair before acceptance whenever a configuration or runtime trigger applies: the Implementer Producer’s E2E and a fresh independent Step 4 E2E on the same stabilized final cumulative revision. The independent run may repeat or extend the Producer’s run. A later material edit affecting exercised behavior, assertions, or configuration invalidates the affected E2E evidence; refresh it before acceptance.

Use the shortest faithful real path that proves the original criteria and checks relevant regressions. Run the full suite only when it supplies coverage required for that proof. Cite the command and actual output/state/screenshot; proxy evidence alone is insufficient.

Every early, Fast, Producer-final, and independent-final E2E report records `started_at_utc` and `finished_at_utc` (RFC 3339 UTC), `elapsed_monotonic_seconds`, command, scope/coverage, tested revision, and environment identity sufficient for comparison (runner/host class, OS/architecture, relevant tool/runtime versions, and test-service/data configuration). Redact secrets. Compare duration only for matching commands and scope/coverage in materially equivalent environments; do not rerun solely to collect timing. A comparable material regression beyond ordinary variance triggers the Fast Owner Producer or a bounded Helper to profile and optimize E2E duration in parallel while main-path ECI continues. Preserve assertions, coverage, real-path evidence, and the independent final E2E.

## Spawn skill routing

Before any Worker spawn, apply the spawn rule in [Initial admission](references/task-orchestrator.md#publication-transfer-and-report-drain) to the Job, root or child.

Before every spawn or resume, apply this section to every brief, including read-only explorer briefs and follow-up briefs to a reused agent.

Match the task against the [trigger table](../../eci-binding-contract.md#eci-supervisor-repository-edit-routing), the single authority for skill routing. A trigger row yields one unit per positional pair when its trigger cell and skill cell have the same number of slash-separated parts; any other row is one unit. For a Helper spawn or resume, the dispatcher is the spawning Worker. For a Worker or Producer that an Orchestrator spawns or resumes, the dispatcher is that Orchestrator (current Job owner); this clause does not apply to Helpers. For an Orchestrator spawn or resume, the dispatcher is the publisher; for a child Job, the publisher is the parent owner, and the Supervisor sends its follow-ups for that Job through it, not directly to the child owner. The dispatcher fills one Routing line per unit:

- `match: <skill>` names the triggering files or task kind; `no match: <reason>` cites what the unit was judged against.
- A unit whose skill cell names several skills gets one line listing each matched skill as `match: <skill>, <skill>`; its `no match` line gives one reason.
- A unit excluded by a restricted skill whose stated predicate is not met gets `no match: <module predicate not met>`.
- The `Skills:` line lists exactly the `match:` skills, or `none, <reason>` when there are none. The dispatcher derives it from the Routing lines.

For task-backed ECI work, select the role below before Model selection. Presets supply defaults, not lifecycle authority; preserve each brief's assignment, exclusions, skill routing, owner, and review role.

| Assigned role | Preset role |
| --- | --- |
| General Step 1 or BRP primary explorer | `explorer` |
| Step 2 design critic | `critic-step2` |
| Implementer Producer | `implementer` |
| Fast Owner Producer | `fast-owner` |
| General Critic A/B/C | `reviewer` |
| Independent E2E | `independent-e2e` |
| Job Orchestrator | `orchestrator` |
| Job-owner-dispatched non-Producer brainstormer, feasibility validator, loop-breaker, or RCA investigator outside Step 1 / RCA critic | `worker` |
| Task-backed Helper | `helper` |

A `worker` brief supplies a self-contained assignment and role contract, matched skills, exclusions, authority, and reporting to the current published Job owner; a child Job owner reports through its parent owner where applicable. A `worker` authors, stages, and commits no tracked repository change; it keeps probes and notes in disposable scratch space and reports them. No ECI role module is required; never load Supervisor-only coordinator modules for a Worker. Preserve literal specialists where required. A Producer assigned RCA retains its Producer role; Helpers retain the `helper` role and report through their spawning Worker.

The Helper brief supplies its authority, reporting route, matched skills and applicable role module. Select its profile from its own activity, not its parent's. A Helper cannot replace an independent reviewer or its duties.

This table does not replace genuine specialist selection. Continue using literal `scout` for exploratory research when required; preserve specialist tool, spawn, and reporting contracts. Never substitute an unrestricted task-backed preset for a specialist.

## Model selection

At each new spawn, read `~/.omp/agent/eci/model-selection.yaml` and choose the profile by assigned activity. Mixed assignments use the highest applicable profile.

Choose the camp, `anthropic` or `openai`, by role:

| Role | Camp |
| --- | --- |
| Critic, reviewer, design-winner selector, independent E2E, independent feasibility validator, independent loop-breaker, RCA critic | Opposite of the Supervisor's own camp |
| Every other role | The Supervisor's own camp |

The Supervisor's own camp is the provider of the model the Supervisor runs on at spawn time, including after a mid-run fallback: `anthropic` for `anthropic/*`, otherwise `openai`. Any spawning agent, including an Orchestrator or Producer, uses the Supervisor's camp. If the Supervisor's current model is unknown, use its configured model.

Brainstormers and exploratory RCA investigators use the Supervisor's own camp. An independent feasibility validator or loop-breaker is a critic for camp selection but is not automatically a design-winner selector. Select `higher` or `max` according to its actual assigned activity, using the highest applicable profile for mixed assignments.

| Profile | Activities |
| --- | --- |
| `max` | Solution design, meaning the design-winner selection, not exploration; unresolved RCA work; design review, including Step 2; long-term health review, including Critic C. |
| `higher` | Other independent reviews, including Critics A/B, settled RCA review, and independent Step 4 E2E. |
| `default` | Implementation; routine work; producer E2E; explorers, including Step 1 exploration. |

For a task-backed role above, spawn `eci-preset-<role>-<camp>-<profile>` without `model` or `effort`. Its generated ordered model list supplies the selected profile and authorized cross-provider fallback. If the named preset is unavailable or disabled, report the failure rather than substitute another agent. Current-session system and tool policy remains authoritative; existing per-call-pinned workers do not acquire fallback.

Compare the preset's ordered selectors with the selected camp/profile and opposite camp/same profile in the YAML. On a concrete mismatch, do not dispatch the stale preset. When safe, regenerate by the operator route below, review the changes, and retry the intended spawn; otherwise report the exact conflict to the Supervisor and preserve user edits. Do not use a per-spawn build gate. A selector match does not establish the effective retry chain or serving model.

Configured exact-model or provider retry keys can precede the list. If no listed candidate can authenticate, the runtime may use the parent model. Cross-provider fallback can put an independent reviewer in the Supervisor's camp; it does not change reviewer independence. Report actual serving model and fallback when observable; do not infer them from the list. Operator `task.agentModelOverrides` retains precedence.

For literal `scout`, `reviewer`, and `security-reviewer` spawns, preserve the agent's tool, spawn, output, and reporting contracts. Omit per-call `model` and `effort` so its configured agent model or inherited parent model, operator overrides, and retry fallback policy apply. Do not substitute a task-backed preset for a specialist. A current-session system or tool instruction requiring a per-call model takes precedence; such a pinned spawn does not gain automatic fallback.

This path does not dynamically apply the Supervisor's camp or the activity profile: the initial specialist model and effort may differ from the profile, and an independent review may run in the Supervisor's camp. Maintain reviewer independence and blind, self-contained review assignments regardless of provider. Record the assigned profile and this routing limitation in the Job update. Report the actual serving model and fallback when observable; do not infer them from a configured chain.

Effective operator `task.agentModelOverrides`, exact-model and provider retry keys, credentials, enabled providers, and parent-auth fallback may change or prevent the intended chain. A retry is not guaranteed for every error. If the configured route cannot serve the activity, report the exact failure to the Supervisor rather than alter operator settings or silently claim fallback succeeded.

Task-backed presets do not establish named-list fallback for other genuine specialists. For those specialists, retain the existing spawn path from the `task` tool description:

| Path | Condition | Spawn call |
| --- | --- | --- |
| Default | `task.perCallModel` is not `on`, or the `task` description lists no approved models. | Omit `model`. Record `profile <name> not applied: per-call model off or no approved models` in the Job update. |
| Enabled | `task.perCallModel` is `on` and the `task` description lists approved per-call models. | Pass `model: <profile model>:<profile level>`, for example `anthropic/claude-opus-5-5:high`. Use only a listed model id (`provider/model-id`); the listed `:level` does not restrict the profile level. |

The `<profile model>` and `<profile level>` are the `model` and `reasoning_effort` fields under the chosen camp and profile.

- A refusal is any `model` error the harness returns: an ungranted model, a level with no supported effort at or below `task.maxEffort`, or a conflict with `task.agentModelOverrides`.
- Do not pass `effort`; the `:level` suffix carries the profile level.
- A refused spawn fails; the harness never substitutes another model.
- A refused `default` profile spawn on the Enabled path stops. Record the refusal text and report it to the Supervisor. Never respawn the same brief on another model or without a model. The Default path passes no `model` and is not refused.
- After a refused `higher` or `max` spawn, send the refusal and profile to the Supervisor and do not respawn. The Supervisor either chooses a listed model for a new spawn decision or stops the activity. That choice is a new spawn decision, not a retry.
- Keep existing launch settings and reusable owners.
- The Supervisor keeps an open Job's owner under [open-Job owner reuse](references/coordinator.md#open-job-owner-reuse).
- Give an activity with a different profile its own spawn.
- Keep independent reviewer assignments self-contained and blind to producer findings.
- Profile selection does not change role authority or reviewer independence.
- Treat the profile file as a routing reference; do not assume auto-loading or effective-model telemetry.

### Preset deployment

After changing `agent/eci/model-selection.yaml`, preset templates, or generator source, regenerate and review the owned files from the repository root. Commit generated changes together with their source changes:

```sh
(cd agent/eci/tools/eci-presets && go run . -config ../../model-selection.yaml -templates templates -out ../../../agents)
(cd agent/eci/tools/eci-presets && go run . -config ../../model-selection.yaml -templates templates -out ../../../agents --check)
```

The generator preflights owned files, stages output beside each target, and rechecks existing files before replacement or obsolete-file removal. New targets use no-clobber creation. A matching legacy output is upgraded only when byte-identical to the current generated legacy body. These checks are not race-free, and publication of the set is not atomic; on an I/O failure inspect and reconcile partial updates before rerunning. Never delete unverified files to force regeneration.

## Module routing

For bounded host process families needing deadline/interruption cleanup, or exact PID/birth cancellation on Linux, use [owned-command](../owned-command/SKILL.md); keep ordinary foreground inspection such as `rg --files` direct. On Linux a launch keeps cgroup v2 and pidfd custody of the inherited family; `-cancel-pid` there takes an external-pidfd path that covers one observed static-family snapshot, not the whole family. On macOS (`Backend=darwin-pgroup`) it guarantees only deadline and cleanup of the command's own process group: no birth identity (`-cancel-pid` with a positive integer PID returns 125; a value that is not an integer exits 2 at flag parse), no subreaper, and descendants that leave the group (`setsid`/`setpgid`) are not cleaned up.

Supervisor routes begin with [coordinator runtime](../references/workflow-runtime/coordinator-runtime.md). Use [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for governed writing/admission, [review policy](../references/workflow-runtime/review-policy.md) only for review/impact routing, [pause-all-work](../references/workflow-runtime/pause-all-work.md) only for its exact direct-user predicate, [stop recovery](../references/workflow-runtime/stop-recovery.md) only for recognized Stop diagnostics, and [policy pressure tests](../references/workflow-runtime/policy-pressure-tests.md) only for workflow-policy changes.

| Role | Required module | Conditional module/predicate |
| --- | --- | --- |
| Supervisor | [coordinator](references/coordinator.md), [coordinator runtime](../references/workflow-runtime/coordinator-runtime.md), [review policy](../references/workflow-runtime/review-policy.md) | [pause-all-work](../references/workflow-runtime/pause-all-work.md), [stop recovery](../references/workflow-runtime/stop-recovery.md), and [policy pressure tests](../references/workflow-runtime/policy-pressure-tests.md) only by their predicates |
| `explorer` | [explore](references/explore.md) | [debugging-discipline](../debugging-discipline/SKILL.md) only for assigned debugging, test-failure, unexpected-behavior, performance, or build-failure investigation; [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for assigned governed source discovery |
| `worker` | None; the self-contained assignment packet carries the role contract | [debugging-discipline](../debugging-discipline/SKILL.md) only for an assigned RCA investigator or critic. Independent validators, loop-breakers, and RCA critics retain their actual review/BRP responsibilities; brainstormers remain fresh, blind and idea-only. Existing lifecycle, role authority and reporting rules remain authoritative. |
| `critic-step2` | [critique](references/critique.md) | [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for independent admission |
| Producer (`implementer`) | [implement](references/implement.md) | [debugging-discipline](../debugging-discipline/SKILL.md) only for assigned code/debug work; [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for a governed scope |
| Orchestrator | [task-orchestrator](references/task-orchestrator.md) | Candidate, handoff, or relay by bounded assignment; Job dispatch only after its designated publisher publishes ownership under that contract; shared [review policy](../references/workflow-runtime/review-policy.md) only for outcome-level review/evidence |
| `fast-owner` (Fast Owner Producer) | [ECI fast path](references/fast-path.md) | Early E2E only under [E2E cadence, scope, and timing](#e2e-cadence-scope-and-timing) |
| Critic A/B/C, E2E | [review](references/review.md) | [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for reviewed governed scope; E2E follows [E2E cadence, scope, and timing](#e2e-cadence-scope-and-timing). |

## Step handoffs

Before step 1, complete the first-spawn rule in [Spawn skill routing](#spawn-skill-routing).

1. Explorer ranks tagged, evidence-backed options and required PoC/style-source proposal.
2. The current Job owner dispatches a fresh Step 2 critic for the outcome. The critic independently baselines, admits scope, rejects bad options, and selects concrete winner text or returns bounded re-exploration.
3. The assigned Implementer Producer applies only the winner and `treatment: now` corrections with causal/proof evidence, then commits its checked scope during handoff.
Before Step 4 or dependent work on another implementation iteration, the Supervisor independently verifies the Producer’s immutable parent-to-commit range. Reviewers receive that range and explicit exclusions; excluded baseline and later ambient changes remain outside review scope.
4. The current Job owner dispatches fresh A/B/C critics in parallel for every outcome iteration and fresh independent E2E when required; E2E follows [E2E cadence, scope, and timing](#e2e-cadence-scope-and-timing). The Supervisor retains final impact/treatment/disposition adjudication and acceptance. Substantive findings return as one design batch; contained fixes return once to implementation. Clean pass needs every original criterion, required proof, and no remaining `now` issue.

## Relationship to other skills

| Skill | Relationship |
| --- | --- |
| `brainstorming` | Explores user intent before design; ECI explores solutions after intent is clear. |
| `agent-teams-execution` | Starts only on explicit user request; once active, may route bounded work through ECI. Re-spawn an ECI critic that cites no issues beyond producer self-reports. |
| `blocker-resolution-protocol` | Supplies shared blocker records and escalation rules. ECI retains role separation, loop-breaker, and hard-escalation semantics. |
| `debugging-discipline` | Diagnoses known bugs; ECI explores open-ended improvement/design. |

Maintenance provenance: [coverage map](references/coverage-map.md).

## Red flags

- A worker loads coordinator/blocker/pause/stop/teardown/review-runtime/pressure policy without explicit assignment.
- A producer reviews itself, a blind critic reuses context, or review critics run sequentially.
- A substantive gate finding is patched directly instead of returning through Explore/Critique.
- A lifecycle is closed to bypass routing or acceptance.

## After compaction

After each ECI compaction, refresh the structured YAML status report and print a proper status report to the user using [writing-status-reports](../writing-status-reports/SKILL.md). Compaction alone does not trigger queue-item critique. Resume safe work after reporting.
