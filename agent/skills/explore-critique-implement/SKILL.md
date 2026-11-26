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
  bounded scope`. A repair necessary to meet or prove that outcome, including [dependency work](../../eci-binding-contract.md#eci-scope-admission) in another component, stays current-lane work.
  For each already-discovered lower-priority code issue that remains unfixed, add the TODO defined by [Main ECI quality responsibility](references/fast-path.md#main-eci-quality-responsibility). It does not change an in-scope disposition or replace its required repair; for an unadmitted separate-outcome issue, its TODO is the only code edit and the issue remains post-ECI.
  Apply [Recording and citing user requirements](../context-ledger/SKILL.md#recording-and-citing-user-requirements). Known in-scope work never awaits missing or stale lineage.
- Derive interface scope and lifetime from the requested capability, affected entities’ meaning and ownership, contracts, and representative consumers. Per [User requirements](../../eci-binding-contract.md#user-requirements), an immediate example alone does not establish a narrower boundary. Preserve explicitly authorized narrow or staged scope; add no speculative capabilities.
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
- Own your assigned outcome through successful completion, required evidence, and complete handoff. Resolve recoverable obstacles with safe, authorized in-scope actions; use and verify available recovery paths before asking for help (for example, use authorized ADB recovery to unlock the task device). If no authorized in-scope path is available, or attempted recovery leaves the outcome stuck, report the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires and the exact missing input, access, or approval. Keep scope bounded; the Supervisor retains ECI lifecycle and final acceptance.
- Do only your role's duties; route another role's duty to that role. Explorers propose options, a Step 2 selection decides the design, Producers implement it and route evidence against it to a fresh Step 2 critic, and reviewers report findings; provisional Fast work follows [ECI fast path](references/fast-path.md).
- The Supervisor and Orchestrators delegate Job work: an Orchestrator owns its Job's progress, and the Supervisor itself enforces ECI across Jobs. Design selection, [finding decisions](#step-handoffs), and implementation choices stay with Steps 1–2, Step 2 critics, and Producers, except a recorded [stall decision](references/coordinator.md#stall-decisions). On a round without a finding critic, finding decisions follow reviewers' stated scope under [Step 4](#step-handoffs). A brief, relay, or user update presents design content as a Job's plan, target, constraint, or acceptance criterion only from its sources, unchanged and attributed: the user's words and any content the user chooses from a passage those words refer to, a Step 2 critic's full output cited as authoritative, a review finding in its reviewer's words, and a recorded stall decision cited as its decider's. A brief or relay cites that content under the Sourced constraints rule in `~/.omp/agent/RULES.md`. A user update quotes or cites it at its source, or summarizes it with its author. Their own observation, concern, or risk or impact remark may appear in any message, including user updates, attributed as theirs, tagged `[INFERENCE]` where unchecked, and presented as input, never as a decision or as another role's content; their own design idea goes to the Job's Steps 1–2 as such input, never into a brief's targets or criteria. When they see a technical mistake in a Job's work, they promptly notify the Worker whose work it concerns, before any dependent action such as its commit, and the owner or fresh Step 2 critic whose decision it affects (for a child Job, through its parent owner); the notice states the mistake and its source, and the receiver decides the remedy. Any other report, including another coordinator's, informs a Job's design only as evidence for Steps 1–2. This bullet does not govern a direct answer to the user's advice request.
- Within your role's duties, decide among authorized, in-scope technical alternatives, including approach and forecast tradeoffs, from available evidence; proceed with the most defensible choice and report material decisions and assumptions. Required ECI roles, steps, and paths are not alternatives; run or queue each one. Do not present an authorized technical choice to the user for a decision, including in prose; answer direct advice requests as requested. Preserve requirements, safety constraints, explicit user choices and reservations, such as a set order, and authorization boundaries, even when you see a better option; report that option through your report route. In a user-set order, make no change that belongs to a step unless every step before it in the order is done and the state that the earlier steps leave, applied in order, still holds. Read each step by its plain meaning; never narrow it to fit a better option, such as a shorter outage. Never drop a required capability or narrow scope without approval. Route each decision outside your role, missing factual input, and missing approval or authorization under the User questions rule in `~/.omp/agent/RULES.md`, with the proof the Stuck work rule in `~/.omp/agent/RULES.md` requires for any stuck claim, and continue independent authorized work.
- The Supervisor alone loads lifecycle, stuck-work, pause, stop, teardown, and pressure-policy modules. The current Job owner—an Orchestrator—loads shared [review policy](../references/workflow-runtime/review-policy.md) only for Job-level review dispatch and evidence coordination under the [Job Orchestrator contract](references/task-orchestrator.md). It sends findings under [Step 4](#step-handoffs) and evidence to the Supervisor. The Supervisor retains acceptance and protected operations.
- Bugs with hard uncertainty or a material competing diagnosis/approach use `debugging-discipline` on either path; retain permitted basic investigation and Fast's deferred main-path sequence.

## Iteration rule

ECI means “implement everything, then test” in every iteration. A normal iteration is one pass of the implementation–critique loop: Explore → Critique → Implement → parallel Review over all the Job's remaining work, including all its outcomes. A repair-only iteration is defined below.

- Step 2 designs all of it. Step 3 implements all of it, including every routed `now` treatment, as one piece, then tests it as one piece. Each Producer applies the [batch procedure](references/implement.md#test-and-debugging-discipline) to its complete part without waiting for another Producer's tests.
- On the main path, never fix and test pending defects one at a time, even where a loaded skill loops per item. A Fast Owner keeps its per-issue checkpoints.
- Any Producer may fix any defect in any part without its author's approval, resolving concurrent-write conflicts under [Commits](references/task-orchestrator.md#commits).
- Diagnosis before a fix, parallel assignments, per-fix or WIP commits, Helper results, and each Producer's checks of its complete part are ordinary Step 3 progress, not iteration boundaries.
- Step 4 starts only after every Step 3 assignment is handed off; the last handoff carries the iteration's focused proof over all parts. That proof and Step 4 review cover the Job's work, skipping only work that earlier tests and reviews cover and this iteration does not affect.
- Later iterations start only for finding-driven repairs, the post-Fast restart, and later-admitted or corrected scope of this Job, which joins its next Steps 1–2. A repair-only iteration (a finding-driven iteration with only a [repair batch](../references/workflow-runtime/review-policy.md#gate-evaluation)) starts at Step 3. Never split the Job's main path into separately designed, implemented, tested, or reviewed stages or per-fix rounds.

## Test efficiency

When the [Tests trigger](../../eci-binding-contract.md#eci-supervisor-repository-edit-routing) matches, read [testing-discipline](../testing-discipline/SKILL.md) in its own `read` call. Recover missing or elided text and recheck applicability when work changes. Apply **Efficient tests** and **Speed-review record** to focused checks, combined iteration proof, Fast checks, and applicable E2E.

For each suite used as proof, cite its command and elapsed time in the existing handoff or review. Include build, setup and waits the suite invokes; exclude external queue time and unrelated phases. If the available timing cannot isolate the suite, report the uncertainty. A speed review or record is test upkeep: do it when cheap, otherwise report it as a follow-up; it never holds delivery. A prod performance regression stays a product defect.

## Configuration E2E contract

Every configuration change, including configuration-only work, requires the final implementer-owned and fresh independent E2E pair on the stabilized final cumulative state described below. Only a user override can waive this requirement.

## Runtime E2E policy

Code/debug work affecting runtime behavior reachable through a UI, API, device, or CLI requires the final E2E pair below. Docs, prompts, design-only changes, tests-only changes, and pure refactors do not require E2E under this policy.

## E2E cadence, scope, and timing

Each iteration gets focused tests/proof and independent code review under the [iteration rule](#iteration-rule). Producers commit their checked work whenever they choose, optionally one commit per fix, and report the ranges in their handoff. Do not run routine E2E between iterations. An early E2E is allowed only to investigate a concrete failure or integration uncertainty; run the shortest faithful real-path scenario that can resolve it. Early evidence does not replace final E2E.

For active ECI, this router controls E2E applicability and cadence within ECI iterations. `testing-discipline` still governs focused-check and required-E2E quality. This ECI-local cadence does not replace separately applicable outer-workflow acceptance evidence, such as ATE root gates.

Once main-path implementation, post-Fast findings/dispositions, and any resulting repairs are complete, run one final pair before acceptance whenever a configuration or runtime trigger applies: the Implementer Producer’s E2E and a fresh independent Step 4 E2E on the same stabilized final cumulative revision. The independent run may repeat or extend the Producer’s run. A later material edit affecting exercised behavior, assertions, or configuration invalidates the affected E2E evidence; refresh it before acceptance.

Use the shortest faithful real path that proves the original criteria and checks relevant regressions. Run the full suite only when it supplies coverage required for that proof. Cite the command and actual output/state/screenshot; proxy evidence alone is insufficient. Manual execution of the required real path counts as E2E; continue after nondiagnostic failures under [Prod-code focus](../../eci-binding-contract.md#prod-code-focus).

Share each product's E2E build from its [SSOT checkout and branch](../../eci-binding-contract.md#git). Job owners report each due scenario and its product to the Supervisor through their normal report route as soon as it is due. Whenever a scenario is due and no build matching the product's current SSOT inputs and build configuration is underway or available, the Supervisor starts or assigns one immediately. Keep the matching artifact available for later scenarios. Route each ready scenario to its Job owner as soon as the matching artifact is available, including later arrivals. Never wait to collect tests or synchronize Jobs. An [execution constraint](../../eci-binding-contract.md#concurrent-tasks), such as exclusive use of a shared test device, may delay only the scenarios it blocks. Keep each Job's assigned runners and separate results by Job, outcome, criterion, scenario, and role. Each runner verifies the artifact and configuration actually exercised; credit only observations that match the required build and scenario.

Every early, Fast, Producer-final, and independent-final E2E report records `started_at_utc` and `finished_at_utc` (RFC 3339 UTC), `elapsed_monotonic_seconds`, command, scope/coverage, tested revision, and environment identity sufficient for comparison (runner/host class, OS/architecture, relevant tool/runtime versions, and test-service/data configuration). Redact secrets. Compare duration only for matching commands and scope/coverage in materially equivalent environments; do not rerun solely to collect timing. A comparable material slowdown that leaves proof faithful is test upkeep under [Test efficiency](#test-efficiency); if it prevents faithful proof, repair or replace the proof without reducing required observations. Timing alone establishes no product performance regression; route an established one as a product finding. Preserve assertions, coverage, real-path evidence, and the independent final E2E.

## Spawn skill routing

Before any Worker spawn, apply the spawn rule in [Initial admission](references/task-orchestrator.md#publication-transfer-and-report-drain) to the Job, root or child.

Before every spawn or resume, apply this section to every brief, including read-only explorer briefs and follow-up briefs to a reused agent.

Match the task against the [trigger table](../../eci-binding-contract.md#eci-supervisor-repository-edit-routing), the single authority for skill routing. A trigger row yields one unit per positional pair when its trigger cell and skill cell have the same number of slash-separated parts; any other row is one unit. For a Helper spawn or resume, the dispatcher is the spawning Worker. For a Worker or Producer that an Orchestrator spawns or resumes, the dispatcher is that Orchestrator (current Job owner); this clause does not apply to Helpers. For an Orchestrator spawn or resume, the dispatcher is the publisher; for a child Job, the publisher is the parent owner, and the Supervisor sends its follow-ups for that Job through it, not directly to the child owner. The dispatcher fills one Routing line per unit:

- `match: <skill> [Ref N]` names the triggering files or task kind, and its entry quotes the trigger-table row; `no match: <reason>` cites what the unit was judged against.
- A unit whose skill cell names several skills gets one line listing each matched skill as `match: <skill>, <skill> [Ref N]`; its `no match` line gives one reason.
- A unit excluded by a restricted skill whose stated predicate is not met gets `no match: <module predicate not met>`.
- The `Skills:` line lists exactly the `match:` skills with their markers, or `none, <reason>` when there are none. The dispatcher derives it from the Routing lines.
- Check the brief as sent, including shared context and per-Worker text. Reviewing suite proof matches the testing-discipline trigger even in a read-only review with no test edits. If you miss a match, supply the skill to the Worker and refresh any affected evidence.

For task-backed ECI work, select the role below before Model selection. Presets supply defaults, not lifecycle authority; preserve each brief's assignment, exclusions, skill routing, owner, and review role.

| Assigned role | Preset role |
| --- | --- |
| General Step 1 or SWR primary explorer | `explorer` |
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
(cd agent/eci/hooks/lib/eci-hook-helper-go && go test ./codingskill)
```

