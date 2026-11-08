#!/usr/bin/env bash

set -euo pipefail
# The gate refuses a bin directory with a group- or other-writable ancestor.
umask 077

SOURCE_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$SOURCE_ROOT/hooks/tests/require-go.sh"
. "$SOURCE_ROOT/hooks/tests/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-repository-allow.XXXXXX")"
trap 'chmod -R -- u+w "$TMP_ROOT" 2>/dev/null || true; rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

HOME_ROOT="$TMP_ROOT/home"
RUNTIME_ROOT="$HOME_ROOT/.omp/agent/eci"
mkdir -p -- "$RUNTIME_ROOT" "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
cp -a -- "$SOURCE_ROOT/hooks" "$RUNTIME_ROOT"
cp -- "$SOURCE_ROOT/hooks.json" "$RUNTIME_ROOT/hooks.json"
mkdir -p -- "$RUNTIME_ROOT/bin"
cp -- "$SOURCE_ROOT/bin/eci-active" "$RUNTIME_ROOT/bin/eci-active"

# Provision the gate binaries the hook verifies ($HOME/.omp/agent/eci/bin and
# their .sha256 receipts) through the ordinary builder, in the fixture HOME only.
# The Go caches stay on the caller's paths so no read-only module cache lands
# under TMP_ROOT.
GOMODCACHE="$("$ECI_TEST_GO" env GOMODCACHE)" GOCACHE="$("$ECI_TEST_GO" env GOCACHE)" \
  HOME="$HOME_ROOT" bash "$RUNTIME_ROOT/hooks/build-native-binaries.sh" \
  eci-command-plan eci-safe-import eci-hook-helper >/dev/null

printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"

WORK="$TMP_ROOT/work"
DEPENDENCY="$TMP_ROOT/dependency"
UNDECLARED_DEPENDENCY="$TMP_ROOT/undeclared-dependency"
ABSENT_DEPENDENCY="$TMP_ROOT/absent-dependency"
ABSENT_WRONG_PATH="$TMP_ROOT/wrong-absent-dependency"
mkdir -p -- "$WORK" "$DEPENDENCY"
mkdir -p -- "$UNDECLARED_DEPENDENCY"
for path in "$WORK" "$DEPENDENCY" "$UNDECLARED_DEPENDENCY"; do
  git -C "$path" init -q
  git -C "$path" config user.email repository-allow-test@example.invalid
  git -C "$path" config user.name 'Repository Allow Test'
  printf 'base\n' >"$path/file.txt"
  git -C "$path" add -- file.txt
  git -C "$path" commit -qm initial
done

WORK="$(eci_realpath_existing "$WORK")"
DEPENDENCY_ALIAS="$DEPENDENCY"
DEPENDENCY="$(eci_realpath_existing "$DEPENDENCY")"
WORKER_SCRIPT="$DEPENDENCY/allowance-script.sh"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$WORKER_SCRIPT"
chmod 755 -- "$WORKER_SCRIPT"
PROOF_ROOT="$TMP_ROOT/proof"
SESSION='repository-allow-session'
mkdir -p -- "$PROOF_ROOT/$SESSION"
printf '%s\n' \
  'scope: repository allowance test' \
  "cwd: $WORK" \
  "session_id: $SESSION" \
  'created_utc: 2026-09-14T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"
ALLOWANCE="$PROOF_ROOT/$SESSION/eci-additional-repository"
OTHER_SESSION='other-repository-allow-session'
mkdir -p -- "$PROOF_ROOT/$OTHER_SESSION"
printf '%s\n' \
  'scope: repository allowance other-session test' \
  "cwd: $WORK" \
  "session_id: $OTHER_SESSION" \
  'created_utc: 2026-09-14T00:00:00Z' \
  >"$PROOF_ROOT/$OTHER_SESSION/eci_active"
# The hook classifies the caller from its callback transcript. Coordinator calls
# carry a main-session transcript in the fixture HOME; worker calls carry none,
# which the hook classifies as a worker.
for transcript_session in "$SESSION" "$OTHER_SESSION"; do
  mkdir -p -- "$HOME_ROOT/.omp/agent/sessions/$transcript_session"
  printf '%s\n' \
    "{\"type\":\"session\",\"version\":3,\"id\":\"$transcript_session\",\"cwd\":\"$WORK\"}" \
    '{"type":"message","role":"user","content":"repository allowance"}' \
    >"$HOME_ROOT/.omp/agent/sessions/$transcript_session/main.jsonl"
