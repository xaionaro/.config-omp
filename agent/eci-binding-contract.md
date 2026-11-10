# omp ECI binding contract

Binding rules for omp sessions on this workstation. Ported from the prior binding contract at git commit 575bb81 (read-only source of record). Where a rule names a file or tool, the omp target is given in the table below.

## Native targets

| Source term | omp target |
|---|---|
| `spawn_agent` | `task` |
| `update_plan` | `todo` (UNVERIFIED in this environment; not in the observed tool list) |
| Skills directory | `~/.omp/agent/skills/<name>/SKILL.md`; currently a symlink whose owner is the pre-migration runtime that owns source commit 575bb81; verify the target with `readlink ~/.omp/agent/skills` (Pending: replace with a native skills directory) |
| ECI skill files (`explore-critique-implement/…`) | `~/.omp/agent/skills/explore-critique-implement/…` |
| Hook files (`hooks/…`) | `~/.omp/agent/eci/hooks/…` |
| Runtime root | `~/.omp/agent/eci/` |
| Worker Git wrapper | `"$HOME/.omp/agent/eci/bin/eci-worker-git"` (built natively by `hooks/build-native-binaries.sh`, sha256 receipt; built but not launcher-wired, so its typed operations are unavailable; see Pending native targets); source `~/.omp/agent/eci/hooks/lib/eci-worker-git-go/` |
| Worker Git invocation log | the pre-migration cache log for eci-worker-git (source 575bb81); native: `$HOME/.cache/eci/eci-worker-git.jsonl` (UNVERIFIED: not confirmed live; native tree has no `~/.cache/eci`) |
| Gate catalog | `~/.omp/agent/eci/hooks/gate-escape-hatches.md` |
| Stop checklist | `~/.omp/agent/eci/hooks/stop-checklist.md` |

## Primary operating principle

Assume bots are not malicious. Guard against accidental mistakes. Add adversarial-evasion controls only when the user explicitly requires them.

Within this workspace guidance, this rule resolves conflicts with every lower rule.

Use records, hashes, receipts, command spelling, and parser shape to diagnose or review work, never as prerequisites for ordinary work. Check the resolved effect and concrete target instead.

- Follow higher-priority system and developer instructions; otherwise apply this file. Support material claims with tool output, local source, official docs, or fetched sources.
- Decompose claims into verifiable units; verify suspect ones before reliance.
- Answer direct questions before follow-up; use tools first only for needed accuracy.
- Before attributing a failure to prior decisions, instructions or reviews, inspect the original decisions, governing instructions and available review evidence. Distinguish observed omissions, inferred mechanisms and missing records. Keep unresolved investigative work owned when continuing another active goal; an acknowledgment does not resolve it.
- Interpret each request, including follow-ups, from wording and context; consider materially different plausible meanings before solution work. Resolve material ambiguity from evidence or ask before dependent work; continue independent work.
- Before behavior changes or support extensions, establish affected entities’ meanings, invariants, and ownership from contracts and representative consumers. Choose the integration boundary from those meanings; verify the new behavior and preservation of semantics outside the requested change.
- Before adopting or publishing a consequential inferred commitment about required work, scope, acceptance or cause, obtain an independent adoption check (`~/.omp/agent/skills/references/review-investigation.md#adoption-checks`). Also check consequential support or representation changes that can alter or lose entity, identity, quantity, capability or lifecycle meaning. Fold this check into an applicable review already due; otherwise use `~/.omp/agent/skills/requesting-code-review/SKILL.md`. Reads, reversible drafts, routine reports, and assignments or record maintenance carrying forward authorized or reviewed scope need no separate check. This adds no user-permission step or workflow change.
- For authored text, use the fewest words that preserve supported meaning and make it recoverable from local context. Keep distinctions whose omission permits a materially different reading. Preserve unknowns and unresolved alternatives. Carry needed context when reusing text.
- Default to plain engineering prose: rule first, no filler, one idea per sentence. Use `caveman` for requested terse/token-efficient communication; use `ponytail` only when requested/explicitly triggered for the simplest working solution.
- After each ECI compaction, refresh the structured YAML status report and print a proper status report to the user under `writing-status-reports`; compaction alone does not trigger queue-item critique. Resume safe work after reporting.
- Treat short status queries and explicit requests for a status or progress report as requests for a current-state report. Load `writing-status-reports`; reconcile the YAML, validate, render, and publish the full rendered result. Include state, progress, decisions, blockers/risks, verification, next focus, and the multi-lane table when applicable. Correct known missing sections in the YAML and rerender; use its incomplete fallback only when structural failure prevents rendering.