That test binds template skill URIs to the coding-skill gate's `roleSkills` and the coding-skill row of `agent/eci/hooks/gate-escape-hatches.md` to the gate's instruction targets, and checks every gated skill exists and fits one read. Also run it after editing that row, or after renaming or moving this router, a role module, coding-common, coding-go, coding-python, or harness-tuning. After committing a `codingskill` change, create and commit the gate's disable switch `agent/coding-skill-gate.disabled` unless already committed. Then rebuild with `bash agent/eci/hooks/build-native-binaries.sh eci-hook-helper` while `git status -suall agent/eci/hooks/lib/eci-hook-helper-go` prints nothing: the build compiles the working tree, and every running omp session uses the new helper at once. Remove the switch with `git rm` and commit only after the ECI Supervisor or the user confirms that no coding-skill gate change awaits review.

The generator preflights owned files, stages output beside each target, and rechecks existing files before replacement or obsolete-file removal. New targets use no-clobber creation. These checks are not race-free, and publication of the set is not atomic; on an I/O failure inspect and reconcile partial updates before rerunning. Never delete unverified files to force regeneration.

## Module routing

For bounded host process families needing deadline/interruption cleanup, or exact PID/birth cancellation on Linux, use [owned-command](../owned-command/SKILL.md); keep ordinary foreground inspection such as `rg --files` direct. On Linux a launch keeps cgroup v2 and pidfd custody of the inherited family; `-cancel-pid` there takes an external-pidfd path that covers one observed static-family snapshot, not the whole family. On macOS (`Backend=darwin-pgroup`) it guarantees only deadline and cleanup of the command's own process group: no birth identity (`-cancel-pid` with a positive integer PID returns 125; a value that is not an integer exits 2 at flag parse), no subreaper, and descendants that leave the group (`setsid`/`setpgid`) are not cleaned up.

