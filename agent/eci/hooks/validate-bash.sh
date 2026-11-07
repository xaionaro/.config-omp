#!/usr/bin/env bash
# Run the published command planner; command policy lives in Go.
set -Eeuo pipefail
# Constant deny when jq is unavailable or failed.  It names the planner and its
# provisioning script so the agent can repair the gate without another hook.
validate_bash_fallback_deny() {
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"ECI_HOOK_NATIVE_GATE_UNAVAILABLE: hook validate-bash denied the tool call because jq is unavailable or failed while reporting the eci-command-plan gate result; install jq, provision eci-command-plan with $HOME/.omp/agent/eci/hooks/build-native-binaries.sh, and retry"}}'
}
hook_deny() {
  jq -cn --arg reason "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}' || validate_bash_fallback_deny
  exit 0
}
case "${HOME:-}" in
  ''|[!/]*) hook_deny 'ECI native gate binary eci-command-plan is unavailable: HOME is unset or not absolute; set HOME to the absolute home directory and retry' ;;
esac
root="$HOME/.omp/agent/eci"
if ! . "$root/hooks/lib/eci-native-binary.sh" 2>/dev/null; then
  hook_deny "ECI native gate binary eci-command-plan is unavailable: $root/hooks/lib/eci-native-binary.sh failed to load; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
fi
if ! . "$root/hooks/lib/eci-tmp.sh" 2>/dev/null; then
  hook_deny "ECI native gate binary eci-command-plan is unavailable: $root/hooks/lib/eci-tmp.sh failed to load; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
fi
eci_install_fail_closed_trap validate-bash
if ! eci_runtime_open_verified eci-command-plan; then
  hook_deny "$ECI_RUNTIME_DENY_REASON"
fi
rc=0
"/proc/self/fd/$ECI_RUNTIME_BINARY_FD" --hook || rc=$?
# The verified descriptor is needed only for the planner call; close it before
# the exit-status classification so no later branch can hold it.
exec {ECI_RUNTIME_BINARY_FD}<&-
case "$rc" in
  0|2) ;;
  126|127) hook_deny "ECI native gate binary eci-command-plan failed to run (exit $rc); provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" ;;
  *) hook_deny "ECI native gate binary eci-command-plan returned unexpected exit $rc (signal or internal failure); rebuild it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" ;;
esac
exit "$rc"