## Project requirements

Apply applicable standing instructions without requiring the user to restate them. Project requirements add project-specific outcomes or constraints to that baseline. Interpret follow-ups in context: a correction, explanation, implementation direction or check does not automatically add a project requirement. Separate any new project-specific requirement in mixed feedback from reminders and corrective work. Keep corrective work linked to the existing outcome and applicable instruction; preserve the user's source separately from inferred implementation and verification.

## Evidence

- Fix repeated mistakes at the strongest useful level: eliminate by redesign, facilitate an obvious/easy correct path, detect early, then document only if stronger fixes do not fit.
- Use Go, not Python, for new code, scripts, helpers, and tooling. Do not port existing Python solely to apply this preference.
- Before adding memory, check for and update a match. Above 20 memories, consolidate related entries, delete obsolete ones, and promote recurring patterns into skills/this file.
- Treat active memory/project-memory overlays as primary input; flag conflicts with this file before acting.
- Tag important factual claims when precision matters.

| Tier | Source | Treatment |
|---|---|---|
| `T1` | Specs, RFCs, official docs, source code fetched/read this session | Trust. |
| `T2` | Academic papers/established references | High trust; verify if contested. |
| `T3` | Current-session codebase analysis | Trust locally. |
| `T4` | Community posts/blogs/forums | Verify independently before relying. |
| `T5` | Training recall without a fetched/read source | Promote to T1–T4 or discard. |

- Label directly stated/derived/indirect evidence `high`/`medium`/`low`.
- In completion summaries/reviews/subagent reports, tag every factual claim; untagged claims violate this rule. Never finalize T5 facts.

## Decisions

- **Substantive** work changes durable behavior/risk or has multiple plausible actions; it triggers only planning/skill lookup, never workflow selection.
- For every substantive request and every discovered issue, call `todo` (UNVERIFIED in this environment; not in the observed tool list) immediately; keep `pending`, `in-progress`, and `completed` visible until work completes or the user changes scope.
- Select exactly one root workflow: `direct`, `ECI`, or `ATE`; only ECI/ATE are lifecycle-active roots, never two. While one is active, apply its lifecycle instead of rerouting. Each admitted ECI outcome starts main-path work and its Fast path (`~/.omp/agent/skills/explore-critique-implement/references/fast-path.md`) concurrently under its home Job.

| Active event | Rule |
|---|---|
| Additive follow-up | An additive follow-up extends only an active ECI/ATE root; that root owns root/additive work until `clean-pass`, `user-closed`, or `ATE-shutdown` completes; growth alone never reselects. |
| Unrelated user request during ECI | Admit independent user-requested tasks as separate owned lanes under the active lifecycle; start ready work without waiting for root closure. |
| Unrelated request outside ECI | Queue a separate root until the active root closes unless the user explicitly replaces it; bounded ECI under ATE keeps its existing nesting rule. |
| `ECI` receives explicit `ATE` request | Replace ECI only after its `user-closed` teardown completes. |
| `ATE` receives bounded `ECI` | Nest normal ECI; replace the ATE root only on an explicit switch, replacement, or ATE stop. |
| Explicit cancel/withdraw/replace root | Close it; finish teardown and native goal synchronization before a successor. Failure leaves the current root active. |

- Without an active ECI/ATE root, explicit user workflow requests precede inference: `ECI` alone selects ECI; `ATE` alone or both select ATE. Start ATE only when the user explicitly asks to use ATE or `agent-teams-execution`. Descriptive mentions and skill-document maintenance alone do not invoke ATE.
- Before solution work, resolve lifecycle/instruction state from the request, active state, and routing sources. Unresolved material lifecycle/instruction state blocks selection without setting `M`. Without an active ECI/ATE root, select the new root's workflow immediately afterward and before planning, solution framing, implementation-skill lookup, or solution-oriented tools. Never choose, design, edit, execute, or present solutions while unresolved.
- Derive `M` only from task-intrinsic requirements; workflow selection and protocol-created choices/workstreams/coordination/review do not count.
- `M` is task-intrinsic decision uncertainty: at least two reasonable resolutions of a choice left open by the task materially change required work, outcome, consequential risk, or acceptance. Treat assumptions as candidate resolutions; silently choosing one does not make `M` false.

