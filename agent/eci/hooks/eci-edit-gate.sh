#!/usr/bin/env bash
# omp edit gate launcher: runs the receipt-verified eci-hook-helper edit-gate mode,
# which runs pretooluse-edit-dispatch.sh once per target of the request on stdin.
# Any launcher or helper failure exits 2 with a stderr reason, so the adapter blocks.
set -Eeuo pipefail
gate_unavailable() {
  printf '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] %s\n' "$1" >&2 || true
  exit 2
}
trap 'gate_unavailable "eci-edit-gate failed before reaching a decision"' ERR
case "${BASH_SOURCE[0]}" in
  */*) hook_dir_arg="${BASH_SOURCE[0]%/*}"; hook_dir_arg="${hook_dir_arg:-/}" ;;
  *) hook_dir_arg=. ;;
esac
hook_dir_abs="$(CDPATH= cd -- "$hook_dir_arg" 2>/dev/null && pwd)" ||
  gate_unavailable "cannot resolve the hook directory"
. "$hook_dir_abs/lib/eci-bash-guard.sh" PreToolUse 2>/dev/null ||
  gate_unavailable "cannot load the Bash version guard"
. "$hook_dir_abs/lib/eci-native-binary.sh" 2>/dev/null ||
  gate_unavailable "cannot load the native binary verifier"
declare -F eci_runtime_verexec >/dev/null 2>&1 ||
  gate_unavailable "native binary verifier is incomplete"
rc=0
# $BASH is this guarded interpreter; the helper runs the dispatcher with it.
eci_runtime_verexec eci-hook-helper - -- edit-gate "$BASH" "$hook_dir_abs/pretooluse-edit-dispatch.sh" || rc=$?
case "$rc" in
  0) exit 0 ;;
  125) [ -z "${ECI_RUNTIME_DENY_REASON:-}" ] || gate_unavailable "$ECI_RUNTIME_DENY_REASON" ;;
esac
gate_unavailable "eci-hook-helper edit-gate exited $rc; a helper built before the edit-gate mode rejects it with exit 2, so rebuild it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" eci-hook-helper and retry"
