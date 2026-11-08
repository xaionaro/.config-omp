#!/usr/bin/env bash

set -Eeuo pipefail

# ROOT identifies the checked-in ECI source tree under test.
ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
# TMP_PARENT keeps the private hook fixture out of the repository tree.
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
# TMP_ROOT contains the complete disposable hook fixture.
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-planner-protocol-fallback.XXXXXX")"
# TMP_ROOT is canonicalized before its paths are embedded in marker records.
TMP_ROOT="$(realpath -e -- "$TMP_ROOT")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

# FIXTURE_HOME is the temporary HOME whose `.eci` child owns the hook.
FIXTURE_HOME="$TMP_ROOT/home"
# FIXTURE_ROOT is the copied source tree selected through FIXTURE_HOME.
FIXTURE_ROOT="$FIXTURE_HOME/.omp/agent/eci"
# FIXTURE_HOOK is the private validating hook with any line-2 bypass removed.
FIXTURE_HOOK="$FIXTURE_ROOT/hooks/validate-bash.sh"
# PROOF_ROOT holds the current-session marker consumed by the fixture hook.
PROOF_ROOT="$TMP_ROOT/proof"
# SESSION names the one marker owned by this isolated test.
SESSION="planner-protocol-fallback"
# FOREIGN_SESSION names a second proof record whose marker must remain untouched.
FOREIGN_SESSION="planner-protocol-fallback-peer"
# INPUT carries one PreToolUse request into the fixture hook.
INPUT="$TMP_ROOT/input.json"
# OUTPUT records the fixture hook's sole JSON response.
OUTPUT="$TMP_ROOT/output.json"

# validate-bash.sh now hands the whole request to the verified native
# eci-command-plan binary (--hook). It no longer has a shell-side planner
# response or recursive callback seam, so this test runs the real hook and
# real binary. It asserts the control-target and operand boundaries that the
# retired malformed-planner fixture used to assert.
mkdir -p -- "$FIXTURE_ROOT" "$PROOF_ROOT/$SESSION" "$PROOF_ROOT/$FOREIGN_SESSION" "$FIXTURE_HOME/tmp" \
  "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
cp -a -- "$ROOT/bin" "$FIXTURE_ROOT/"
cp -a -- "$ROOT/hooks" "$FIXTURE_ROOT/"

sed -i '2{/^exit 0$/d;}' -- "$FIXTURE_HOOK"
cmp -- "$FIXTURE_HOOK" <(sed '2{/^exit 0$/d;}' -- "$ROOT/hooks/validate-bash.sh")

printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"
printf '%s\n' \
  'scope: hook boundary regression' \
  "cwd: $FIXTURE_ROOT" \
  "session_id: $SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"
printf '%s\n' \
  'scope: hook boundary peer fixture' \
  "cwd: $FIXTURE_ROOT" \
  "session_id: $FOREIGN_SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$PROOF_ROOT/$FOREIGN_SESSION/eci_active"

# The planner takes the callback role from its transcript, never from ECI_ROLE:
# a depth-two main transcript is the coordinator, and a callback without a
# verifiable transcript fails closed to the worker (hook_context.go).
mkdir -p -- "$FIXTURE_HOME/.omp/agent/sessions/fallback"
MAIN_TRANSCRIPT="$FIXTURE_HOME/.omp/agent/sessions/fallback/coordinator.jsonl"
printf '%s\n' "{\"type\":\"session\",\"id\":\"$SESSION\"}" >"$MAIN_TRANSCRIPT"

# run_hook submits one command without executing it, then preserves the hook
# response for the assertion immediately following the call.
#
# Example: run_hook 'printf ordinary'.
run_hook() {
  local command="$1" transcript=""

  [ "$ROLE" = worker ] || transcript="$MAIN_TRANSCRIPT"
  jq -cn --arg cwd "$FIXTURE_ROOT" --arg command "$command" --arg session "$SESSION" \
    --arg transcript "$transcript" \
    '{tool_name:"Bash",session_id:$session,cwd:$cwd,tool_input:{command:$command}}
      + (if $transcript == "" then {} else {transcript_path:$transcript} end)' >"$INPUT"
  HOME="$FIXTURE_HOME" ECI_HOME="$FIXTURE_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" \
    ECI_HOOK_IS_SUBAGENT="$IS_WORKER" \
    PATH="/usr/bin:/bin" bash "$FIXTURE_HOOK" <"$INPUT" >"$OUTPUT"
}

