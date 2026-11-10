# Hook gate escape-hatch catalog

This catalog audits hook decisions in the ECI runtime; it does not enable hooks. ECI is an
accidental-deviation aid, not an adversarial-security system. A gate may stop a
resolved accidental broad, destructive, wrong-target, wrong-owner, or
cross-scope effect. Syntax, punctuation, wrappers, hashes, receipts, and other
metadata are advisory unless they resolve to one of those effects.

The Bash `--hook` callback never activates legacy marker-based ownership
gates. Native Git and scripts remain available in any repository, subject to
independent concrete effect checks. Worker session-transcript writes, broad-root
and broad Git checks remain applicable.
When the mode tool is executable, enforcing mode forwards callback denials and
permissive mode allows them while attempting to record telemetry. An absent or
non-executable mode tool leaves the denial in force; a finalization failure blocks
the call. Direct planner and other hook paths may retain separate legacy behavior
documented below.

Bash mutation bookkeeping shares a five-second budget across repository queries,
plus a one-second bound on output pipes held by Git's children. It discovers
repository roots before scanning their baselines. For identified repositories,
a failed Git query or context/output-pipe timeout records `status_sha: unavailable` (and
`head: unavailable` if HEAD was not read), warns, and remains advisory. Later
writes preserve that first record instead of rescanning. Git uses no optional
locks and remains in the runner-owned process group for descendant cleanup.
Forced or unconfirmed runner cleanup still blocks the command; bookkeeping does
not override that safety check.

A repository that cannot be identified within the budget has no baseline record.
The callback reports incomplete bookkeeping, not complete repository coverage.
If that repository is first identified on a later callback, its record describes
that later observation, not necessarily the session's initial state; `created_utc`
is the observation timestamp.

