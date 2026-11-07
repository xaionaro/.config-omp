#!/usr/bin/env bash
set -euo pipefail

runtime="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
scratch="$(mktemp -d "${ECI_TMPDIR:-$HOME/tmp}/eci-native-allow.XXXXXX")"
trap 'rm -rf -- "$scratch"' EXIT
mkdir -p "$scratch/proof/session" "$scratch/proof/other" "$scratch/work" "$scratch/dependency"
git -C "$scratch/work" init -q
git -C "$scratch/dependency" init -q
work="$(realpath -e -- "$scratch/work")"
dependency="$(realpath -e -- "$scratch/dependency")"
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

mkdir -p "$scratch/home/.omp/agent/eci" "$scratch/home/tmp/eci-runtime/eci"
cp -a "$runtime/hooks" "$scratch/home/.omp/agent/eci/"
(cd "$runtime/hooks/lib/eci-command-plan-go" && go build -o "$scratch/home/tmp/eci-runtime/eci/eci-command-plan" .)
printf 'scope: test\ncwd: %s\nsession_id: session\n' "$work" > "$scratch/proof/session/eci_active"
run_hook() {
  jq -cn --arg cwd "$work" --arg command "bash $dependency/check.sh" \
    '{session_id:"session",cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}' |
    HOME="$scratch/home" ECI_PROOF_ROOT="$scratch/proof" ECI_HOOK_IS_SUBAGENT=false \
      bash "$scratch/home/.omp/agent/eci/hooks/validate-bash.sh"
}
printf '#!/bin/sh\nexit 0\n' > "$dependency/check.sh"
run_hook | grep -q ECI_COORDINATOR_SCRIPT_TARGET_DENIED
"$runtime/bin/eci-active" repository-allow-on "$dependency" 'dependency repair' >/dev/null
if [ -n "$(run_hook)" ]; then echo 'Declared script remains denied' >&2; exit 1; fi
"$runtime/bin/eci-active" repository-allow-off "$dependency" >/dev/null
run_hook | grep -q ECI_COORDINATOR_SCRIPT_TARGET_DENIED
printf '%s\n' 'Native repository allowance: PASS'
