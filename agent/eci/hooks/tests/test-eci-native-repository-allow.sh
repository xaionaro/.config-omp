#!/usr/bin/env bash
set -euo pipefail
umask 077

runtime="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
scratch="$(mktemp -d "${ECI_TMPDIR:-$HOME/tmp}/eci-native-allow.XXXXXX")"
scratch="$(eci_realpath_existing "$scratch")"
trap 'rm -rf -- "$scratch"' EXIT
mkdir -p "$scratch/proof/session" "$scratch/proof/other" "$scratch/work" "$scratch/dependency"
git -C "$scratch/work" init -q
git -C "$scratch/dependency" init -q
work="$(eci_realpath_existing "$scratch/work")"
dependency="$(eci_realpath_existing "$scratch/dependency")"
# Provision an entirely private launcher runtime before invoking eci-active.
# Keep the caller's Go caches while all runtime and XDG writes stay in scratch.
GOCACHE="$(go env GOCACHE)"
GOMODCACHE="$(go env GOMODCACHE)"
export GOCACHE GOMODCACHE
scratch_home="$scratch/home"
fixture_runtime="$scratch_home/.omp/agent/eci"
mkdir -p "$fixture_runtime/bin" "$fixture_runtime/tools" "$scratch/config/eci" \
  "$scratch/state" "$scratch/cache" "$scratch/data" "$scratch/runtime" "$scratch/tmp"
cp -a "$runtime/hooks" "$fixture_runtime/"
cp -a "$runtime/tools/eci-active" "$fixture_runtime/tools/"
cp -- "$runtime/bin/eci-active" "$fixture_runtime/bin/eci-active"
export HOME="$scratch_home" ECI_HOME="$fixture_runtime" KIMI_CODE_HOME="$scratch_home/.kimi-code"
export XDG_CONFIG_HOME="$scratch/config" XDG_STATE_HOME="$scratch/state" XDG_CACHE_HOME="$scratch/cache" XDG_DATA_HOME="$scratch/data"
export XDG_RUNTIME_DIR="$scratch/runtime" TMPDIR="$scratch/tmp" GOTMPDIR="$scratch/tmp" ECI_TMPDIR="$scratch/tmp"
export ECI_PROOF_ROOT="$scratch/proof" ECI_SESSION_ID=session
cd -- "$work"
"$fixture_runtime/bin/eci-active" repository-allow-on "$dependency" 'dependency repair'
"$fixture_runtime/bin/eci-active" repository-allow-status | grep -Fq -- "$dependency"
if "$fixture_runtime/bin/eci-active" repository-allow-on "$dependency" again; then
  echo 'Existing record overwritten' >&2; exit 1
fi
if ECI_SESSION_ID=other "$fixture_runtime/bin/eci-active" repository-allow-off "$dependency"; then
  echo 'Other session removed allowance' >&2; exit 1
fi
"$fixture_runtime/bin/eci-active" repository-allow-off "$dependency"
"$fixture_runtime/bin/eci-active" repository-allow-status | grep -Fq inactive


# The hook runs under a scratch HOME. The native gate loads eci-command-plan only
# from $HOME/.omp/agent/eci/bin with its .sha256 receipt, so provision that HOME
# with the real build step. The coordinator role comes from a main-session
# transcript under $HOME/.omp/agent/sessions; without one the hook classifies the
# caller as a worker.
mkdir -p "$scratch_home/.omp/agent/sessions/-native-allow-"
bash "$fixture_runtime/hooks/build-native-binaries.sh" >/dev/null
transcript="$scratch_home/.omp/agent/sessions/-native-allow-/main.jsonl"
jq -cn --arg cwd "$work" '{type:"session",version:3,id:"session",cwd:$cwd}' >"$transcript"
printf '%s\n' '{"type":"message","message":{"role":"user","content":"native allowance"}}' >>"$transcript"
printf 'scope: test\ncwd: %s\nsession_id: session\n' "$work" > "$scratch/proof/session/eci_active"
printf 'enforcing\n' >"$scratch/config/eci/command-gate-mode"
run_hook() {
  jq -cn --arg cwd "$work" --arg command "${1:-bash $dependency/check.sh}" --arg transcript "$transcript" \
    '{session_id:"session",cwd:$cwd,transcript_path:$transcript,tool_name:"Bash",tool_input:{command:$command}}' |
    HOME="$scratch_home" ECI_PROOF_ROOT="$scratch/proof" XDG_CONFIG_HOME="$scratch/config" XDG_STATE_HOME="$scratch/state" \
      bash "$scratch_home/.omp/agent/eci/hooks/validate-bash.sh"
}
printf '#!/bin/sh\nexit 0\n' > "$dependency/check.sh"
output="$(run_hook)" || { echo 'Dependency callback failed' >&2; exit 1; }
[ -z "$output" ] || { echo 'Dependency script unexpectedly denied' >&2; exit 1; }
negative="$(run_hook 'rm -rf /')" || { echo 'Negative callback failed' >&2; exit 1; }
jq -e '.hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("ECI_BROAD_DESTRUCTIVE_DENIED"))' <<<"$negative" >/dev/null
printf '%s\n' 'Native repository allowance: PASS'