| Gate family | Stops only this resolved effect | Legitimate path / escape hatch |
| --- | --- | --- |
| Optional coding-skill hook (`agent/optional-hooks/coding-skill-gate.ts`) | Disabled unless separately installed as `agent/hooks/pre/coding-skill-gate.ts`; enabling the ECI hooks does not install it. For recognized Go and Python writes it requires prior completed whole reads of coding-common and the language skill. Dynamic shell/eval effects, custom tools, and unresolved `conflict://` targets are not fully mediated. AST resolve can discover files not present in its earlier preview. This is not a security boundary. | Read each named `skill://` URI, or the canonical local `SKILL.md` path, without a line range in a completed earlier tool batch; then retry. For a blocked structural preview resolve, write `xd://reject`, read the skills, then rerun the preview. If incorrect, the operator can remove the separately installed hook and restart the session. |
| Command/planner and environment checks (`ECI_COMMAND_*`, `ECI_PLAN_*`, `ECI_ENVIRONMENT_*`) | A command whose parsed target or effect is concretely unsafe, unresolved at a safety boundary, or an invalid lifecycle identity | Use the ordinary command when its effect is harmless; use the named direct lifecycle, cleanup, or verification route for the affected control operation. Parser uncertainty alone remains transparent. |
| Make memory cap (`ECI_MAKE_MEMORY_CAP_REQUIRED`) | On Linux and other non-Darwin builds, a recognized make launch lacks a finite memory cap, subject to gate mode. On Darwin this gate is disabled: the recognized `prlimit` and `systemd-run` routes are not verified macOS cap routes. | On Linux, use a suitable available finite limit, for example `prlimit --as=1073741824 -- make ...` or `systemd-run --scope -p MemoryMax=1G make ...`. The gate does not check whether either launcher is installed. On Darwin, run make normally; this gate provides no memory cap. |
| Git destructive checks (`ECI_BROAD_DESTRUCTIVE_DENIED`) | Concrete broad Git effects, subject to the gate's mode. A whole-worktree `git add -A` remains checked when no narrowing path or preview is established. Opaque scalar contents, unavailable expansion cardinality, unknown option/action identity, and generic parser uncertainty remain advisory. The blanket `git reset` denial is disabled; protection of live hooks from reset is under separate design review. | Name exact owned paths for checked operations. For reset, inspect status and affected paths, preserve unrelated work, and use native Git with the intended repository context. |
| Direct planner clone context (`ECI_GIT_EXECUTION_CONTEXT_DENIED`, `git-clone-launch-context`) | An active-marker direct planner request changes the executable or inherited environment for a Git clone launch. | Use direct `git clone`, `command [--] git clone`, or `env [--] git clone` without launcher options or environment changes. |
| Proof, ledger, marker, and live-control targets (`ECI_PROOF_PATH_ESCAPE_DENIED`, `ECI_PLAN_LIVE_CONTROL_DENIED`, `ECI_MARKER_*`, `ECI_LEDGER_*`) | These marker-dependent checks apply to active-marker direct planner requests, not the Bash callback. Edit and patch hooks have separate control-target checks. | Use the current session's canonical path and the Supervisor lifecycle route where available; do not modify another session's control state. |
| Lifecycle identity and ownership (`ECI_CONTROL_OWNER_REQUIRED`, `ECI_CONTROL_IDENTITY_DENIED`, `ECI_LIFECYCLE_*`, `ECI_COORDINATOR_*`) | Active-marker direct planner requests may check lifecycle/control identity and ownership; the Bash callback does not activate those marker-dependent gates. | The Supervisor owns lifecycle through native goals and ordinary coordination. Legacy lifecycle and session-scoped self-edit/permissive mutators are unavailable and reject without publishing state; there is no replacement marker-free escape hatch. |
| Gate-mode control (`gate-mode-identity`, `gate-mode-owner`, `gate-mode-mutation`) | A callback denial is finalized using the configured mode. Enforcing mode blocks a denied Worker mode change; permissive mode records it without blocking. | The coordinator uses the canonical gate-mode tool; the user's selected mode remains in force. |
| Worker session transcripts (`ECI_WORKER_SESSIONS_ROOT_DENIED`) | A resolved Worker write or removal reaches the omp sessions root. Ambiguous spellings and failed resolution are advisory. | Write task files outside the runtime sessions root. |
| Direct planner Worker Git (`ECI_WORKER_GIT_OWNERSHIP_DENIED`) | An active-marker direct planner request selects a Worker Git route. This is not the Bash callback path. | The typed route is built but not launcher-wired; report it unavailable rather than repeating the denied planner request. Ordinary Bash work uses the separate callback path with native Git, preserving repository context and Producer coordination. |
| Protected live-hook worktree effects | The Bash planner's protected-hook checks for Git checkout, rm and mv, and cleanup commands are disabled because they lacked bounded legitimate same-actor hatches. Reset is not checked. The Bash hook remains disabled and is not ready for safe enablement. | Inspect the affected targets and preserve unrelated work; no protected-hook denial is provided by these checks. |
| Review gates (`ECI_REVIEW_*`, `ECI_REVIEW_VERDICT_MALFORMED`) | A malformed current review target/verdict or a required review that found a substantive issue | Correct the named review input, rerun the affected independent review, or return the issue to the implementer. Missing historical evidence is reconciled without denying harmless work. |
| Go module/workspace validation (`GO_*`, `ECI_GO_*`) | A commit or install operation with malformed module/workspace state, unsafe replacement, missing required tool, or wrong destination | Fix the named module/workspace/config/tool/path issue, or use the normal relative replacement and default hook destination. These checks do not gate unrelated commands. |
| Edit/Write local `go.mod` replacements | The direct edit validator no longer rejects writes merely because their text contains a local replacement; that check also rejected existing or commented directives and missed hashline edits. The separate legacy `apply_patch` validator denies a patch targeting `go.mod` when any added line has `=>` followed by optional whitespace and `./` or `../`; it is not used by the omp edit adapter. | Follow Go policy: put local replacements in `go.work` using paths relative to that file. |
| Runtime/setup and advisory diagnostics (`ECI_TMPDIR_*`, `ECI_HOOK_FAIL_OPEN`, reminders) | No ordinary-work effect; these report unavailable or failed maintenance context | Continue harmless work and repair the runtime/setup alongside it. |
| Edit/Write/apply_patch guard failure (`ECI_EDIT_GATE_FAILED`) | With an active or unknown legacy marker, or a concretely resolved session, control or Git approval target, a failed child guard denies the call. In marker-free operation an ordinary target remains editable; an unclassified target is reported as such, not claimed safe. A healthy guard's concrete denial takes precedence. | In marker-free operation inspect and repair the named hook through an ordinary Edit or Write, then retry. If the dispatcher itself fails, use Bash to diagnose it. Active-marker failures have no equivalent Edit/Write repair hatch. The omp edit adapter remains disabled. |
| Edit routing jq or identity failure (`ECI_HOOK_JQ_FAILED`, `ECI_HOOK_IDENTITY_MALFORMED`) | An active or unknown marker can cause denial when jq cannot parse the callback or the edit identity fields are malformed. | Install or expose jq when unavailable; correct the callback's typed `tool_name`, `session_id`, and `cwd` fields, then retry in the same session. |
| Edit target inspection failure or empty result | The edit adapter blocks a patch whose targets cannot be inspected. | Correct the patch and retry. Alternatively read the exact file and use Write for the intended content; Write still runs the target guards under the same actor and session. |
| Edit/Write target-check deadline | The adapter blocks when its shared 20-second target-check budget expires; cleanup may take up to seven more seconds. | Retry in the same session or split multi-target edits. If a single target still times out, use Bash to diagnose `pretooluse-edit-dispatch.sh` and its child guards, repair the cause within your owned scope, then retry. |
| Native gate binaries (`eci-command-plan`, `eci-safe-import`, `eci-hook-helper`) | Native execution checks reject unavailable, untrusted or incompatible binaries. A missing command planner blocks the Bash callback; other hooks have their own binary requirements. | Where exposed, `eci_bash_gate_repair` probes first; only recognized buildable failures trigger `--gate-repair`, which selects and retains the affected planner or Linux verifier pair and verifies the result. It does not repair `eci-safe-import` or `eci-hook-helper`. If the native denial offers it, an actor with Bash may instead send exactly `: eci-bash-gate-repair`; approvals still apply. Unsafe ownership, symlinks, nonregular leaves, unsafe binary modes or hardlinks, and unknown failures are refused; ask the responsible owner to resolve them without losing content. A user-owned regular receipt may be group-writable and is retained with its mode. No automatic chmod is performed. |
| Retained planner publication conflict (`ECI_PLANNER_PUBLISH_UNSAFE`) | A `<transaction>/<tool>.conflict` record refuses ordinary planner publication and provisioning. | If Bash works, run `bash "$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" --preserve-conflict NAME`, using the exact named `eci-command-plan`, `eci-safe-import` or `eci-hook-helper`. A healthy gate probe does not invoke repair. For a native planner or Linux verifier failure, `eci_bash_gate_repair` uses fixed `--gate-repair`; where unavailable, send exactly `: eci-bash-gate-repair` as a Bash call when the native denial offers it. Normal denials and Bash approvals remain effective. The fixed mode repairs only recognized buildable failures, preserves existing regular user-owned pairs and refuses unsafe or unknown states. Existing non-native or unexecutable verifier failures may be indistinguishable from other execution errors and are refused. These routes use current working-tree source and change shared binaries for all sessions. Under the publication lock they retain leaves and conflict records in a new transaction before publication, report its location and leave original backups untouched. Its prefix excludes peer sync; retired markers retain source transaction names without matching active conflicts. The user manages cleanup after inspection. Retry after interruption; never remove a marker and overwrite unresolved leaves. Foreign, symlink or nonregular leaves require owner intervention. Restricted actors without a repair tool or a buildable native denial have no established same-actor route; the hook remains disabled. |
| Relative proof root (`ECI_PROOF_ROOT_RELATIVE`) | An edit or patch whose target resolves under the proof root or a context root while `ECI_PROOF_ROOT` is a relative path, so the proof root cannot be verified and the marker state is unknown. Targets outside those roots are not affected. | Set `ECI_PROOF_ROOT` to an absolute path, then retry the call. |
| Undeliverable edit or patch deny decision (`could not write the deny decision to stdout`) | An edit or patch gate is about to deny while stdout is `/dev/null` (closed by the caller), its target cannot be checked (no `/proc`), or the write fails. The call is blocked with exit 2 and a stderr message so the denial is never lost. | Run the call where stdout is a pipe or file the caller reads, then retry. |
| Bash hook runtime, transport, or cleanup failure | A missing or failed planner, malformed callback or decision, deadline, forced or unconfirmed process cleanup, or adapter exception blocks the call independently of markers or gate mode. The Bash runner uses a 20-second deadline plus up to 5 seconds for termination and 2 seconds for cleanup/drain. | For a recognized native planner or Linux verifier denial, `eci_bash_gate_repair` or the offered exact Bash route uses `--gate-repair` to build only the affected pair from the current working tree, including uncommitted edits, retaining replaced leaves before changing the shared installation for all sessions. A missing verifier can then reveal a planner failure and require a second build. Other failures have no fixed Bash route: continue non-Bash work and report them. Restricted children may not receive the tool; the Bash route requires access and approval. The user can set `tools.approval.eci_bash_gate_repair` to `prompt` or `deny`; prompting may be unavailable without a UI. |

