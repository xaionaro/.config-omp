# TEST-DISPOSITION: hooks/tests, per-file disposition against source 575bb81 (kept, renamed, excluded with reason)

Source: `git -C ~/.codex archive 575bb81 hooks/tests` (134 files: 53 under `fixtures/`, 77 top-level `*.sh` including `run.sh`, the `eci-hook-e2e-go/` Go module, and its README).
Destination: `agent/eci/hooks/tests` (133 files, 53 under `fixtures/`). One source file is excluded and listed below.

## Isolation

Run every test through `run-isolated.sh <test-file>`. It needs `XDG_RUNTIME_DIR` (0700) for the per-run work area, because the planner refuses a bin directory whose ancestor is group- or other-writable, and `~/tmp` is group-writable. The runtime is copied to `<work>/home/.omp/agent/eci` and the test runs from that copy; `$HOME/tmp/eci-runtime` (published Go helpers) is copied too. HOME is `<work>/home`, where `<work>` is a realpath, so `pwd -P` and HOME agree. `~/.kimi-code` is a symlink to the real peer tree. Context and proof roots resolve under `<work>` and are removed on exit. GOPATH, GOMODCACHE, and GOCACHE stay on account paths. Running a test directly writes to the real account HOME.

Measured with one `run.sh` run each (SUMMARY): symlinked-runtime runner `pass=139 fail=65`, which wrote one `wrong-cwd probe` line into the real `t00-session` log; copy runner before this revision `pass=114 fail=90`; copy runner after this revision `pass=144 fail=60`, with no write to the real `t00-session` log. The copy-only failures came from the `/home` vs `/mnt/nvme0n1/home` path split (`~/tmp` is a symlink) and from a group-writable ancestor of the temp area. Both are fixed in `run-isolated.sh`.

## Exclusions

| File | Reason |
| --- | --- |
| `test-eci-runtime-sync.sh` | Tests `bin/eci-runtime-sync` and `hooks/lib/eci-runtime-sync.sh`. Both exist in source 575bb81 only; neither exists in omp `agent/eci` HEAD. Porting would mean implementing that tool, which is outside this port. The file is 683 lines with 73 `runtime-sync` references. `run.sh` does not invoke it. |

## Renames

- `codex-hook-e2e-go/` -> `eci-hook-e2e-go/`; module `codex-hook-e2e` -> `eci-hook-e2e`; Go package `codexhooke2e` -> `ecihooke2e`.
- `test-codex-home-authority.sh` -> `test-eci-home-authority.sh`
- `test-codex-runtime-single-source.sh` -> `test-eci-runtime-single-source.sh`
- `test-eci-codex-kimi-peer-sync.sh` -> `test-eci-kimi-peer-sync.sh`

## Codex-free mapping applied

- `$HOME/.codex`, `~/.codex`, `/.codex` -> `$HOME/.omp/agent/eci` (runtime root; matches `eci-native-binary.sh`, which resolves `$HOME/.omp/agent/eci/bin`).
- `/.codex/sessions` -> `/.omp/agent/sessions` (transcript store, per `hook_context.go`).
- Dotfile markers that are not the runtime root map to HEAD names, not to `.omp/agent/`: `.codex-proof-alias` -> `.eci-proof-alias` (HEAD `eci-proof-state.sh`); `.codex-runner-test.*` -> `.eci-runner-test.*` (HEAD `cleanup_route.go`).
- `CODEX.md` -> `AGENTS.md`.
- `CODEX_*` -> `ECI_*`; `codex_*` -> `eci_*`; `codex-*` -> `eci-*`; `Codex`/`CODEX` -> `ECI`; `codex` -> `eci`.
- Source names that already carried an `eci` segment (`codex_eci_*`, `eci-codex-*`) were double-mapped by the first port pass into `eci_eci_*` / `eci-eci-*`. Corrected to `eci_*` / `eci-*`, which matches HEAD production names (for example `eci_control_basename`, `eci_accidental_override_consume`, `eci_marker_metadata_is_valid`, `eci_additional_repository_is_allowed`). Affects 17 files.
- `ECI_*` names used by tests map to HEAD production names where they exist. Harness-only names (`ECI_E2E`, `ECI_FILE_LOCK_TEST_LOG`, ...) have no production reader and are test-local.
- Fixtures under `fixtures/` match the mapped source.

