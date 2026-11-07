#!/usr/bin/env bash

# Regression for R11 command admission.  This is not a shell-syntax firewall:
# ordinary commands with punctuation, expansion, unfamiliar tools, or ordinary
# repository targets must proceed.  Only a resolved concrete wrong target is
# a hook denial.

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
FIXTURE_TMP="${HOME:?}/tmp"
mkdir -p -- "$FIXTURE_TMP"
FIXTURE_TMP="$(realpath -e -- "$FIXTURE_TMP")"
TMP_ROOT="$(mktemp -d "$FIXTURE_TMP/eci-command-syntax.XXXXXX")"
TMP_ROOT="$(realpath -e -- "$TMP_ROOT")"
case "$TMP_ROOT" in
"$FIXTURE_TMP"/*) ;;
*)
  printf 'fixture root is outside physical home temp: tmp_root=%s fixture_tmp=%s\n' \
    "$TMP_ROOT" "$FIXTURE_TMP" >&2
  exit 1
  ;;
esac
trap 'rm -rf -- "$TMP_ROOT"' EXIT

SESSION_ID="t00-command-syntax"
PROOF_ROOT="$TMP_ROOT/proof"
MARKER="$PROOF_ROOT/$SESSION_ID/eci_active"
OTHER_MARKER="$PROOF_ROOT/other-session/eci_active"
INPUT="$TMP_ROOT/input.json"
OUTPUT="$TMP_ROOT/output.json"
ERROR_OUTPUT="$TMP_ROOT/error.txt"
EVIDENCE_DIR="$PROOF_ROOT/$SESSION_ID/evidence"
OUTSIDE_PROOF="$TMP_ROOT/outside-proof.txt"

mkdir -p -- "$(dirname -- "$MARKER")" "$(dirname -- "$OTHER_MARKER")" \
  "$EVIDENCE_DIR" "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
printf '%s\n' \
  'scope: command syntax regression' \
  "cwd: $ROOT" \
  "session_id: $SESSION_ID" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$MARKER"
printf '%s\n' \
  'scope: foreign-session control fixture' \
  "cwd: $ROOT" \
  'session_id: other-session' \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$OTHER_MARKER"
printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"
printf '%s\n' outside >"$OUTSIDE_PROOF"
ln -s -- "$OUTSIDE_PROOF" "$EVIDENCE_DIR/outside-link"

# Exercise the private hook bodies, removing a line-2 bypass if present.
cp -- "$ROOT/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.before"
TEST_HOOK_ROOT="$TMP_ROOT/private-hooks"
NO_PLANNER_HOOK_ROOT="$TMP_ROOT/private-hooks-no-planner"
cp -a -- "$ROOT/hooks" "$TEST_HOOK_ROOT"
cp -a -- "$ROOT/hooks" "$NO_PLANNER_HOOK_ROOT"
sed -i '2{/^exit 0$/d;}' -- "$TEST_HOOK_ROOT/validate-bash.sh"
sed -i '2{/^exit 0$/d;}' -- "$NO_PLANNER_HOOK_ROOT/validate-bash.sh"
cmp -- "$TMP_ROOT/validate-bash.before" "$ROOT/hooks/validate-bash.sh" || {
  printf '%s\n' 'private hook setup modified the source' >&2
  exit 1
}
cmp -- "$TEST_HOOK_ROOT/validate-bash.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/validate-bash.before")
cmp -- "$NO_PLANNER_HOOK_ROOT/validate-bash.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/validate-bash.before")
mv -- "$NO_PLANNER_HOOK_ROOT/lib/eci-command-plan-go" "$TMP_ROOT/no-planner-command-plan"

run_hook() {
  local command="$1" role="$2" planner_mode="${3:-available}" hook_root hook_status

  case "$planner_mode" in
  available)
    hook_root="$TEST_HOOK_ROOT"
    ;;
  unavailable)
    # This second copied hook has no planner source directory, which selects
    # the hook's transparent target-aware fallback without touching the live
    # runtime or executing any tested command.
    hook_root="$NO_PLANNER_HOOK_ROOT"
    ;;
  *)
    printf 'unknown planner mode: %s\n' "$planner_mode" >&2
    exit 1
    ;;
  esac

  jq -cn \
    --arg session_id "$SESSION_ID" \
    --arg cwd "$ROOT" \
    --arg command "$command" \
    '{session_id:$session_id,tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}' \
    >"$INPUT"

  set +e
  ECI_TMPDIR="$FIXTURE_TMP" TMPDIR="$FIXTURE_TMP" \
    HOME="$HOME" ECI_HOME="$ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    ECI_ROLE="$role" ECI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" \
    XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
    PATH="$ROOT/bin:$PATH" \
    bash "$hook_root/validate-bash.sh" <"$INPUT" >"$OUTPUT" 2>"$ERROR_OUTPUT"
  hook_status=$?
  set -e
  [ "$hook_status" -eq 0 ] || {
    printf 'private command-syntax hook exited nonzero: status=%s command=%s\n' "$hook_status" "$command" >&2
    cat -- "$ERROR_OUTPUT" >&2
    exit 1
  }
  grep -v -e '^command hook advisory: classify callback transcript: callback transcript is unknown' -- "$ERROR_OUTPUT" >"$TMP_ROOT/error-unexpected.txt" || true
  [ ! -s "$TMP_ROOT/error-unexpected.txt" ] || {
    printf 'private command-syntax hook wrote stderr for command=%s\n' "$command" >&2
    cat -- "$TMP_ROOT/error-unexpected.txt" >&2
    exit 1
  }
}

assert_allowed() {
  local command="$1" role

  for role in coordinator worker; do
    run_hook "$command" "$role"
    [ ! -s "$OUTPUT" ] || {
      printf 'ordinary command was hook-denied for role=%s: %s\n' "$role" "$command" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

assert_allowed_without_planner() {
  local command="$1" role

  for role in coordinator worker; do
    run_hook "$command" "$role" unavailable
    [ ! -s "$OUTPUT" ] || {
      printf 'ordinary no-planner command was hook-denied for role=%s: %s\n' "$role" "$command" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

assert_broad_root_denied() {
  local role

  for role in coordinator worker; do
    run_hook 'rm -rf / | head -n 20' "$role"
    jq -e '
      .hookSpecificOutput.permissionDecision == "deny" and
      (.hookSpecificOutput.permissionDecisionReason |
        contains("[ECI_BROAD_DESTRUCTIVE_DENIED]") and
        contains("operation=broad-destructive") and
        contains("token=/") and
        contains("path=/") and
        contains("predicate=broad-destructive-root")
      )
    ' "$OUTPUT" >/dev/null || {
      printf 'broad root denial contract mismatch for role=%s\n' "$role" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

assert_broad_root_denied_without_planner() {
  local role

  for role in coordinator worker; do
    run_hook 'rm -rf / | head -n 20' "$role" unavailable
    jq -e '
      .hookSpecificOutput.permissionDecision == "deny" and
      (.hookSpecificOutput.permissionDecisionReason |
        contains("[ECI_BROAD_DESTRUCTIVE_DENIED]") and
        contains("operation=broad-destructive") and
        contains("token=/") and
        contains("target=/") and
        contains("class=broad")
      )
    ' "$OUTPUT" >/dev/null || {
      printf 'no-planner broad root denial contract mismatch for role=%s\n' "$role" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

assert_current_marker_denied() {
  local command="${1:-touch $MARKER | head -n 20}" role predicate

  for role in coordinator worker; do
    case "$role" in
    coordinator) predicate=coordinator-proof-control ;;
    worker) predicate=worker-proof-control ;;
    esac
    run_hook "$command" "$role"
    jq -e --arg marker "$MARKER" --arg predicate "$predicate" '
      .hookSpecificOutput.permissionDecision == "deny" and
      (.hookSpecificOutput.permissionDecisionReason |
        contains("[ECI_PLAN_LIVE_CONTROL_DENIED]") and
        contains("operation=plan-segment") and
        contains("token=" + $marker) and
        contains("path=" + $marker) and
        contains("predicate=" + $predicate)
      )
    ' "$OUTPUT" >/dev/null || {
      printf 'current marker denial contract mismatch for role=%s\n' "$role" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

assert_current_marker_denied_without_planner() {
  local command="${1:-touch $MARKER | head -n 20}" role

  for role in coordinator worker; do
    run_hook "$command" "$role" unavailable
    jq -e --arg marker "$MARKER" '
      .hookSpecificOutput.permissionDecision == "deny" and
      (.hookSpecificOutput.permissionDecisionReason |
        contains("[ECI_CONTROL_OWNER_REQUIRED]") and
        contains("resolved_control_target=" + $marker)
      )
    ' "$OUTPUT" >/dev/null || {
      printf 'no-planner current marker denial contract mismatch for role=%s: %s\n' "$role" "$command" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

assert_foreign_marker_denied() {
  local role command foreign_marker

  foreign_marker="$(realpath -e -- "$OTHER_MARKER")"
  for command in \
    "touch $OTHER_MARKER | head -n 20" \
    "novel-inspection-tool --format table | touch $OTHER_MARKER" \
    "printf '%s\\n' marker > $OTHER_MARKER"; do
    for role in coordinator worker; do
      run_hook "$command" "$role"
      jq -e --arg foreign_marker "$foreign_marker" '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason |
          contains("[ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED]") and
          contains("operation=eci-control") and
          contains("target=" + $foreign_marker) and
          contains("foreign_session=other-session")
        )
      ' "$OUTPUT" >/dev/null || {
        printf 'foreign marker denial contract mismatch for role=%s: %s\n' "$role" "$command" >&2
        cat -- "$OUTPUT" >&2
        exit 1
      }
    done
  done
}

assert_foreign_marker_denied_without_planner() {
  local role command foreign_marker

  command="novel-inspection-tool --format table | touch $OTHER_MARKER"
  foreign_marker="$(realpath -e -- "$OTHER_MARKER")"
  for role in coordinator worker; do
    run_hook "$command" "$role" unavailable
    jq -e --arg foreign_marker "$foreign_marker" '
      .hookSpecificOutput.permissionDecision == "deny" and
      (.hookSpecificOutput.permissionDecisionReason |
        contains("[ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED]") and
        contains("operation=eci-control") and
        contains("target=" + $foreign_marker) and
        contains("foreign_session=other-session")
      )
    ' "$OUTPUT" >/dev/null || {
      printf 'no-planner foreign marker denial contract mismatch for role=%s: %s\n' "$role" "$command" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

assert_worker_fsck_lost_found_denied() {
  run_hook 'env git fsck --lost-found | head -n 20' worker
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason |
      contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]") and
      contains("operation=worker-git-ownership") and
      contains("token=--lost-found") and
      contains("predicate=worker-git-ownership") and
      contains("worker argv selects Git fsck --lost-found writer") and
      (contains("predicate=worker-env-git-fsck-lost-found") | not)
    )
  ' "$OUTPUT" >/dev/null || {
    printf '%s\n' 'worker Git fsck --lost-found denial contract mismatch' >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

# Current-control effects depend on operand roles, not mentions of the marker
# in source, script, option-value or ordinary data positions.
assert_current_control_operand_boundaries() {
  local command suffix

  for suffix in '' ' | cat'; do
    for command in \
      "cp $OUTSIDE_PROOF $MARKER" \
      "cp -frR -v --force --recursive --verbose -- $OUTSIDE_PROOF $MARKER" \
      "rm -f -- $MARKER" \
      "rm -frR -- $MARKER" \
      "touch -- $MARKER" \
      "env touch $MARKER" \
      "env -u CONTROL_NAME NAME=value touch -- $MARKER" \
      "env -C ${MARKER%/*} touch eci_active" \
      "sed -i 's/needle/replacement/' $MARKER"; do
      assert_current_marker_denied_without_planner "$command$suffix"
    done
    for command in \
      "cat $MARKER" \
      "cp $MARKER $TMP_ROOT/ordinary-destination" \
      "printf '%s' $MARKER" \
      "sed -n '1p' $MARKER" \
      "sed -i '$MARKER' $TMP_ROOT/ordinary-destination" \
      "sed -i.bak '1p' $MARKER" \
      "sed -i -e '1p' $MARKER" \
      "rm --unknown $MARKER" \
      "cp --output=$MARKER $OUTSIDE_PROOF $TMP_ROOT/ordinary-destination" \
      "cp $OUTSIDE_PROOF $MARKER $TMP_ROOT/ordinary-destination" \
      "env -u touch $MARKER" \
      "env - -v touch $MARKER" \
      "env NAME=value -v touch $MARKER" \
      "printf '%s' '|' touch $MARKER"; do
      assert_allowed_without_planner "$command$suffix"
    done
  done

  # The first child's CWD must not become the second segment's CWD.
  assert_allowed_without_planner "env -C ${MARKER%/*} printf ordinary; touch eci_active"
  assert_current_marker_denied_without_planner "cp --output=$MARKER ordinary elsewhere > $MARKER"

  for command in \
    "cp $MARKER $TMP_ROOT/ordinary-destination" \
    "cp -f -- $MARKER $TMP_ROOT/ordinary-destination" \
    "cp --output=$MARKER $OUTSIDE_PROOF $TMP_ROOT/ordinary-destination"; do
    assert_allowed "$command"
  done
  assert_current_marker_denied "cp $OUTSIDE_PROOF $MARKER"
  assert_current_marker_denied "cp --output=$MARKER ordinary elsewhere > $MARKER"
}

# These commands may fail when executed in a real shell (for example an
# unfamiliar executable), but the admission hook must not deny them merely for
# their form.  The test executes only the hook, never these commands.
assert_current_control_operand_boundaries
assert_allowed $'printf \'%s\\n\' first\nprintf \'%s\\n\' second'
assert_allowed 'printf "%s\\n" "$(printf nested)"'
assert_allowed 'novel-inspection-tool --format table'
assert_allowed 'novel-inspection-tool --format table | head -n 20'
assert_allowed 'env'
assert_allowed "env | sort | rg '^PATH='"
assert_allowed 'printenv'
assert_allowed 'env --unset=9FOO novel-tool'
assert_allowed "env FOO=bar python3 -c 'print(1)'"
assert_allowed 'file -C -m hooks/validate-bash.sh | head -n 20'
assert_allowed 'touch hooks/validate-bash.sh'
assert_allowed "printf '%s\\n' ordinary > $ROOT/.eci-normal-redirect-target"
assert_allowed "printf '%s\\n' nested <(printf '%s\\n' input)"
assert_allowed 'printf "%s\n" brace-{one,two}'
assert_allowed 'printf "%s\n" `printf nested`'
assert_allowed 'printf "%s\n" background &'
assert_allowed "cat $EVIDENCE_DIR/outside-link"
assert_allowed "rm -f -- $ROOT/.eci-normal-target-does-not-exist"
assert_allowed 'cat /tmp/ordinary-eci-inspection'
assert_allowed 'rm -f -- /tmp/ordinary-eci-scratch'
assert_allowed 'TMPDIR=/tmp novel-tool --scratch'
assert_allowed "cat $OTHER_MARKER"
assert_allowed "cat $OTHER_MARKER | head -n 20"
assert_allowed "printf '%s\\n' '|' touch $OTHER_MARKER"
assert_allowed "printf '%s\\n' '|' touch $OTHER_MARKER | head -n 20"
assert_allowed_without_planner "cat $OTHER_MARKER | head -n 20"
assert_allowed_without_planner "printf '%s\\n' '|' touch $OTHER_MARKER | head -n 20"

# `git fsck --lost-found` writes recovered objects under .git/lost-found.  Its
# worker denial preserves the concrete Git ownership boundary, independent of
# the surrounding env wrapper or pipeline syntax.
assert_worker_fsck_lost_found_denied

# Retain only resolved targets that would concretely escape the session's
# ownership/control boundary.  A pipe or quote cannot change this outcome.
assert_broad_root_denied
assert_current_marker_denied
assert_foreign_marker_denied
assert_broad_root_denied_without_planner
assert_current_marker_denied_without_planner
assert_foreign_marker_denied_without_planner

printf '%s\n' 'target-aware command syntax regression: PASS'
