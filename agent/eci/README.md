# ECI runtime: host requirements

These are the tools the ECI runtime under `~/.omp/agent/eci` needs on the host. The omp
hook adapters (`agent/hooks-disabled/{pre,post}/eci-*.ts` until enablement, then
`agent/hooks/`) run each hook as `timeout … bash <script>`. Both `timeout` and `bash`
are resolved through omp's `PATH`; the script's shebang is not used. `hooks.json`
launches (`bash -c`, `bash -lc 'exec "$script"'`) reach the scripts through their
`#!/usr/bin/env bash` shebang, so they too take the first `bash` on `PATH`.

## bash 4.4 or newer for hooks

The hook entry points (`hooks/validate-bash.sh`, `hooks/pretooluse-edit-dispatch.sh`,
`hooks/session-snapshot.sh`, `hooks/prompt-task-reminder.sh`,
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
```

`eci-active status`, and the launcher and native-tool provisioning behind it, run on
bash 3.2.

## timeout

The adapters need `timeout` on omp's `PATH`. Stock macOS has none, and Homebrew
coreutils installs it as `gtimeout` unless its `gnubin` directory comes first:

```sh
brew install coreutils
export PATH="$(brew --prefix)/opt/coreutils/libexec/gnubin:$PATH"
```

Without it, every hook fails to start and PreToolUse blocks the tool call (fail-closed).

## Go, sha256 and BSD userland

- Go: `go` on `PATH`, at least the newest `go` directive in `hooks/lib/*/go.mod` and
  `tools/*/go.mod`. Native binaries are built on first use by
  `hooks/build-native-binaries.sh`.
- Apart from `timeout`, the hook libraries need no GNU coreutils: they take their
  `sha256sum`, `realpath` and `stat` forms from `hooks/lib/eci-portable.sh`.
