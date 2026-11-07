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

kimi_home="$TMP_ROOT/kimi-home"
kimi_wire="$kimi_home/.kimi-code/sessions/wd_probe_0000000000000000/t00-session/agents/main/wire.jsonl"
mkdir -p "$(dirname "$kimi_wire")"
now_ms="$(( $(date +%s%N) / 1000000 ))"
printf '%s\n' \
  '{"type":"metadata","protocol_version":"1.4","created_at":1784381699557}' \
  "{\"type\":\"context.append_loop_event\",\"event\":{\"type\":\"tool.call\",\"uuid\":\"tool_kimiopen\",\"turnId\":\"1\",\"step\":1,\"stepUuid\":\"step-uuid\",\"toolCallId\":\"tool_kimiopen\",\"name\":\"Agent\",\"args\":{\"description\":\"malformed parity\",\"prompt\":\"malformed parity\"},\"time\":$((now_ms - 5000))}}" \
  >"$kimi_wire"

run_hook() {
  local provider="$1" payload="$2" output="$TMP_ROOT/$provider.out"
  case "$provider" in
    eci)
      printf '%s' "$payload" |
        ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" \
        ECI_HOOK_IS_SUBAGENT=true ECI_VALIDATE_CWD="$ROOT" \
        ECI_VALIDATE_SESSION_ID=t00-session PATH="$ROOT/bin:$PATH" \
        bash "$ROOT/hooks/validate-bash.sh" >"$output"
      ;;
    kimi)
      printf '%s' "$payload" |
        KIMI_PROOF_ROOT="$proof_root" HOME="$kimi_home" \
        ECI_HOME="$ROOT" KIMI_CODE_HOME="$kimi_home/.kimi-code" \
        KIMI_HOOK_IS_SUBAGENT=true KIMI_VALIDATE_CWD="$ROOT" \
        KIMI_VALIDATE_SESSION_ID=t00-session PATH="$ROOT/bin:$PATH" \
        bash "/home/pheona/.kimi-code/hooks/validate-bash.sh" >"$output"
      ;;
    *)
      return 1
      ;;
  esac
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
truncated="$(jq -cn --arg cwd "$ROOT" \
  '{session_id:"t00-session",cwd:$cwd,tool_input:{command:"git status --short"}}')"
truncated="${truncated%?}"

for provider in eci kimi; do
  typed_output="$(run_hook "$provider" "$typed_malformed")"
  assert_common_diagnostic "$provider" "$typed_output"
  jq -e --arg cwd "$ROOT" '
    (.hookSpecificOutput.permissionDecisionReason | contains("session=t00-session")) and
    (.hookSpecificOutput.permissionDecisionReason | contains(("cwd=" + $cwd)))
  ' "$typed_output" >/dev/null

  if [ "$provider" = kimi ]; then
    truncated_output="$(run_hook "$provider" "$truncated")"
    assert_common_diagnostic "$provider" "$truncated_output"
    jq -e --arg cwd "$ROOT" '
      (.hookSpecificOutput.permissionDecisionReason | contains("marker=active")) and
      (.hookSpecificOutput.permissionDecisionReason | contains("session=t00-session")) and
      (.hookSpecificOutput.permissionDecisionReason | contains(("cwd=" + $cwd)))
    ' "$truncated_output" >/dev/null
  fi
done

printf '%s\n' 'malformed-input parity: PASS'
