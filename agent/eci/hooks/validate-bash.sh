#!/usr/bin/env bash
# Run the published command planner; command policy lives in Go.
set -euo pipefail
case "${HOME:-}" in ''|[!/]*) exit 0 ;; esac
root="$HOME/.omp/agent/eci"
[ -d "$root" ] || exit 0
source "$root/hooks/lib/eci-native-binary.sh" || exit 0
binary="$(eci_runtime_artifact_path eci-command-plan)" || exit 0
eci_runtime_binary_is_native "$binary" || exit 0
exec {planner_fd}<"$binary" || exit 0
exec "/proc/self/fd/$planner_fd" --hook