done

run_hook() {
  local command="$1" role="${2:-worker}" session="${3:-$SESSION}" output="$TMP_ROOT/hook-output.json"
  local is_subagent=true transcript=""
  [ "$role" = worker ] || is_subagent=false
  [ "$role" = worker ] || transcript="$HOME_ROOT/.omp/agent/sessions/$session/main.jsonl"

  jq -cn --arg session "$session" --arg cwd "$WORK" --arg command "$command" --arg transcript "$transcript" \
    '{session_id:$session,tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}} + (if $transcript == "" then {} else {transcript_path:$transcript} end)' |
    (
      export HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT"
      export ECI_ROLE="$role" ECI_HOOK_IS_SUBAGENT="$is_subagent"
      export XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state"
      PATH="$RUNTIME_ROOT/bin:/usr/local/bin:/usr/bin:/bin" \
        /bin/bash -c 'bash "$HOME/.omp/agent/eci/hooks/validate-bash.sh"'
    ) >"$output" 2>/dev/null
  cat -- "$output"
}

assert_denied_foreign() {
  local command="$1" session="${2:-$SESSION}"
  assert_denied_foreign_as worker "$command" "$session"
}

assert_denied_foreign_as() {
  local role="$1" command="$2" session="${3:-$SESSION}" output
  output="$(run_hook "$command" "$role" "$session")"
  grep -Fq -- 'ECI_GIT_CROSS_SCOPE_DENIED' <<<"$output" || {
    printf 'undeclared dependency command was not denied: role=%s command=%s\n%s\n' "$role" "$command" "$output" >&2
    return 1
  }
}

assert_additional_repository_not_allowed() {
  local repository="$1"
  if (
    export HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT"
    . "$RUNTIME_ROOT/hooks/lib/eci-proof-state.sh"
    eci_additional_repository_is_allowed "$SESSION" "$WORK" "$repository"
  ); then
    printf 'missing repository target was accepted by the allowance predicate: %s\n' \
      "$repository" >&2
    return 1
  fi
}

assert_allowed_foreign() {
  local command="$1" session="${2:-$SESSION}"
  assert_allowed_foreign_as worker "$command" "$session"
}

assert_allowed_foreign_as() {
  local role="$1" command="$2" session="${3:-$SESSION}" output
  output="$(run_hook "$command" "$role" "$session")"
  [ -z "$output" ] || {
    printf 'declared dependency command was denied: role=%s command=%s\n%s\n' "$role" "$command" "$output" >&2
    return 1
  }
}

assert_denied_worker_script() {
  local command="$1" output
  output="$(run_hook "$command" worker)"
  grep -Fq -- 'ECI_WORKER_SCRIPT_TARGET_DENIED' <<<"$output" || {
    printf 'undeclared dependency script was not denied: %s\n%s\n' "$command" "$output" >&2
    return 1
  }
}

assert_worker_git_ownership_denied() {
  local command="$1" output
  output="$(run_hook "$command" worker)"
  grep -Fq -- '[ECI_WORKER_GIT_OWNERSHIP_DENIED]' <<<"$output" || {
    printf 'worker Git mutation was not routed to the worker Git owner: %s\n%s\n' "$command" "$output" >&2
    return 1
  }
}

assert_allowed_worker_script() {
  local command="$1" output
  output="$(run_hook "$command" worker)"
  [ -z "$output" ] || {
    printf 'declared dependency script was denied: %s\n%s\n' "$command" "$output" >&2
    return 1
  }
}

assert_allowed_lifecycle() {
  local command="$1" output
  output="$(run_hook "$command")"
  [ -z "$output" ] || {
    printf 'valid repository-allow lifecycle route was denied: %s\n%s\n' "$command" "$output" >&2
    return 1
  }
}

