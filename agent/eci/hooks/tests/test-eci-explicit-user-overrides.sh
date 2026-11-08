#!/usr/bin/env bash

# Regression for retired explicit-user override artifacts.  They are advisory
# compatibility state, never a prerequisite or bypass for ordinary work.

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-legacy-override-advisory.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

SESSION_ID='t00-legacy-override-advisory'
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
  'scope: legacy override advisory fixture' \
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

# Model the historical failure: malformed permissive state plus a stranded,
# nonempty override claim.  No valid authorization artifact is created.
printf '%s\n' 'malformed legacy permissive state' >"$PERMISSIVE_RECORD"
printf '%s\n' 'malformed legacy override state' >"$OVERRIDE_RECORD"
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
  local command="$1" role="${2:-coordinator}" hook_status

  jq -cn \
    --arg session_id "$SESSION_ID" \
    --arg cwd "$ROOT" \
    --arg command "$command" \
    '{session_id:$session_id,cwd:$cwd,tool_input:{command:$command}}' \
    >"$INPUT"

  set +e
  HOME="$HOME" ECI_HOME="$ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    ECI_ROLE="$role" ECI_HOOK_IS_SUBAGENT=false \
    XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
    PATH="$ROOT/bin:$PATH" \
    bash "$TEST_HOOK_ROOT/validate-bash.sh" <"$INPUT" >"$OUTPUT" 2>"$ERROR_OUTPUT"
  hook_status=$?
  set -e
  [ "$hook_status" -eq 0 ] || {
    printf 'private legacy-override hook exited nonzero: status=%s command=%s\n' \
      "$hook_status" "$command" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  }
  [ ! -s "$ERROR_OUTPUT" ] || {
    printf 'private legacy-override hook wrote stderr for command=%s\n' "$command" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  }
}

assert_allowed() {
  local command="$1"

  run_hook "$command"
  [ ! -s "$OUTPUT" ] || {
    printf 'legacy override state incorrectly blocked ordinary work: command=%s\n' \
      "$command" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

assert_denied() {
  local command="$1" code="$2" target="$3"

  run_hook "$command"
  jq -e --arg code "$code" --arg target "$target" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains($target))
  ' "$OUTPUT" >/dev/null || {
    printf 'concrete unsafe target was not independently denied: command=%s\n' \
      "$command" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

# This was previously denied by a stranded claim.  It needs no artifact,
# fingerprint, expiry, or user authorization to be normal coordinator work.
assert_allowed "bash -c 'printf %s ordinary-coordination'"

# Missing records are equally ordinary; lifecycle visibility is not a request
# to repair or enable a mode.
rm -f -- "$PERMISSIVE_RECORD" "$OVERRIDE_CLAIM/record" "$OVERRIDE_RECORD"
rmdir -- "$OVERRIDE_CLAIM"
assert_allowed 'printf %s no-artifact-needed'
assert_allowed '"$HOME/.omp/agent/eci/bin/eci-active" --help'
assert_allowed '"$HOME/.omp/agent/eci/bin/eci-active" status'

# Advisory legacy state cannot suppress concrete accidental-effect boundaries.
assert_denied 'rm -rf /' 'ECI_BROAD_DESTRUCTIVE_DENIED' '/'
assert_denied "printf '%s\\n' overwrite > $FOREIGN_MARKER" \
  'ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED' "$FOREIGN_MARKER"

printf '%s\n' 'legacy override advisory regression: PASS'
