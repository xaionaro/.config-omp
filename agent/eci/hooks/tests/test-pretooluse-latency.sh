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
  "$SOURCE_ROOT/hooks.json" "$ROOT/"
# Hooks read the omp guidance as $HOME/.omp/agent/AGENTS.md (the parent of the ECI root).
cp -- "$SOURCE_ROOT/../AGENTS.md" "$ROOT/../AGENTS.md"
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
# Coordinator samples carry a main omp transcript (a session header with no
# parentSession), so the hook classifies them as coordinator, not worker.
main_transcript="$TMP_ROOT/home/.omp/agent/sessions/-latency-/latency-main.jsonl"
helper_hardlink="$ROOT/hooks/lib/.eci-latency-helper-$BASHPID"
altered_mode="$TMP_ROOT/eci-command-gate-mode"
export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export XDG_STATE_HOME="$TMP_ROOT/xdg-state"
mkdir -p "$session_dir" "$(dirname -- "$status_file")" "$XDG_CONFIG_HOME/eci" "$(dirname -- "$main_transcript")"
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
printf '%s\n' \
  '{"type":"session","version":3,"id":"t00-session","cwd":"/work"}' \
  '{"type":"message","message":{"role":"user","content":"hello"}}' \
  >"$main_transcript"
chmod 0600 "$main_transcript"

role_transcript() {
  if [ "$1" = worker ]; then printf '%s' "$worker_transcript"; else printf '%s' "$main_transcript"; fi
}

jq -e '
  [.hooks.PreToolUse[]?.hooks[]?.command]
  | length > 0
    and all(test("/edit-bash-pre-reviewer\\.sh|reviewer-call\\.sh|edit_bash_pre_reviewer_controller\\.py") | not)
' "$ROOT/hooks.json" >/dev/null || fail 'a backend reviewer remains in PreToolUse'

bash_input() {
  local role="$1" command="$2"
  jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg command "$command" \
    --arg transcript "$(role_transcript "$role")" \
    '{session_id:$session,cwd:$cwd,transcript_path:$transcript,tool_name:"Bash",tool_input:{command:$command}}'
}

bash_input_inactive() {
  local role="$1" command="$2"
  jq -cn --arg cwd "$ROOT" --arg command "$command" --arg transcript "$(role_transcript "$role")" \
    '{session_id:"latency-inactive",cwd:$cwd,transcript_path:$transcript,tool_name:"Bash",tool_input:{command:$command}}'
}

edit_input() {
  local role="$1" tool="$2" target="$3"
  jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg tool "$tool" \
    --arg path "$target" --arg transcript "$(role_transcript "$role")" \
    '{session_id:$session,cwd:$cwd,transcript_path:$transcript,tool_name:$tool,tool_input:{file_path:$path,old_string:"old",new_string:"new"}}'
}

patch_input() {
  local role="$1" target="$2" patch
  patch="$(printf '%s\n' '*** Begin Patch' "*** Update File: $target" '@@' '-old' '+new' '*** End Patch')"
  jq -cn --arg cwd "$ROOT" --arg session "$session_id" --arg patch "$patch" \
    --arg transcript "$(role_transcript "$role")" \
    '{session_id:$session,cwd:$cwd,transcript_path:$transcript,tool_name:"apply_patch",tool_input:{patch:$patch}}'
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
  # Wall-clock budget, sensitive to host load. At load 30, measured wall time was
  # 0.69-0.96 s per sample (cold CPU 0.59-0.63 s user+sys), so wall-clock slack
  # against 1000 ms is only 0.04-0.31 s. Expect flakes when the 1-min load is above
  # the CPU count; re-run at idle before treating a timing failure as a regression.
  [ "$elapsed" -lt 1000 ] || fail "callback exceeded 1 second: matcher=$matcher role=$role scenario=$scenario phase=$phase elapsed_ms=$elapsed"
  [ "$outcome" = "$effective_expected" ] || {
    cat "$output" "$error_output" >&2
    fail "fixture outcome mismatch: mode=$command_gate_mode matcher=$matcher role=$role scenario=$scenario expected=$effective_expected actual=$outcome"
  }
}

# Cold samples follow one untimed, unasserted prime per distinct callback command
# (keyed on the command string, so a callback shared by two matchers is primed once),
# using the first fixture's input, so host page-cache and start-up noise do not count
# against the 1000 ms budget. A prime failure is still caught by the cold sample.
declare -A primed_callbacks=()
prime_callback() {
  local callback="$1" input="$2"
  [ -z "${primed_callbacks[$callback]:-}" ] || return 0
  primed_callbacks[$callback]=1
  printf '%s' "$input" | timeout 1s env \
    ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" KIMI_CODE_HOME="${KIMI_CODE_HOME:-$HOME/.kimi-code}" \
    XDG_CONFIG_HOME="$XDG_CONFIG_HOME" XDG_STATE_HOME="$XDG_STATE_HOME" \
    HOME="$TMP_ROOT/home" bash -lc "$callback" >"$TMP_ROOT/prime-output" 2>"$TMP_ROOT/prime-error" || true
}

run_fixture() {
  local callback="$1" matcher="$2" role="$3" scenario="$4" expected="$5" input="$6" phase
  prime_callback "$callback" "$input"
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
        # Bare env is ordinary for both roles (classifier_test.go); the planner never denies it.
        run_fixture "$callback" "$matcher" "$role" protected-middle allow "$(bash_input "$role" 'printf before && env && printf after')"
        run_fixture "$callback" "$matcher" "$role" helper-hardlink allow "$(bash_input "$role" "cat $helper_hardlink")"
        run_fixture "$callback" "$matcher" "$role" inactive allow "$(bash_input_inactive "$role" 'novel-inactive-tool --flag value')"
        run_fixture "$callback" "$matcher" "$role" environment-query allow "$(bash_input "$role" 'printenv PATH')"
        run_fixture "$callback" "$matcher" "$role" environment-deny allow "$(bash_input "$role" 'env')"
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