assert_denied_lifecycle() {
  local command="$1" output
  output="$(run_hook "$command")"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' <<<"$output" >/dev/null 2>&1 || {
    printf 'invalid repository-allow lifecycle route was admitted: %s\n' "$command" >&2
    printf '%s\n' "$output" >&2
    return 1
  }
}

assert_denied_foreign_as coordinator "git -C $DEPENDENCY add -- file.txt"
assert_denied_foreign_as coordinator 'git -C ../dependency add -- file.txt'
assert_denied_worker_script "bash $WORKER_SCRIPT"

if (
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-on "$ABSENT_DEPENDENCY" \
    'missing repositories cannot be granted'
) >"$TMP_ROOT/absent-grant-output.txt" 2>&1; then
  printf 'repository-allow-on accepted an absent repository target\n' >&2
  exit 1
fi
[ ! -e "$ALLOWANCE" ] && [ ! -L "$ALLOWANCE" ]
assert_additional_repository_not_allowed "$ABSENT_DEPENDENCY"
if (
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-off "$ABSENT_DEPENDENCY"
) >"$TMP_ROOT/absent-unbound-revocation-output.txt" 2>&1; then
  printf 'repository-allow-off accepted an absent path without a stored allowance\n' >&2
  exit 1
fi
[ ! -e "$ALLOWANCE" ] && [ ! -L "$ALLOWANCE" ]

(
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-on "$DEPENDENCY_ALIAS" \
    'active session owns narrow dependency Git work'
)

[ -f "$ALLOWANCE" ] && [ "$(eci_stat mode "$(eci_realpath_existing "$ALLOWANCE")")" = 600 ]
grep -Fqx -- "repository: $DEPENDENCY" "$ALLOWANCE"
grep -Fqx -- 'reason: active session owns narrow dependency Git work' "$ALLOWANCE"
assert_allowed_lifecycle '"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-status'
assert_allowed_lifecycle "env ECI_SESSION_ID=$SESSION \"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-status"
assert_allowed_lifecycle "env ECI_SESSION_ID=$SESSION \"$RUNTIME_ROOT/bin/eci-active\" repository-allow-status"
assert_allowed_lifecycle "\"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-on $DEPENDENCY \"owner keeps dependency repair local\""
assert_allowed_lifecycle "\"$RUNTIME_ROOT/bin/eci-active\" repository-allow-on $DEPENDENCY \"owner keeps dependency repair local\""
assert_allowed_lifecycle "\"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-off $DEPENDENCY"
assert_allowed_lifecycle "\"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-on $DEPENDENCY \"dependency; repair remains local\""
assert_allowed_lifecycle "\"$RUNTIME_ROOT/bin/eci-active\" repository-allow-on $DEPENDENCY 'literal \$(id) stays text'"
assert_allowed_lifecycle "\"$RUNTIME_ROOT/bin/eci-active\" repository-allow-on $DEPENDENCY \"reason\$(id)\""
assert_allowed_lifecycle "\"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-on $DEPENDENCY \"safe first segment\"; id"
assert_allowed_lifecycle "\"$RUNTIME_ROOT/bin/eci-active\" repository-allow-on $DEPENDENCY \"safe first segment\" && id"
assert_denied_lifecycle '"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-on /tmp "x" extra'
assert_denied_lifecycle "\"$RUNTIME_ROOT/bin/eci-active\" repository-allow-on /tmp \"x\" extra"
assert_denied_lifecycle "env ECI_SESSION_ID=$OTHER_SESSION \"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-on $DEPENDENCY \"other session\""
assert_denied_lifecycle "env ECI_SESSION_ID=$OTHER_SESSION \"$RUNTIME_ROOT/bin/eci-active\" repository-allow-on $DEPENDENCY \"other session\""
assert_denied_foreign "\"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-on $DEPENDENCY \"safe first segment\"; git -C $UNDECLARED_DEPENDENCY add -- file.txt"
assert_denied_foreign "\"$RUNTIME_ROOT/bin/eci-active\" repository-allow-on $DEPENDENCY \"reason\$(git -C $UNDECLARED_DEPENDENCY add -- file.txt)\""
assert_denied_lifecycle "\"\$HOME/.omp/agent/eci/bin/eci-active\" repository-allow-on $DEPENDENCY \"safe first segment\"; \"$RUNTIME_ROOT/bin/eci-active\" on \"worker must not activate an ECI session\""
assert_allowed_foreign "git -C $DEPENDENCY status --short"
assert_allowed_foreign "git -C $DEPENDENCY diff -- file.txt"
assert_worker_git_ownership_denied "git -C $DEPENDENCY add -- file.txt"
assert_allowed_foreign_as coordinator "git -C $DEPENDENCY add -- file.txt"
assert_allowed_foreign_as coordinator 'git -C ../dependency add -- file.txt'
assert_allowed_worker_script "bash $WORKER_SCRIPT"