The Bash adapter is currently disabled with the other adapters. If it has been
enabled and a runtime failure blocks repair, only the user may temporarily move
`$HOME/.omp/agent/hooks/pre/eci-bash-gate.ts` out of the discovered `hooks/`
tree, for example into `$HOME/.omp/agent/hooks-disabled/pre/` if that destination
does not already contain the file. This disables the Bash adapter, not the other
adapters; a running session may need restarting to reload extensions. This is an
extraordinary temporary bridge, not a normal escape hatch. The user restores the
adapter after the cause is repaired and verified.

## Escape-hatch contract

- Name the resolved effect and target in every user-facing denial.
- Offer the bounded legitimate route in the same message.
- Preserve the legitimate owner and session; never reroute owner-scoped dependency work merely because a target is a dependency repository.
- Do not require a permission artifact, hash, receipt, generated plan, special punctuation, or canonical spelling for ordinary work.
- If a future gate has no bounded legitimate path, disable that gate until one exists.
- Audit new gates against this catalog and update it in the same change.

## Typed Worker Git operations

For an exact Git command explicitly authorized by the user, the one-use `run-once` exception of the typed Worker Git route is unavailable because `eci-worker-git` is built but not launcher-wired; report the unavailable route instead of invoking another executable. Its contract, if provisioned, is:
Provide a nonblank reason. Use this only after the user authorizes the entire native
argument and environment context; `--user-authorized` acknowledges that authorization
and does not establish it. The CLI runs Git once, without a stored grant or automatic
retry. `--repo` supplies the initial Git `-C`; native arguments and environment may
select another repository. Shell sibling commands keep their ordinary gates.
Routine Producer ownership and Git coordination still apply outside the exact exception.

