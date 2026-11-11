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

# Prints the hook's exit status; its stdout goes to $TMP_ROOT/output.
run_hook() {
  local command="$1" rc=0
  jq -cn --arg cwd "$ROOT" --arg command "$command" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$TMP_ROOT/proof" ECI_HOME="$ROOT" PATH="$ROOT/bin:$PATH" \
      bash "$ROOT/hooks/validate-bash.sh" >"$TMP_ROOT/output" || rc=$?
  printf '%s\n' "$rc"
}

# The Bash callback never activates marker-based repository approval, so
# read-only Git stays native for repeated and for differing `-C` roots.
for command in "git -C $ROOT -C $ROOT status --short" "git -C $ROOT -C $TMP_ROOT status --short"; do
  rc="$(run_hook "$command")"
  if [ "$rc" != 0 ] || [ -s "$TMP_ROOT/output" ]; then
    printf 'read-only git -C was not allowed (rc=%s): %s\n' "$rc" "$command" >&2
    cat -- "$TMP_ROOT/output" >&2
    exit 1
  fi
done

printf '%s\n' 'validate-bash repeated git -C roots: PASS'
