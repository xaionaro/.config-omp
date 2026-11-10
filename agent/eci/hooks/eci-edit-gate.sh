#!/usr/bin/env bash
# omp edit gate launcher: runs the receipt-verified eci-hook-helper edit-gate mode (line 3: bash guard).
case "${BASH_SOURCE[0]}" in */*) . "${BASH_SOURCE[0]%/*}/lib/eci-bash-guard.sh" PreToolUse ;; *) . ./lib/eci-bash-guard.sh PreToolUse ;; esac
# The helper runs pretooluse-edit-dispatch.sh once per target of the request on stdin.
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
. "$hook_dir_abs/lib/eci-native-binary.sh" 2>/dev/null ||
  gate_unavailable "cannot load the native binary verifier"
declare -F eci_runtime_verexec >/dev/null 2>&1 ||
  gate_unavailable "native binary verifier is incomplete"
umask 077
# The stderr capture only refines the stale-helper hint; without a usable scratch
# directory the helper's stderr passes through and failures keep the generic message.
error_file=""
tmp_parent="${TMPDIR:-${ECI_TMPDIR:-/tmp}}"
if error_file="$(mktemp "${tmp_parent%/}/eci-edit-gate.XXXXXXXX" 2>/dev/null)"; then
  trap 'rm -f -- "$error_file"' EXIT
else
  error_file=""
fi
rc=0
# $BASH is this guarded interpreter; the helper runs the dispatcher with it.
if [ -n "$error_file" ]; then
  eci_runtime_verexec eci-hook-helper - -- edit-gate "$BASH" "$hook_dir_abs/pretooluse-edit-dispatch.sh" 2>"$error_file" || rc=$?
  cat -- "$error_file" >&2 || true
else
  eci_runtime_verexec eci-hook-helper - -- edit-gate "$BASH" "$hook_dir_abs/pretooluse-edit-dispatch.sh" || rc=$?
fi
case "$rc" in
  0) exit 0 ;;
  125) [ -z "${ECI_RUNTIME_DENY_REASON:-}" ] || gate_unavailable "$ECI_RUNTIME_DENY_REASON" ;;
  # A verified helper built before the edit-gate mode rejects the unknown mode with a silent exit 2.
  2) [ -z "$error_file" ] || [ -s "$error_file" ] || gate_unavailable "the verified eci-hook-helper does not support edit-gate (built before it was added); rebuild it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" eci-hook-helper and retry" ;;
esac
gate_unavailable "eci-hook-helper edit-gate exited $rc"