assert_worker_git_ownership_denied "git -C $DEPENDENCY commit --allow-empty -m worker-commit"

printf '%s\n' \
  'scope: repository allowance test' \
  "cwd: $DEPENDENCY" \
  "session_id: $SESSION" \
  'created_utc: 2026-09-14T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"
# A marker binds to its callback cwd: with the marker moved to the dependency,
# no marker is active for the $WORK callback, so the hook stays transparent.
cwd_mismatch_output="$(run_hook "git -C $DEPENDENCY add -- file.txt" coordinator)"
[ -z "$cwd_mismatch_output" ] || {
  printf 'callback outside the moved marker cwd was not transparent\n%s\n' "$cwd_mismatch_output" >&2
  exit 1
}
printf '%s\n' \
  'scope: repository allowance test' \
  "cwd: $WORK" \
  "session_id: $SESSION" \
  'created_utc: 2026-09-14T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"

assert_denied_foreign_as coordinator "git -C $UNDECLARED_DEPENDENCY add -- file.txt"

(
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-status
)

(
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-off "$DEPENDENCY_ALIAS"
)
[ ! -e "$ALLOWANCE" ] && [ ! -L "$ALLOWANCE" ]

(
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-on "$DEPENDENCY" \
    'active session owns narrow dependency Git work'
)
cp -- "$ALLOWANCE" "$TMP_ROOT/allowance-before-absent-revocation"
cp -- "$PROOF_ROOT/$SESSION/eci_active" "$TMP_ROOT/marker-before-absent-revocation"
mv -- "$DEPENDENCY" "$TMP_ROOT/dependency-removed"

if (
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-status
) >"$TMP_ROOT/absent-status-output.txt" 2>&1; then
  printf 'repository-allow-status accepted an allowance for a missing target\n' >&2
  exit 1
fi
assert_additional_repository_not_allowed "$DEPENDENCY"

if (
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-off "$ABSENT_WRONG_PATH"
) >"$TMP_ROOT/wrong-absent-revocation-output.txt" 2>&1; then
  printf 'repository-allow-off accepted a different absent path\n' >&2
  exit 1
fi
cmp -- "$TMP_ROOT/allowance-before-absent-revocation" "$ALLOWANCE"
cmp -- "$TMP_ROOT/marker-before-absent-revocation" "$PROOF_ROOT/$SESSION/eci_active"

if exact_revocation_output="$(
  cd -- "$WORK"
  env ECI_PROOF_ROOT="$PROOF_ROOT" ECI_SESSION_ID="$SESSION" ECI_ROLE=coordinator \
    "$RUNTIME_ROOT/bin/eci-active" repository-allow-off "$DEPENDENCY" 2>&1
)"; then
  [ ! -e "$ALLOWANCE" ] && [ ! -L "$ALLOWANCE" ]
  cmp -- "$TMP_ROOT/marker-before-absent-revocation" "$PROOF_ROOT/$SESSION/eci_active"
else
  printf 'repository-allow-off rejected the exact absent target:\n%s\n' \
    "$exact_revocation_output" >&2
  exit 1
fi

# A worker may not turn the route into a second command or a different owner.
if output="$(run_hook '"$HOME/.omp/agent/eci/bin/eci-active" repository-allow-on /tmp "x" extra')"; then
  [ -n "$output" ] || {
    printf 'malformed repository-allow route was admitted\n' >&2
    exit 1
  }
fi

printf '%s\n' 'ECI repository allowance: PASS'
