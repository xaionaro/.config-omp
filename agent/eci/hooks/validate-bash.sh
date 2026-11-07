#!/usr/bin/env bash
# Run the published command planner; command policy lives in Go.
set -Eeuo pipefail
# Constant deny when jq is unavailable or failed.  It names the planner and its
# provisioning script so the agent can repair the gate without another hook.
validate_bash_fallback_deny() {
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] hook validate-bash denied the tool call because jq is unavailable or failed while reporting the eci-command-plan gate result; install jq, provision eci-command-plan with $HOME/.omp/agent/eci/hooks/build-native-binaries.sh, and retry"}}'
}
hook_deny() {
  jq -cn --arg reason "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}' || validate_bash_fallback_deny
  exit 0
}
lib_dir="${BASH_SOURCE[0]%/*}/lib"
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
# The native artifact path is $HOME-rooted and requires an absolute HOME
# (eci_runtime_artifact_path).  An empty or relative HOME denies while a marker
# is active or unknown; with no marker it exits 0 with empty output.
case "${HOME:-}" in
  /*) ;;
  *)
    if eci_marker_active_or_unknown; then
      hook_deny "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] hook validate-bash denied the tool call because HOME is empty or relative; the native gate binary eci-command-plan lives under an absolute \$HOME/.omp/agent/eci/bin; set HOME to an absolute path and retry"
    fi
    exit 0
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
"/proc/self/fd/$ECI_RUNTIME_BINARY_FD" --hook || rc=$?
# The verified descriptor is needed only for the planner call; close it before
# the exit-status classification so no later branch can hold it.
exec {ECI_RUNTIME_BINARY_FD}<&-
case "$rc" in
  0|2) ;;
  126|127) native_gate_fail "$rc" "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-command-plan failed to run (exit $rc); provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" ;;
  *) native_gate_fail "$rc" "[ECI_HOOK_NATIVE_GATE_UNAVAILABLE] ECI native gate binary eci-command-plan returned unexpected exit $rc (signal or internal failure); rebuild it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" ;;
esac
exit "$rc"
