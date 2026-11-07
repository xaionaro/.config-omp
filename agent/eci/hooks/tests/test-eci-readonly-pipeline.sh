#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-readonly-pipeline.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

SESSION_ID="t00-readonly-pipeline"
PROOF_ROOT="$TMP_ROOT/proof"
MARKER="$PROOF_ROOT/$SESSION_ID/eci_active"
OTHER_MARKER="$PROOF_ROOT/other-session/eci_active"
INPUT="$TMP_ROOT/input.json"
OUTPUT="$TMP_ROOT/output.json"
ERROR_OUTPUT="$TMP_ROOT/error.txt"
UNIQ_INPUT="$TMP_ROOT/uniq-input"
UNIQ_OUTPUT="$TMP_ROOT/uniq-output"

mkdir -p -- "$(dirname -- "$MARKER")" "$(dirname -- "$OTHER_MARKER")" "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
printf '%s\n' duplicate duplicate >"$UNIQ_INPUT"
printf '%s\n' sentinel >"$UNIQ_OUTPUT"
printf '%s\n' \
  'scope: read-only pipeline regression' \
  "cwd: $ROOT" \
  "session_id: $SESSION_ID" \
  'created_utc: 2026-08-26T00:00:00Z' \
  >"$MARKER"
printf '%s\n' \
  'scope: other-session control fixture' \
  "cwd: $ROOT" \
  'session_id: other-session' \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$OTHER_MARKER"
printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"

# Exercise the private hook body, removing a line-2 bypass if present.
TEST_HOOK_ROOT="$TMP_ROOT/private-hooks"
cp -- "$ROOT/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.before"
cp -a -- "$ROOT/hooks" "$TEST_HOOK_ROOT"
sed -i '2{/^exit 0$/d;}' -- "$TEST_HOOK_ROOT/validate-bash.sh"
cmp -- "$TMP_ROOT/validate-bash.before" "$ROOT/hooks/validate-bash.sh" || {
  printf '%s\n' 'private hook setup modified the source' >&2
  exit 1
}
cmp -- "$TEST_HOOK_ROOT/validate-bash.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/validate-bash.before")

run_hook() {
  local command="$1"

  jq -cn \
    --arg session_id "$SESSION_ID" \
    --arg cwd "$ROOT" \
    --arg command "$command" \
    '{session_id:$session_id,cwd:$cwd,tool_input:{command:$command}}' \
    >"$INPUT"

  set +e
  HOME="$HOME" ECI_HOME="$ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
    PATH="$ROOT/bin:$PATH" \
    bash "$TEST_HOOK_ROOT/validate-bash.sh" <"$INPUT" >"$OUTPUT" 2>"$ERROR_OUTPUT"
  HOOK_STATUS=$?
  set -e

  if [ "$HOOK_STATUS" -ne 0 ]; then
    printf 'read-only pipeline hook exited nonzero: status=%s\n' "$HOOK_STATUS" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  fi
  if [ -s "$ERROR_OUTPUT" ]; then
    printf '%s\n' 'read-only pipeline hook wrote stderr:' >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  fi
}

assert_allowed_pipeline() {
  local command="$1"

  run_hook "$command"
  if [ -s "$OUTPUT" ]; then
    printf 'read-only pipeline was not admitted: command=%s\n' "$command" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  fi
}

assert_denied_pipeline() {
  local command="$1"

  run_hook "$command"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$OUTPUT" >/dev/null || {
    printf 'unsafe pipeline was not denied: command=%s\n' "$command" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

assert_allowed_pipeline 'ls -lt sessions/2026/08/26 | head -n 20'
assert_allowed_pipeline 'novel-inspection-tool --format table | head -n 20'
assert_allowed_pipeline 'ls -lt sessions/2026/08/26 | head -n 20 > sessions/2026/08/26/output'
assert_allowed_pipeline 'ls -lt sessions/2026/08/26 | touch hooks/validate-bash.sh'
assert_allowed_pipeline 'ls -lt sessions/2026/08/26 | head "$HOME"'
assert_allowed_pipeline 'file -C -m hooks/validate-bash.sh | head -n 20'
assert_allowed_pipeline "uniq $UNIQ_INPUT $UNIQ_OUTPUT | head -n 20"

# Keep only concrete accidental-wrong-target boundaries.  The gate may not
# turn an unfamiliar executable, shell punctuation, or ordinary repository
# source target into a denial.
assert_denied_pipeline 'rm -rf / | head -n 20'
assert_denied_pipeline "touch $MARKER | head -n 20"
assert_denied_pipeline "touch $OTHER_MARKER | head -n 20"

printf '%s\n' 'target-aware compound pipeline assertions: PASS'
