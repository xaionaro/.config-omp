# TEST-DISPOSITION: hooks/tests, per-file disposition against source 575bb81 (kept, renamed, excluded with reason)

Source: `git -C ~/.codex archive 575bb81 hooks/tests` (134 files: 53 under `fixtures/`, 77 top-level `*.sh` including `run.sh`, the `eci-hook-e2e-go/` Go module, and its README).
Destination: `agent/eci/hooks/tests` (133 files, 53 under `fixtures/`). One source file is excluded and listed below.

## Isolation

Run every test through `run-isolated.sh <test-file>`. It copies the runtime to `<tmp>/home/.omp/agent/eci` and runs the test from that copy, so `ROOT` matches the HOME-derived path. HOME is `<tmp>/home`, so the context root (`$HOME/.agent/context`) and proof root (`$HOME/tmp/agent/proof`, also `ECI_PROOF_ROOT`) resolve under `<tmp>` and are removed on exit. `~/.kimi-code` is a symlink to the real peer tree. GOPATH, GOMODCACHE, and GOCACHE stay on account paths. Running a test directly writes to the real account HOME (for example `test-eci-structured-log.sh` and `test-eci-understanding-yaml.sh` write `$HOME/.agent/context/sessions/<id>`).

Measured on one `run.sh` run each: symlinked runtime, `SUMMARY pass=139 fail=65`, real `t00-session` log rewritten once (12:37:41, `wrong-cwd probe` line); copied runtime, `SUMMARY pass=115 fail=89`, real `t00-session` log unchanged. Copy-only failures include `pre-commit-go-mod.sh` byte checks whose log paths show `/mnt/nvme0n1/home` while HOME is `/home/pheona`, because `/home` is a symlink to `/mnt/nvme0n1/home`. The `validate-bash ... make without memory cap` failures are not diagnosed.

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
- Paths absent from HEAD `agent/eci`: `hooks/stop-gate.sh`, `hooks.json`, `AGENTS.md`, `hooks/lib/eci-runtime-sync.sh`, `hooks/lib/eci-environment-command.sh`, `hooks/lib/eci-command-plan-go/eci-command-plan`, several `skills/*`.
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