## Known gaps (not papered over)

- `eci-hook-e2e-go` drives an installed runtime (`ECI_E2E_BINARY`, default `/usr/bin/eci`) and copies `hooks.json`. It is skipped unless `ECI_E2E=1` and is not run by `run.sh`. HEAD has no `hooks.json`, and `/usr/bin/eci` does not exist.
- Paths absent from HEAD `agent/eci` after this revision: `hooks/stop-gate.sh` (deleted in codex `f50ca64`, which precedes 575bb81, so there is no source to port), `AGENTS.md`, `bin/eci-runtime-sync` (sourced by `hooks/lib/eci-runtime-sync.sh`; not ported), `bin/eci-active-dispatch`, `bin/context7-mcp`, `config.toml`, `hooks/lib/eci-command-plan-go/eci-command-plan`, several `skills/*`.
- Function referenced by tests but absent from HEAD `hooks/lib`: `eci_accidental_override_cleanup_command` (source had `codex_eci_accidental_override_cleanup_command`). Used by `test-lifecycle-source-authority.sh`.
- HEAD retired the lifecycle: several tests expect behavior that HEAD replaces with `[ECI_LIFECYCLE_RETIRED] Native goals own continuation; the Supervisor owns ECI lifecycle.` Those assertions fail as a result.
- `hooks/tests/test-eci-off-history-reconciliation.sh` is referenced by `test-eci-review-gate.sh` but absent from source 575bb81. Carried over unchanged; the reference is unresolved.
- `test-stop-loop-guidance.sh` appears only as expected-output text in `test-eci-review-gate.sh`; no such file exists in source or port.

## e2e run (`eci-hook-e2e-go`)

Command (from `eci-hook-e2e-go/README.md`, run from `~/.omp/agent/eci`, `TMPDIR=$HOME/tmp/mtlog GOPROXY=off GOTOOLCHAIN=local GOWORK=off`):

```sh
ECI_E2E=1 ECI_E2E_EVIDENCE="$HOME/tmp/eci-hook-e2e-<ts>" \
  "$HOME/.omp/agent/eci/bin/owned-command" -deadline 90s \
  -receipt "$HOME/tmp/eci-hook-e2e-receipt-<ts>.json" -- \
  go -C hooks/tests/eci-hook-e2e-go test -v -count=1
```

Result: FAIL (exit 1), before any runtime is started:

```
=== RUN   TestRegisteredBashHook
    e2e_test.go:49: copy fixture: exit status 1: cp: cannot stat '/home/pheona/.omp/agent/eci/hooks.json': No such file or directory
--- FAIL: TestRegisteredBashHook (0.78s)
FAIL
exit status 1
FAIL	eci-hook-e2e	0.808s
```

Cause: HEAD `agent/eci` has no `hooks.json`, and `/usr/bin/eci` is absent. Not worked around.

## Per-file run after port

Each `hooks/tests/*.sh` was run once with `bash <file>` from `~/.omp/agent/eci` (900 s cap per file, `TMPDIR=$HOME/tmp/mtlog`). Logs are in `~/tmp/hooktests-results/` (not committed).

Totals (first run, before the doubled-prefix fix): 77 top-level `*.sh` files; 9 exit 0; 68 non-zero.

`run.sh` (suite entry, exit 1): `SUMMARY pass=144 fail=60 xfail=0 xpass=0 todo=0`.

