#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
trap 'status=$?; printf "protected Git target test failure: line=%s status=%s command=%q\n" "$LINENO" "$status" "$BASH_COMMAND" >&2' ERR

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/eci-protected-git-target.XXXXXX")"
trap 'chmod -R -- u+w "$TMP_ROOT"; rm -rf -- "$TMP_ROOT"' EXIT

# Use a private runtime so callbacks resolve the same HOME-based hook paths
# without changing the installed runtime.
fixture_root="$TMP_ROOT/home/.omp/agent/eci"
mkdir -p -- "$(dirname -- "$fixture_root")"
cp -a -- "$ROOT" "$fixture_root"
rm -rf -- "$fixture_root/.git"
# An absent mode tool preserves denials, independent of the caller's mode.
rm -f -- "$fixture_root/bin/eci-command-gate-mode"
eci_sed_inplace '2{/^exit 0$/d;}' "$fixture_root/hooks/validate-bash.sh"
git -C "$fixture_root" init --quiet
git -C "$fixture_root" add -A
git -C "$fixture_root" -c user.name=eci-test -c user.email=eci-test@invalid commit --quiet -m fixture
ROOT="$fixture_root"


proof_root="$TMP_ROOT/proof"
context_root="$TMP_ROOT/home/.agent/context/sessions"
session_dir="$proof_root/t00-session"
mkdir -p -- "$session_dir/evidence"
printf '%s\n' \
  'scope: coordinator protected Git target test' \
  "cwd: $ROOT" \
  'session_id: t00-session' \
  'created_utc: 2026-09-13T00:00:00Z' \
  >"$session_dir/eci_active"
mkdir -p -- "$context_root/t00-session"
printf '%s\n' '{"event":"decision","summary":"protected Git target test"}' >"$context_root/t00-session/high_level_log.jsonl"
printf '%s\n' '# test instructions' >"$session_dir/instructions.md"

# The planner derives the coordinator role only from a main-session transcript;
# without one a callback fails closed to the worker role.
MAIN_TRANSCRIPT="$TMP_ROOT/home/.omp/agent/sessions/protected-git/main.jsonl"
mkdir -p -- "$(dirname -- "$MAIN_TRANSCRIPT")"
printf '%s\n' \
  "{\"type\":\"session\",\"version\":3,\"id\":\"t00-session\",\"cwd\":\"$ROOT\"}" \
  '{"type":"message","role":"user","content":"protected git target"}' >"$MAIN_TRANSCRIPT"


cleanup() {
  chmod -R -- u+w "$TMP_ROOT"
  rm -rf -- "$TMP_ROOT"
}
trap cleanup EXIT

run_hook() {
  run_hook_at t00-session "$ROOT" "$1" "$2"
}

run_hook_at() {
  local session="$1" hook_cwd="$2" command="$3" output="$4"
  local transcript="${CALLBACK_TRANSCRIPT-$MAIN_TRANSCRIPT}"
  local -a hook_command=(bash "$fixture_root/hooks/validate-bash.sh")
  if [ -n "${HOOK_TIMEOUT_SECONDS:-}" ]; then
    hook_command=(eci_test_run_with_deadline "$HOOK_TIMEOUT_SECONDS" "${hook_command[@]}")
  fi
  jq -cn --arg session "$session" --arg cwd "$hook_cwd" --arg command "$command" --arg transcript "$transcript" \
    '{session_id:$session,tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}} + (if $transcript == "" then {} else {transcript_path:$transcript} end)' |
    HOME="$TMP_ROOT/home" ECI_SESSION_ID="$session" ECI_ROLE=coordinator \
    ECI_PROOF_ROOT="$proof_root" ECI_HOME="$ROOT" \
    KIMI_CODE_HOME="${KIMI_CODE_HOME:-$HOME/.kimi-code}" PATH="$ROOT/bin:$PATH" \
      "${hook_command[@]}" >"$output"
}


assert_allowed() {
  local command="$1" output="$TMP_ROOT/allowed.json"
  run_hook "$command" "$output" || return $?
  [ ! -s "$output" ] || {
    printf 'transparent or ordinary Git target was denied: command=%q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}
assert_broad_denied() {
  local output="$TMP_ROOT/broad-denial.json"
  run_hook 'git add -A' "$output" || return $?
  jq -e '.hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_BROAD_DESTRUCTIVE_DENIED]"))' "$output" >/dev/null
}



assert_allowed 'git rm -- hooks/validate-bash.sh'
assert_allowed 'git mv -- hooks/validate-bash.sh hooks/renamed-hook.sh'
assert_allowed 'git checkout -- hooks/validate-bash.sh'
assert_allowed 'rm hooks/validate-bash.sh'
assert_broad_denied

printf '%s\n' 'coordinator protected Git target contract: PASS'
