#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_BASE="$(realpath -m -- "${ECI_TMPDIR:-${HOME:?}/tmp}")"
TMP_ROOT="$(mktemp -d "$TMP_BASE/eci-pretooluse-latency.XXXXXX")"
# Each callback runs against a private runtime copy under a real fixture HOME,
# so no hook, ledger, or transcript write reaches the live ~/.omp/agent/eci or ~/tmp.
SOURCE_ROOT="$ROOT"
ROOT="$TMP_ROOT/home/.omp/agent/eci"
mkdir -p -- "$ROOT/sessions"
rsync -a --exclude='sessions/' --exclude='*.sqlite*' --exclude='auth*.json' \
  --exclude='.cache/' --exclude='__pycache__/' \
  "$SOURCE_ROOT/hooks" "$SOURCE_ROOT/bin" "$SOURCE_ROOT/tools" "$SOURCE_ROOT/skills" \
  "$SOURCE_ROOT/AGENTS.md" "$SOURCE_ROOT/hooks.json" "$ROOT/"
# Publish the native planner into the fixture runtime so validate-bash never
# rebuilds it on the timed path; the 1000 ms budget measures hook work only.
mkdir -p -- "$TMP_ROOT/home/tmp/eci-runtime/eci"
(
  cd "$ROOT/hooks/lib/eci-command-plan-go"
  go build -o "$TMP_ROOT/home/tmp/eci-runtime/eci/eci-command-plan" .
)
proof_root="$TMP_ROOT/proof"
session_id="t00-session"
session_dir="$proof_root/$session_id"
status_file="$TMP_ROOT/home/.agent/context/sessions/$session_id/latest-status-report.md"
worker_transcript="$(mktemp "$ROOT/sessions/latency-worker.XXXXXX.jsonl")"
helper_hardlink="$ROOT/hooks/lib/.eci-latency-helper-$BASHPID"
altered_mode="$TMP_ROOT/eci-command-gate-mode"
export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export XDG_STATE_HOME="$TMP_ROOT/xdg-state"
mkdir -p "$session_dir" "$(dirname -- "$status_file")" "$XDG_CONFIG_HOME/eci"
chmod 700 "$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/eci"
trap 'rm -f -- "$worker_transcript" "$helper_hardlink"; rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM
# HOME is a real fixture directory whose .omp/agent/eci is the private runtime copy, so
# the provenance guard sees a real final $HOME/.omp/agent/eci directory (no symlink).
ln -- "$ROOT/hooks/lib/eci-environment-command.sh" "$helper_hardlink"
printf '%s\n' '#!/bin/sh' 'exit 0' >"$altered_mode"
chmod 755 "$altered_mode"

fail() {
  printf 'ECI PreToolUse latency probe: %s\n' "$1" >&2
  exit 1
}

[ ! -L "$TMP_ROOT/home/.omp/agent/eci" ] ||
  fail 'fixture must not make the final $HOME/.omp/agent/eci authority a symlink'
[ "$(realpath -e -- "$TMP_ROOT/home/.omp/agent/eci")" = "$ROOT" ] ||
  fail 'fixture HOME-parent alias does not resolve to the canonical ECI root'

printf '%s\n' \
  'scope: latency probe' \
  "cwd: $ROOT" \
  "session_id: $session_id" \
  'created_utc: 2026-08-21T00:00:00Z' \
  >"$session_dir/eci_active"
printf '%s\n' '# latency status' >"$status_file"
printf '%s\n' \
  '{"timestamp":"2026-08-21T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Latency","agent_role":"default"}}}}}' \
  >"$worker_transcript"
chmod 0600 "$worker_transcript"

jq -e '
  [.hooks.PreToolUse[]?.hooks[]?.command]
  | length > 0
    and all(test("/edit-bash-pre-reviewer\\.sh|reviewer-call\\.sh|edit_bash_pre_reviewer_controller\\.py") | not)
' "$ROOT/hooks.json" >/dev/null || fail 'a backend reviewer remains in PreToolUse'

bash_input() {
  local role="$1" command="$2"
  if [ "$role" = worker ]; then
    jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg command "$command" \
      --arg transcript "$worker_transcript" \
      '{session_id:$session,cwd:$cwd,transcript_path:$transcript,tool_name:"Bash",tool_input:{command:$command}}'
  else
    jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg command "$command" \
      '{session_id:$session,cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}'
  fi
}