Supervisor routes begin with [coordinator runtime](../references/workflow-runtime/coordinator-runtime.md). Use [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for governed writing/admission, [review policy](../references/workflow-runtime/review-policy.md) only for review/impact routing, [pause-all-work](../references/workflow-runtime/pause-all-work.md) only for its exact direct-user predicate, [stop recovery](../references/workflow-runtime/stop-recovery.md) only for recognized Stop diagnostics, and [policy pressure tests](../references/workflow-runtime/policy-pressure-tests.md) only for workflow-policy changes.

| Role | Required module | Conditional module/predicate |
| --- | --- | --- |
| Supervisor | [coordinator](references/coordinator.md), [coordinator runtime](../references/workflow-runtime/coordinator-runtime.md), [review policy](../references/workflow-runtime/review-policy.md) | [pause-all-work](../references/workflow-runtime/pause-all-work.md), [stop recovery](../references/workflow-runtime/stop-recovery.md), and [policy pressure tests](../references/workflow-runtime/policy-pressure-tests.md) only by their predicates |
| `explorer` | [explore](references/explore.md) | [debugging-discipline](../debugging-discipline/SKILL.md) only for assigned debugging, test-failure, unexpected-behavior, performance, or build-failure investigation; [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for assigned governed source discovery |
| `worker` | None; the self-contained assignment packet carries the role contract | [debugging-discipline](../debugging-discipline/SKILL.md) only for an assigned RCA investigator or critic. Independent validators, loop-breakers, and RCA critics retain their actual review/SWR responsibilities; brainstormers remain fresh, blind and idea-only. Existing lifecycle, role authority and reporting rules remain authoritative. |
| `critic-step2` | [critique](references/critique.md) | [review policy](../references/workflow-runtime/review-policy.md) for scope, impact, and treatment decisions; [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for independent admission |
| Producer (`implementer`) | [implement](references/implement.md) | [debugging-discipline](../debugging-discipline/SKILL.md) only for assigned code/debug work; [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for a governed scope |
| Orchestrator | [task-orchestrator](references/task-orchestrator.md) | Candidate, handoff, or relay by bounded assignment; Job dispatch only after its designated publisher publishes ownership under that contract; shared [review policy](../references/workflow-runtime/review-policy.md) only for Job-level review/evidence |
| `fast-owner` (Fast Owner Producer) | [ECI fast path](references/fast-path.md) | Early E2E only under [E2E cadence, scope, and timing](#e2e-cadence-scope-and-timing) |
| Critic A/B/C, E2E | [review](references/review.md) | [coding-style admission](../references/workflow-runtime/coding-style-admission.md) only for reviewed governed scope; [review policy](../references/workflow-runtime/review-policy.md) only for finding classification and each CONDITIONAL's scope; E2E follows [E2E cadence, scope, and timing](#e2e-cadence-scope-and-timing). |

## Step handoffs

Before step 1, complete the first-spawn rule in [Spawn skill routing](#spawn-skill-routing).

1. Explorer ranks tagged, evidence-backed options and required PoC/style-source proposal.
2. The current Job owner dispatches a fresh Step 2 critic for each iteration that runs Step 2. The critic independently baselines, admits scope, rejects bad options, and selects concrete winner text or returns bounded re-exploration. The Job owner sends a Producer's evidence against the selected design, Fast reports included, at once to the running Step 2 critic, or dispatches a fresh one when none is running.
3. The assigned Implementer Producer applies only the winner and `treatment: now` corrections with causal/proof evidence, then hands off with its checked work committed and its ranges reported.
Reviewers receive the Producers' reported ranges in the iteration's review scope and explicit exclusions; excluded baseline and later ambient changes remain outside review scope.
4. The current Job owner dispatches fresh A/B/C critics in parallel for every iteration, with each brief naming its lane as [review](references/review.md) defines it (Critic A coding requirements, Critic B correctness and fidelity, Critic C long-term health), and fresh independent E2E when required; E2E follows [E2E cadence, scope, and timing](#e2e-cadence-scope-and-timing).
   An E2E incident or missing observation alone is not a REJECT. Return incomplete evidence for collection; do not accept it. Route an observed product defect on its merits. When any report, E2E included, has a REJECT finding, it sends every finding, unchanged with its report, to a fresh Step 2 critic. That critic makes the finding decisions under [critique](references/critique.md#admission-and-treatment): each finding's scope, impact, and [treatment](../references/workflow-runtime/review-policy.md#gate-evaluation), and each in-scope inventory item's disposition. The critic gives the `open decision` treatment to each finding whose decision its role cannot own.
   When no report has one (a round without a finding critic), these rules replace the critic:

   | Item | Takes |
   | --- | --- |
   | CONDITIONAL its reviewer states as in scope | Treatment `now` in the repair batch, with its reviewer's fix direction as correction text |
   | CONDITIONAL its reviewer states as a follow-up | Treatment post-ECI observation/follow-up |
   | NIT or PASS | Treatment `no change` with reason "no rejection" |
   | In-scope inventory item | Its latest Step 2 critic's disposition |

   Before sending the unchanged reports to the Supervisor as the iteration's gate evidence, the Job owner asks a CONDITIONAL's reviewer to add a missing scope to its report.
   On a round without a finding critic, the Job owner sends a gap the Supervisor returns in a reviewer's statement or evidence to that reviewer, and reapplies these rules to a gap from a misapplied rule; no critic runs for either.
   The Job owner dispatches the batches as decided, without Supervisor confirmation: [redesign](../references/workflow-runtime/review-policy.md#gate-evaluation) findings return as one design batch, joined by any repair batch; a repair batch alone returns once to implementation.
   It sends each `open decision` to its owning role, the Supervisor for any with no other owner; that role returns its decision, or the user's quoted answer, to the Job owner for a fresh Step 2 critic. The Supervisor enforces ECI and retains acceptance. Clean pass needs every original criterion, required proof, and no remaining `now` issue.

## Relationship to other skills

| Skill | Relationship |
| --- | --- |
| `brainstorming` | Explores user intent before design; ECI explores solutions after intent is clear. |
| `agent-teams-execution` | Starts only on explicit user request; once active, may route bounded work through ECI. Re-spawn an ECI critic that cites no issues beyond producer self-reports. |
| `stuck-work-resolution` | Supplies shared stuck records and escalation rules. ECI retains role separation, loop-breaker, and hard-escalation semantics. |
| `debugging-discipline` | Diagnoses known bugs; ECI explores open-ended improvement/design. |

Maintenance provenance: [coverage map](references/coverage-map.md).

## Red flags

- A worker loads coordinator/stuck-work/pause/stop/teardown/review-runtime/pressure policy that neither its Module routing row nor its assignment routes.
- A producer reviews itself, a blind critic reuses context, or review critics run sequentially.
- A redesign gate finding is patched directly instead of returning through Explore/Critique.
- A lifecycle is closed to bypass routing or acceptance.

## After compaction

After each ECI compaction, refresh the structured YAML status report and print a proper status report to the user using [writing-status-reports](../writing-status-reports/SKILL.md). Compaction alone does not trigger queue-item critique. Resume safe work after reporting.
