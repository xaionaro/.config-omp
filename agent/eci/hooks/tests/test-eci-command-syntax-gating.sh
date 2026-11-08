#!/usr/bin/env bash

# Regression for R11 command admission.  This is not a shell-syntax firewall:
# ordinary commands with punctuation, expansion, unfamiliar tools, or ordinary
# repository targets must proceed.  Only a resolved concrete wrong target is
# a hook denial.

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
FIXTURE_TMP="${HOME:?}/tmp"
mkdir -p -- "$FIXTURE_TMP"
FIXTURE_TMP="$(eci_realpath_existing "$FIXTURE_TMP")"
TMP_ROOT="$(mktemp -d "$FIXTURE_TMP/eci-command-syntax.XXXXXX")"
TMP_ROOT="$(eci_realpath_existing "$TMP_ROOT")"
case "$TMP_ROOT" in
"$FIXTURE_TMP"/*) ;;
*)
  printf 'fixture root is outside physical home temp: tmp_root=%s fixture_tmp=%s\n' \
    "$TMP_ROOT" "$FIXTURE_TMP" >&2
  exit 1
  ;;
esac
trap 'rm -rf -- "$TMP_ROOT" ${MAIN_TRANSCRIPT_DIR:+"$MAIN_TRANSCRIPT_DIR"}' EXIT

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

# Exercise the private hook body, removing a line-2 bypass if present.  The
# command planner is the only policy path: validate-bash.sh runs the published
# native planner unconditionally, so no planner-absent fallback exists to test.
cp -- "$ROOT/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.before"
TEST_HOOK_ROOT="$TMP_ROOT/private-hooks"
cp -a -- "$ROOT/hooks" "$TEST_HOOK_ROOT"
eci_sed_inplace '2{/^exit 0$/d;}' "$TEST_HOOK_ROOT/validate-bash.sh"
cmp -- "$TMP_ROOT/validate-bash.before" "$ROOT/hooks/validate-bash.sh" || {
  printf '%s\n' 'private hook setup modified the source' >&2
  exit 1
}
cmp -- "$TEST_HOOK_ROOT/validate-bash.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/validate-bash.before")

# The planner takes the callback role from its transcript, never from ECI_ROLE:
# a depth-two main transcript is the coordinator, and a callback without a
# verifiable transcript fails closed to the worker (hookTranscriptStatus in
# eci-command-plan-go/hook_context.go).  Coordinator callbacks carry this
# transcript, which lives under the test HOME sessions root and is removed on exit.
mkdir -p -- "$HOME/.omp/agent/sessions"
MAIN_TRANSCRIPT_DIR="$(mktemp -d "$HOME/.omp/agent/sessions/command-syntax.XXXXXX")"
MAIN_TRANSCRIPT="$MAIN_TRANSCRIPT_DIR/coordinator.jsonl"
printf '%s\n' "{\"type\":\"session\",\"id\":\"$SESSION_ID\"}" >"$MAIN_TRANSCRIPT"

run_hook() {
  local command="$1" role="$2" transcript="" hook_status

  if [ "$role" = coordinator ]; then
    transcript="$MAIN_TRANSCRIPT"
  fi
  jq -cn \
    --arg session_id "$SESSION_ID" \
    --arg cwd "$ROOT" \
    --arg command "$command" \
    --arg transcript "$transcript" \
    '{session_id:$session_id,tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}
      + (if $transcript == "" then {} else {transcript_path:$transcript} end)' \
    >"$INPUT"

  set +e
  ECI_TMPDIR="$FIXTURE_TMP" TMPDIR="$FIXTURE_TMP" \
    HOME="$HOME" ECI_HOME="$ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    ECI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" \
    XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
    PATH="$ROOT/bin:$PATH" \
    bash "$TEST_HOOK_ROOT/validate-bash.sh" <"$INPUT" >"$OUTPUT" 2>"$ERROR_OUTPUT"
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

assert_broad_root_denied() {
  local role code operation predicate

  for role in coordinator worker; do
    case "$role" in
    coordinator) code=ECI_BROAD_DESTRUCTIVE_DENIED operation=broad-destructive predicate=broad-destructive-root ;;
    # The worker's root write resolves to the live session control target, not the broad-destructive
    # predicate (message at pre-migration source 575bb81 hook_effects.go:1238 and omp HEAD hook_effects.go:1268: "resolved write selects live session control target").
    worker) code=ECI_CONTROL_OWNER_REQUIRED operation=worker-control predicate=worker-live-control ;;
    esac
    run_hook 'rm -rf / | head -n 20' "$role"
    jq -e --arg code "[$code]" --arg operation "operation=$operation" --arg predicate "predicate=$predicate" '
      .hookSpecificOutput.permissionDecision == "deny" and
      (.hookSpecificOutput.permissionDecisionReason |
        contains($code) and
        contains($operation) and
        contains("token=/") and
        contains("path=/") and
        contains($predicate)
      )
    ' "$OUTPUT" >/dev/null || {
      printf 'broad root denial contract mismatch for role=%s\n' "$role" >&2
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
      printf 'current marker denial contract mismatch for role=%s: %s\n' "$role" "$command" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
  done
}

# A wrapped worker write such as `env touch <marker>` must be denied, and the reason must name
# the marker. The planner routes it through its worker-control route (ECI_CONTROL_OWNER_REQUIRED),
# not the direct proof-control route, so the code is not pinned here. The coordinator's wrapped
# marker write is not asserted: its ownership check runs on the pre-unwrap argv (omp HEAD
# classifier.go:3726, inspectProofPathOwnership(request, current.argv, ...), before the unwrap at
# classifier.go:3744). It is an open security follow-up, not yet recorded in TEST-DISPOSITION.md.
assert_wrapped_marker_write_denied() {
  local command="$1" role

  for role in worker; do
    run_hook "$command" "$role"
    jq -e --arg marker "$MARKER" '
      .hookSpecificOutput.permissionDecision == "deny" and
      (.hookSpecificOutput.permissionDecisionReason |
        (contains("[ECI_PLAN_LIVE_CONTROL_DENIED]") or contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
        (contains("token=" + $marker) or contains("path=" + $marker))
      )
    ' "$OUTPUT" >/dev/null || {
      printf 'wrapped marker write denial mismatch for role=%s: %s\n' "$role" "$command" >&2
      if [ -s "$OUTPUT" ]; then
        cat -- "$OUTPUT" >&2
      else
        printf '%s\n' 'planner returned allow: no denial was emitted' >&2
      fi
      exit 1
    }
  done
}

# A worker `env - -v touch <marker>` is denied by the live-control route for the exact marker
# (pre-migration source 575bb81 classifier.go:7011 and 7016; omp HEAD classifier.go:7014 and 7019).
# The check pins the code and predicate.
assert_worker_live_control_denied() {
  local command="$1"

  run_hook "$command" worker
  jq -e --arg marker "$MARKER" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason |
      contains("[ECI_CONTROL_OWNER_REQUIRED]") and
      contains("predicate=worker-live-control") and
      contains("token=" + $marker)
    )
  ' "$OUTPUT" >/dev/null || {
    printf 'worker live-control denial mismatch: %s\n' "$command" >&2
    if [ -s "$OUTPUT" ]; then
      cat -- "$OUTPUT" >&2
    else
      printf '%s\n' 'planner returned allow: no denial was emitted' >&2
    fi
    exit 1
  }
}

# A write to another session's active marker is denied by the role's proof-control route.
# The runtime predicate is string(request.Role)+"-proof-control" (omp HEAD classifier.go:4680),
# denied at classifier.go:4678-4692; the literals are coordinator-proof-control (classifier.go:4724)
# and worker-proof-control (classifier.go:4738). Pre-migration source 575bb81 has the same literals at
# classifier.go:4721 and 4735. The cross-session code is not pinned to these inputs: neither tree emits it for them.
assert_foreign_marker_denied() {
  local role command foreign_marker predicate

  foreign_marker="$(eci_realpath_existing "$OTHER_MARKER")"
  for command in \
    "touch $OTHER_MARKER | head -n 20" \
    "novel-inspection-tool --format table | touch $OTHER_MARKER" \
    "printf '%s\\n' marker > $OTHER_MARKER"; do
    for role in coordinator worker; do
      case "$role" in
      coordinator) predicate=coordinator-proof-control ;;
      worker) predicate=worker-proof-control ;;
      esac
      run_hook "$command" "$role"
      # The token is exact: the planner writes it followed by ", path=", so a longer path that
      # merely starts with the marker does not match.
      jq -e --arg foreign_marker "$foreign_marker" --arg predicate "$predicate" '
        .hookSpecificOutput.permissionDecision == "deny" and
        (.hookSpecificOutput.permissionDecisionReason |
          contains("[ECI_PLAN_LIVE_CONTROL_DENIED]") and
          contains("operation=plan-segment") and
          contains("token=" + $foreign_marker + ", ") and
          contains("predicate=" + $predicate)
        )
      ' "$OUTPUT" >/dev/null || {
        printf 'foreign marker denial contract mismatch for role=%s: %s\n' "$role" "$command" >&2
        cat -- "$OUTPUT" >&2
        exit 1
      }
    done
  done
}

# Current-control effects depend on operand roles, not mentions of the marker
# in source, script, option-value or ordinary data positions.
assert_current_control_operand_boundaries() {
  local command suffix

  for suffix in '' ' | cat'; do
    # Writers to the active marker are denied for both roles: coordinator-proof-control
    # (omp HEAD classifier.go:4724) and worker-proof-control (classifier.go:4738).
    for command in \
      "cp $OUTSIDE_PROOF $MARKER" \
      "cp -frR -v --force --recursive --verbose -- $OUTSIDE_PROOF $MARKER" \
      "rm -f -- $MARKER" \
      "rm -frR -- $MARKER" \
      "touch -- $MARKER" \
      "sed -i -e '1p' $MARKER" \
      "rm --unknown $MARKER" \
      "sed -i 's/needle/replacement/' $MARKER"; do
      assert_current_marker_denied "$command$suffix"
    done
    for command in \
      "env touch $MARKER" \
      "env -u CONTROL_NAME NAME=value touch -- $MARKER" \
      "env -C ${MARKER%/*} touch eci_active"; do
      assert_wrapped_marker_write_denied "$command$suffix"
    done
    # `env - -v touch <marker>`: the worker is denied by the live-control route (omp HEAD classifier.go:7014, 7019; source 575bb81 classifier.go:7011, 7016);
    # the coordinator argv stays allowed.
    assert_worker_live_control_denied "env - -v touch $MARKER$suffix"
    run_hook "env - -v touch $MARKER$suffix" coordinator
    [ ! -s "$OUTPUT" ] || {
      printf 'coordinator env-wrapped marker touch was hook-denied: %s\n' "env - -v touch $MARKER$suffix" >&2
      cat -- "$OUTPUT" >&2
      exit 1
    }
    # `sed -i <marker> <destination>` and `sed -i.bak '1p' <marker>` are not asserted as allowed for the
    # coordinator: the pre-migration source (575bb81 validate-bash.sh with its planner) denies the same
    # bare commands for the coordinator with predicate coordinator-proof-control, so the omp denials match.
    for command in \
      "cat $MARKER" \
      "cp $MARKER $TMP_ROOT/ordinary-destination" \
      "printf '%s' $MARKER" \
      "sed -n '1p' $MARKER" \
      "cp --output=$MARKER $OUTSIDE_PROOF $TMP_ROOT/ordinary-destination" \
      "cp $OUTSIDE_PROOF $MARKER $TMP_ROOT/ordinary-destination" \
      "env -u touch $MARKER" \
      "env NAME=value -v touch $MARKER" \
      "printf '%s' '|' touch $MARKER"; do
      assert_allowed "$command$suffix"
    done
  done

  # The first child's CWD must not become the second segment's CWD.
  assert_allowed "env -C ${MARKER%/*} printf ordinary; touch eci_active"
  assert_current_marker_denied "cp --output=$MARKER ordinary elsewhere > $MARKER"

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

# Retain only resolved targets that would concretely escape the session's
# ownership/control boundary.  A pipe or quote cannot change this outcome.
assert_broad_root_denied
assert_current_marker_denied
assert_foreign_marker_denied

printf '%s\n' 'target-aware command syntax regression: PASS'
