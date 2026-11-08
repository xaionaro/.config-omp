#!/usr/bin/env bash

set -Eeuo pipefail
umask 077
trap 'status=$?; printf "protected Git target test failure: line=%s status=%s command=%q\n" "$LINENO" "$status" "$BASH_COMMAND" >&2' ERR

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/eci-protected-git-target.XXXXXX")"
trap 'chmod -R -- u+w "$TMP_ROOT"; rm -rf -- "$TMP_ROOT"' EXIT

# The planner protects live hooks under $HOME/.omp/agent/eci and the hook runs
# with HOME=$TMP_ROOT/home, so the runtime under test is a private copy at that
# protected root. It is its own repository root, as in the source layout
# (575bb81 ROOT is the ~/.codex repository root).
fixture_root="$TMP_ROOT/home/.omp/agent/eci"
mkdir -p -- "$(dirname -- "$fixture_root")"
cp -a -- "$ROOT" "$fixture_root"
rm -rf -- "$fixture_root/.git"
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

assert_protected_denial() {
  local command="$1" output="$TMP_ROOT/denial.json"
  run_hook "$command" "$output"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_EDIT_ROUTING_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-protected-target")) and
    (.hookSpecificOutput.permissionDecisionReason | test("path=/[^,]*/hooks/validate-bash\\.sh,")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'protected target was not denied: command=%q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_protected_denial_with_timeout() {
  local seconds="$1" command="$2" output="$TMP_ROOT/timeout-denial.json"
  if ! HOOK_TIMEOUT_SECONDS="$seconds" run_hook "$command" "$output"; then
    printf 'protected target hook timed out or failed: timeout=%ss command=%q\n' "$seconds" "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_EDIT_ROUTING_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-protected-target"))
  ' "$output" >/dev/null || {
    printf 'timed protected target was not denied: timeout=%ss command=%q\n' "$seconds" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_protected_denial_at() {
  local hook_cwd="$1" command="$2" output="$TMP_ROOT/denial-at.json"
  run_hook_at "$hooks_session" "$hook_cwd" "$command" "$output"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_EDIT_ROUTING_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-protected-target")) and
    (.hookSpecificOutput.permissionDecisionReason | test("path=/[^,]*/hooks/validate-bash\\.sh,")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'protected target was not denied at cwd: cwd=%q command=%q\n' "$hook_cwd" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_protected_denial_at_session() {
  local session="$1" hook_cwd="$2" command="$3" output="$TMP_ROOT/denial-session.json"
  run_hook_at "$session" "$hook_cwd" "$command" "$output"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COORDINATOR_EDIT_ROUTING_REQUIRED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=coordinator-protected-target")) and
    (.hookSpecificOutput.permissionDecisionReason | test("path=/[^,]*/hooks/validate-bash\\.sh,")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'protected target was not denied at session/cwd: session=%q cwd=%q command=%q\n' "$session" "$hook_cwd" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_allowed() {
  local command="$1" output="$TMP_ROOT/allowed.json"
  run_hook "$command" "$output"
  [ ! -s "$output" ] || {
    printf 'transparent or ordinary Git target was denied: command=%q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_allowed_at() {
  local hook_cwd="$1" command="$2" output="$TMP_ROOT/allowed-at.json"
  run_hook_at "$hooks_session" "$hook_cwd" "$command" "$output"
  [ ! -s "$output" ] || {
    printf 'transparent or ordinary Git target was denied at cwd: cwd=%q command=%q\n' "$hook_cwd" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_allowed_at_session() {
  local session="$1" hook_cwd="$2" command="$3" output="$TMP_ROOT/allowed-session.json"
  run_hook_at "$session" "$hook_cwd" "$command" "$output"
  [ ! -s "$output" ] || {
    printf 'transparent or ordinary Git target was denied at session/cwd: session=%q cwd=%q command=%q\n' "$session" "$hook_cwd" "$command" >&2
    cat -- "$output" >&2
    return 1
  }
}

assert_protected_denial 'git rm -- hooks/validate-bash.sh'
assert_protected_denial 'git rm -r -- hooks/validate-bash.sh'
assert_protected_denial 'git mv -- hooks/validate-bash.sh hooks/renamed-hook.sh'
assert_protected_denial 'env git rm -- hooks/validate-bash.sh'
assert_protected_denial 'command git rm -- hooks/validate-bash.sh'
assert_protected_denial 'timeout 10 git rm -- hooks/validate-bash.sh'
assert_protected_denial 'nice -n 5 git rm -- hooks/validate-bash.sh'
assert_protected_denial 'git rm -- hooks/../hooks/validate-bash.sh'
assert_protected_denial 'timeout 10 env FOO=bar command git rm -- hooks/validate-bash.sh'
assert_protected_denial 'env timeout 10 command git rm -- hooks/validate-bash.sh'
assert_protected_denial 'if git rm -- hooks/validate-bash.sh; then true; fi'
assert_protected_denial 'git rm -- hooks/validate-bash.sh > /tmp/eci-protected-target-test.log'
assert_protected_denial 'git checkout HEAD -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout -- --detach hooks/validate-bash.sh'
assert_protected_denial 'git checkout --detach --no-detach HEAD -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --patch --unified=3 -- hooks/validate-bash.sh'
assert_protected_denial_with_timeout 10 'git checkout --patc --unified=3 -- hooks/validate-bash.sh'
assert_protected_denial "git --work-tree=\"$ROOT\" checkout -- hooks/validate-bash.sh"
assert_protected_denial "GIT_WORK_TREE=\"$ROOT\" git checkout -- hooks/validate-bash.sh"
assert_protected_denial 'bash -c "git rm -- hooks/validate-bash.sh"'
assert_protected_denial 'bash -c "echo before; git rm -- hooks/validate-bash.sh"'
assert_protected_denial_at_session "$alias_session" "$alias_cwd" 'git rm -- hooks/validate-bash.sh'
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
assert_protected_denial 'git checkout --patch --unified=-1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --patch -U-1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --patch --inter-hunk-context=-1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --patch --unified -1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --unified=-1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout -U-1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --inter-hunk-context=-1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --patch --unified=-0 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --patch -U-0 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --patch --inter-hunk-context=-0 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout --unified=3 --unified=-1 -- hooks/validate-bash.sh'
assert_protected_denial 'git checkout -U3 -U-1 -- hooks/validate-bash.sh'
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
