#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
HOST_HOME="${HOME:?}"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-edit-controls.XXXXXX")"
control_hook_fixture="$(mktemp "$ROOT/hooks/.validate-bash-edit-controls.${BASHPID}.XXXXXX")"
export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export XDG_STATE_HOME="$TMP_ROOT/xdg-state"
mkdir -p "$XDG_CONFIG_HOME/eci" "$XDG_STATE_HOME"
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"
chmod 700 "$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/eci"
chmod 600 "$XDG_CONFIG_HOME/eci/command-gate-mode"
proof_root="$ROOT/.eci-edit-control-proof-$BASHPID"
repo_hardlink_alias="$(pwd)/.eci-active-hardlink-alias-$BASHPID"
trap 'rm -f -- "$control_hook_fixture"; rm -rf -- "$TMP_ROOT" "$proof_root" "$repo_hardlink_alias"' EXIT

# Exercise the private hook body, removing a line-2 bypass if present.
cp -- "$ROOT/hooks/validate-bash.sh" "$control_hook_fixture"
eci_sed_inplace '2{/^exit 0$/d;}' "$control_hook_fixture"
cmp -- "$control_hook_fixture" <(sed '2{/^exit 0$/d;}' -- "$ROOT/hooks/validate-bash.sh") || {
  printf '%s\n' 'edit-control fixture changed bytes other than an optional line-2 bypass' >&2
  exit 1
}

home="$TMP_ROOT/home"
eci_home="$TMP_ROOT/eci-home"
sid=t00-session
mkdir -p "$proof_root/$sid" "$proof_root/pre-reviewer" "$proof_root/reviewer" "$home" "$eci_home/sessions" "$home/.kimi-code/sessions"
printf '%s\n' \
  'scope: edit control test' \
  "cwd: $ROOT" \
  "session_id: $sid" \
  'created_utc: 2026-08-15T00:00:00Z' \
  >"$proof_root/$sid/eci_active"
transcript="$eci_home/sessions/eci-edit-control-test.jsonl"
printf '%s\n' '{"timestamp":"2026-08-15T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}}}' >"$transcript"

run_bash_worker_write_denied() {
  local command="$1" expected_target="$2" out="$TMP_ROOT/bash-worker-write.out"
  jq -cn --arg command "$command" --arg cwd "$ROOT" --arg transcript "$transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" HOME="$HOST_HOME" \
      ECI_HOOK_IS_SUBAGENT=true ECI_ROLE=worker \
      bash "$control_hook_fixture" >"$out"
  jq -e --arg expected_target "$expected_target" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($expected_target))
  ' "$out" >/dev/null || {
    printf 'write target was not preserved in denial: command=%s expected_target=%s\n' \
      "$command" "$expected_target" >&2
    cat -- "$out" >&2
    return 1
  }
}

run_bash_worker_allowed() {
  local command="$1" out="$TMP_ROOT/bash-worker-allowed.out" status
  jq -cn --arg command "$command" --arg cwd "$ROOT" --arg transcript "$transcript" \
    '{session_id:"t00-session",tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" HOME="$HOST_HOME" \
      ECI_HOOK_IS_SUBAGENT=true ECI_ROLE=worker \
      bash "$control_hook_fixture" >"$out" || status=$?
  [ -z "${status:-}" ] || {
    printf 'worker read hook exited %s: command=%s\n' "$status" "$command" >&2
    cat -- "$out" >&2
    return 1
  }
  [ ! -s "$out" ] || {
    cat -- "$out" >&2
    return 1
  }
}

# A stale marker grants no callback authority. Reads and writes remain ordinary.
for command in \
  "cat $proof_root/$sid/eci_active" \
  "sed -n '1p' $proof_root/$sid/eci_active"; do
  run_bash_worker_allowed "$command"
done
run_bash_worker_allowed "printf forged > $proof_root/$sid/eci_active"
run_bash_worker_write_denied "printf forged > $HOST_HOME/.omp/agent/sessions/fixture.jsonl" "$HOST_HOME/.omp/agent/sessions/fixture.jsonl"

for report in latest-status-report.md latest-status-report.yaml; do
  printf '%s\n' 'coordinator report' >"$proof_root/$sid/$report"
  run_bash_worker_allowed "cat $proof_root/$sid/$report"
  run_bash_worker_allowed "printf updated > $proof_root/$sid/$report"
done
if [ "${STATUS_REPORT_ONLY:-0}" = 1 ]; then
  printf '%s\n' 'status report Bash ownership assertions: PASS'
  exit 0
fi

ln "$proof_root/$sid/latest-status-report.md" "$repo_hardlink_alias"
for command in \
  "cat $repo_hardlink_alias" \
  "sed -n '1p' $repo_hardlink_alias"; do
  run_bash_worker_allowed "$command"
done
run_bash_worker_allowed "printf forged > $repo_hardlink_alias"
unlink "$repo_hardlink_alias"

# Shell punctuation is not an effect.  A finite ordinary worker inspection
# must reach the tool even when the planner does not recognize its compound
# spelling; concrete writes are still checked separately above and below.
run_bash_worker_allowed 'printf inspection && printf follow-up'
run_bash_worker_allowed "printf inspection && printf forged > $proof_root/$sid/eci_active"
run_bash_worker_write_denied "printf inspection && printf forged > $HOST_HOME/.omp/agent/sessions/fixture.jsonl" "$HOST_HOME/.omp/agent/sessions/fixture.jsonl"

# Reading a peer provider path has no effect.  It can be reported separately
# if useful, but cannot turn an ordinary worker inspection into a denial.
run_bash_worker_allowed "cat $HOST_HOME/.kimi-code/AGENTS.md"
run_bash_worker_allowed "git -C $ROOT status --short"

# An unexpanded loop is not a known write target.  Parser uncertainty must
# defer to normal execution rather than turn this ordinary worker inspection
# into an allowlist/grammar denial.
run_bash_worker_allowed 'for item in one; do printf "$item"; done'

# Malformed callback envelopes are runtime failures, independent of stale markers.
malformed_envelope='{"session_id":"t00-session","tool_name":"Bash","cwd":[],"tool_input":{"command":"printf harmless"}}'
metadata_output="$TMP_ROOT/metadata-advisory.out"
inactive_proof="$TMP_ROOT/inactive-proof"
mkdir -p "$inactive_proof"
for root in "$proof_root" "$inactive_proof"; do
  status=0
  printf '%s\n' "$malformed_envelope" |
    ECI_PROOF_ROOT="$root" ECI_HOME="$ROOT" HOME="$HOST_HOME" \
      bash "$control_hook_fixture" >"$metadata_output" 2>"$TMP_ROOT/native-error" || status=$?
  [ "$status" -eq 2 ]
  [ ! -s "$metadata_output" ]
  grep -Fq '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE]' "$TMP_ROOT/native-error"
done
# An invalid HOME prevents native planner verification, regardless of marker state.
for root in "$proof_root" "$inactive_proof"; do
  status=0
  printf '%s\n' '{"session_id":"t00-session","tool_name":"Bash","cwd":"/tmp","tool_input":{"command":"printf harmless"}}' |
    ECI_PROOF_ROOT="$root" ECI_HOME="$ROOT" HOME=relative \
      bash "$control_hook_fixture" >"$metadata_output" 2>"$TMP_ROOT/native-error" || status=$?
  [ "$status" -eq 2 ]
  [ ! -s "$metadata_output" ]
  grep -Fq '[ECI_HOOK_NATIVE_GATE_UNAVAILABLE]' "$TMP_ROOT/native-error"
done

printf '%s\n' 'edit control path assertions: PASS'
