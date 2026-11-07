#!/usr/bin/env bash
# Run the published command planner; command policy lives in Go.
set -Eeuo pipefail
# Constant deny when jq is unavailable or failed.  It names the planner and its
# provisioning script so the agent can repair the gate without another hook.
validate_bash_fallback_deny() {
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] hook validate-bash denied the tool call because jq is unavailable or failed while reporting the eci-command-plan gate result; install jq, provision eci-command-plan with $HOME/.omp/agent/eci/hooks/build-native-binaries.sh, and retry"}}'
}
# A deny decision that cannot be written exits 2 with a stderr message: the tool
# call is blocked, and the hook never exits 1 with no output.
hook_write_failed() {
  printf '%s\n' 'validate-bash: could not write the deny decision to stdout; the tool call is blocked' >&2 || true
  exit 2
}
hook_deny() {
  jq -cn --arg reason "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}' || validate_bash_fallback_deny || hook_write_failed
  exit 0
}
# Resolve the lib directory from the script path.  A bare name (run from inside
# hooks/) has no slash and means the current directory.
case "${BASH_SOURCE[0]}" in
  */*) hook_dir_arg="${BASH_SOURCE[0]%/*}"; hook_dir_arg="${hook_dir_arg:-/}" ;;
  *) hook_dir_arg=. ;;
esac
hook_dir_abs="$(CDPATH= cd -- "$hook_dir_arg" 2>/dev/null && pwd)" || hook_deny "[ECI_HOOK_LIB_UNAVAILABLE] hook validate-bash cannot resolve its hook directory $hook_dir_arg; reinstall the ECI hooks and retry"
lib_dir="$hook_dir_abs/lib"
if ! . "$lib_dir/eci-native-binary.sh" 2>/dev/null; then
  hook_deny "[ECI_HOOK_LIB_UNAVAILABLE] hook validate-bash cannot load $lib_dir/eci-native-binary.sh; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
fi
if ! . "$lib_dir/eci-tmp.sh" 2>/dev/null; then
  hook_deny "[ECI_HOOK_LIB_UNAVAILABLE] hook validate-bash cannot load $lib_dir/eci-tmp.sh; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
fi
if ! . "$lib_dir/eci-proof-state.sh" 2>/dev/null; then
  hook_deny "[ECI_HOOK_LIB_UNAVAILABLE] hook validate-bash cannot load $lib_dir/eci-proof-state.sh; reinstall the ECI hooks and retry"
fi
# Every lib function this hook calls must be defined; a partial load would let
# a missing marker check fall through to a silent exit.
for fn in eci_runtime_open_verified eci_install_fail_closed_trap eci_marker_active_or_unknown; do
  declare -F "$fn" >/dev/null 2>&1 || hook_deny "[ECI_HOOK_LIB_UNAVAILABLE] hook validate-bash: ECI library function $fn is not defined; reinstall the ECI hooks and retry"
done
# The proof root is $HOME-rooted (eci_configured_proof_root) unless ECI_PROOF_ROOT
# is set, and the native artifact path requires an absolute HOME
# (eci_runtime_artifact_path).  When HOME is empty, unset or relative AND
# ECI_PROOF_ROOT is not absolute, the proof root is unknown: deny regardless of
# marker presence.  This blocks Bash in a broken HOME environment, by design.
# With an absolute ECI_PROOF_ROOT the marker logic below decides.
case "${HOME:-}" in
  /*) ;;
  *)
    case "${ECI_PROOF_ROOT:-}" in
      /*) ;;
      *) hook_deny "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] hook validate-bash denied the tool call because HOME is empty, unset or relative and ECI_PROOF_ROOT is not an absolute path, so the proof root is unknown; set HOME to an absolute path, or set ECI_PROOF_ROOT to an absolute path, and retry" ;;
    esac
    ;;
esac
# Native-gate failures deny only while a marker is active or unknown; with no
# marker the hook exits with the source status, as the source does.
native_gate_fail() {
  local code="$1"
  if ! eci_marker_active_or_unknown; then
    exit "$code"
  fi
  hook_deny "$2"
}
eci_install_fail_closed_trap validate-bash
if ! eci_runtime_open_verified eci-command-plan; then
  native_gate_fail 0 "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] $ECI_RUNTIME_DENY_REASON"
fi
rc=0
"$ECI_RUNTIME_BINARY_EXEC" --hook || rc=$?
# The verified descriptor (Linux) or private copy (macOS) is needed only for the planner call;
# release it before the exit-status classification so no later branch can hold it.
eci_runtime_close_verified
case "$rc" in
  0|2) ;;
  126|127) native_gate_fail "$rc" "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-command-plan failed to run (exit $rc); provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" ;;
  *) native_gate_fail "$rc" "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-command-plan returned unexpected exit $rc (signal or internal failure); rebuild it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" ;;
esac
exit "$rc"
