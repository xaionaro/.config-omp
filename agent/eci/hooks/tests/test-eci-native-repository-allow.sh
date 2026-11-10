#!/usr/bin/env bash
set -euo pipefail

runtime="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
scratch="$(mktemp -d "${ECI_TMPDIR:-$HOME/tmp}/eci-native-allow.XXXXXX")"
trap 'rm -rf -- "$scratch"' EXIT
mkdir -p "$scratch/proof/session" "$scratch/proof/other" "$scratch/work" "$scratch/dependency"
git -C "$scratch/work" init -q
git -C "$scratch/dependency" init -q
work="$(eci_realpath_existing "$scratch/work")"
dependency="$(eci_realpath_existing "$scratch/dependency")"
export ECI_PROOF_ROOT="$scratch/proof" ECI_SESSION_ID=session
cd -- "$work"
"$runtime/bin/eci-active" repository-allow-on "$dependency" 'dependency repair'
"$runtime/bin/eci-active" repository-allow-status | grep -Fq -- "$dependency"
if "$runtime/bin/eci-active" repository-allow-on "$dependency" again; then
  echo 'Existing record overwritten' >&2; exit 1
fi
if ECI_SESSION_ID=other "$runtime/bin/eci-active" repository-allow-off "$dependency"; then
  echo 'Other session removed allowance' >&2; exit 1
fi
"$runtime/bin/eci-active" repository-allow-off "$dependency"
"$runtime/bin/eci-active" repository-allow-status | grep -Fq inactive


# The hook runs under a scratch HOME. The native gate loads eci-command-plan only
# from $HOME/.omp/agent/eci/bin with its .sha256 receipt, so provision that HOME
# with the real build step. The coordinator role comes from a main-session
# transcript under $HOME/.omp/agent/sessions; without one the hook classifies the
# caller as a worker.
scratch_home="$scratch/home"
mkdir -p "$scratch_home/.omp/agent/eci" "$scratch_home/.omp/agent/sessions/-native-allow-"
cp -a "$runtime/hooks" "$scratch_home/.omp/agent/eci/"
GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" HOME="$scratch_home" \
  bash "$scratch_home/.omp/agent/eci/hooks/build-native-binaries.sh" >/dev/null
transcript="$scratch_home/.omp/agent/sessions/-native-allow-/main.jsonl"
jq -cn --arg cwd "$work" '{type:"session",version:3,id:"session",cwd:$cwd}' >"$transcript"
printf '%s\n' '{"type":"message","message":{"role":"user","content":"native allowance"}}' >>"$transcript"
printf 'scope: test\ncwd: %s\nsession_id: session\n' "$work" > "$scratch/proof/session/eci_active"
run_hook() {
  jq -cn --arg cwd "$work" --arg command "bash $dependency/check.sh" --arg transcript "$transcript" \
    '{session_id:"session",cwd:$cwd,transcript_path:$transcript,tool_name:"Bash",tool_input:{command:$command}}' |
    HOME="$scratch_home" ECI_PROOF_ROOT="$scratch/proof" \
      bash "$scratch_home/.omp/agent/eci/hooks/validate-bash.sh"
}
printf '#!/bin/sh\nexit 0\n' > "$dependency/check.sh"
if [ -n "$(run_hook)" ]; then echo 'Dependency script unexpectedly denied' >&2; exit 1; fi
printf '%s\n' 'Native repository allowance: PASS'
