#!/usr/bin/env bash

# Regression for R11 marker observations.  A malformed, stale, duplicate, or
# unsafe marker scan is diagnostic context for ordinary work.  The hook must
# still stop a concrete mutation of another session's marker.

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-marker-metadata-advisory.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

SESSION_ID='t00-marker-metadata'
INPUT="$TMP_ROOT/input.json"
OUTPUT="$TMP_ROOT/output.json"
ERROR_OUTPUT="$TMP_ROOT/error.txt"
TEST_HOOK_ROOT="$TMP_ROOT/private-hooks"

# Exercise the private hook body, removing a line-2 bypass if present.
cp -- "$ROOT/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.before"
cp -a -- "$ROOT/hooks" "$TEST_HOOK_ROOT"
sed -i '2{/^exit 0$/d;}' -- "$TEST_HOOK_ROOT/validate-bash.sh"
cmp -- "$TMP_ROOT/validate-bash.before" "$ROOT/hooks/validate-bash.sh" || {
  printf '%s\n' 'private hook setup modified the source' >&2
  exit 1
}
cmp -- "$TEST_HOOK_ROOT/validate-bash.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/validate-bash.before")

write_marker() {
  local marker="$1" marker_cwd="$2" marker_session="$3"

  mkdir -p -- "$(dirname -- "$marker")"
  printf '%s\n' \
    'scope: marker metadata advisory fixture' \
    "cwd: $marker_cwd" \
    "session_id: $marker_session" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$marker"
}

run_hook() {
  local proof_root="$1" command="$2" hook_status

  jq -cn \
    --arg session_id "$SESSION_ID" \
    --arg cwd "$ROOT" \
    --arg command "$command" \
    '{session_id:$session_id,cwd:$cwd,tool_input:{command:$command}}' \
    >"$INPUT"

  set +e
  HOME="$HOME" ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" \
    ECI_ROLE=coordinator ECI_HOOK_IS_SUBAGENT=false \
    PATH="$ROOT/bin:$PATH" \
    bash "$TEST_HOOK_ROOT/validate-bash.sh" <"$INPUT" >"$OUTPUT" 2>"$ERROR_OUTPUT"
  hook_status=$?
  set -e
  [ "$hook_status" -eq 0 ] || {
    printf 'private marker-metadata hook exited nonzero: status=%s command=%s\n' \
      "$hook_status" "$command" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  }
  [ ! -s "$ERROR_OUTPUT" ] || {
    printf 'private marker-metadata hook wrote stderr for command=%s\n' "$command" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  }
}

assert_allowed() {
  local proof_root="$1" label="$2"

  run_hook "$proof_root" "printf '%s\\n' ordinary-marker-metadata-work"
  [ ! -s "$OUTPUT" ] || {
    printf 'ordinary work was denied by %s marker metadata:\n' "$label" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

assert_foreign_marker_mutation_denied() {
  local proof_root="$1" foreign_marker="$2"

  run_hook "$proof_root" "printf '%s\\n' accidental-overwrite > $foreign_marker"
  jq -e --arg foreign_marker "$foreign_marker" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED")) and
    (.hookSpecificOutput.permissionDecisionReason | contains($foreign_marker))
  ' "$OUTPUT" >/dev/null || {
    printf 'foreign active-marker mutation was not concretely denied:\n' >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

# A direct current-session marker plus its accepted session_-alias used to be
# treated as two owners and stopped an otherwise harmless command.
duplicate_proof="$TMP_ROOT/duplicate-proof"
write_marker "$duplicate_proof/$SESSION_ID/eci_active" "$ROOT" "$SESSION_ID"
write_marker "$duplicate_proof/session_$SESSION_ID/eci_active" "$ROOT" "session_$SESSION_ID"
assert_allowed "$duplicate_proof" 'duplicate alias'

# A malformed direct record must not poison an independently valid current
# session alias or unrelated ordinary work.
malformed_proof="$TMP_ROOT/malformed-proof"
mkdir -p -- "$malformed_proof/$SESSION_ID"
printf '%s\n' 'scope: malformed fixture' >"$malformed_proof/$SESSION_ID/eci_active"
write_marker "$malformed_proof/session_$SESSION_ID/eci_active" "$ROOT" "session_$SESSION_ID"
assert_allowed "$malformed_proof" 'malformed direct'

# A valid record retained at the current path with an old cwd is stale
# metadata, not a reason to reject a valid alias for this callback cwd.
stale_proof="$TMP_ROOT/stale-proof"
stale_cwd="$TMP_ROOT/stale-cwd"
mkdir -p -- "$stale_cwd"
write_marker "$stale_proof/$SESSION_ID/eci_active" "$stale_cwd" "$SESSION_ID"
write_marker "$stale_proof/session_$SESSION_ID/eci_active" "$ROOT" "session_$SESSION_ID"
assert_allowed "$stale_proof" 'stale direct'

# A proof-root scan that cannot form a safe observation is equally advisory
# for a harmless command; there is no resolved marker target to protect.
unsafe_proof="$TMP_ROOT/unsafe-proof"
printf '%s\n' 'not a proof directory' >"$unsafe_proof"
assert_allowed "$unsafe_proof" 'unsafe scan observation'

# Concrete cross-session marker mutation remains blocked when the current
# session has a resolved marker.
foreign_proof="$TMP_ROOT/foreign-proof"
foreign_marker="$foreign_proof/foreign-session/eci_active"
write_marker "$foreign_proof/$SESSION_ID/eci_active" "$ROOT" "$SESSION_ID"
write_marker "$foreign_marker" "$ROOT" 'foreign-session'
assert_foreign_marker_mutation_denied "$foreign_proof" "$foreign_marker"

# Marker readers keep the same owner/session/CWD semantics after a hardlink,
# while unrelated state readers retain their default single-link contract.
shared_proof="$TMP_ROOT/shared-proof"
shared_marker="$shared_proof/$SESSION_ID/eci_active"
write_marker "$shared_marker" "$ROOT" "$SESSION_ID"
ln -- "$shared_marker" "$TMP_ROOT/ordinary-marker-hardlink"
shared_marker="$(realpath -e -- "$shared_marker")"
(
  export ECI_PROOF_ROOT="$shared_proof"
  . "$ROOT/hooks/lib/eci-proof-state.sh"
  eci_marker_metadata_is_valid "$shared_marker" "$ROOT"
  [ "$(eci_state_value "$shared_marker" cwd false)" = "$ROOT" ]
  [ "$(eci_direct_marker_cwd "$(realpath -e -- "$shared_marker")" "$SESSION_ID")" = "$ROOT" ]
  [ "$(eci_marker_failure_code "$shared_marker" "$ROOT" "$SESSION_ID")" = ECI_MARKER_VALID ]
  ! eci_state_value "$shared_marker" cwd >/dev/null
  ! eci_marker_metadata_is_valid "$shared_marker" "$TMP_ROOT"
  [ "$(eci_marker_failure_code "$shared_marker" "$ROOT" wrong-session)" = ECI_MARKER_SCOPE_MISMATCH ]
)

printf '%s\n' 'marker metadata advisory regression: PASS'