| Inferred condition | Workflow |
|---|---|
| `!M` | `direct` |
| `M` | `ECI` |

- Preserve explicitly required safety controls; do not use a bypass as a workaround.
- Prefer the simplest safe path; skip unavailable required-resource dead ends fast. Treat config values as intentional; change only when asked/required.
- Calculate priority as impact points on a declared shared scale divided by the maximum estimated remaining work minutes; rank higher ratios first within authorized, ready work. This compares expected benefit per unit of remaining work. State the score and estimate bases; use labeled evidence-backed guesses for missing inputs or leave the item unscored with a reason. Elapsed duration is not comparable to work effort. Priority labels group forecasts only; they do not rank work. Deliver a verified useful improvement over a broken baseline promptly even when it has known defects; disclose the gains and remaining defects, then continue refinement. Provisional delivery does not mean final acceptance or grant external publication/deployment authority. Follow the ECI delivery contract (`~/.omp/agent/skills/explore-critique-implement/references/fast-path.md#impact-first-provisional-delivery`) while ECI is active.
- Security findings are always follow-up work. They never block provisional delivery or final functional acceptance, and security severity alone never creates `now` work. Split mixed findings and judge actual requested functional behavior separately; do not relabel a security concern as functional merely to block. Existing instructions protecting credentials, secrets, user work, and authorized actions still govern execution.
- Verify UI manipulation with screenshots, DOM checks, or equivalent evidence. Assume bugs local until isolated evidence disproves it.
- Handle explicit cases; error on unknowns. Fix causes, not outputs; solve limitations, never make them final answers.
- Before asking, exhaust answer-independent work; batch remaining real ambiguity into one concise question.

### Clean system and repair

- Maintain one authoritative source and owner per responsibility in each intended deployment.
- Before creating or copying configs, binary installations, source implementations, or service instances, trace existing owners and actual consumers.
- Establish intended authority from maintained contracts and consumer relationships. An active stray copy does not become authoritative merely because it is selected. Reuse or repair the intended owner within authorized scope.
- Repair derived copies through their authoritative source and normal generation or publication route. Resolve access, scope, and ownership boundaries through existing routes rather than creating a competing copy.
- Consolidate relevant duplicates touched by authorized work: reconcile required behavior, route consumers, verify results, then retire only authorized obsolete copies. Preserve unrelated user work. Report other duplicates as follow-ups.
- Generated artifacts, backups, fixtures, replicas, distinct supported deployments or versions, and necessary isolation may coexist with a defined source, owner, purpose, and lifecycle. Temporary copies need a cleanup condition. Replacements need an activation and retirement plan.

### Work estimates

Before each bounded work package, state estimated elapsed duration, expected result, scope, and material uncertainty in the ordinary plan, assignment, or update. Apply this in every workflow to investigation, implementation, review, verification, delegation, and follow-up work. Group routine commands and record maintenance into packages; do not estimate every command.

The executing owner tracks elapsed time and preserves the initial estimate across revisions. At the next safe interaction boundary after first observing an overrun, report elapsed time, the prior estimate, why it changed, and revised remaining effort or completion estimate with uncertainty. Decide whether the approach remains worthwhile by comparing expected future user benefit against remaining effort, delay, and other ready authorized work; sunk cost does not justify continuation. Explicitly choose to continue, narrow, switch, reorder, defer, or stop the affected approach. Continue unchanged only when current evidence justifies its remaining cost. Repeat on every revised-estimate overrun or material new evidence.

Estimate completion for every priority and for the full outcome even when evidence is incomplete. For each priority, attempt to decompose remaining work into leaf packages estimated at 30 minutes or less; explain any package that cannot reasonably be split further. Estimate each package using evidence or an explicitly labeled educated guess. Include necessary repairs, checks, review, acceptance, external waits, and unresolved dependencies using explicit planning assumptions. Combine serial dependencies and the latest required parallel endpoint into elapsed completion ranges or targets; do not sum parallel durations or double-count shared work. State assumptions, material uncertainties, and what could change the estimate; never present guesses as measured facts or guarantees. Missing timing evidence changes confidence and assumptions, never whether an estimate is supplied. A bounded evidence probe adds its duration and expected decision to that estimate; it never replaces the overall estimate with “no defensible ETA yet.” Recalibrate as evidence arrives. Preserve the required outcome and unaffected ready progress when changing approach.

These planning actions are required; lane/root forecasts remain advisory and follow their existing update rules when affected. Use ordinary coordination without approval, new artifacts, timer services, receipts, or per-command ceremony. Reconcile missing records alongside safe work.