Exit 0 (9): `test-eci-aggregate-commit-tree-cas`, `test-eci-control-alias-target`, `test-eci-marker-scope`, `test-forecast-yaml`, `test-home-tmp-policy`, `test-policy-design-boundary`, `test-pre-commit-go-mod`, `test-status-report-launcher`, `test-stop-ordinary-continuation`.

Re-run after the doubled-prefix fix (the 17 files that changed; one run each):

| File | Exit | Observed output |
| --- | --- | --- |
| `test-eci-aggregate-commit-tree-cas` | 0 | — |
| `test-eci-marker-scope` | 0 | — |
| `test-eci-coordinator-edit-gate` | 1 | gomod local-replace deny on a normal coordinator repo edit |
| `test-eci-coordinator-edit-hatch` | 126 | no output |
| `test-eci-edit-control-paths` | 1 | `write target was not preserved in denial` |
| `test-eci-fast-path` | 1 | no output |
| `test-eci-file-lock` | 1 | no output |
| `test-eci-home-authority` | 1 | `validate-bash retains a non-diagnostic ECI_HOME reference` |
| `test-eci-marker-metadata-advisory` | 1 | `ECI_MALFORMED_HOOK_IDENTITY` deny on ordinary work |
| `test-eci-repository-allow` | 1 | `cp: cannot stat .../hooks.json` |
| `test-eci-review-gate-least-restriction` | 1 | `stale history blocked the current in-repository diff`; fixture copy also lacks `hooks/lib/eci-native-binary.sh` |
| `test-eci-review-gate` | 2 | `ECI_LIFECYCLE_RETIRED` |
| `test-eci-structured-log` | 2 | `ECI_LIFECYCLE_RETIRED` |
| `test-eci-understanding-yaml` | 2 | `ECI_LIFECYCLE_RETIRED` |
| `test-eci-wait-repair` | 2 | no output |
| `test-lifecycle-source-authority` | 1 | `eci_accidental_override_cleanup_command: command not found` |
| `test-validate-bash-classifier` | 1 | `ledger append unexpectedly accepted a marker from another cwd` |

Not re-run (first run only, 53 files):

- Exit 1 (42): `run`, `test-command-hook-go`, `test-coordinator-protected-git-target`, `test-eci-active-normal-git`, `test-eci-active-readonly-visibility`, `test-eci-active-sync-runtime-local-first`, `test-eci-command-plan`, `test-eci-command-syntax-gating`, `test-eci-diagnostic-specificity`, `test-eci-dynamic-pipeline-visibility`, `test-eci-environment-command`, `test-eci-explicit-user-overrides`, `test-eci-kimi-peer-sync`, `test-eci-lifecycle-visibility`, `test-eci-maintain-planner-local-first`, `test-eci-maintain-planner`, `test-eci-malformed-input-parity`, `test-eci-marker-retirement-cli`, `test-eci-native-repository-allow`, `test-eci-permissive-state-advisory`, `test-eci-planner-fd-handoff`, `test-eci-planner-protocol-fallback`, `test-eci-post-compact-refresh`, `test-eci-provider-dispatch`, `test-eci-readonly-pipeline`, `test-eci-runtime-single-source`, `test-go-mod-hook-parity`, `test-normal-git-admission`, `test-pretooluse-configured-consumers`, `test-pretooluse-edit-dispatch-json`, `test-repository-default-git-latency`, `test-security-reminder-opt-in`, `test-sentry-iterate-skill`, `test-session-snapshot-refresh`, `test-stop-gitleaks-advisory`, `test-stop-hook-trust-stale-red`, `test-system-tmp-diagnostic-order`, `test-validate-bash-git-approvals`, `test-validate-bash-git-c-roots`, `test-validate-bash-git-normalization`, `test-worker-git-cli`, `test-workflow-skill-routing`.
- Exit 2 (7): `test-eci-aggregate-coordination-residue`, `test-eci-aggregate-git-independence`, `test-eci-aggregate-lifecycle-normalization`, `test-eci-safe-import-lifecycle`, `test-retired-eci-marker-hooks`, `test-status-report-reminder`, `test-status-report-trigger-guidance`.
- Exit 23 (1): `test-pretooluse-latency` (rsync of missing `AGENTS.md`, `hooks.json`).
- Exit 127 (3): `test-stop-aux-metadata-advisory`, `test-stop-history-advisory`, `test-stop-r11-recovery`. Each needs `hooks/stop-gate.sh`, which is absent from HEAD.

