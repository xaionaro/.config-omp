#!/usr/bin/env bash

# Regression for R11 permissive/override state.  Those historical records are
# advisory only: they cannot become prerequisites for ordinary commands or
# read-only lifecycle visibility.  Concrete destructive and cross-session
# targets remain independently checked.

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-permissive-state-advisory.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

SESSION_ID='t00-permissive-advisory'
PROOF_ROOT="$TMP_ROOT/proof"
CURRENT_MARKER="$PROOF_ROOT/$SESSION_ID/eci_active"
FOREIGN_MARKER="$PROOF_ROOT/other-session/eci_active"
PERMISSIVE_RECORD="$PROOF_ROOT/$SESSION_ID/eci-permissive-mode"
OVERRIDE_RECORD="$PROOF_ROOT/$SESSION_ID/.eci-accidental-mistake-override"
OVERRIDE_CLAIM="$OVERRIDE_RECORD.claim"
INPUT="$TMP_ROOT/input.json"
OUTPUT="$TMP_ROOT/output.json"
ERROR_OUTPUT="$TMP_ROOT/error.txt"
TEST_HOOK_ROOT="$TMP_ROOT/private-hooks"

mkdir -p -- "$(dirname -- "$CURRENT_MARKER")" "$(dirname -- "$FOREIGN_MARKER")" \
  "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
printf '%s\n' \
  'scope: permissive state advisory fixture' \
  "cwd: $ROOT" \
  "session_id: $SESSION_ID" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$CURRENT_MARKER"
printf '%s\n' \
  'scope: foreign marker fixture' \
  "cwd: $ROOT" \
  'session_id: other-session' \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$FOREIGN_MARKER"
printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"

# A malformed permissive file and a stranded legacy override claim model the
# historical state that previously turned an unrelated command into
# ECI_POLICY_STATE_UNAVAILABLE.  Neither record is a permission or recovery
# prerequisite.
printf '%s\n' 'malformed historical permissive state' >"$PERMISSIVE_RECORD"
printf '%s\n' 'stale historical override state' >"$OVERRIDE_RECORD"
mkdir -- "$OVERRIDE_CLAIM"
printf '%s\n' consumed >"$OVERRIDE_CLAIM/record"

# Exercise the private hook body, removing a line-2 bypass if present.
cp -- "$ROOT/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.before"
cp -a -- "$ROOT/hooks" "$TEST_HOOK_ROOT"
eci_sed_inplace '2{/^exit 0$/d;}' "$TEST_HOOK_ROOT/validate-bash.sh"
cmp -- "$TMP_ROOT/validate-bash.before" "$ROOT/hooks/validate-bash.sh" || {
  printf '%s\n' 'private hook setup modified the source' >&2
  exit 1
}
cmp -- "$TEST_HOOK_ROOT/validate-bash.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/validate-bash.before")

run_hook() {
  local command="$1" role="$2" hook_status

  jq -cn \
    --arg session_id "$SESSION_ID" \
    --arg cwd "$ROOT" \
    --arg command "$command" \
    '{session_id:$session_id,cwd:$cwd,tool_input:{command:$command}}' \
    >"$INPUT"

  set +e
  HOME="$HOME" ECI_HOME="$ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    ECI_ROLE="$role" ECI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" \
    XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
    PATH="$ROOT/bin:$PATH" \
    bash "$TEST_HOOK_ROOT/validate-bash.sh" <"$INPUT" >"$OUTPUT" 2>"$ERROR_OUTPUT"
  hook_status=$?
  set -e
  [ "$hook_status" -eq 0 ] || {
    printf 'private permissive-state hook exited nonzero: status=%s command=%s\n' \
      "$hook_status" "$command" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  }
  [ ! -s "$ERROR_OUTPUT" ] || {
    printf 'private permissive-state hook wrote stderr for command=%s\n' "$command" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  }
}

assert_allowed() {
  local command="$1" role="$2"

  run_hook "$command" "$role"
  [ ! -s "$OUTPUT" ] || {
    printf 'historical permissive state denied ordinary work: role=%s command=%s\n' \
      "$role" "$command" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

assert_denied() {
  local command="$1" code="$2" target="$3"

  run_hook "$command" coordinator
  jq -e --arg code "$code" --arg target "$target" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains($target))
  ' "$OUTPUT" >/dev/null || {
    printf 'concrete unsafe target was not denied independently: command=%s\n' "$command" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

# An ordinary shell launcher is not a request for an override record.  The
# stranded legacy claim makes this assertion RED before its old consultation
# is removed from validate-bash.
assert_allowed "bash -c 'printf %s ordinary-work'" coordinator
assert_allowed 'printf %s worker-ordinary-work' worker

# Read-only lifecycle visibility has no reason to inspect permissive or
# accidental-override state.
assert_allowed '"$HOME/.omp/agent/eci/bin/eci-active" --help' coordinator
assert_allowed '"$HOME/.omp/agent/eci/bin/eci-active" status' coordinator

# State that is advisory for normal work cannot weaken concrete effect checks.
assert_denied 'rm -rf /' 'ECI_BROAD_DESTRUCTIVE_DENIED' '/'
assert_denied "printf '%s\\n' overwrite > $FOREIGN_MARKER" \
  'ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED' "$FOREIGN_MARKER"

printf '%s\n' 'permissive state advisory regression: PASS'