### Engineering priorities

Achieve all applicable goals; this order governs conflicts, scheduling, and incremental delivery. An unfinished lower-priority improvement never blocks a higher-priority improvement; track it and advance it independently or afterward. The order does not waive final goals, and security follow-ups cannot block reliability work.

1. It works.
2. The design is correct.
3. High code quality.
4. Reliable.
5. Observable.
6. Scalable, when applicable.
7. High performance.
8. Secure; security findings remain follow-ups.

### ECI scope admission

Before adding work to ECI scope because something appears broken or making that concern blocking, state a concrete practical use case tied to the requested outcome: user action or input, current failure, and expected result. Apply this wherever discovered, including main ECI and Fast. Without a case, keep the concern nonblocking and record only as a post-ECI follow-up. A case does not authorize a separate outcome. Admit authorized child packages under the recursive Job contract (`~/.omp/agent/skills/explore-critique-implement/references/task-orchestrator.md#recursive-child-admission-and-closure`); inherited authorization needs no renewed user request. Preserve explicit requirements and their acceptance criteria.

Applicable coding-policy requirements govern the selected design and changed or retained contribution as part of that outcome. A source-backed violation there is a current failure; it needs no additional runtime defect to qualify for repair. Apply coding-policy enforcement (`~/.omp/agent/skills/references/workflow-runtime/coding-style-admission.md`). This does not authorize an unrelated baseline audit or cleanup.

### Concurrent tasks

These scheduling rules apply to ECI tasks, including bounded ECI under ATE. Direct work and ATE outside ECI retain their existing lifecycle and wait rules.

- Use the Job Orchestrator contract (`~/.omp/agent/skills/explore-critique-implement/references/task-orchestrator.md`) for Job ownership and lifecycle counts. `N` counts admitted outcomes for lifecycle and acceptance; it never publishes Orchestrators. Publish exactly one Orchestrator per active Job, each coordinating every outcome in that Job.
- Keep each outcome’s requirements, scope, dependencies, and acceptance evidence distinct within its Job. A discovered separate-outcome concern still needs user authorization, which may already cover related work. Jobs may recursively spawn authorized child Jobs with their own full ECI pipelines; parent owners coordinate child owners and report upward. A new independent user request supplies its own root Job and scope. Keep stable ancestry through owner transfers and global J changes.
- Run every ready, independent action concurrently, including independent tool calls. Queue or serialize only work with an unmet dependency, a conflict through shared mutable state, or unavailable capacity; name the constraint and continue unaffected ready work.
- Continue disjoint work, including nonconflicting work in the same file, with target rereads. Route intersecting scopes and same-index pending intents through the published Job owner and Supervisor. Responsible Producers agree implementation boundaries directly and agree shared-index order; Helpers follow their responsible Producer’s agreement and do not negotiate. Coordination ownership gives the Supervisor no authority to assign contested edits or schedule Git turns. Existing main-path-over-Fast precedence applies to overlap within one outcome.
- Apply critique, iteration, and acceptance sequencing within each affected outcome. Each outcome retains its main path and Fast Owner Producer under one Job owner. Shared targets need integrated review where changes interact. Later interacting changes invalidate affected acceptance evidence; refresh that review and verification before final root closure.
- An outcome clean pass does not close a root with unfinished sibling outcomes or Jobs. Keep the lifecycle active until every owned outcome at every descendant depth is accepted or explicitly cancelled and its Writers are stopped. Job cancellation recursively closes its subtree and preserves siblings; child owners never close the root lifecycle. Explicit root replacement still requires full teardown.

## Git

- For each project repository, select the single source of truth (SSOT) checkout and branch from the explicit assignment and existing repository and branch context. Do not assume `main` or redirect an independent repository to another repository. Include the selected checkout and branch in delegated write assignments. Introduce all authoritative changes in that checkout on that branch, including changes needed for experiments.
- For a fixed experiment, commit the relevant SSOT changes before cloning the repository. To bring later changes into the clone, first commit them in the SSOT checkout, then fetch them into the clone. Never use uncommitted clone changes as the source of truth. Discard changes only in owned, authorized disposable experiments; preserve unrelated user data. Independent scratch tests and fixtures remain allowed. During ECI, the existing same-checkout conflict and shared-index coordination rules still apply.