Every wrapper call, including rejected input, appends a JSONL inventory row to
`$HOME/.cache/eci/eci-worker-git.jsonl`: UTC `timestamp`, `pid`, `argc`, and
`route` (`run-once` or `typed-or-invalid`). Arguments, repository, reason and
environment are omitted; rows cannot reconstruct commands. Native streams are
preserved during normal logging. Unavailable storage adds a stderr warning and
continues the original command, validation and exit behavior.

The typed Worker Git operations below (unavailable because `eci-worker-git` is built but not launcher-wired; see the Worker Git row) take an existing `--repo` repository. Paths after `--` are literal names, including metacharacters/newlines; absolute paths, empty segments and `.`/`..` escapes are rejected. Typed operations accept no arbitrary native Git argv; the explicit user-authorized `run-once` exception above does.

| Operation | Fixed input and effect |
| --- | --- |
| `stage-content -- <files>` | Existing exact file/symlink names to index; no directory/deletion sweep. |
| `stage-removals -- <paths>` | Exact named absent tracked entries staged as removals. |
| `stage-hunks --patch-file <file> -- <paths>` | Frozen patch applied only to index; complete native preview must select only named paths, including rename source/destination. |
| `unstage -- <paths>` | HEAD to named index entries; proven unborn branch removes only named cached entries. |
| `restore --source index\|head\|<full-object-id> --destination worktree\|index\|both -- <paths>` | Explicit source/destination; index source permits worktree only. Submodule worktree recursion is disabled. Worktree restoration refuses existing ancestor symlinks, naming the selected leaf and ancestor. Follow the denial's exact recovery route. For an unresolved hook relationship, apply the stated owned visibility correction or resolve that exact lookup in place, then rerun the identical restore. Move the exact owned ancestor entry to an unused owned name with `mv -- <ancestor> <saved-alias>` only when the fresh CLI check offers that move route, then retry the selected restore. Observed absence of named hooks keeps ordinary owned move/retry available. Index-only restore remains available. |
| `remove --destination index\|worktree\|both -- <paths>` | Explicit state destination; index-only preserves live files. |
| `move -- <source> <destination>` | Two checked endpoints; destination must be absent. |
| `commit --message <text>` | Prepared index only; no path selection, automatic staging or history rewrite. Project validation hooks remain active. |

