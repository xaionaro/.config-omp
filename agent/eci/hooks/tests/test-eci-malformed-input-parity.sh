#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-malformed-parity.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
mkdir -p "$XDG_CONFIG_HOME/eci"
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"
chmod 600 "$XDG_CONFIG_HOME/eci/command-gate-mode"

proof_root="$TMP_ROOT/proof"
mkdir -p "$proof_root/t00-session"
printf '%s\n' \
  'scope: malformed-input parity' \
  "cwd: $ROOT" \
  'session_id: t00-session' \
  'created_utc: 2026-08-19T00:00:00Z' \
  >"$proof_root/t00-session/eci_active"

eci_home="$TMP_ROOT/eci-home"
eci_transcript="$eci_home/sessions/eci-malformed-parity.jsonl"
mkdir -p "$(dirname "$eci_transcript")"
printf '%s\n' \
  '{"timestamp":"2026-08-19T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Parity","agent_role":"default"}}}}}' \
  >"$eci_transcript"

run_hook() {
  local payload="$1" output="$TMP_ROOT/omp.out"
  printf '%s' "$payload" |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" \
    ECI_HOOK_IS_SUBAGENT=true ECI_VALIDATE_CWD="$ROOT" \
    ECI_VALIDATE_SESSION_ID=t00-session PATH="$ROOT/bin:$PATH" \
    bash "$ROOT/hooks/validate-bash.sh" >"$output"
  printf '%s\n' "$output"
}

assert_common_diagnostic() {
  local provider="$1" output="$2"
  jq -e --arg provider "$provider" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_HOOK_IDENTITY_MALFORMED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("provider=" + $provider))) and
    (.hookSpecificOutput.permissionDecisionReason | contains("role=worker"))
  ' "$output" >/dev/null
}

typed_malformed="$(jq -cn --arg cwd "$ROOT" --arg transcript "$eci_transcript" \
  '{session_id:"t00-session",cwd:$cwd,transcript_path:$transcript,tool_input:{command:null}}')"

# Retired: the kimi rows (typed-malformed and truncated payloads). No source path emits
# ECI_HOOK_IDENTITY_MALFORMED with provider=kimi role=worker: eci-command-plan-go/hook.go:109
# hard-codes Request{Provider: <own provider>, Role: RoleCoordinator} in both HEAD and
# pre-migration source 575bb81, and the only other emitter (hooks/eci-active-gate.sh:32)
# carries no provider/role fields. The omp rows are unchanged.
typed_output="$(run_hook "$typed_malformed")"
assert_common_diagnostic omp "$typed_output"
jq -e --arg cwd "$ROOT" '
  (.hookSpecificOutput.permissionDecisionReason | contains("session=t00-session")) and
  (.hookSpecificOutput.permissionDecisionReason | contains(("cwd=" + $cwd)))
' "$typed_output" >/dev/null

printf '%s\n' 'malformed-input parity: PASS'
