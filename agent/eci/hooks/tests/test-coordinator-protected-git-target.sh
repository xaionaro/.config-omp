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

protected_pathspec_file="$TMP_ROOT/protected-pathspecs"
protected_pathspec_nul_file="$TMP_ROOT/protected-pathspecs.nul"
missing_pathspec_file="$TMP_ROOT/missing-pathspecs"
printf '%s\n' 'hooks/validate-bash.sh' >"$protected_pathspec_file"
printf 'hooks/validate-bash.sh\0' >"$protected_pathspec_nul_file"

# A symlink is a distinct Git worktree entry.  Keep the alias outside the
# tracked set and remove it with the temporary test state on exit.
symlink_alias="$ROOT/hooks/.eci-protected-target-alias.$(basename -- "$TMP_ROOT")"
ln -s -- "$ROOT/hooks/validate-bash.sh" "$symlink_alias"

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

hooks_session='t01-hooks-session'
hooks_cwd="$ROOT/hooks"
hooks_session_dir="$proof_root/$hooks_session"
mkdir -p -- "$hooks_session_dir/evidence"
printf '%s\n' \
  'scope: coordinator protected Git target test' \
  "cwd: $hooks_cwd" \
  "session_id: $hooks_session" \
  'created_utc: 2026-09-13T00:00:00Z' \
  >"$hooks_session_dir/eci_active"
mkdir -p -- "$context_root/$hooks_session"
printf '%s\n' '{"event":"decision","summary":"protected Git target test"}' >"$context_root/$hooks_session/high_level_log.jsonl"
printf '%s\n' '# test instructions' >"$hooks_session_dir/instructions.md"

alias_session='t02-canonical-cwd-session'
alias_cwd="$TMP_ROOT/eci-callback-alias"
ln -s -- "$ROOT" "$alias_cwd"
alias_session_dir="$proof_root/$alias_session"
mkdir -p -- "$alias_session_dir/evidence"
printf '%s\n' \
  'scope: coordinator protected Git target test' \
  "cwd: $alias_cwd" \
  "session_id: $alias_session" \
  'created_utc: 2026-09-13T00:00:00Z' \
  >"$alias_session_dir/eci_active"
mkdir -p -- "$context_root/$alias_session"
printf '%s\n' '{"event":"decision","summary":"protected Git target test"}' >"$context_root/$alias_session/high_level_log.jsonl"
printf '%s\n' '# test instructions' >"$alias_session_dir/instructions.md"

# `--pathspec-from-file` consumes the following `--detach` token as its
# filename.  Keep the value file in the temporary callback CWD while the
# explicit work-tree points at the repository under test.
pathspec_value_session='t03-pathspec-value-session'
printf '%s\n' 'hooks/validate-bash.sh' >"$TMP_ROOT/--detach"
pathspec_value_session_dir="$proof_root/$pathspec_value_session"
mkdir -p -- "$pathspec_value_session_dir/evidence"
printf '%s\n' \
  'scope: coordinator protected Git target test' \
  "cwd: $TMP_ROOT" \
  "session_id: $pathspec_value_session" \
  'created_utc: 2026-09-13T00:00:00Z' \
  >"$pathspec_value_session_dir/eci_active"
mkdir -p -- "$context_root/$pathspec_value_session"
printf '%s\n' '{"event":"decision","summary":"protected Git target test"}' >"$context_root/$pathspec_value_session/high_level_log.jsonl"
printf '%s\n' '# test instructions' >"$pathspec_value_session_dir/instructions.md"

literal_dash_session='t04-literal-dash-session'
literal_dash_cwd="$TMP_ROOT/literal-dash-cwd"
mkdir -p -- "$literal_dash_cwd"
printf '%s\n' 'hooks/validate-bash.sh' >"$literal_dash_cwd/--"
literal_dash_target_file="$ROOT/--"
[ ! -e "$literal_dash_target_file" ] && [ ! -L "$literal_dash_target_file" ]
literal_dash_session_dir="$proof_root/$literal_dash_session"
mkdir -p -- "$literal_dash_session_dir/evidence"
printf '%s\n' \
  'scope: coordinator protected Git target test' \
  "cwd: $literal_dash_cwd" \
  "session_id: $literal_dash_session" \
  'created_utc: 2026-09-13T00:00:00Z' \
  >"$literal_dash_session_dir/eci_active"
mkdir -p -- "$context_root/$literal_dash_session"
printf '%s\n' '{"event":"decision","summary":"protected Git target test"}' >"$context_root/$literal_dash_session/high_level_log.jsonl"
printf '%s\n' '# test instructions' >"$literal_dash_session_dir/instructions.md"

literal_dash_missing_session='t05-literal-dash-missing-session'
literal_dash_missing_cwd="$TMP_ROOT/literal-dash-missing-cwd"
mkdir -p -- "$literal_dash_missing_cwd"
literal_dash_missing_session_dir="$proof_root/$literal_dash_missing_session"
mkdir -p -- "$literal_dash_missing_session_dir/evidence"
printf '%s\n' \
  'scope: coordinator protected Git target test' \
  "cwd: $literal_dash_missing_cwd" \
  "session_id: $literal_dash_missing_session" \
  'created_utc: 2026-09-13T00:00:00Z' \
  >"$literal_dash_missing_session_dir/eci_active"
mkdir -p -- "$context_root/$literal_dash_missing_session"
printf '%s\n' '{"event":"decision","summary":"protected Git target test"}' >"$context_root/$literal_dash_missing_session/high_level_log.jsonl"
printf '%s\n' '# test instructions' >"$literal_dash_missing_session_dir/instructions.md"