A typed request does not authorize semantic ownership of already staged content. Producers inspect the complete staged diff/tree and make their own checked append-only handoff commit after fresh same-index coordination. The Supervisor verifies that immutable range. Unsupported native selection modes require an exact supported operation, a separately assigned bounded operational route, or the exact user-authorized `run-once` exception above.

Every path is an exact file or symlink leaf; directory and sparse-directory
selection is unsupported. Worktree-affecting gitlinks are rejected because
native operations can alter unnamed administrative metadata. Observed live hook entries, referents and consumed aliases remain protected. An unresolved hook relationship stays advisory for a selected direct file; ancestor movement requires the stated visibility correction and fresh recheck. Configured proof roots' lexical entries, resolved-parent entries and existing referents, and the worktree `.git` pointer retain protection. Proof-root discovery applies only to physical writes; missing roots are supported and unexpected resolution errors name the root. Hunk preview compares complete native index stage records and preserves unrelated conflicts.

## Native tools on first use

The first `eci` or `eci-active` command after a checkout builds the native tools that the hooks verify. These are `eci-command-plan`, `eci-hook-helper`, `eci-safe-import` and `eci-worker-git`, built through `hooks/lib/eci-native-provision.sh` and `hooks/build-native-binaries.sh`. Most commands trigger it; the exceptions are `--help`, `-h`, no argument, and `maintain-planner`, which builds the pair itself. Expect one stderr line naming the tools, about 1-2 minutes of build time, then a line with the elapsed time. Only absent or host-incompatible tools are built. A tool whose bytes no longer match its receipt is not rebuilt: the hooks deny it, and `manifest-write` refuses to run it. If a present gate binary fails its check (for example a receipt mismatch), provisioning stops with the loader's denial and builds none of the other absent tools; fix or remove that binary, then rerun. Hooks never build.

Prerequisites:
- `go` 1.24 or newer on `PATH`, plus network access or a warm module cache for the first build. Without go, eci-active stops with exit 127 and names the Go version to install.
- Clone `~/.omp` with `umask 022`, the macOS default. Under `umask 002` the clone's directories are mode 0775, and the loader denies them as group-writable ("bin directory or an ancestor is not owned by the current user or root, or is group/other-writable"). To repair an existing clone, run `chmod g-w` on `~/.omp` and on the directories down to `~/.omp/agent/eci/bin`.

Check after the first use: run `manifest-write` against a scratch proof root. It builds the tools on a fresh checkout, verifies `eci-safe-import` through the loader, and runs it:

```bash
d="$(mktemp -d)" && mkdir -p "$d/proof/sess-1" && printf '{"required_critics":[]}\n' >"$d/src.json" &&
ECI_PROOF_ROOT="$(cd -P -- "$d/proof" && pwd -P)" ECI_SESSION_ID=sess-1 \
  "$HOME/.omp/agent/eci/bin/eci-active" manifest-write "$d/src.json"; echo "rc=$?"
```

Expected: `ECI required-critic manifest published: <proof>/sess-1/eci-required-critics.json`, then `rc=0`.

The typed Worker Git route is not launcher-wired; do not treat a scratch build as an available Bash callback escape hatch.