bash_input_inactive() {
  local role="$1" command="$2"
  if [ "$role" = worker ]; then
    jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$worker_transcript" \
      '{session_id:"latency-inactive",cwd:$cwd,transcript_path:$transcript,tool_name:"Bash",tool_input:{command:$command}}'
  else
    jq -cn --arg cwd "$ROOT" --arg command "$command" \
      '{session_id:"latency-inactive",cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}'
  fi
}

edit_input() {
  local role="$1" tool="$2" target="$3"
  if [ "$role" = worker ]; then
    jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg tool "$tool" \
      --arg path "$target" --arg transcript "$worker_transcript" \
      '{session_id:$session,cwd:$cwd,transcript_path:$transcript,tool_name:$tool,tool_input:{file_path:$path,old_string:"old",new_string:"new"}}'
  else
    jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg tool "$tool" --arg path "$target" \
      '{session_id:$session,cwd:$cwd,tool_name:$tool,tool_input:{file_path:$path,old_string:"old",new_string:"new"}}'
  fi
}

patch_input() {
  local role="$1" target="$2" patch
  patch="$(printf '%s\n' '*** Begin Patch' "*** Update File: $target" '@@' '-old' '+new' '*** End Patch')"
  if [ "$role" = worker ]; then
    jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg patch "$patch" \
      --arg transcript "$worker_transcript" \
      '{session_id:$session,cwd:$cwd,transcript_path:$transcript,tool_name:"apply_patch",tool_input:{patch:$patch}}'
  else
    jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg patch "$patch" \
      '{session_id:$session,cwd:$cwd,tool_name:"apply_patch",tool_input:{patch:$patch}}'
  fi
}

run_sample() {
  local callback="$1" matcher="$2" role="$3" scenario="$4" expected="$5" phase="$6" input="$7"
  local output="$TMP_ROOT/output" error_output="$TMP_ROOT/error" route=legacy
  local started ended elapsed status=0 outcome=allow effective_expected="$expected"

  # Keep the worker fast-path sample identifiable in the per-sample trace;
  # its command is deliberately a plain plan_status=0 argv, while all other
  # scenarios exercise the existing deferred capability routes.
  if [ "$role" = worker ] && [ "$scenario" = direct ]; then
    route=worker-plain-plan-status0
  fi

  [ -n "$input" ] || fail "missing fixture input for matcher=$matcher role=$role scenario=$scenario"
  started="$(date +%s%N)"
  printf '%s' "$input" | timeout 1s env \
    ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" KIMI_CODE_HOME="${KIMI_CODE_HOME:-$HOME/.kimi-code}" \
    XDG_CONFIG_HOME="$XDG_CONFIG_HOME" XDG_STATE_HOME="$XDG_STATE_HOME" \
    HOME="$TMP_ROOT/home" bash -lc "$callback" >"$output" 2>"$error_output" || status=$?
  ended="$(date +%s%N)"
  elapsed=$(( (ended - started) / 1000000 ))
  if jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null 2>&1; then
    outcome=deny
  fi
  if [ "$command_gate_mode" = permissive ] && [ "$matcher" = '^Bash$' ] && [ "$expected" = deny ]; then
    effective_expected=allow
  fi
  printf 'callback=PreToolUse mode=%s matcher=%q role=%s scenario=%s route=%s phase=%s outcome=%s elapsed_ms=%d status=%d\n' \
    "$command_gate_mode" "$matcher" "$role" "$scenario" "$route" "$phase" "$outcome" "$elapsed" "$status"
  [ "$status" -eq 0 ] || {
    cat "$output" "$error_output" >&2
    fail "callback failed: matcher=$matcher role=$role scenario=$scenario phase=$phase status=$status"
  }
  [ "$elapsed" -lt 1000 ] || fail "callback exceeded 1 second: matcher=$matcher role=$role scenario=$scenario phase=$phase elapsed_ms=$elapsed"
  [ "$outcome" = "$effective_expected" ] || {
    cat "$output" "$error_output" >&2
    fail "fixture outcome mismatch: mode=$command_gate_mode matcher=$matcher role=$role scenario=$scenario expected=$effective_expected actual=$outcome"
  }
}