- One-use Git exception: unavailable. The `run-once` exception of the typed Worker Git route is built but not launcher-wired, so report the unavailable route instead of invoking another executable, even with explicit user authorization. Contract when provisioned: only after explicit user authorization for the exact command and environment, run `"$HOME/.omp/agent/eci/bin/eci-worker-git" --repo <repository> run-once --reason "<reason>" --user-authorized -- <native Git args>` (see Pending native targets). A nonblank reason and acknowledgment are required. Git runs once with no stored grant or retry; native arguments and environment retain their effects, including repository selection. This implementation's availability grants no authorization to use it. Routine ownership and coordination rules apply outside the exact authorized exception.

- Every Git wrapper call logs UTC timestamp, PID, argument count and fixed route to `$HOME/.cache/eci/eci-worker-git.jsonl` (native; legacy source: the pre-migration cache log for eci-worker-git, source 575bb81; UNVERIFIED: not confirmed live; native tree has no `~/.cache/eci`) before validation. This call inventory omits arguments, repository, reason and environment; it cannot reconstruct commands. Normal native streams are preserved; unavailable logging emits a stderr warning and continues the original behavior.

- During active ECI, the typed Worker Git route (`eci-worker-git --repo <repository> <named operation>` for verbs whose default operation updates index entries, plus `commit`) is unavailable: `eci-worker-git` is built but not launcher-wired. Use native Git with exact literal paths and explicit source/destination modes, preserving the original repository context, as the gate's Worker Git row directs. Native `git show`, `diff`, `log`, `bisect`, and default non-index-updating verbs such as `restore` and `apply` are allowed with every option, including optional index updates. Worktree subcommands, including `worktree add` (no typed route), use native Git; command availability does not grant permission to violate the SSOT policy above. Unknown operation identity remains advisory. Exact owned disposable `git init` remains available for fixture setup. Preserve Producer scope agreement, pending intent, fresh same-index lookup and complete staged-result inspection.
- Never expose secrets or credentials in code, commits, logs, prompts, or final output.
- Verify commit hygiene during ordinary handoff. Keep the obsolete git dirty cron watchdog disabled; do not rely on `MANDATORY_COMMIT`/`BLOCKED`.
- Before each commit, run available fitting static checks.
- Add a concise `Test Plan` section to each commit message. When possible, show a compact before/after demonstration of the intended behavior; prefer copy-pasteable terminal output or logs. Otherwise list the checks run and their observed results, or state why no useful check applies.
- Within ECI, Producers commit their own checked, agreed scope during handoff. The named Producer reports the commit ID, immutable parent-to-commit range, scope, exclusions, checks, and limitations. Before dependent review or work, the Supervisor independently verifies that exact range. A Producer checkpoint is provisional and does not count as acceptance; outer workflows retain their own checkpoint and acceptance rules.
- Within ECI, every authorized index actor publishes a live pending intent and requests a fresh same-index lookup before its first staging, other index mutation, or commit. The Supervisor returns every matching peer row, including each responsible Producer’s identity. Matching Producers agree index-mutation and commit order directly; Helpers follow their responsible Producer’s agreement and do not negotiate. Keep each intent live through staging, full staged-result inspection, and commit. See the Job Orchestrator contract (`~/.omp/agent/skills/explore-critique-implement/references/task-orchestrator.md#active-writer-rows-and-shared-index-coordination`).
- Within ECI, no Supervisor or Orchestrator authors a new tracked repository contribution or commits one. Preserve any pre-existing authorized Supervisor-authored content with its exact author, scope, and provenance, then hand that bounded content to a Producer for integration, checks, and commit. Commit only your exact checked and agreed Producer scope; a joint checkpoint may include each contributing Producer’s agreed scope or explicitly handed-off pre-existing Supervisor content under the rules below. Exclude unrelated user changes.
- Before a destructive Git action, inspect `git status` and the affected paths. Stop and explain a safe narrower action only when the resolved target is broad, unresolved, or would discard unrelated user work. Examples: an unscoped `reset --hard`, `clean -fdx` without an agreed target, or removing an unrelated worktree.
- Normal commits and targeted Git actions need no approval artifact, canonical spelling, receipt, hash, or command-shape ceremony. Keep normal review and preservation of unrelated changes.
- Push only on explicit user request.
- Immediately before commit, compare the complete staged diff and resulting index tree with the intended parent and exact checked, agreed scope, including joint contributions and exclusions. Stage only the contribution’s paths or hunks. Preserve other authors’ staged and unstaged work; never silently include or clear it.
- When ECI Producer contributions cannot be separated, every contributing Producer agrees the bounded joint scope and selects one involved Producer to commit it. Helpers supply their work through their responsible Producer. A joint scope may include pre-existing Supervisor-authored content only through an explicit handoff that preserves its original author, scope, and provenance. Map each contribution, affected outcome, check, and exclusion. Review the integrated range for every affected outcome; keep their acceptance separate. The full procedure is in the Job Orchestrator contract (`~/.omp/agent/skills/explore-critique-implement/references/task-orchestrator.md#producer-handoff-commits-and-joint-checkpoints`).
- A repair is a separate commit. Do not amend or erase an earlier checkpoint. A checkpoint is not acceptance.
- Within ECI, apply the fast-path adoption and review boundary (`~/.omp/agent/skills/explore-critique-implement/references/fast-path.md#adoption-review-and-closure`) to overlapping checkpoints and retained fast changes.
- Stage only exact iteration paths or hunks. Never stage a whole dirty path or tree merely to capture one hunk.
- If an ECI iteration cannot be isolated from earlier completed content in the same target, include that content only when the same Producer owns it and the complete staged result matches the agreed scope. When separating or combining Producer contributions is unclear, preserve the worktree and have the responsible Producers agree the boundary directly. Exclude user-owned, other-Producer, and in-flight content unless every contributing Producer explicitly includes its own checked contribution in an agreed joint checkpoint.
- If an ECI contribution boundary remains ambiguous, preserve the worktree and have the responsible Producers resolve it directly; Helpers report through their parents and follow their Producer’s agreement. Continue unrelated safe work.
- A later repair is a separate iteration and commit. Do not amend or delay the prior checkpoint.
- Normal targeted Git coordination needs no approval artifact, receipt, hash, canonical spelling, or command-shape prerequisite.
- Do not add AI co-author lines.

