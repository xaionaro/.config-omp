#!/usr/bin/env bash

set -euo pipefail

ECI_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
KIMI_ROOT="$(cd "${KIMI_CODE_HOME:-${HOME:?}/.kimi-code}" && pwd -P)"
TMP_ROOT="$(mktemp -d "${HOME:?}/tmp/eci-system-tmp-order.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM
mkdir -p "$TMP_ROOT/home"

write_marker() {
  local marker="$1" root="$2" session="$3"
  mkdir -p "$(dirname "$marker")"
  printf '%s\n' \
    'scope: system temporary path diagnostic-order regression' \
    "cwd: $root" \
    "session_id: $session" \
    'created_utc: 2026-08-23T00:00:00Z' \
    >"$marker"
  chmod 600 "$marker"
}

run_case() {
  local provider="$1" role="$2" active="$3" command="$4" expected="$5"
  local root hook proof session input output
  case "$provider" in
    eci)
      root="$ECI_ROOT"
      hook="$ECI_ROOT/hooks/validate-bash.sh"
      ;;
    kimi)
      root="$KIMI_ROOT"
      hook="$KIMI_ROOT/hooks/validate-bash.sh"
      ;;
    *)
      printf 'unknown provider: %s\n' "$provider" >&2
      return 1
      ;;
  esac
  proof="$TMP_ROOT/$provider/$role-$active"
  session="system-tmp-$provider-$role-$active"
  mkdir -p "$proof"
  if [ "$active" = active ]; then
    write_marker "$proof/$session/eci_active" "$root" "$session"
  fi
  input="$(jq -cn --arg cwd "$root" --arg session "$session" --arg command "$command" \
    '{session_id:$session,cwd:$cwd,tool_input:{command:$command}}')"
  output="$proof/result.json"
  printf '%s\n' "$input" |
    HOME="$TMP_ROOT/home" ECI_HOME="$ECI_ROOT" KIMI_CODE_HOME="$KIMI_ROOT" \
    ECI_PROOF_ROOT="$proof" KIMI_PROOF_ROOT="$proof" \
    ECI_ROLE="$role" KIMI_ROLE="$role" \
    ECI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" \
    KIMI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" \
    bash "$hook" >"$output"
  if [ "$expected" = allow ]; then
    [ ! -s "$output" ] || {
      printf 'unexpected denial: provider=%s role=%s active=%s command=%q\n' "$provider" "$role" "$active" "$command" >&2
      cat -- "$output" >&2
      return 1
    }
    return 0
  fi
  jq -e --arg code "[$expected]" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=")) and
    (.hookSpecificOutput.permissionDecisionReason | (($code == "[ECI_WORKER_LAUNCHER_DENIED]") or contains("path=/tmp"))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'diagnostic mismatch: provider=%s role=%s active=%s command=%q expected=%s\n' "$provider" "$role" "$active" "$command" "$expected" >&2
    cat -- "$output" >&2
    return 1
  }
}

for provider in eci kimi; do
  for active in active inactive; do
    run_case "$provider" coordinator "$active" 'env /tmp/eci-escape.sh' ECI_TMPDIR_SYSTEM_ROOT
    run_case "$provider" coordinator "$active" 'env TMPDIR=/tmp novel-tool' ECI_TMPDIR_SYSTEM_ROOT
    run_case "$provider" coordinator "$active" 'TMPDIR=/tmp novel-tool' ECI_TMPDIR_SYSTEM_ROOT
    run_case "$provider" coordinator "$active" '/tmp/eci-escape.sh' ECI_TMPDIR_SYSTEM_ROOT
    run_case "$provider" coordinator "$active" 'novel-tool --option=/tmp' ECI_TMPDIR_SYSTEM_ROOT
  done
  run_case "$provider" coordinator active 'env FOO=bar novel-tool --flag value' allow
  run_case "$provider" coordinator active "env TMPDIR=${HOME}/tmp novel-tool --flag value" allow
  # Shell-script launches retain the worker launcher diagnostic; the wrapper
  # route owns the executable identity before inspecting its script body.
  run_case "$provider" worker active 'bash /tmp/eci-escape.sh' ECI_WORKER_LAUNCHER_DENIED
done

printf 'system temporary diagnostic ordering: PASS providers=2 active/inactive=2 cases=10\n'