run_fixture() {
  local callback="$1" matcher="$2" role="$3" scenario="$4" expected="$5" input="$6" phase
  for phase in cold warm; do
    run_sample "$callback" "$matcher" "$role" "$scenario" "$expected" "$phase" "$input"
  done
}

exercise_callback() {
  local matcher="$1" callback="$2" role
  case "$matcher" in
    '^Bash$')
      for role in coordinator worker; do
        run_fixture "$callback" "$matcher" "$role" direct allow "$(bash_input "$role" 'novel-finite-tool --flag value')"
        run_fixture "$callback" "$matcher" "$role" eight-segment allow "$(bash_input "$role" 'printf 1; printf 2; printf 3; printf 4; printf 5; printf 6; printf 7; printf 8')"
        run_fixture "$callback" "$matcher" "$role" protected-middle deny "$(bash_input "$role" 'printf before && env && printf after')"
        run_fixture "$callback" "$matcher" "$role" helper-hardlink allow "$(bash_input "$role" "cat $helper_hardlink")"
        run_fixture "$callback" "$matcher" "$role" inactive allow "$(bash_input_inactive "$role" 'novel-inactive-tool --flag value')"
        run_fixture "$callback" "$matcher" "$role" environment-query allow "$(bash_input "$role" 'printenv PATH')"
        run_fixture "$callback" "$matcher" "$role" environment-deny deny "$(bash_input "$role" 'env')"
        if [ "$role" = worker ]; then
          run_fixture "$callback" "$matcher" "$role" gate-mode-set deny "$(bash_input "$role" "$ROOT/bin/eci-command-gate-mode set enforcing")"
        else
          run_fixture "$callback" "$matcher" "$role" gate-mode-set allow "$(bash_input "$role" "$ROOT/bin/eci-command-gate-mode set enforcing")"
        fi
        run_fixture "$callback" "$matcher" "$role" gate-mode-identity deny "$(bash_input "$role" "$altered_mode set enforcing")"
      done
      ;;
    '^apply_patch$')
      run_fixture "$callback" "$matcher" coordinator proof-write allow "$(patch_input coordinator "$status_file")"
      run_fixture "$callback" "$matcher" coordinator source-write deny "$(patch_input coordinator "$ROOT/hooks/validate-bash.sh")"
      run_fixture "$callback" "$matcher" worker source-write allow "$(patch_input worker "$ROOT/hooks/validate-bash.sh")"
      run_fixture "$callback" "$matcher" worker control-write deny "$(patch_input worker "$session_dir/eci_active")"
      ;;
    '^(Edit|Write|MultiEdit|NotebookEdit)$')
      run_fixture "$callback" "$matcher" coordinator proof-write allow "$(edit_input coordinator Edit "$status_file")"
      run_fixture "$callback" "$matcher" coordinator source-write deny "$(edit_input coordinator Edit "$ROOT/hooks/validate-bash.sh")"
      run_fixture "$callback" "$matcher" worker source-write allow "$(edit_input worker Edit "$ROOT/hooks/validate-bash.sh")"
      run_fixture "$callback" "$matcher" worker control-write deny "$(edit_input worker Edit "$session_dir/eci_active")"
      ;;
    *) fail "registered PreToolUse matcher has no latency fixture: $matcher" ;;
  esac
}

for command_gate_mode in enforcing permissive; do
  printf '%s\n' "$command_gate_mode" >"$XDG_CONFIG_HOME/eci/command-gate-mode"
  chmod 600 "$XDG_CONFIG_HOME/eci/command-gate-mode"
  callback_count=0
  while IFS=$'\t' read -r matcher callback; do
    [ -n "$matcher" ] && [ -n "$callback" ] || fail 'registered PreToolUse callback is missing matcher or command'
    callback_count=$((callback_count + 1))
    exercise_callback "$matcher" "$callback"
  done < <(jq -r '.hooks.PreToolUse[] as $entry | $entry.hooks[] | [$entry.matcher, .command] | @tsv' "$ROOT/hooks.json")
  [ "$callback_count" -gt 0 ] || fail 'hooks.json contains no PreToolUse callbacks'
  printf 'ECI PreToolUse callback latency: PASS mode=%s callbacks=%d\n' "$command_gate_mode" "$callback_count"
done