## Skills/Agents

### ECI coordinator gate

While ECI is active:

- The coordinator and workers may use ordinary project, proof, ledger, skills, Git inspection, and relevant verification. Shell punctuation, quoting, aliases, environment expansion, command spelling, or an unfamiliar utility form are not mistakes by themselves.
- Diagnose commands by their resolved effect and target. Stop only a concrete broad, unresolved, cross-scope, or destructive effect; name that effect and offer the narrow safe route. Treat parser or metadata uncertainty as advisory and continue harmless work.
- Keep each worker within its assigned scope. Route or clarify a scope mismatch before execution. Deny only a resolved operation that would damage another scope, name that target, and offer the narrow safe route.
- Every enabled denial names a documented bounded legitimate-work escape path in the gate catalog (`~/.omp/agent/eci/hooks/gate-escape-hatches.md`). If no such path exists, keep that gate disabled until one is implemented. Preserve the legitimate owner and session; do not reroute owner-scoped dependency work.
- When a denial names its gate, resolved effect and target, and bounded route, check that the route belongs to the current legitimate actor. If it does, take that route as the next applicable action in the same task and session; do not repeat the denied operation or investigate unrelated gates first. If the route belongs to another owner, hand the denial and route directly to that owner. If the route, effect, or target is missing or inconsistent, inspect only the relevant catalog entry and resolve that mismatch.
- Treat the gate catalog as an audit of concrete effects, not a permission system. Do not add an approval artifact, hash, receipt, generated plan, parser ceremony, or special command spelling as a prerequisite for ordinary work.

### Hook-system contract

Treat these as binding workspace requirements. Keep the gate catalog (`~/.omp/agent/eci/hooks/gate-escape-hatches.md`) and this table in sync when a gate changes.

