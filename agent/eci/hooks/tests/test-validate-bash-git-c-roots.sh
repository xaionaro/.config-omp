#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${HOME:?}/tmp/eci-git-c-roots.XXXXXX")"
TMP_ROOT="$(eci_realpath_canonical "$TMP_ROOT")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export XDG_STATE_HOME="$TMP_ROOT/xdg-state"
mkdir -p "$XDG_CONFIG_HOME/eci" "$TMP_ROOT/proof/t00-session"
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"
printf '%s\n' \
  'scope: repeated git -C root test' \
  "cwd: $ROOT" \
  'session_id: t00-session' \
  'created_utc: 2026-08-23T00:00:00Z' \
  >"$TMP_ROOT/proof/t00-session/eci_active"

run_hook() {
  local command="$1" output="$TMP_ROOT/output"
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$TMP_ROOT/proof" ECI_HOME="$ROOT" PATH="$ROOT/bin:$PATH" \
      bash "$ROOT/hooks/validate-bash.sh" >"$output"
  printf '%s\n' "$output"
}

# The Bash callback never activates marker-based repository approval, so
# read-only Git stays native for repeated and for differing `-C` roots.
for command in "git -C $ROOT -C $ROOT status --short" "git -C $ROOT -C $TMP_ROOT status --short"; do
  output="$(run_hook "$command")"
  if [ -s "$output" ]; then
    printf 'read-only git -C was denied: %s\n' "$command" >&2
    cat -- "$output" >&2
    exit 1
  fi
done

printf '%s\n' 'validate-bash repeated git -C roots: PASS'
