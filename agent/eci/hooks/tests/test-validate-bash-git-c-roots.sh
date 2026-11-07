#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_ROOT="$(mktemp -d "${HOME:?}/tmp/eci-git-c-roots.XXXXXX")"
TMP_ROOT="$(realpath -m -- "$TMP_ROOT")"
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
    '{session_id:"t00-session",cwd:$cwd,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$TMP_ROOT/proof" ECI_HOME="$ROOT" PATH="$ROOT/bin:$PATH" \
      bash "$ROOT/hooks/validate-bash.sh" >"$output"
  printf '%s\n' "$output"
}

# A repeated approved canonical root stays on the bounded read-only path.
allowed_output="$(run_hook "git -C $ROOT -C $ROOT status --short")"
if [ -s "$allowed_output" ]; then
  cat -- "$allowed_output" >&2
  exit 1
fi

# Every leading `-C` must be approved. The rejected second root must not
# fall through from bounded parsing to the ordinary-command allow path.
denied_output="$(run_hook "git -C $ROOT -C $TMP_ROOT status --short")"
jq -e '
  .hookSpecificOutput.permissionDecision == "deny" and
  (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_GIT_EXECUTION_CONTEXT_DENIED]")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("operation=git-execution-context")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("token=-C")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("argv_index=3")) and
  (.hookSpecificOutput.permissionDecisionReason | contains("unapproved-canonical-repository-root"))
' "$denied_output" >/dev/null

printf '%s\n' 'validate-bash repeated git -C roots: PASS'