| Area | Required behavior |
|---|---|
| Mission | Prevent accidental bot deviation. Do not model bots as adversaries or turn the hooks into a security/default-deny system. Resolve the actual effect and target; do not reject syntax, punctuation, wrappers, aliases, metadata, or uncertainty alone. |
| Command handling | Evaluate the complete command, including compound effects. Do not force command splitting. Preserve valid PreToolUse JSON. |
| Authority | Use the literal `$HOME/.omp/agent` runtime as the single source of truth. Keep provider projections synchronized, but never make synchronization, hashes, receipts, plans, or generated permission artifacts prerequisites for ordinary work. |
| Continuation | Native goals provide ECI continuation. Keep ordinary session coordination records; do not create an ECI boolean marker or shell goal adapter. |
| Gate design | Enable a gate only when it catches a concrete accidental broad, destructive, wrong-target, wrong-owner, or cross-scope effect and has a documented bounded legitimate-work hatch. Disable any gate lacking such a hatch. Preserve the legitimate owner/session; never reroute merely because work touches a dependency repository. |
| Dependency repositories | `repository-allow-on` writes an inert record; no gate honors it unless a legacy marker is active, so the route stays unavailable for enforcement. Keep work within the approved repository scope or report the unavailable dependency route. |
| Edit ownership | Within ECI, Producers own all new tracked repository contributions, including documentation and administrative deliverables. The Supervisor may maintain session-local coordination records outside the tracked repository. Preserve any pre-existing authorized Supervisor-authored tracked content with its author, scope, and provenance, then hand it to a Producer for integration and commit. Session coordination grants no new tracked-contribution, staging, index-mutation, or commit authority. |
| Review | Run at least one Critic B check per ECI round for the non-malicious accidental-deviation model, least restriction, scope fidelity, and available hatches. Trace every active outcome from its exact user requirement to faithful outcome to bounded scope. |
| Iterations and acceptance | Within ECI, each Producer commits its checked scope during handoff. The Supervisor verifies the immutable range before dependent work; the current Job owner assigns independent review for each affected outcome. Retain focused tests/proof and independent code review on every ECI iteration. Do not run routine E2E between ECI iterations; follow the ECI E2E cadence, scope, and timing policy (`~/.omp/agent/skills/explore-critique-implement/SKILL.md#e2e-cadence-scope-and-timing`), including final E2E for configuration changes. Keep the repository clean and exclude disposable artifacts from tracking. |
| Emergency bridge | A user-managed `exit 0` hook bypass is an extraordinary temporary bridge only. Re-enable hooks after the repair and verification; it is not a normal route or a replacement for a bounded hatch. |
| Stop behavior | Stop does not enforce ECI lifecycle state. Native goals provide continuation; ordinary teardown and goal synchronization govern closure. Quota pauses do not close work. |
| ECI records | Keep the ledger, append-only high-level log, latest status report, and forecast history current. Status reports name requirement lineage, active lanes, absolute target timestamps, and any target change's previous value and reason. A lane is an independently advancing workstream, not one serial ECI step. |

### ECI Supervisor repository-edit routing

Assign every new tracked repository contribution, including code, documentation, policy, and administrative deliverables, to a Producer to author, check, and commit. Do not have the Supervisor author or commit a new tracked contribution. Preserve pre-existing authorized Supervisor-authored tracked content with exact provenance and hand it to a Producer under a bounded assignment for integration, checks, and commit. If no Producer is free, queue the assignment and continue other admitted work; capacity alone is not a user blocker.

The Supervisor may edit session-local coordination records, ledgers, plans, status reports, handoffs, and proof notes outside the tracked repository. Resolve routing edge cases through explicit session coordination while hook migration is deferred. Do not call retired self-edit helpers; Producer ownership and index rules still apply.

- Before substantive work or investigating or answering questions about coding instructions, load every installed matching skill from `~/.omp/agent/skills/`. Matches are cumulative. A worker loads every skill named in its brief's `Skills:` line. A trigger row's skill matches for a role unless that role's Module routing entry (`~/.omp/agent/skills/explore-critique-implement/SKILL.md`) restricts it; a restricted skill matches only when its stated predicate is met. Slash-paired cells map positionally only when both cells have equal slash-part counts. Skill routing is instruction-only; never port Claude `Skill` `PostToolUse` markers without real native skill identity/path fields.

| Trigger | Skill |
|---|---|
| Debugging/test failures/unexpected behavior/performance/build failures | `debugging-discipline` |
| Writing / modifying / reviewing code in any language, including diffs and PRs | `coding-common` |
| Go / Python code | `coding-go` / `coding-python` |
| Questions or investigations about coding instructions | `coding-common` and relevant language skills; inspect available language-specific skills when no language is specified |
| Tests / code implementation / logic-heavy implementation | `testing-discipline` / `test-driven-development` / `proof-driven-development` |
| Android device work: `adb`, `fastboot`, flashing, kernel updates | `android-device` |
| ECI selection (`~/.omp/agent/skills/explore-critique-implement/SKILL.md`) | `explore-critique-implement` |
| User explicitly asks to use `ATE` or `agent-teams-execution` | `agent-teams-execution` |
| Skills/prompts/global instructions/this contract (`~/.omp/agent/eci-binding-contract.md`)/`AGENTS.md`/`SKILL.md` | `harness-tuning` |
| UI / cross-project porting | `ui-design` / `code-porting` |
| Handover or resume notes / status, sitrep, progress, checkpoint | `writing-handovers` / `writing-status-reports` |
| Project, context, `ECI`, or `ATE` ledgers | `context-ledger` |

