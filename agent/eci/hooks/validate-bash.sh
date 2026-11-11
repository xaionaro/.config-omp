#!/usr/bin/env bash
# Run the verified command planner; failures must not admit an unchecked call.
# Every failure names the step the same actor takes to repair it.
set -Eeuo pipefail
hook_unavailable() {
  printf '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] %s\n' "$1" >&2 || true
  exit 2
}
hook_source_repair='Read the named file under $HOME/.omp/agent/eci/hooks, restore or fix it with Edit or Write (or git checkout -- <path> when Bash is available), then retry'
trap 'hook_unavailable "hooks/validate-bash.sh failed at line $LINENO before reaching a decision; $hook_source_repair"' ERR
case "${BASH_SOURCE[0]}" in
  */*) hook_dir_arg="${BASH_SOURCE[0]%/*}"; hook_dir_arg="${hook_dir_arg:-/}" ;;
  *) hook_dir_arg=. ;;
esac
hook_dir_abs="$(CDPATH= cd -- "$hook_dir_arg" 2>/dev/null && pwd)" ||
  hook_unavailable "cannot resolve the hook directory $hook_dir_arg; invoke validate-bash.sh by its absolute path and retry"
. "$hook_dir_abs/lib/eci-bash-guard.sh" PreToolUse 2>/dev/null ||
  hook_unavailable "cannot load the Bash version guard lib/eci-bash-guard.sh; $hook_source_repair"
. "$hook_dir_abs/lib/eci-native-binary.sh" 2>/dev/null ||
  hook_unavailable "cannot load the native binary verifier lib/eci-native-binary.sh; $hook_source_repair"
declare -F eci_runtime_verexec >/dev/null 2>&1 ||
  hook_unavailable "native binary verifier lib/eci-native-binary.sh does not define eci_runtime_verexec; $hook_source_repair"
case "${HOME:-}" in
  /*) ;;
  *) hook_unavailable "HOME must be absolute; set HOME to the account's absolute home directory and retry" ;;
esac
umask 077
output_file="$(mktemp "${TMPDIR:-/tmp}/eci-bash-gate.XXXXXXXX")" ||
  hook_unavailable "cannot create a private decision file in ${TMPDIR:-/tmp}; set TMPDIR to a writable directory you own and retry"
trap 'rm -f -- "$output_file"' EXIT
if eci_runtime_verexec eci-command-plan - -- --hook >"$output_file"; then
  cat -- "$output_file" || hook_unavailable "cannot deliver the planner decision; run the hook with stdout connected to a pipe or file the caller reads, then retry"
  exit 0
else
  if [ -n "${ECI_RUNTIME_DENY_REASON:-}" ]; then
    hook_unavailable "$ECI_RUNTIME_DENY_REASON"
  fi
  hook_unavailable "the native command planner exited with a failure; follow the repair named in its preceding diagnostic, or rebuild it with \"$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" eci-command-plan and retry"
fi