Not diagnosed: `test-eci-coordinator-edit-hatch`, `test-eci-fast-path`, `test-eci-file-lock`, `test-eci-wait-repair` exit non-zero with no output in their logs.

## Ported in this revision

- `hooks.json` (from 575bb81): `$HOME/.codex/hooks/` -> `$HOME/.omp/agent/eci/hooks/`; status text `Loading Codex session rules` -> `Loading ECI session rules`. The file does not enable hooks; `agent/hooks-disabled/` is untouched.
- `hooks/lib/eci-environment-command.sh` (from 575bb81): verbatim; no codex references.
- `hooks/lib/eci-runtime-sync.sh` (from 575bb81): `codex-file-lock.sh` -> `eci-file-lock.sh`; `codex_file_lock_*` -> `eci_file_lock_*`; `$HOME/.codex` and `*/.codex` -> `$HOME/.omp/agent/eci`. Deliberately kept: the provider token `codex` and the env name `CODEX_RUNTIME_ROOTS`. The provider token names the published artifact directories `~/tmp/eci-runtime/codex`, which exist on disk; renaming would republish to a new directory. This is a decision for the user. The file sources `bin/eci-runtime-sync`, which is not ported, so that source step fails.
- `hooks/stop-gate.sh`: not ported. It is absent from 575bb81 because codex commit `f50ca64` deleted it.

## Disposition after port triage

Commits cited below exist in `git log` at HEAD b3f2c90. No item here claims a pass. Items marked "unverified after b3f2c90" have no measured run.

## Known gaps (Supervisor decisions 1, 2; not ported, tests stay failing and disclosed)

- bin/eci-runtime-sync absent: tests 47, 49, 51, 116, 307 (run.sh). Reason: not ported; Supervisor decision 1.
- bin/eci-active-dispatch absent: 69, 72, 89. Reason: not ported; decision 1.
- bin/context7-mcp, eci/config.toml, eci/AGENTS.md absent: 91, 93, 95, 98, 100, 114. Reason: not ported; decision 1.
- Launchers ~/.local/bin/eci and ~/bin/eci: 85, 87. Reason: user-owned; decision 2.
- aggregate-manifest-write deferred (main.go:54, ECI_COORDINATION_DEFERRED). aggregate-review prewrite/final now exit 1 naming the unmet manifest gate (f88c626, aggregate.go). Disclosed gap; reason: the producer is not ported.

## Decisions and rewrites (Supervisor decisions 3-6)