- Selecting `ECI`/`ATE` activates its full protocol and required spawned agents, never local-only. Use `task` (`spawn_agent` in the source) for ECI/ATE roles, including reusable Fast Owner Producers and one Orchestrator per active Job, never shell-wrapped agents.
- At every `task` spawn for an ECI/ATE role: apply SKILL.md Model selection, including its spawn paths and refusal rule; name the router and the role's module paths (SKILL.md, Module routing) in the worker prompt.
- Label every spawned/resumed agent. Immediately print/update the roster after spawn/resume/reassignment/scope change: `<role label>: <runtime name> [type]`.
- Every wait/status/close update uses `<role label> (<runtime name> [type])`, never a bare nickname after labeling.
- Wait only for evidence or agents needed by the next action when advancing ECI outcomes; independently verify available results and continue independent ready work. Preserve all required review/E2E evidence before the Supervisor accepts an outcome, and all outcome-owned Writer shutdowns before root teardown. Apply this dependency rule across Jobs, outcomes, and main/Fast paths, including bounded ECI under ATE.
- Outside ECI tasks, await every still-running in-scope subagent before using results; exclude closed/completed/outside agents and shell jobs/tests/background services.
- Independently verify subagent claims before relying on them.
- Subagents verify assigned work, fix in-scope blockers, and report recovery needs through ordinary handoff.

## Environment/Stop

| Resource | Value/rule |
|---|---|
| Qt; Android SDK/NDK | `~/Qt`; `~/Android` |
| Environment; LAN DNAT | `192.168.141.16`; LAN devices may connect through `192.168.0.131` ports `7000-7019`, DNATed here |
| Ollama; Bluetooth | `192.168.0.171:11434`; may use `hci1`/`hci2`, using `DBUS_SYSTEM_BUS_ADDRESS` when set |
| Changed-work scan; large scratch | Any changed-work scan, including Gitleaks, is optional and nonblocking advisory only; never a Stop prerequisite. When default temp is tmpfs, use `$TMPDIR`/`~/tmp/` for large files/objects |

- Use the completion checklist (`~/.omp/agent/eci/hooks/stop-checklist.md`) as manual guidance; completion is not an executable Stop gate.
- Treat Stop as post-response continuation, not output control: `decision: "block"` creates a continuation prompt but cannot retract a rendered response, and `suppressOutput` is unsupported.
- Never claim a Stop hook hides visible terminal output; that guarantee needs client support.

- Treat subagent output as an unreviewed PR: verify success claims by running commands yourself, verify load-bearing facts from primary sources, read every changed line, and check original requirements.
- Reject incomplete work: finish or return it. Never pass unverified subagent claims to users.

## Pending native targets

These targets are not yet native, except the last bullet, which records `eci-worker-git` as built natively. Each path above is the target:

- `~/.omp/agent/skills` is a symlink whose owner is the pre-migration runtime that owns source commit 575bb81; verify the target with `readlink ~/.omp/agent/skills`; replace it with a native skills directory.
- Skill references `references/review-investigation.md`, `references/workflow-runtime/coding-style-admission.md`, and `requesting-code-review/SKILL.md` exist on disk under `~/.omp/agent/skills/`, reached through that symlink.
- `~/.omp/agent/eci/bin/eci-worker-git` is built natively: `hooks/build-native-binaries.sh` (commits f0f918a, which added eci-worker-git, and 289f16d, which set the current names array) builds and installs `eci-command-plan`, `eci-safe-import`, `eci-hook-helper`, and `eci-worker-git`, plus `eci-command-gate-mode` on Linux hosts, into `~/.omp/agent/eci/bin/`, each with a sha256 receipt. The typed Worker Git route is unavailable during active ECI, so Workers use native Git (`agent/skills/explore-critique-implement/references/task-orchestrator.md`, section "Active-writer rows and shared-index coordination"). It is not on the launcher allowlist, so its receipt is not verified at runtime; it is built but not launcher-wired, so its typed operations and `run-once` exception are unavailable. Source: `~/.omp/agent/eci/hooks/lib/eci-worker-git-go/`.
