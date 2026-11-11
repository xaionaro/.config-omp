# ECI runtime: host requirements

These are the tools the ECI runtime under `~/.omp/agent/eci` needs on the host. The omp
hook adapters (`agent/hooks-disabled/{pre,post}/eci-*.ts` until enablement, then
`agent/hooks/`) run each hook as `<bash> <script>` and enforce its deadline themselves.
`<bash>` is `$ECI_BASH` when that is set to an absolute path, else the first `bash` on
omp's `PATH`; the script's shebang is not used. `hooks.json` launches (`bash -c`,
`bash -lc 'exec "$script"'`) reach the scripts through their `#!/usr/bin/env bash`
shebang, so they take the first `bash` on `PATH`.

## bash 4.4 or newer for hooks

The hook entry points (`hooks/validate-bash.sh`, `hooks/pretooluse-edit-dispatch.sh`,
`hooks/eci-edit-gate.sh`, `hooks/session-snapshot.sh`, `hooks/prompt-task-reminder.sh`,
`hooks/eci-post-compact-refresh.sh`) need bash 4.4 or newer. Stock macOS ships bash 3.2
as `/bin/bash`. Under an older bash, every hook answers with one
`[ECI_HOOK_BASH_TOO_OLD]` message (`hooks/lib/eci-bash-guard.sh`): a PreToolUse hook
denies the tool call, and SessionStart and UserPromptSubmit show the message as context.

On macOS:

```sh
brew install bash
# Put Homebrew's bin first on the PATH that omp starts with, e.g. in ~/.zprofile:
export PATH="$(brew --prefix)/bin:$PATH"
bash --version   # must report 4.4 or newer
# Or point the adapters at it directly:
export ECI_BASH="$(brew --prefix)/bin/bash"
```

`eci-active status`, and the launcher and native-tool provisioning behind it, run on
bash 3.2.

## Go, sha256 and BSD userland

- Go: `go` on `PATH`, at least the newest `go` directive in `hooks/lib/*/go.mod` and
  `tools/*/go.mod`. `hooks/build-native-binaries.sh` is the only builder of the native
  tools (`eci-command-plan`, `eci-safe-import`, `eci-hook-helper`, `eci-worker-git`, and
  `eci-verexec` on Linux). `bin/eci-active` provisions missing native tools
  (`hooks/lib/eci-native-provision.sh`). The optional coding-skill and Bash survivor
  bridges and the disabled edit-hook bridge provision a missing shared helper and,
  on Linux, verifier. The edit launcher verifies the helper receipt; the coding-skill
  and Bash survivor bridges invoke the helper directly. Provisioning does not enable
  any hook. A source change
  does not refresh an existing binary: rebuild it with
  `"$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" eci-hook-helper`.
  Hook provisioning inherits `GOCACHE` and `GOMODCACHE` when set; otherwise it
  resolves them with `go env` using the account home, not the hook's `HOME`.
- No GNU coreutils are needed: the hook libraries take their `sha256sum`, `realpath`
  and `stat` forms from `hooks/lib/eci-portable.sh`, and the adapters need no `timeout`.

## Running the hook tests

Run each test through `hooks/tests/run-isolated.sh <test-file>`. It needs
`XDG_RUNTIME_DIR` set to a private work area: every ancestor owned by you or root and
not group- or other-writable, or the run is refused. Linux sessions usually provide
`/run/user/<uid>`. macOS sets no `XDG_RUNTIME_DIR`, and `/tmp` there is world-writable,
so use a private directory under the per-user `$TMPDIR` (`/var/folders/.../T`):

```sh
export XDG_RUNTIME_DIR="$(mktemp -d "$TMPDIR/eci-xdg.XXXXXX")"
```
