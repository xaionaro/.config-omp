#!/usr/bin/env bash
# Run the verified command planner; failures must not admit an unchecked call.
set -Eeuo pipefail
hook_unavailable() {
  printf '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] %s\n' "$1" >&2 || true
  exit 2
}
trap 'hook_unavailable "validate-bash failed before reaching a decision"' ERR
case "${BASH_SOURCE[0]}" in
  */*) hook_dir_arg="${BASH_SOURCE[0]%/*}"; hook_dir_arg="${hook_dir_arg:-/}" ;;
  *) hook_dir_arg=. ;;
esac
hook_dir_abs="$(CDPATH= cd -- "$hook_dir_arg" 2>/dev/null && pwd)" ||
  hook_unavailable "cannot resolve the hook directory"
. "$hook_dir_abs/lib/eci-bash-guard.sh" PreToolUse 2>/dev/null ||
  hook_unavailable "cannot load the Bash version guard"
. "$hook_dir_abs/lib/eci-native-binary.sh" 2>/dev/null ||
  hook_unavailable "cannot load the native binary verifier"
declare -F eci_runtime_verexec >/dev/null 2>&1 ||
  hook_unavailable "native binary verifier is incomplete"
case "${HOME:-}" in
  /*) ;;
  *) hook_unavailable "HOME must be absolute" ;;
esac
umask 077
output_file="$(mktemp "${TMPDIR:-/tmp}/eci-bash-gate.XXXXXXXX")" ||
  hook_unavailable "cannot create a private decision file"
trap 'rm -f -- "$output_file"' EXIT
if eci_runtime_verexec eci-command-plan - -- --hook >"$output_file"; then
  cat -- "$output_file" || hook_unavailable "cannot deliver the planner decision"
  exit 0
else
  hook_unavailable "the native command planner failed or is unavailable"
fi