- Engineering-goal block (143, 144 goal assertions): ported into agent/skills/explore-critique-implement/SKILL.md in migration commit e6c1edf (decision 3).
- Go-module parity (139, 140): peer comparisons removed; executable-bit check instead of mode 755; omp copy canonical (decision 4). Commits e96e4ab, eb48691.
- ECI_HOME (188): test adapted to the context root; ECI_HOME override dropped; child classified through the HOME-derived root (decision 5). Commit b3f2c90. Unverified after b3f2c90.
- Stale rewrites, each with design reason:
- 73 planner-fd greps repointed to eci_runtime_open_verified (d56ad4e).
- 81 shell-library block removed, Go binary is not sourceable (d56ad4e).
- 56, 167 transcript-unknown advisory tolerated by prefix, hook_context.go:271-305 (4fe45de).
- 142 review-policy path to agent/skills (3aaefee).
- 202 (was 203) filter result via REVIEWER_FILTER_RESULT, reviewer-filter.sh:17-23 (b3f2c90). Unverified after b3f2c90.
- 219-221 and 225-227 (same matcher rewrite): hooks.json matcher now routes to pretooluse-edit-dispatch.sh; ate-orchestrator-gate and security-reminder assertions dropped because they are no longer wired (hooks.json:49-68) (b3f2c90). Unverified after b3f2c90.
- 295 (was 302) go test admitted, classifier_test.go:6179, 6194-6197 (b3f2c90). Unverified after b3f2c90.
- 189, 190 renamed and rewritten (b3f2c90): the cwd-less deny was accidental, after cwd was added to the fixture the gate delegates; rewritten to cwd-less control denied with marker live and transcript classified main. ACCEPTED BY SUPERVISOR WITH CONDITIONS: (1) the cwd-less control case stays in the suite; (2) per-assertion old/new expectations listed under "Assertion rewrites" below. Unverified after b3f2c90.
- Fixture preconditions: 175-177, 178, 185-187 retargeted to eci sessions root (eci-proof-state.sh:2236-2240) (b3f2c90). Unverified after b3f2c90.
- 253, 256 omp main transcript added (b3f2c90). Unverified after b3f2c90.
- Harness: 178-184 read ECI_HOOK_SUBAGENT_STATUS / variable, not stdout (b3f2c90). Unverified after b3f2c90.
- Isolation: AGENTS.md link (d56ad4e) and account skills symlink (1b1266c) in run-isolated.sh; reviewer and skill-path cases depend on these. Unverified after b3f2c90.
- Counter: CASE_RECORDED dedupe, so the submodule case counts once (8e01045). The 60/144 vs 71/145 log gap was child-script output, not lost counts.
- Fixtures: cwd added to 8 gate fixtures (d56ad4e).
- 111 mkdir (d56ad4e).

## Assertion rewrites (b3f2c90, Supervisor accepted)