# assert_empty fails when the hook produced any response for $1.
assert_empty() {
  [ ! -s "$OUTPUT" ] || {
    printf '%s\n' "$ROLE: $1 was blocked:" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

# assert_deny fails unless the response denies with code $1 and contains $2.
assert_deny() {
  jq -e --arg code "$1" --arg text "$2" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[" + $code + "]") and contains($text))
  ' "$OUTPUT" >/dev/null || {
    printf '%s\n' "$ROLE: expected $1 denial for: $3" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

# assert_control_deny fails unless the response denies a write that resolves
# to the current control marker $1 (same codes as test-eci-command-syntax-gating.sh).
assert_control_deny() {
  jq -e --arg marker "$1" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason |
      (contains("[ECI_PLAN_LIVE_CONTROL_DENIED]") or contains("[ECI_CONTROL_OWNER_REQUIRED]")) and
      (contains("token=" + $marker) or contains("path=" + $marker)))
  ' "$OUTPUT" >/dev/null || {
    printf '%s\n' "$ROLE: current-control writer escaped: $2" >&2
    cat -- "$OUTPUT" >&2
    exit 1
  }
}

for ROLE in coordinator worker; do
  IS_WORKER=false
  if [ "$ROLE" = worker ]; then
    IS_WORKER=true
  fi
  marker="$PROOF_ROOT/$SESSION/eci_active"

  run_hook 'printf first; printf second'
  assert_empty 'ordinary compound'

  # A worker's root write resolves to the live control target instead
  # (test-eci-command-syntax-gating.sh documents the same split).
  run_hook 'printf first; rm -rf /'
  if [ "$ROLE" = coordinator ]; then
    assert_deny ECI_BROAD_DESTRUCTIVE_DENIED operation=broad-destructive 'printf first; rm -rf /'
  else
    assert_deny ECI_CONTROL_OWNER_REQUIRED operation=worker-control 'printf first; rm -rf /'
  fi

  run_hook "printf first; printf marker > $PROOF_ROOT/$FOREIGN_SESSION/eci_active"
  assert_deny ECI_PLAN_LIVE_CONTROL_DENIED "token=$PROOF_ROOT/$FOREIGN_SESSION/eci_active" 'foreign marker write'

  # Writers of the current control marker.
  for command in \
    "cp $INPUT $marker" \
    "rm -f -- $marker" \
    "touch -- $marker" \
    "sed -i 's/needle/replacement/' $marker"; do
    run_hook "printf first; $command"
    assert_control_deny "$marker" "$command"
  done

  # Wrapped env writers. The coordinator currently allows both forms (open
  # security follow-up in TEST-DISPOSITION: pre-unwrap ownership check); restore
  # the coordinator assertions when that classifier fix lands.
  if [ "$ROLE" = worker ]; then
    for command in \
      "env -u CONTROL_NAME NAME=value touch $marker" \
      "env -C ${marker%/*} touch eci_active"; do
      run_hook "printf first; $command"
      assert_control_deny "$marker" "$command"
    done
  fi

  # Source operands and consumed env options are not write targets. The real
  # planner conservatively denies `sed -i '<marker>' <file>` and
  # `rm --unknown <marker>`; those were fallback-only allowances (TEST-DISPOSITION).
  for command in \
    "cat $marker" \
    "cp $marker $TMP_ROOT/ordinary-destination" \
    "cp --output=$marker ordinary elsewhere" \
    "printf '%s' $marker" \
    "sed -n '1p' $marker" \
    "env -u touch $marker"; do
    run_hook "printf first; $command"
    assert_empty "source/data operand: $command"
  done

  # GNU env treats `-` as -i and then execs `-v` (rc 127), so touch never runs:
  # coordinators are allowed; workers fail closed on the marker operand.
  run_hook "printf first; env - -v touch $marker"
  if [ "$ROLE" = coordinator ]; then
    assert_empty "consumed env option: env - -v touch"
  else
    assert_deny ECI_CONTROL_OWNER_REQUIRED operation=worker-control 'env - -v touch'
  fi
  printf '%s\n' "$ROLE: ordinary compound allowed; broad, foreign and control targets denied"
done

printf '%s\n' 'planner protocol fallback: PASS'
