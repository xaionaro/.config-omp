# Hook gate escape-hatch catalog

This catalog audits the enabled hook decisions in the ECI runtime. ECI is an
accidental-deviation aid, not an adversarial-security system. A gate may stop a
resolved accidental broad, destructive, wrong-target, wrong-owner, or
cross-scope effect. Syntax, punctuation, wrappers, hashes, receipts, and other
metadata are advisory unless they resolve to one of those effects.

The Bash `--hook` callback never activates legacy marker-based ownership
gates. Native Git and scripts remain available in any repository, subject to
independent concrete effect checks. Worker session-transcript writes, protected
live-hook mutations, cleanup, broad-root and broad Git checks remain applicable.
The user's gate mode finalizes callback denials. Direct planner and other hook
paths may retain separate legacy behavior documented below.

| Gate family | Stops only this resolved effect | Legitimate path / escape hatch |
| --- | --- | --- |
| Command/planner and environment checks (`ECI_COMMAND_*`, `ECI_PLAN_*`, `ECI_ENVIRONMENT_*`) | A command whose parsed target or effect is concretely unsafe, unresolved at a safety boundary, or an invalid lifecycle identity | Use the ordinary command when its effect is harmless; use the named direct lifecycle, cleanup, or verification route for the affected control operation. Parser uncertainty alone remains transparent. |
| Make memory cap (`ECI_MAKE_MEMORY_CAP_REQUIRED`) | A recognized make launch lacks a finite memory cap, subject to gate mode. | Run the same make command with a suitable finite limit, for example `prlimit --as=1073741824 -- make ...` or `systemd-run --scope -p MemoryMax=1G make ...`. |
| Git destructive/context checks (`ECI_BROAD_DESTRUCTIVE_DENIED`, `ECI_GIT_EXECUTION_CONTEXT_DENIED`, `ECI_COMPOUND_MUTATION_DENIED`) | Concrete broad Git effects and protected targets, subject to the gate's mode. A whole-worktree `git add -A` remains checked when no narrowing path or preview is established. Opaque scalar contents, unavailable expansion cardinality, unknown option/action identity, and generic parser uncertainty remain advisory. The blanket `git reset` denial is disabled because it has no equivalent bounded route; other protected-target checks remain. | Name exact owned paths for checked operations. For reset, inspect status and affected paths, preserve unrelated work, and use native Git with the intended repository context. |
| Direct planner clone context (`ECI_GIT_EXECUTION_CONTEXT_DENIED`, `git-clone-launch-context`) | An active-marker direct planner request changes the executable or inherited environment for a Git clone launch. | Use direct `git clone`, `command [--] git clone`, or `env [--] git clone` without launcher options or environment changes. |
| Proof, ledger, marker, and live-control targets (`ECI_PROOF_PATH_ESCAPE_DENIED`, `ECI_PLAN_LIVE_CONTROL_DENIED`, `ECI_MARKER_*`, `ECI_LEDGER_*`) | A write resolving to another session's control state, an escaped proof root, or a live marker/ledger/anchor | Use the current session's canonical path and the Supervisor lifecycle route. A direct Worker `mktemp -d <absolute PREFIX.XXXXXX>` resolves its existing output parent and checks the template against the same protected-control namespace; a reserved template such as `<proof-session>/eci_active.XXXXXX` retains `ECI_CONTROL_OWNER_REQUIRED`. Use an ordinary task-owned temporary-directory template, or route the resolved control-state mutation through the Supervisor. Ordinary project files and harmless reads remain transparent. Missing or stale historical records are repaired alongside work; they do not block ordinary work. |
| Lifecycle identity and ownership (`ECI_CONTROL_OWNER_REQUIRED`, `ECI_CONTROL_IDENTITY_DENIED`, `ECI_LIFECYCLE_*`, `ECI_COORDINATOR_*`) | A lifecycle/control operation aimed at the wrong provider, session, owner, target, or route | The Supervisor owns lifecycle through native goals and ordinary coordination. Legacy lifecycle and session-scoped self-edit/permissive mutators are unavailable and reject without publishing state; there is no replacement marker-free escape hatch. |
| Gate-mode control (`gate-mode-identity`, `gate-mode-owner`, `gate-mode-mutation`) | A callback denial is finalized using the configured mode. Enforcing mode blocks a denied Worker mode change; permissive mode records it without blocking. | The coordinator uses the canonical gate-mode tool; the user's selected mode remains in force. |
| Worker session transcripts (`ECI_WORKER_SESSIONS_ROOT_DENIED`) | A resolved Worker write or removal reaches the omp sessions root. Ambiguous spellings and failed resolution are advisory. | Write task files outside the runtime sessions root. |
| Direct planner Worker Git (`ECI_WORKER_GIT_OWNERSHIP_DENIED`) | An active-marker direct planner request selects a Worker Git route. This is not the Bash callback path. | The typed route is built but not launcher-wired; report it unavailable rather than repeating the denied planner request. Ordinary Bash work uses the separate callback path with native Git, preserving repository context and Producer coordination. |
| Commit-time staging (`ECI_GIT_COMMIT_STAGING_DENIED`) | A Git commit selects content outside the prepared index through known path operands, pathspec files, include/only options, or interactive selection, or a known effect-selecting option has an unresolved target at its documented boundary. Known value operands such as `git commit -m "$MESSAGE"` remain allowed. An opaque `git commit -m $message` remains advisory unless source-backed expansion establishes a protected option such as `--amend`; a same-command prefix assignment does not establish that binding. Unknown option identity, opaque scalar contents, unavailable expansion cardinality, and generic parser uncertainty alone remain advisory. The gate does not verify ownership of content already staged. | Preserve the prepared index, make effect-selecting options and targets literal/resolved, inspect the complete staged diff, then make the ordinary Producer commit. Whole-worktree `-a`/`--all` uses a separate broad-effect denial; ownership of already staged content remains a Producer/workflow check. Read-only forms and known value operands remain allowed. Dry-run previews remain read-only. |
| Protected live-hook target (`ECI_COORDINATOR_EDIT_ROUTING_REQUIRED`, `ECI_BROAD_DESTRUCTIVE_DENIED` predicate `live-hook-cleanup`) | A resolved Git worktree mutation or cleanup would overwrite or remove a live hook. | Edit tracked hook source in place and stage the exact owned changes; do not repeat the destructive operation. |
| Review gates (`ECI_REVIEW_*`, `ECI_REVIEW_VERDICT_MALFORMED`) | A malformed current review target/verdict or a required review that found a substantive issue | Correct the named review input, rerun the affected independent review, or return the issue to the implementer. Missing historical evidence is reconciled without denying harmless work. |
| Go module/workspace validation (`GO_*`, `ECI_GO_*`) | A commit or install operation with malformed module/workspace state, unsafe replacement, missing required tool, or wrong destination | Fix the named module/workspace/config/tool/path issue, or use the normal relative replacement and default hook destination. These checks do not gate unrelated commands. |
| Runtime/setup and advisory diagnostics (`ECI_TMPDIR_*`, `ECI_HOOK_FAIL_OPEN`, reminders) | No ordinary-work effect; these report unavailable or failed maintenance context | Continue harmless work and repair the runtime/setup alongside it. |
| Gate hook failure after its decision point (`ECI_HOOK_FAIL_CLOSED`, `ECI_HOOK_GATE_ERROR`), jq absent while an active ECI marker exists (`ECI_HOOK_JQ_MISSING`), an empty tool name, edit path, or patch while an active ECI marker exists, or a missing `session_id`/`cwd` while an active ECI marker exists | The Edit/Write/apply_patch or Bash call is denied; the reason names the failing hook, line, and command, or the missing identity field or jq | Inspect the named hook command and the temporary-directory/disk state, install jq if it is reported missing, then retry the call. |
| Native gate binaries (`eci-command-plan`, `eci-safe-import`, `eci-hook-helper`) | A missing, symlinked, non-native (ELF on Linux; on macOS a thin Mach-O for the host CPU type, with fat binaries denied), foreign-owned, group/other-writable, or receipt-mismatched binary under `$HOME/.omp/agent/eci/bin`. A missing command-plan artifact blocks Bash until the build step runs; prompt reminders deny until the hook helper verifies. On macOS the verified binary runs as a private copy. The loader itself runs under the stock `/bin/bash` 3.2; the rest of the hook suite needs bash 4.4 or newer (`mapfile -d`, `mapfile -t`, `exec {lock_fd}`), a known macOS limit outside this change. | Run any `eci` or `eci-active` command once (not `--help`): it builds missing or host-incompatible native tools; go 1.24 or newer must be on `PATH`. Or run `"$HOME/.omp/agent/eci/hooks/build-native-binaries.sh"`. Then retry the command. Clone `~/.omp` with `umask 022`: under `umask 002` its directories are 0775 and the loader denies them. On macOS, put bash 4.4 or newer first on `PATH` for the hooks. |
| Relative proof root (`ECI_PROOF_ROOT_RELATIVE`) | An edit or patch whose target resolves under the proof root or a context root while `ECI_PROOF_ROOT` is a relative path, so the proof root cannot be verified and the marker state is unknown. Targets outside those roots are not affected. | Set `ECI_PROOF_ROOT` to an absolute path, then retry the call. |
| Undeliverable deny decision (`could not write the deny decision to stdout`) | A gate is about to deny while stdout is `/dev/null` (closed by the caller), its target cannot be checked (no `/proc`), or the write fails. The call is blocked with exit 2 and a stderr message so the denial is never lost. Ordinary work with no active marker is unaffected when the call can be delivered; the deny path still blocks when delivery fails. | Run the call where stdout is a pipe or file the caller reads, then retry. |
| Bash hook runtime or transport failure | A missing or failed planner, malformed callback or decision, timeout, or adapter exception blocks the call independently of markers or gate mode. The Bash runner uses a 20-second deadline plus bounded process-group cleanup. | Persistent failures require repair outside the blocked Bash tool; no registered repair tool is available yet. Continue non-Bash work and report the failure. |

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

To check `eci-worker-git`, use **only a disposable scratch repository that the command creates itself, never a real repository**. Every typed operation writes the index or the worktree, and every call appends a row to `$HOME/.cache/eci/eci-worker-git.jsonl`. `--help` exits 1 by design, so its exit status proves nothing.

```bash
r="$(mktemp -d)" && git init -q "$r" && echo hi >"$r/a.txt" &&
"$HOME/.omp/agent/eci/bin/eci-worker-git" --repo "$r" stage-content -- a.txt; echo "rc=$?"; git -C "$r" diff --cached --name-only
```

Expected (observed on Linux): `eci-worker-git: completed fixed operation; inspect the full staged result before checkpoint`, `rc=0`, then `a.txt`. Remove the scratch directory afterwards.
