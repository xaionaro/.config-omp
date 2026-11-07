#!/usr/bin/env bash
# Run the published command planner; command policy lives in Go.
set -euo pipefail
hook_deny() {
  jq -cn --arg reason "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}'
  exit 0
}
case "${HOME:-}" in
  ''|[!/]*) hook_deny 'ECI native gate binary eci-command-plan is unavailable: HOME is unset or not absolute; set HOME to the absolute home directory and retry' ;;
esac
root="$HOME/.omp/agent/eci"
if ! . "$root/hooks/lib/eci-native-binary.sh" 2>/dev/null; then
  hook_deny "ECI native gate binary eci-command-plan is unavailable: $root/hooks/lib/eci-native-binary.sh failed to load; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
fi
if ! eci_runtime_open_verified eci-command-plan; then
  hook_deny "$ECI_RUNTIME_DENY_REASON"
fi
exec "/proc/self/fd/$ECI_RUNTIME_BINARY_FD" --hook