- 175 | `[ ! -s out ]` for short/boundary/large -> short/boundary output `subagent`, large `unknown`, trace scoped | helper publishes via variable; large over 64 KiB: hook_context.go:339, eci-proof-state.sh:2350-2377; fixtures to eci/sessions (eci-proof-state.sh:2236-2240) | run.sh 799-817. Depends on eci-hook-helper provisioned.
- 176 | status 2 -> status 1, output empty | eci-proof-state.sh:2401-2402 | run.sh 819-831
- 177 | path to eci/sessions; expectation unchanged | eci-proof-state.sh:2236-2240 | run.sh 833-847
- 178 | dir to eci/sessions; expectations unchanged | eci-proof-state.sh:2236-2240, 2350-2405 | run.sh 849-909
- 180 | stdout read -> variable via STATE_HELPER_SCRIPT; expect `subagent` kept | shell subagent vs Go unknown = D: eci-proof-state.sh:2335-2338, hook_context.go:323-324 | run.sh 737-797, 905-915
- 181 | same harness change; expect `subagent` kept | D: eci-proof-state.sh:2343-2345 vs hook_context.go:313-314 | run.sh 905-915
- 182 | same; expect `subagent` kept | D: eci-proof-state.sh:2335-2338 vs hook_context.go:323-324 | run.sh 905-915
- 183 | same; expect `unknown` kept (shell returns unknown) | eci-proof-state.sh:2322-2331 | run.sh 905-915
- 184 | same; expect `main` kept (shell returns main) | eci-proof-state.sh:2319-2348 | run.sh 905-915
- 185 | fixture to eci/sessions; expect no output kept | vacuous: eci-active-gate.sh never reads transcript_path (:87-211) | run.sh 1045
- 186 | fixture to eci/sessions; expect no output kept | same vacuity | run.sh 1062
- 187 | sessions root to eci/sessions symlink; expect no output kept | eci-proof-state.sh:2236-2240 | run.sh 1079-1082
- 188 | ECI_HOME=logical_eci, no output -> ECI_HOME dropped, eci/sessions symlink; no gate output AND status `subagent` | eci-proof-state.sh:638-650 (never reads ECI_HOME), 2236-2240 | run.sh 1095-1118
- 189 | is_pretool_deny (passed only via cwd-less malformed deny, eci-active-gate.sh:96; cwd added by d56ad4e) -> no gate output; cwd-less control denied; status `main` | eci-active-gate.sh:202-211 delegates code edits | run.sh 1120-1145
- 190 | ECI_HOME override, deny -> no gate output; status `main` via eci/sessions symlink | eci-active-gate.sh:202-211, eci-proof-state.sh:638-650 | run.sh 1147-1167
- 202 | `kept=$(filter_violations ...)` -> direct call + `$REVIEWER_FILTER_RESULT`; assertions unchanged | reviewer-filter.sh:17-23 | run.sh 1285-1302
- 219 | matcher from /eci-active-gate.sh (absent) -> /pretooluse-edit-dispatch.sh; MultiEdit assertion kept | hooks.json:49-68; pretooluse-edit-dispatch.sh:62-63, 85-88 | run.sh 1587-1597, 1607-1611
- 220 | same matcher rewrite; NotebookEdit assertion kept | hooks.json:49-68 | run.sh 1613-1617
- 221 | separate validator/security-reminder/ate-gate commands -> dispatcher wiring; security-reminder and ate-orchestrator-gate dropped (not wired) | hooks.json:49-68; dispatcher :62-63, :85-88 | run.sh 2059-2073
- 225-227 | log labels mapped by name; no separate change; covered by the 219-220 matcher rewrite | — | run.sh 1587-1617
- 253 | no transcript (worker, no shell marker) -> omp main transcript at depth 2 via transcript_path and HOME; expectations unchanged | hook_context.go:53-65, 317-320, 592-602, 725-759 | run.sh 2129-2153
- 256 | same fixture change; expectations unchanged | hook_context.go:53-65, 725-759 | run.sh 2185-2199
- 295 | expect deny "captured to a file" -> expect no output; renamed test_validate_bash_admits_bare_go_test | classifier_test.go:6179, 6194-6197; text absent from lib/eci-command-plan-go | run.sh 2530-2536, reg 3063-3064

## Still failing, not yet resolved (classified)

- 109 edit marker overflow (run.sh "Edit marker overflow remains session scoped", test-eci-fast-path.sh): design decision open. c214a8c scopes marker candidate capacity to the expected session (eci-proof-state.sh:137-180, 2120). The gate path is session-less and still reports overflow as active. The malformed sub-case (`session_id:123`, Edit) still denies ECI_HOOK_IDENTITY_MALFORMED; assertion not rewritten. Unverified after 360b0ad: run.sh line 2834 status not measured.
- 156 canonical-handoff hardlink denial: 2e3c28a denies a hardlinked canonical handoff with the proof target (classifier.go:4639-4643). Unverified after 360b0ad.
- 157 ledger append other cwd: 16e911d binds ledger-append to marker cwd (tools/eci-active/ledger.go). Unverified after 360b0ad.
- 180, 181, 182 omp status depth rule: D, shell (eci-proof-state.sh:2335-2346) vs Go (hook_context.go:313-324). Pick one contract.
- 213, 216, 217, 222, 223, 224: F+R, the routing text "Delegate repository edits" has no producer outside tests (eci-active-gate.sh:202-211). Stale expectation, not yet rewritten.
- 164 command-plan NAME=value escaped-space branch: U.
- 236, 240 relative control path reason text: U.
- 245 subagent ledger paths: U (depends on whether omp-root transcripts carry thread_spawn).
- 260, 263 bash activity writer: R, no writer in validate-bash.sh; no design citation for removal.
- 175-187 helper-backed cases: depend on eci-hook-helper being present and the isolated copy providing it; unverified.
- 311 security reminder: cascade of 307.
- 201, 203, 205, 207, 210, 212: expected to pass after AGENTS.md link; unverified after b3f2c90.
