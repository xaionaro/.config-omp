# ECI fast path

This is the normative contract for the Fast Producer and its integration with main ECI. Each role follows its assigned duties; the Supervisor retains lifecycle and acceptance authority.

## Start and lifetime

- Start one Fast Producer alongside Step 1 for every new ECI task, including bounded ECI under ATE. Neither path waits for the other's solution.
- Additive extensions update the existing owner's scope. A distinct new ECI task gets its own owner; iterations, reviews, repairs, and re-exploration reuse that identity.
- Preserve the root workflow and lifecycle. Direct work and ATE outside nested ECI do not acquire this protocol by loading it.
- Assign original requirements, authorized targets, dirty-work exclusions, current ECI decisions, verification, and shared-tree rules. The Fast Producer owns its whole assigned outcome, including a child-Job outcome, not a lane; it is distinct from the main-path Implementer and all independent critics. Completion and post-Fast rules apply independently to every assigned outcome.
- Task-local dispatch and reporting follow the [task-orchestrator contract](task-orchestrator.md); the Fast Producer remains one Producer within its assigned outcome.
- Follow [outcome continuation and completion](#outcome-continuation-and-completion) through task-appropriate execution, verification, and the Fast Producer's scoped commits. Milestones do not shorten this ownership.
- Reserve launch capacity for both paths. If constrained, name the action lacking capacity, queue it for the next slot, and report actual concurrency. Retain Fast ownership and continue independent ready work; main process or review capacity alone does not suspend Fast execution. Do not merge producer/reviewer identities or add compensating artifacts.

## Outcome continuation and completion

- Advance the next authorized action until task-appropriate evidence demonstrates the whole requested outcome, such as implementing features, modifying and validating configuration, diagnosing and repairing defects, or delivering analysis. Use progress messages for audits, milestones, helper results, and partial work; continue working instead of returning a final handoff at those boundaries.
- Main ECI explores and reviews concurrently. A pending main design or review alone does not block provisional Fast work. Preserve current ECI decisions and yield only actual conflicting writes under [shared-tree priority](#shared-tree-and-eci-priority).
- The current Job owner checks incomplete reports against the remaining outcome. If the Fast Producer has returned early, immediately write to `agent://<id>` to resume that retained Worker with the next action. A promise to route later, a retained owner label, or `send_message` to an idle Worker does not start work. Preserve the published report route and existing active assignments; avoid duplicate dispatch.
- Apply [active Job compliance](task-orchestrator.md#active-job-compliance) to Fast progress, activity changes, and completion reports, including its Helpers. Fast's deferred main-path sequence does not waive the duties required by the work it actually performs.
- If later normal-path evidence disproves a required result claimed complete, the current Job owner must reopen provisional Fast completion and immediately write to `agent://<id>` for the retained idle Fast Worker with the evidence, unmet criterion, and next bounded action. For an already-active Worker, send that bounded update with `send_message`; preserve its active assignment and avoid duplicate dispatch. Preserve authorized scope, main-path write priority, and independent progress. The old done claim does not satisfy the unmet criterion; unrelated findings do not reopen completion.
- For a concrete dependency, capacity constraint, or write conflict, name the affected action, dependency owner, and resume condition. Continue unaffected ready work and resume the blocked action when the condition clears. A blocked commit remains unfinished work; preserve the authorized owner and resolve its bounded route. Explicit user cancellation follows normal closure.
- Genuine Fast completion requires evidence that the original requested result is provisionally achieved, Helper work is integrated, required Producer verification passes, and the Fast Producer has committed each completed, checked Fast-produced issue within its exact scope. Analysis or other outcomes with no file changes need appropriate evidence, not empty commits. Unmet criteria, unfinished execution, missing verification, and pending scoped commits are incomplete.
- Then finish delegated Fast work and stop the Fast Producer's and its Helpers' write-capable tools, deliver the findings, changes, verification, and commit references, and keep the reusable Worker idle for [post-Fast completion](#post-fast-completion). Main-path work continues. Independent review and final acceptance remain Supervisor-owned; reactivation follows the continuation rule above.

Use the [continuation pressure cases](fast-continuation-cases.md) to check these boundaries.

## Impact-first provisional delivery

- Prioritize actual user benefit within authorized scope. Compare the candidate's verified behavior with the current broken baseline, including the effect of known defects. When it provides a useful improvement, deliver it promptly even if it remains defective or incomplete. Pending final design, review, E2E, or acceptance gates alone do not justify withholding that verified improvement.
- Deliver through the authorized form: checked scoped checkpoint, usable artifact, demonstrated result, or already-authorized rollout. State the working gains, focused verification, known defects, limitations, and remaining work explicitly. A checkpoint alone does not establish usability; demonstrate the benefit appropriate to the task. External publishing or deployment still requires its existing authorization.
- Provisional delivery neither completes Fast nor accepts the outcome. Continue remaining original requirements, required checks, main ECI quality work, independent reviews, and refinement. Keep the lifecycle and ownership active until ordinary closure. Do not silently waive criteria or substitute a defective candidate for the final selected design.
- An unsupported gain or a candidate whose defects eliminate its benefit does not establish an improvement. Verify or revise that candidate while independent useful work continues. Preserve explicit constraints and existing safeguards; impact priority grants no broader scope.
- Apply the canonical [security follow-up rule](../../../../eci-binding-contract.md#decisions) regardless of severity or generic defer eligibility. Record findings explicitly.

## Progress waits

Use either path's available results once independently verified and the next action's dependencies are satisfied. Independent work in the other path does not block intermediate progress. Normal-path completion requires the [post-Fast sequence](#post-fast-completion). Apply the [eci-binding-contract.md dependency scheduling rule](../../../../eci-binding-contract.md#concurrent-tasks) across independent tasks, including ECI nested under ATE. Preserve [required review and E2E aggregation](coordinator.md#step-4--review-coordination) and the [write-yield, final-acceptance, and closure boundaries](#adoption-review-and-closure).

## Fast solving and delegation

- Track the assigned outcome as achievable milestones in existing task tracking. Extend them only for in-scope work; report out-of-scope findings without expanding authorization. Keep milestone status, verification evidence, and checkpoints current. Delegated work need not be organized into lanes; delegation alone does not create a lane.
- Prioritize in-scope paths under [Main ECI quality responsibility](#main-eci-quality-responsibility).
- Own the whole assigned outcome while delegating any bounded, in-scope work that can accelerate it. Delegation is highly encouraged. Helpers may advance different lanes or other work; delegation does not transfer outcome accountability or create a separate outcome. Assign clear scope and expected evidence, integrate Helper results, and account for every delegated finding and change.
- Choose each Helper's model and reasoning effort under [Model selection](../SKILL.md#model-selection) for its assigned activity, independently of the Fast Producer's profile.
- Personally advance other bounded work and validate the provisional Fast result with focused checks, with or without Helpers. Run an early E2E only for a concrete failure or integration uncertainty under the [central ECI E2E policy](../SKILL.md#e2e-cadence-scope-and-timing). Iterate without waiting for main ECI Steps 1–4.
- Analysis-only requests authorize analysis and evidence only. Preserve secret-handling, destructive-action, external-mutation, unrelated-dirty-work, and target-reread safeguards. Speed grants no broader or irreversible authority.
- Defer the main path's design/style/TDD/review sequence for provisional fast work. Run useful focused checks; do not run routine E2E between iterations. Any early E2E follows the central policy's shortest-faithful-scope and timing rules. If an early E2E is needed but unavailable, report the missing resource and attempted evidence; never equate proxy checks with E2E.
- Promptly send actionable discoveries, failed assumptions, observed behavior, tradeoffs, touched targets, and verification limits to the task's current Job owner under the [task-orchestrator handoff rules](task-orchestrator.md#publication-transfer-and-report-drain), for Explorer and Step 2.

Fast Producer E2E follows the [central ECI policy](../SKILL.md#e2e-cadence-scope-and-timing), including per-run timestamps, comparable-run regression checks, and parallel optimization when a material regression appears.

## Shared tree and ECI priority

- Both paths use the same checkout and live files. Do not copy the project, branch, stash, or create worktrees to avoid conflicts.
- Apply the selected ECI design across both paths. Within one outcome, the main-path Implementer has priority over the Fast Producer for overlapping work. Across outcomes, no automatic main/Fast priority applies. Protect unrelated user and other-Job work.
- Before each write, reread the current target and edit narrowly. If concurrent changes invalidate the edit, reread and adapt; never force a stale whole-file replacement.
- When Producers need an overlapping target, they compare intended and actual changes and directly decide whether to separate the work or combine inseparable contributions. They agree who stages and commits when under [shared-index coordination](task-orchestrator.md#active-writer-rows-and-shared-index-coordination). Apply the within-outcome precedence above; across outcomes or Jobs, neither path gets automatic priority. The Supervisor and any published Orchestrator relay exact identities and scope/index facts only; they do not allocate contested content or Git turns. Producers proceed from their agreement without waiting for user direction. Continue unaffected work; ECI does not wait for the Fast result.
- Attribute results to the state exercised. A relevant concurrent change makes affected results stale; rerun them.

## Main ECI quality responsibility

Main ECI establishes design and quality from original requirements and applicable standards. Treat existing Fast work as a candidate implementation, never as acceptance or an authoritative design premise. Its presence, checkpoint, sunk cost, deadline pressure, or passing tests alone does not settle quality or restrict exploration to polishing it.

Evaluate viable alternatives within authorized scope for correctness, maintainability, architecture, and applicable style. Explorer develops options; Step 2 independently selects the winner; implementation meets it; Step 4 applies the same quality scrutiny to retained Fast work and main-path changes.

Apply the ordered [engineering priorities](../../../../eci-binding-contract.md#engineering-priorities) when comparing improvements; retain impact-first delivery and security follow-ups.

Preserve useful verified discoveries, still-valid tests, and qualifying code in place. Revise or replace material deficiencies through normal ECI. Fast provenance or cosmetic preference alone does not justify a rewrite.

After scope-screening, rank ready in-scope options and repair directions by impact points on a declared shared scale divided by maximum estimated remaining work minutes. The ratio compares expected benefit per unit of remaining work. Base impact points on expected useful improvement to the requested outcome over the current baseline; state the score and estimate bases. Use labeled evidence-backed guesses for missing inputs or leave the item unscored with a reason. Elapsed duration is not comparable to work effort; zero work minutes produces no finite priority. Priority labels group forecasts only; they do not rank work. Use the [canonical ECI scenario forecast method](../../context-ledger/SKILL.md#eci-scenario-forecasts) and current milestone or dependency evidence for time estimates. Estimate bounded execution packages under [Work estimates](../../../../eci-binding-contract.md#work-estimates); a finding alone creates no scope or estimate. Apply [provisional delivery](#impact-first-provisional-delivery) while quality work continues. Unadmitted separate-outcome concerns stay post-ECI; original requirements, necessary repairs, checks, proof, and final acceptance gates remain required.

After scope-screening and priority ranking, maintain one concise, actionable `TODO:` comment beside the relevant code for each already-discovered code issue below the current top-priority direction that remains unfixed. Reuse an existing TODO if it covers the issue, update it if stale or incomplete, and add one only if absent. State the observed issue and known fix direction; do not search for or investigate further issues to populate TODOs. A TODO records follow-up only: it changes no scope, priority, `now` or blocker status, acceptance, deferral eligibility, or in-scope disposition, and never replaces a required in-scope repair.

An unadmitted separate-outcome issue remains post-ECI; its TODO is the only code change directed at that issue. Do not investigate or repair it, or create a separate issue-specific lane, blocker, deadline, forecast, proof, or review. Remove the TODO when the issue is fixed.

Reviewers report only. The assigned Implementer authors the TODO within the ordinary implementation, full current-diff review, independent verification, and checkpoint flow; review the complete diff, not only the TODO hunk. If no current assignment covers it, the published Job owner routes a bounded TODO-only Implementer assignment under [repository-edit routing](coordinator.md#engage-and-route), and the Supervisor independently verifies the Producer's checkpoint. The Supervisor authors no new tracked contribution; its self-edit hatch changes routing only and grants no staging, index-mutation, or commit authority.

## Evidence can reopen design

- The current Job owner independently verifies material Fast discoveries and gives the evidence to Explorer and fresh Step 2. The Supervisor retains cross-Job decisions and final acceptance.
- When evidence invalidates a selected premise, obsoletes the design, or demonstrates a materially better in-scope approach, the current Job owner returns affected work to Steps 1–2. Suspend only dependent implementation/review; keep independent work moving. The Supervisor retains cross-Job decisions.
- The selected ECI design controls quality, and the within-outcome precedence above controls overlapping main/Fast edits. Neither rule dismisses contradictory evidence. The Fast Producer proposes alternatives; Step 2 selects the authoritative winner.
- Route minor compatible corrections through the contained-fix path. Unsupported preferences and ordinary fast edits do not restart design.

## Adoption, review, and closure

- Report fast results as provisional with exact evidence and limits. Fast E2E is producer evidence, not acceptance or independent review.
- After each changed Fast issue is complete and checked, the Fast Producer makes its own exact scoped checkpoint commit, including integrated Helper changes. Keep distinct Fast issues in separate checkpoints; only contributing Producers may agree that inseparable work shares one joint checkpoint. Commit each issue promptly; do not wait for remaining milestones, main-path adoption, or Step 4. Outcomes without file changes need evidence, not empty commits.
- Before staging or committing, follow [shared-index coordination](task-orchestrator.md#active-writer-rows-and-shared-index-coordination). Stage only owned paths or hunks, inspect the complete staged result against the intended parent and exact agreed scope, preserve unrelated work, and include a concise `Test Plan`.
- Send the commit ID, immutable parent-to-commit range, contribution scope, exclusions, focused checks, and limitations through the published Job owner to the Supervisor. Before dependent review or work, the Supervisor independently verifies that exact range. A Producer checkpoint is provisional evidence, not acceptance.
- The main-path Implementer inspects retained Fast changes in place, adopts or revises them under the selected winner, and runs focused checks. Required final E2E follows the central policy after post-Fast implementation is complete. No copying or redundant rewrite is needed.
- For overlapping work, the involved Producers compare actual edits and directly agree whether to separate or combine contributions and the shared-index order. Within one outcome, preserve main-path-over-Fast precedence; across outcomes or Jobs, apply no automatic priority. The Supervisor and Orchestrator relay peer identities and scope/index facts only; neither assigns contested content or Git turns. Producers resolve conflicts without waiting for user direction. If contributions are inseparable, every contributing Producer agrees the exact joint scope and selects a contributing Producer to commit it. Helpers report changes through their parents and follow their responsible Producer’s agreement; they never negotiate boundaries or commit. Preserve separate checkpoints for distinct Fast issues except that agreed inseparable joint checkpoint; exclude unrelated and in-flight changes.
- If the involved Producers have not agreed the exact boundary or order, keep that issue checkpoint-pending, name the overlap, and continue unaffected work. Resume when the involved Producers resolve it directly; never silently treat it as committed or include unrelated/in-flight changes.
- The current Job owner reconciles every Fast finding and Fast-originated changed hunk under [post-Fast completion](#post-fast-completion), then tracks each in-scope inventory item into review and sends reports, evidence, and disposition recommendations to the Supervisor. Retained Fast hunks are cumulative code targets. When a preceding baseline contains adopted Fast changes, add its unreviewed hunks as an explicit cumulative review target alongside the narrow iteration checkpoint; never exclude them as predecessor context.
- Neither producer supplies its own independent acceptance reviews. Step 4 runs the fresh independent final E2E required by the central policy.
- Before normal-path completion or final acceptance, satisfy [post-Fast completion](#post-fast-completion). The current Job owner coordinates the existing stop for both Producers' task-owned writes, including delegated Fast work, inspects the current cumulative scoped diff, verifies reviews and checks cover that state, and sends closure evidence to the Supervisor. The Supervisor retains task and cancellation closure decisions, cross-Job decisions, and final acceptance; it verifies closure evidence as needed. Material late edits require fresh appropriate review and verification.
- On task cancellation/replacement, the Supervisor owns closure, cancels the Fast Producer and its delegated work with the main task, and preserves dirty changes/evidence. On task clean pass, it observes both Producers' final state. On either closure path, it observes both Producers and their task-owned write-capable tools stopped or finished. Root teardown and goal synchronization follow [concurrent task scheduling](../../../../eci-binding-contract.md#concurrent-tasks) and [Job-owner closure](task-orchestrator.md#publication-transfer-and-report-drain). Fast success alone never closes ECI or outer ATE.

## Post-Fast completion

The published Job owner coordinates task-local post-Fast dispatch and evidence under the [task-orchestrator contract](task-orchestrator.md#authority-and-reporting). The Supervisor retains closed-Job evidence, final adjudication, acceptance, and teardown.

Main ECI may advance and review iterations concurrently with Fast.

After the current Job owner confirms genuine Fast completion to the Supervisor, report root-task status
`FOLLOWUP` while the required sequence below remains unaccepted. Use
`FOLLOWUP_PAUSED` only when the next required post-Fast action cannot advance
because it awaits a named in-scope dependency. Report the dependency, impact,
owner, and resume condition; return to `FOLLOWUP` when it clears. A user input
or decision remains `BLOCKED` in the affected lane, never `FOLLOWUP_PAUSED`.
End either root-task status on Supervisor acceptance or explicit user closure.
Keep these statuses outside lane Implementation/Test/Prod readiness; see
[status guidance](../../writing-status-reports/SKILL.md#multi-lane-mission-status).

A genuine Fast completion restarts the normal path from a fresh Step 1.

Any Step 4 review that runs concurrently before this restart is intermediate only and never acceptance. The normal path remains incomplete until this post-Fast sequence passes for the task:

1. The current Job owner verifies [genuine Fast completion](#outcome-continuation-and-completion), including the full provisional outcome, producer verification, the Fast Producer's scoped commits, finished and integrated Fast Helper work, and stopped Fast/Helper write-capable tools. If the Job owner is an Orchestrator, it sends this evidence to the Supervisor. Main-path writers may continue. A completed audit, milestone, agent turn, write-yield, idle label, timeout, or cancellation is not Fast completion. Keep Fast idle while the main ECI path performs the following steps; Fast need not wait for Supervisor acceptance to finish.
   - The Fast Producer's completion report enumerates every finding and changed hunk from the Fast Producer and its Helpers.
   - Include findings with no retained hunk and changes that are retained, revised, non-retained, superseded, or reverted.
   - Inventory states retained, revised, non-retained, superseded, and reverted are provenance only, not canonical dispositions; no inventory state implies a disposition.
   - The current Job owner reconciles task-local findings with shared state and sends reports, evidence, and disposition recommendations to the Supervisor.
   - This complete inventory is review context, never a manifest, receipt, or admission/write gate.
   - Final acceptance requires every in-scope inventory item to have a disposition and evidence.
2. Restart main ECI at a fresh Step 1.
   - Scope-screen every Fast finding and every Fast-originated changed hunk against `exact user source → faithful requested outcome → bounded scope`. Keep only repairs necessary to meet or prove that outcome in scope.
   - Keep every Fast-originated hunk in inventory/review context; do not expand authorization. An unadmitted separate-outcome finding stays a post-ECI observation/follow-up and creates no issue-specific repair, review, proof, or acceptance work. Allow only the TODO comment defined under [Main ECI quality responsibility](#main-eci-quality-responsibility) for an already-discovered code issue; include its hunk in ordinary current-diff review and keep the issue in inventory context only.
   - Then the current Job owner assigns the reusable Explorer a new Step 1 exploration of the final shared scoped code, started after Fast completion. The Explorer reviews every in-scope Fast finding and every in-scope Fast-originated changed hunk for quality.
   - The Explorer reviews each in-scope inventory item against original requirements and quality standards, alongside retained main-path changes. Reread current targets and relevant surrounding code. Prior exploration, diffs, and passing tests are context, not this new exploration.
3. A fresh Step 2 critic independently assesses the current sources and Explorer's options.
   - The fresh Step 2 critic reviews every in-scope Fast finding and every in-scope Fast-originated changed hunk and recommends exactly one canonical disposition for each in-scope inventory item: retain, revise, replace, superseded, resolved-with-evidence, or policy-valid deferred-with-reason, under [main ECI quality responsibility](#main-eci-quality-responsibility).
   - Step 2 authority is limited to design-winner selection; it does not apply treatment.
   - The Supervisor owns final disposition application/treatment and applies exactly one canonical disposition per in-scope inventory item.
   - Apply impact-proportional routing before implementation.
   - Return substantive `now` findings or design/API uncertainty through one complete fresh Steps 1–2 repair batch.
   - Send a `treatment: now` finding to the implementer once only if it is contained, in-scope, impact-trivial, and isolated.
   - A Supervisor-applied `revise` or `replace` disposition reaches the Implementer as `treatment: now` only when it is in-scope, contained, impact-trivial, and isolated; otherwise return through one complete fresh Steps 1–2 design-repair batch before implementation.
   - Other dispositions require evidence, not implementation.
   - Carry the resulting disposition and evidence into Step 4; never leave it unresolved.
   - Use policy-valid deferred-with-reason only where [review-policy.md#impact-proportional-routing](../../references/workflow-runtime/review-policy.md#impact-proportional-routing) permits.
   - A policy-valid deferred-with-reason disposition is only for an in-scope, non-hard, impact-trivial, isolated finding; it requires evidence supporting each eligibility condition, plus a technical reason and revisit trigger; it never waives original criteria.
   - Missing evidence invalidates only that defer conclusion; it never waives criteria or gates unrelated bounded work.
   - Preserve qualifying code; make only justified changes through the main-path Implementer.
   - The Implementer fixes every routed `treatment: now` finding and fixes or justifies every retained Fast finding and every retained Fast-originated change under that selected winner, including no-hunk findings.
   - Every no-hunk retain or resolved-with-evidence outcome requires evidence.
   - The Implementer implements those routed Supervisor-applied revise/replace changes and validates retained/revised changes.
   - The Implementer supplies evidence for no-hunk resolutions and for non-retained, superseded, reverted, resolved, and deferred outcomes.
   - Run required focused checks and checkpoint changed iterations normally. Reserve triggered final E2E for the stabilized candidate under the central policy.
4. After the fresh Step 1, Step 2, and Implementer disposition, the final cumulative Step 4 independently reviews the final cumulative scoped state even when no further edits are needed.
   - The final cumulative Step 4 verifies every in-scope inventory item has exactly one disposition and final evidence.
   - The final cumulative Step 4 leaves no unresolved in-scope `treatment: now` finding or failed-eligibility `revise`/`replace` needing implementation.
   - Unadmitted separate-outcome observations remain outside acceptance; admitted child outcomes retain their own acceptance evidence.
   - Aggregate all required critics and the final E2E pair when triggered, resolve remaining `now` findings, and verify coverage of the current state before completing the normal path or accepting the task.
   - Earlier reviews alone cannot satisfy this sequence.

Reopened provisional Fast completion invalidates this sequence even without resumed writes. Resumed Fast writes invalidate this sequence; after Fast finishes again, repeat it. Restart from fresh Step 1 after renewed genuine Fast completion. Later main-path or interacting task edits refresh affected review and verification; material design findings return through Steps 1–2. Independent sibling tasks keep advancing.

Cancellation uses user closure, never a clean pass or substitute Fast completion. Preserve changes and evidence, observe task-owned writers stopped, and keep uncancelled siblings active under the existing closure rules.