cleanup() {
  rm -f -- "$symlink_alias"
  rm -f -- "$literal_dash_target_file"
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


assert_allowed_at_session() {
  local session="$1" hook_cwd="$2" command="$3" output="$TMP_ROOT/allowed-session.json"
  run_hook_at "$session" "$hook_cwd" "$command" "$output" || return $?
  [ ! -s "$output" ] || {
    printf 'transparent or ordinary Git target was denied at session/cwd: session=%q cwd=%q command=%q\n' "$session" "$hook_cwd" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_allowed 'git rm -- hooks/validate-bash.sh'
assert_allowed 'git rm -r -- hooks/validate-bash.sh'
assert_allowed 'git mv -- hooks/validate-bash.sh hooks/renamed-hook.sh'
assert_allowed 'env git rm -- hooks/validate-bash.sh'
assert_allowed 'command git rm -- hooks/validate-bash.sh'
assert_allowed 'timeout 10 git rm -- hooks/validate-bash.sh'
assert_allowed 'nice -n 5 git rm -- hooks/validate-bash.sh'
assert_allowed 'git rm -- hooks/../hooks/validate-bash.sh'
assert_allowed 'timeout 10 env FOO=bar command git rm -- hooks/validate-bash.sh'
assert_allowed 'env timeout 10 command git rm -- hooks/validate-bash.sh'
assert_allowed 'if git rm -- hooks/validate-bash.sh; then true; fi'
assert_allowed 'git rm -- hooks/validate-bash.sh > /tmp/eci-protected-target-test.log'
assert_allowed 'git checkout HEAD -- hooks/validate-bash.sh'
assert_allowed 'git checkout -- hooks/validate-bash.sh'
assert_allowed 'git checkout -- --detach hooks/validate-bash.sh'
assert_allowed 'git checkout --detach --no-detach HEAD -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=3 -- hooks/validate-bash.sh'
HOOK_TIMEOUT_SECONDS=10 assert_allowed 'git checkout --patc --unified=3 -- hooks/validate-bash.sh'
assert_allowed "git --work-tree=\"$ROOT\" checkout -- hooks/validate-bash.sh"
assert_allowed "GIT_WORK_TREE=\"$ROOT\" git checkout -- hooks/validate-bash.sh"
assert_allowed 'bash -c "git rm -- hooks/validate-bash.sh"'
assert_allowed 'bash -c "echo before; git rm -- hooks/validate-bash.sh"'
assert_allowed_at_session "$alias_session" "$alias_cwd" 'git rm -- hooks/validate-bash.sh'
assert_broad_denied
assert_allowed 'git rm --cached -- hooks/validate-bash.sh'
assert_allowed 'git rm -n -- hooks/validate-bash.sh'
assert_allowed 'git rm --dry-run -- hooks/validate-bash.sh'
assert_allowed 'git restore --staged -- hooks/validate-bash.sh'
assert_allowed 'git checkout -- hooks/does-not-exist'
assert_allowed 'git checkout HEAD'
assert_allowed 'git checkout --detach HEAD'
assert_allowed 'git checkout --detach hooks/validate-bash.sh'
assert_allowed 'git checkout -d hooks/validate-bash.sh'
assert_allowed 'git checkout --no-detach=foo hooks/validate-bash.sh'
assert_allowed "git checkout --patch --pathspec-from-file=\"$protected_pathspec_file\""
assert_allowed "git checkout --pathspec-from-file=\"$protected_pathspec_file\" --patch"
assert_allowed 'git checkout --conflict --no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout --orphan --no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout -b --no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout -B --no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout --conflict=--no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout --orphan=--no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout -b--no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout -B--no-detach hooks/validate-bash.sh'
assert_allowed 'git checkout --conflict bogus hooks/validate-bash.sh'
assert_allowed 'git checkout --conflict=bogus hooks/validate-bash.sh'
assert_allowed 'git checkout --unified=3 hooks/validate-bash.sh'
assert_allowed 'git checkout -U3 hooks/validate-bash.sh'
assert_allowed 'git checkout --inter-hunk-context=3 hooks/validate-bash.sh'
assert_allowed 'git checkout --unified 3 hooks/validate-bash.sh'
assert_allowed 'git checkout -U 3 hooks/validate-bash.sh'
assert_allowed 'git checkout --inter-hunk-context 3 hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch -U-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --inter-hunk-context=-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified -1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --unified=-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout -U-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --inter-hunk-context=-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --unified=-0 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch -U-0 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --patch --inter-hunk-context=-0 -- hooks/validate-bash.sh'
assert_allowed 'git checkout --unified=3 --unified=-1 -- hooks/validate-bash.sh'
assert_allowed 'git checkout -U3 -U-1 -- hooks/validate-bash.sh'
for detach_option in -dq -qd --d --de --det --deta --detac; do
  assert_allowed "git checkout $detach_option hooks/validate-bash.sh"
done
assert_allowed 'git checkout missing-branch'
assert_allowed "git rm --pathspec-from-file=\"$missing_pathspec_file\""
assert_allowed "git restore --pathspec-from-file=\"$missing_pathspec_file\""
assert_allowed "git checkout --pathspec-from-file=\"$missing_pathspec_file\""
assert_allowed 'bash -c "$ECI_DYNAMIC_COMMAND"'
assert_allowed 'sh -c "$ECI_DYNAMIC_COMMAND"'
assert_allowed 'git rm -- hooks'
assert_allowed 'git rm -- hooks/does-not-exist'
assert_allowed 'git rm -- hooks/alias-to-validate.sh'
assert_allowed 'git rm -- hooks/tests/test-coordinator-protected-git-target.sh'
assert_allowed 'git mv -- hooks/missing-source hooks/also-missing'

printf '%s\n' 'coordinator protected Git target contract: PASS'
