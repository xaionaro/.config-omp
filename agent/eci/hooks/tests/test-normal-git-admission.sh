#!/usr/bin/env bash

set -Eeuo pipefail

SOURCE_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-normal-git-admission.XXXXXX")"
TMP_ROOT="$(realpath -e -- "$TMP_ROOT")"
trap 'chmod -R u+w -- "$TMP_ROOT"; rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM
trap 'printf "fixture failed: target=%s line=%s\n" "${NORMAL_GIT_ADMISSION_TARGET:-full}" "$LINENO" >&2' ERR

# Exercise a complete private runtime, removing only the exact development
# bridge when present. Never change the live runtime.
HOME_ROOT="$TMP_ROOT/home"
RUNTIME_ROOT="$HOME_ROOT/.omp/agent/eci"
mkdir -p -- "$RUNTIME_ROOT" "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
cp -a -- "$SOURCE_ROOT/hooks" "$RUNTIME_ROOT"
if [ -n "${NORMAL_GIT_ADMISSION_HOOK_SOURCE:-}" ]; then
  cp -- "$NORMAL_GIT_ADMISSION_HOOK_SOURCE" "$RUNTIME_ROOT/hooks/validate-bash.sh"
fi
cp -- "$SOURCE_ROOT/hooks.json" "$RUNTIME_ROOT/hooks.json"
mkdir -p -- "$RUNTIME_ROOT/bin"
# Build the disposable runtime from its copied sources. Clean archives contain
# neither ignored executables nor optional planner provenance.
source "$RUNTIME_ROOT/hooks/lib/eci-runtime-sync.sh"
PATH="/usr/lib/go-1.26/bin:$PATH" eci_runtime_build_missing "$RUNTIME_ROOT" \
  eci-command-gate-mode eci-command-plan eci-safe-import eci-worker-git
source_hook_sha="$(sha256sum -- "$RUNTIME_ROOT/hooks/validate-bash.sh" | awk '{print $1}')"
if cmp -s -- <(head -n 9 -- "$RUNTIME_ROOT/hooks/validate-bash.sh") <(cat <<'BRIDGE'
#!/usr/bin/env bash
# PreToolUse hook: validate Bash commands before execution.

# Temporarily bypass command validation: Git preparation checks reject scoped
# Producer operations (mv, rm, restore, checkout), and some denials route
# them to a Supervisor that cannot perform their contribution. Remove this
# bypass once effect-aware routing and actionable guidance are fixed and verified.
exit 0

BRIDGE
); then
  sed '4,9d' "$RUNTIME_ROOT/hooks/validate-bash.sh" >"$RUNTIME_ROOT/hooks/validate-bash.sh.tmp"
  mv -- "$RUNTIME_ROOT/hooks/validate-bash.sh.tmp" "$RUNTIME_ROOT/hooks/validate-bash.sh"
fi
exercised_hook_sha="$(sha256sum -- "$RUNTIME_ROOT/hooks/validate-bash.sh" | awk '{print $1}')"
printf 'registered_hook source_sha256=%s exercised_sha256=%s\n' "$source_hook_sha" "$exercised_hook_sha"
BASH_LAUNCHER="$(jq -er '.hooks.PreToolUse[] | select(.matcher == "^Bash$") | .hooks[] | select(.type == "command") | .command' "$RUNTIME_ROOT/hooks.json")"
[ "$BASH_LAUNCHER" = 'bash "$HOME/.omp/agent/eci/hooks/validate-bash.sh"' ] || {
  printf 'unexpected copied Bash launcher: %s\n' "$BASH_LAUNCHER" >&2
  exit 1
}

# Refresh only this private fixture's planner receipt. This keeps the test on
# the real hook path while avoiding a source build for every Git probe.
PLANNER_DIR="$RUNTIME_ROOT/hooks/lib/eci-command-plan-go"
(
  cd -- "$PLANNER_DIR"
  /usr/lib/go-1.26/bin/go build -trimpath -buildvcs=false -o eci-command-plan .
)
chmod 755 -- "$PLANNER_DIR/eci-command-plan"
planner_go_mod_sha="$(sha256sum -- "$PLANNER_DIR/go.mod" | awk '{print $1}')"
planner_main_sha="$(sha256sum -- "$PLANNER_DIR/main.go" | awk '{print $1}')"
planner_classifier_sha="$(sha256sum -- "$PLANNER_DIR/classifier.go" | awk '{print $1}')"
planner_binary_sha="$(sha256sum -- "$PLANNER_DIR/eci-command-plan" | awk '{print $1}')"
planner_binary_size="$(stat -Lc '%s' -- "$PLANNER_DIR/eci-command-plan")"
[ -e "$PLANNER_DIR/.eci-command-plan.provenance" ] || : >"$PLANNER_DIR/.eci-command-plan.provenance"
awk \
  -v go_mod="$planner_go_mod_sha" \
  -v main="$planner_main_sha" \
  -v classifier="$planner_classifier_sha" \
  -v binary="$planner_binary_sha" \
  -v size="$planner_binary_size" '
  $1 == "source_go.mod_sha256" { print $1 "\t" go_mod; next }
  $1 == "source_main.go_sha256" { print $1 "\t" main; next }
  $1 == "source_classifier.go_sha256" { print $1 "\t" classifier; next }
  $1 == "binary_sha256" { print $1 "\t" binary; next }
  $1 == "binary_size" { print $1 "\t" size; next }
  { print }
' "$PLANNER_DIR/.eci-command-plan.provenance" >"$PLANNER_DIR/.eci-command-plan.provenance.tmp"
mv -- "$PLANNER_DIR/.eci-command-plan.provenance.tmp" "$PLANNER_DIR/.eci-command-plan.provenance"
chmod 600 -- "$PLANNER_DIR/.eci-command-plan.provenance"

printf '%s\n' enforcing >"$TMP_ROOT/config/eci/command-gate-mode"

REPO="$TMP_ROOT/repo"
FOREIGN_REPO="$TMP_ROOT/foreign-repo"
NON_REPO="$TMP_ROOT/not-a-repository"
for path in "$REPO" "$FOREIGN_REPO"; do
  mkdir -p -- "$path"
  git -C "$path" init -q
  git -C "$path" config user.email normal-git-test@example.invalid
  git -C "$path" config user.name 'Normal Git Test'
  printf 'base\n' >"$path/file.txt"
  printf '{"fixture":true}\n' >"$path/hooks.json"
  printf '# Fixture\n' >"$path/README.md"
  git -C "$path" add -- file.txt hooks.json README.md
  git -C "$path" commit -qm initial
done
REPO="$(realpath -e -- "$REPO")"
FOREIGN_REPO="$(realpath -e -- "$FOREIGN_REPO")"
mkdir -p -- "$NON_REPO"
NON_REPO="$(realpath -e -- "$NON_REPO")"

SESSION='normal-git-session'
PROOF_ROOT="$TMP_ROOT/proof"
mkdir -p -- "$PROOF_ROOT/$SESSION"
printf '%s\n' \
  'scope: normal Git admission regression' \
  "cwd: $REPO" \
  "session_id: $SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"

KIMI_PROOF_ROOT_RAW="$TMP_ROOT/kimi-proof"
mkdir -p -- "$KIMI_PROOF_ROOT_RAW"
KIMI_PROOF_ROOT="$(realpath -e -- "$KIMI_PROOF_ROOT_RAW")"
KIMI_FOREIGN_SESSION='foreign-kimi-session'
KIMI_FOREIGN_MARKER="$KIMI_PROOF_ROOT/$KIMI_FOREIGN_SESSION/eci_active"
mkdir -p -- "${KIMI_FOREIGN_MARKER%/*}"
printf '%s\n' \
  'scope: foreign Kimi marker regression' \
  "cwd: $FOREIGN_REPO" \
  "session_id: $KIMI_FOREIGN_SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$KIMI_FOREIGN_MARKER"

# A second valid marker gives timeout replay tests a concrete foreign control
# target without changing the callback's current-session binding.
FOREIGN_SESSION='foreign-timeout-session'
FOREIGN_TIMEOUT_CONTROL_INNER="$PROOF_ROOT/$FOREIGN_SESSION"
mkdir -p -- "$FOREIGN_TIMEOUT_CONTROL_INNER"
printf '%s\n' \
  'scope: foreign timeout marker regression' \
  "cwd: $FOREIGN_REPO" \
  "session_id: $FOREIGN_SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"

# Leading punctuation is valid in a proof-session identity.  Keep concrete
# foreign markers for the timeout replay boundary instead of re-validating
# their spelling in the consumer under test.
FOREIGN_UNDERSCORE_SESSION='_foreign-timeout-session'
FOREIGN_DASH_SESSION='-foreign-timeout-session'
for foreign_session in "$FOREIGN_UNDERSCORE_SESSION" "$FOREIGN_DASH_SESSION"; do
  mkdir -p -- "$PROOF_ROOT/$foreign_session"
  printf '%s\n' \
    'scope: foreign timeout marker regression' \
    "cwd: $FOREIGN_REPO" \
    "session_id: $foreign_session" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$PROOF_ROOT/$foreign_session/eci_active"
done

# The callback PATH is deliberately deterministic because the planner must
# resolve a bare timeout from the callback's original PATH, before the hook
# prepends its own trusted utility directories.
BASE_CALLBACK_PATH="$RUNTIME_ROOT/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
CALLBACK_PATH="$BASE_CALLBACK_PATH"

FAKE_TIMEOUT_LAUNCH_DIR="$TMP_ROOT/fake-timeout-launch"
FAKE_TIMEOUT_ACCEPT_DIR="$TMP_ROOT/fake-timeout-accept"
FAKE_TIMEOUT_INVALID_DIR="$TMP_ROOT/fake-timeout-invalid"
FAKE_TIMEOUT_REQUIRED_DIR="$TMP_ROOT/fake-timeout-required"
mkdir -p -- "$FAKE_TIMEOUT_LAUNCH_DIR" "$FAKE_TIMEOUT_ACCEPT_DIR" "$FAKE_TIMEOUT_INVALID_DIR" "$FAKE_TIMEOUT_REQUIRED_DIR"
printf '%s\n' \
  '#!/bin/bash' \
  'set -euo pipefail' \
  'signal=""' \
  'while (($#)); do' \
  '  case "$1" in' \
  '    --signal) signal="${2:-}"; shift 2 ;;' \
  '    --signal=*) signal="${1#--signal=}"; shift ;;' \
  '    -s) signal="${2:-}"; shift 2 ;;' \
  '    -s*) signal="${1#-s}"; shift ;;' \
  '    --preserve-status|--foreground|--verbose|-p|-f|-v) shift ;;' \
  '    --) shift; break ;;' \
  '    -*) exit 125 ;;' \
  '    *) shift; break ;;' \
  '  esac' \
  'done' \
  'case "$signal" in' \
  '  0|invalid-signal) exit 125 ;;' \
  'esac' \
  'exec "$@"' \
  >"$FAKE_TIMEOUT_LAUNCH_DIR/timeout"
printf '%s\n' '#!/bin/bash' 'exit 0' >"$FAKE_TIMEOUT_ACCEPT_DIR/timeout"
printf '%s\n' '#!/bin/bash' 'exit 125' >"$FAKE_TIMEOUT_INVALID_DIR/timeout"
printf '%s\n' \
  '#!/bin/bash' \
  'set -euo pipefail' \
  '[ "${PROBE_REQUIRED-}" = present ] || exit 125' \
  'signal=""' \
  'while (($#)); do' \
  '  case "$1" in' \
  '    --signal) signal="${2:-}"; shift 2 ;;' \
  '    --signal=*) signal="${1#--signal=}"; shift ;;' \
  '    -s) signal="${2:-}"; shift 2 ;;' \
  '    -s*) signal="${1#-s}"; shift ;;' \
  '    --preserve-status|--foreground|--verbose|-p|-f|-v) shift ;;' \
  '    --) shift; break ;;' \
  '    -*) exit 125 ;;' \
  '    *) shift; break ;;' \
  '  esac' \
  'done' \
  'case "$signal" in' \
  '  0|invalid-signal) exit 125 ;;' \
  'esac' \
  'exec "$@"' \
  >"$FAKE_TIMEOUT_REQUIRED_DIR/timeout"
chmod 755 -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$FAKE_TIMEOUT_ACCEPT_DIR/timeout" "$FAKE_TIMEOUT_INVALID_DIR/timeout" "$FAKE_TIMEOUT_REQUIRED_DIR/timeout"

run_hook() {
  local command="$1" role="${2:-coordinator}" path_mode="${3:-configured}" probe_required="${4:-absent}" callback_cwd="${5:-$REPO}" timeout_replays="${6:-[]}" compound_replay="${7:-false}" compound_handoff="${8:-true}" output="$TMP_ROOT/output.json" stderr=/dev/null
  local compound_cwd="${9:-$callback_cwd}"
  local compound_physical
  compound_physical="$(realpath -e -- "$compound_cwd" 2>/dev/null || true)"
  local compound_handoff_token=normal-git-test-handoff
  local fd9_mode="${10:-none}" fd9_token="${11:-$compound_handoff_token}"
  local subagent=false
  if [ "$role" = worker ]; then
    subagent=true
  fi
  local -a runner=(/bin/bash)
  if [ "${DEBUG_GIT_HOOK:-false}" = true ]; then
    runner=(/bin/bash -x)
    stderr="$TMP_ROOT/hook-xtrace.log"
  fi
  jq -cn \
    --arg session "$SESSION" \
    --arg cwd "$callback_cwd" \
    --arg compound_cwd "$compound_cwd" \
    --arg compound_physical "$compound_physical" \
    --arg handoff_token "$compound_handoff_token" \
    --arg command "$command" \
    --arg command_path "$CALLBACK_PATH" \
    --argjson timeout_replays "$timeout_replays" \
    --argjson compound_validation "$compound_replay" \
    '{session_id:$session,cwd:$cwd,timeout_replays:$timeout_replays,tool_input:{command:$command}} |
      if $compound_validation then
        .eci_compound_segment = {
          validation: true,
          cwd: $compound_cwd,
          cwd_known: true,
          cwd_unknown: false,
          cwd_candidates: [$compound_cwd],
          cwd_physical: $compound_physical,
          cwd_physical_candidates: [$compound_physical],
          reachability: "reachable",
          command_path: $command_path,
          command_path_set: true,
          command_path_exported: true,
          handoff_token: $handoff_token
        }
      else . end' |
    (
      export HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" KIMI_PROOF_ROOT="$KIMI_PROOF_ROOT"
      export ECI_ROLE="$role" ECI_HOOK_IS_SUBAGENT="$subagent"
      export XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state"
      if [ "$compound_replay" = true ] && [ "$compound_handoff" = true ]; then
        exec 9<<<"$fd9_token"
      elif [ "$fd9_mode" = sentinel ]; then
        exec 9<<<"sentinel"
      elif [ "$fd9_mode" = blocking ]; then
        exec 9<"${COMPOUND_FD9_FIFO:?missing blocking FD9 FIFO}"
      fi
      case "$probe_required" in
        present) export PROBE_REQUIRED=present ;;
        absent) unset PROBE_REQUIRED ;;
        wrong) export PROBE_REQUIRED=wrong ;;
        *) printf 'unknown probe-required mode: %s\n' "$probe_required" >&2; exit 64 ;;
      esac
      case "$path_mode" in
        configured)
          PATH="$CALLBACK_PATH" "${runner[@]}" -c "$BASH_LAUNCHER"
          ;;
        raw)
          PATH="$CALLBACK_PATH" "${runner[@]}" "$RUNTIME_ROOT/hooks/validate-bash.sh"
          ;;
        empty)
          PATH="" "${runner[@]}" "$RUNTIME_ROOT/hooks/validate-bash.sh"
          ;;
        unset)
          /bin/bash -c 'unset PATH; source "$1"' -- "$RUNTIME_ROOT/hooks/validate-bash.sh"
          ;;
        *) printf 'unknown callback PATH mode: %s\n' "$path_mode" >&2; exit 64 ;;
      esac
    ) >"$output" 2>>"$stderr"
  printf '%s\n' "$output"
}

run_compound_handoff_boundary_target() {
  local inner fifo writer hook_pid status output nonlive_session nonlive_dir nonlive_marker

  inner="$TMP_ROOT/compound-handoff-inner"
  fifo="$TMP_ROOT/compound-handoff-blocking.fifo"
  mkdir -p -- "$inner"
  printf '%s\n' ordinary >"$inner/eci_active"
  mkfifo -- "$fifo"
  ln -- "$PROOF_ROOT/$SESSION/eci_active" "$REPO/eci_active"

  foreign_marker="$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"
  foreign_goal_state="$FOREIGN_TIMEOUT_CONTROL_INNER/goal_state"
  foreign_wait="$FOREIGN_TIMEOUT_CONTROL_INNER/eci_wait"
  printf '%s\n' pending >"$foreign_goal_state"
  printf '%s\n' pending >"$foreign_wait"
  printf '%s\n' ordinary >"$TMP_ROOT/ordinary-copy"

  # Source-only operands are inputs, not writes; only destination/effect
  # positions may route a worker through the control boundary.
  assert_allowed "cp $foreign_marker $TMP_ROOT/ordinary-copy" worker
  assert_allowed "ln $foreign_marker $TMP_ROOT/ordinary-link" worker
  assert_allowed "dd if=$foreign_marker" worker
  assert_allowed "cat $foreign_goal_state" worker
  for target in "$foreign_marker" "$foreign_goal_state" "$foreign_wait"; do
    output="$(run_hook "rm $target" worker configured absent "$REPO")"
    jq -e --arg target "$target" '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains($target))' "$output" >/dev/null || {
      printf 'recognized foreign control write was not denied: %s\n' "$(cat -- "$output")" >&2
      return 1
    }
  done
  assert_allowed "cp $KIMI_FOREIGN_MARKER $TMP_ROOT/kimi-marker-copy" worker
  output="$(run_hook "rm $KIMI_FOREIGN_MARKER" worker configured absent "$REPO")"
  jq -e --arg target "$KIMI_FOREIGN_MARKER" '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED")) and (.hookSpecificOutput.permissionDecisionReason | contains($target))' "$output" >/dev/null || {
    printf 'configured Kimi foreign marker write was not dedicated-denied: %s\n' "$(cat -- "$output")" >&2
    return 1
  }
  KIMI_FOREIGN_HARDLINK="$TMP_ROOT/kimi-foreign-marker-hardlink"
  ln -- "$KIMI_FOREIGN_MARKER" "$KIMI_FOREIGN_HARDLINK"
  output="$(run_hook "rm $KIMI_FOREIGN_HARDLINK" worker configured absent "$REPO")"
  jq -e --arg target "$KIMI_FOREIGN_MARKER" '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED")) and (.hookSpecificOutput.permissionDecisionReason | contains($target))' "$output" >/dev/null || {
    printf 'configured Kimi foreign marker hardlink was not dedicated-denied: %s\n' "$(cat -- "$output")" >&2
    return 1
  }
  rm -- "$KIMI_FOREIGN_HARDLINK"
  KIMI_FOREIGN_GOAL_STATE="${KIMI_FOREIGN_MARKER%/*}/goal_state"
  KIMI_FOREIGN_WAIT="${KIMI_FOREIGN_MARKER%/*}/eci_wait"
  printf '%s\n' pending >"$KIMI_FOREIGN_GOAL_STATE"
  printf '%s\n' pending >"$KIMI_FOREIGN_WAIT"
  for target in "$KIMI_FOREIGN_GOAL_STATE" "$KIMI_FOREIGN_WAIT"; do
    assert_denied_code "rm $target" ECI_CONTROL_OWNER_REQUIRED worker
  done
  printf '%s\n' 'scope: malformed Kimi marker' >"$KIMI_FOREIGN_MARKER"
  assert_allowed "rm $KIMI_FOREIGN_MARKER" worker
  printf '%s\n' \
    "cwd: $FOREIGN_REPO" \
    "session_id: $KIMI_FOREIGN_SESSION" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$KIMI_FOREIGN_MARKER"
  assert_allowed "rm $KIMI_FOREIGN_MARKER" worker
  printf '%s\n' \
    'scope: foreign Kimi goal state' \
    "cwd: $FOREIGN_REPO" \
    "session_id: $KIMI_FOREIGN_SESSION" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"${KIMI_FOREIGN_MARKER%/*}/goal_state"
  assert_denied_code "rm ${KIMI_FOREIGN_MARKER%/*}/goal_state" ECI_CONTROL_OWNER_REQUIRED worker

  # A foreign marker pathname without a recognized live record is not itself
  # a live-control effect. Keep the exact proof/session spelling visible to
  # the adapter so the generic reserved-name route cannot blanket-deny these
  # advisory, malformed, mismatched, and stale observations.
  nonlive_session=foreign-nonlive-session
  nonlive_dir="$PROOF_ROOT/$nonlive_session"
  nonlive_marker="$nonlive_dir/eci_active"
  mkdir -p -- "$nonlive_dir"
  assert_allowed "rm $nonlive_marker" worker
  printf '%s\n' 'scope: malformed foreign marker' >"$nonlive_marker"
  assert_allowed "rm $nonlive_marker" worker
  printf '%s\n' \
    'scope: foreign marker mismatch' \
    "cwd: $FOREIGN_REPO" \
    'session_id: another-foreign-session' \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$nonlive_marker"
  assert_allowed "rm $nonlive_marker" worker
  printf '%s\n' \
    'scope: stale foreign marker' \
    "cwd: $TMP_ROOT/nonexistent-stale-cwd" \
    "session_id: $nonlive_session" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$nonlive_marker"
  assert_allowed "rm $nonlive_marker" worker
  assert_allowed "./timeout 5 rm $nonlive_marker" worker

  # An ordinary callback with an inherited sentinel must not consume FD9 or
  # activate recursive metadata merely because the descriptor exists.
  assert_allowed "printf ordinary" worker configured absent "$REPO" '[]' false none sentinel

  # A blocking inherited descriptor proves the adapter gates its read behind
  # structurally eligible handoff metadata. The callback must finish before
  # the writer closes the FIFO.
  (
    sleep 0.1
    exec 8>"$fifo"
    sleep 12
  ) &
  writer=$!
  COMPOUND_FD9_FIFO="$fifo" run_hook "printf ordinary" worker configured absent "$REPO" '[]' false true none blocking >"$TMP_ROOT/blocking-run.out" &
  hook_pid=$!
  status=0
  for _ in $(seq 1 80); do
    if ! kill -0 "$hook_pid" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  if kill -0 "$hook_pid" 2>/dev/null; then
    kill "$hook_pid" 2>/dev/null || true
    wait "$hook_pid" 2>/dev/null || status=$?
  else
    wait "$hook_pid" || status=$?
  fi
  kill "$writer" 2>/dev/null || true
  wait "$writer" 2>/dev/null || true
  [ "$status" -eq 0 ] || {
    printf 'ordinary callback blocked or failed while FD9 was inherited: status=%s\n' "$status" >&2
    return 1
  }

  # Shape-valid metadata from an ordinary callback still cannot activate the
  # recursive route when FD9 carries only an inherited sentinel.
  output="$(run_hook "rm eci_active" worker configured absent "$REPO" '[]' true false "$inner" sentinel)"
  jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_PLAN_LIVE_CONTROL_DENIED"))' "$output" >/dev/null || {
    printf 'forged compound metadata activated without the handoff token: %s\n' "$(cat -- "$output")" >&2
    return 1
  }

  local replay
  replay="$(jq -cn --arg cwd "$inner" '[{segment:1,parent_segment:2,prefix:["./timeout","5"],cwd:$cwd,command_path:"",command_path_set:false,command_path_exported:false,disposition:"observed"}]')"
  output="$(run_hook "./timeout 5 rm eci_active" worker configured absent "$REPO" "$replay" true true "$inner")"
  [ ! -s "$output" ] || {
    printf 'valid compound handoff was denied: %s\n' "$(cat -- "$output")" >&2
    return 1
  }

  # Capture the planner-selected timeout record at the actual recursive FD9
  # child boundary, then compare it with the equivalent explicit replay. The
  # compared fields are the effective CWD, replay disposition/prefix, command,
  # and decision; the parent callback record is deliberately filtered out.
  cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$inner/timeout"
  chmod 755 -- "$inner/timeout"
  local replay_trace actual_replay explicit_replay
  replay_trace="$TMP_ROOT/fd9-replay-trace"
  : >"$replay_trace"
  actual_replay="$(jq -cn --arg cwd "$inner" --arg command_path "$CALLBACK_PATH" '[{segment:1,parent_segment:2,prefix:["./timeout","5"],cwd:$cwd,command_path:$command_path,command_path_set:true,command_path_exported:true,disposition:"observed"}]')"
  ECI_TEST_REPLAY_TRACE_FILE="$replay_trace" assert_allowed "cd $inner; ./timeout 5 printf ordinary" worker configured absent "$REPO"
  export ECI_TEST_REPLAY_TRACE_FILE="$replay_trace"
  output="$(run_hook "./timeout 5 printf ordinary" worker configured absent "$REPO" "$actual_replay" true true "$inner")"
  unset ECI_TEST_REPLAY_TRACE_FILE
  [ ! -s "$output" ] || {
    printf 'explicit replay trace command was denied: %s\n' "$(cat -- "$output")" >&2
    return 1
  }
  explicit_replay="$(jq -cs '[.[] | select(.compound_segment_validation == true and (.command | startswith("./timeout 5")))]' "$replay_trace")"
  jq -e 'length == 2 and .[0].command == .[1].command and .[0].decision == .[1].decision and .[0].outer_cwd == .[1].outer_cwd and .[0].effective_cwd == .[1].effective_cwd and .[0].cwd_candidates == .[1].cwd_candidates and .[0].cwd_unknown == .[1].cwd_unknown and .[0].reachability == .[1].reachability and .[0].command_path == .[1].command_path and .[0].timeout_replays == .[1].timeout_replays and .[0].timeout_replays[0].cwd == .[1].timeout_replays[0].cwd and .[0].timeout_replays[0].prefix == .[1].timeout_replays[0].prefix and .[0].timeout_replays[0].command_path == .[1].timeout_replays[0].command_path and .[0].timeout_replays[0].command_path_set == true and .[0].timeout_replays[0].command_path_exported == true and .[1].timeout_replays[0].command_path_set == true and .[1].timeout_replays[0].command_path_exported == true and .[0].timeout_replays[0].disposition == .[1].timeout_replays[0].disposition' <<<"$explicit_replay" >/dev/null || {
    printf 'actual FD9 replay did not match explicit replay: %s\n' "$explicit_replay" >&2
    return 1
  }

  # A valid shape with a mismatched FD9 token is not an authenticated handoff;
  # the callback CWD remains authoritative and the hardlink reaches the
  # current marker's direct-control denial.
  output="$(run_hook "./timeout 5 rm eci_active" worker configured absent "$REPO" "$replay" true true "$inner" none wrong-token)"
  jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$output" >/dev/null || {
    printf 'mismatched compound handoff was accepted: %s\n' "$(cat -- "$output")" >&2
    return 1
  }
  unlink "$REPO/eci_active"
}

assert_allowed() {
  local command="$1" role="${2:-coordinator}" path_mode="${3:-configured}" probe_required="${4:-absent}" callback_cwd="${5:-$REPO}" timeout_replays="${6:-[]}" compound_replay="${7:-false}" fd9_mode="${8:-none}" fd9_token="${9:-normal-git-test-handoff}" output
  output="$(run_hook "$command" "$role" "$path_mode" "$probe_required" "$callback_cwd" "$timeout_replays" "$compound_replay" true "$callback_cwd" "$fd9_mode" "$fd9_token")"
  if [ -s "$output" ]; then
    printf 'ordinary Git command was denied: %q\n' "$command" >&2
    cat -- "$output" >&2
    return 1
  fi
}

assert_denied_code() {
  local command="$1" code="$2" role="${3:-coordinator}" detail="${4:-}" path_mode="${5:-configured}" probe_required="${6:-absent}" callback_cwd="${7:-$REPO}" timeout_replays="${8:-[]}" compound_replay="${9:-false}" compound_handoff="${10:-true}" output
  local compound_cwd="${11:-$callback_cwd}"
  output="$(run_hook "$command" "$role" "$path_mode" "$probe_required" "$callback_cwd" "$timeout_replays" "$compound_replay" "$compound_handoff" "$compound_cwd")"
  jq -e --arg code "[$code]" --arg detail "$detail" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    ($detail == "" or (.hookSpecificOutput.permissionDecisionReason | contains($detail)))
  ' "$output" >/dev/null || {
    printf 'expected concrete %s denial for: %q\n' "$code" "$command" >&2
    cat -- "$output" >&2
    [ "${DEBUG_GIT_HOOK:-false}" != true ] || tail -n 240 -- "$TMP_ROOT/hook-xtrace.log" >&2
    return 1
  }
}

foreign_timeout_observed_replays() {
  local timeout_token="$1" child_cwd="$2"

  jq -cn --arg timeout_token "$timeout_token" --arg cwd "$child_cwd" '
    [{segment:1,parent_segment:2,prefix:[$timeout_token,"5"],cwd:$cwd,command_path:"",command_path_set:false,command_path_exported:false,disposition:"observed"}]
  '
}

# Exercise each timeout-child meaning through both sources of evidence: a
# real launching child and the exact synthetic observed replay consumed during
# compound recursion.  Keep both coordinator and worker roles in the helper
# so a new semantic case cannot accidentally cover just one admission path.
assert_foreign_timeout_pair() {
  local expectation="$1" tail="$2" child_cwd="${3:-$FOREIGN_TIMEOUT_CONTROL_INNER}" foreign_session="${4:-$FOREIGN_SESSION}" timeout_token="${5:-./timeout}"
  local actual_command synthetic_command observed_replays role

  actual_command="cd $child_cwd; $timeout_token 5 $tail"
  synthetic_command="$timeout_token 5 $tail"
  observed_replays="$(foreign_timeout_observed_replays "$timeout_token" "$child_cwd")"
  for role in coordinator worker; do
    case "$expectation" in
      deny)
        assert_denied_code "$actual_command" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
          "foreign_session=$foreign_session"
        assert_denied_code "$synthetic_command" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
          "foreign_session=$foreign_session" configured absent "$REPO" "$observed_replays" true
        ;;
      allow)
        assert_allowed "$actual_command" "$role"
        assert_allowed "$synthetic_command" "$role" configured absent "$REPO" "$observed_replays" true
        ;;
      *)
        printf 'unknown foreign timeout expectation: %s\n' "$expectation" >&2
        return 64
        ;;
    esac
  done
}

# This bounded target exercises the copied real hook's timeout replay path
# without requiring the broad normal-Git matrix to finish. The default matrix
# below remains its established entrypoint and coverage surface.
run_foreign_timeout_marker_target() {
  local role current_timeout_inner outside_timeout_inner literal_timeout_dir literal_timeout_token missing_first_marker stale_foreign_cwd target_failures=0
  local observed_replays opaque_replays duplicate_replays malformed_replays prefix_mismatch_replays

  current_timeout_inner="$PROOF_ROOT/$SESSION"
  outside_timeout_inner="$TMP_ROOT/timeout-outside-proof"
  literal_timeout_dir="$TMP_ROOT/literal+timeout"
  literal_timeout_token="$literal_timeout_dir/timeout"
  missing_first_marker='../foreign-timeout-missing-first/eci_active'
  cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$FOREIGN_TIMEOUT_CONTROL_INNER/timeout"
  cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$current_timeout_inner/timeout"
  cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$PROOF_ROOT/timeout"
  mkdir -p -- "$literal_timeout_dir"
  cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$literal_timeout_token"
  mkdir -p -- "$outside_timeout_inner"
  cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$outside_timeout_inner/timeout"
  printf '%s\n' ordinary >"$outside_timeout_inner/eci_active"
  chmod 755 -- "$FOREIGN_TIMEOUT_CONTROL_INNER/timeout" "$current_timeout_inner/timeout" "$PROOF_ROOT/timeout" "$literal_timeout_token" "$outside_timeout_inner/timeout"
  CALLBACK_PATH="$FAKE_TIMEOUT_ACCEPT_DIR:$BASE_CALLBACK_PATH"

  # A valid observed replay, rather than a narrow raw-source spelling, owns
  # child mode.  The literal '+' path is deliberately outside the old source
  # regex while remaining an executable timeout pathname.
  for tail in \
    'rm eci_active' \
    'env rm eci_active' \
    '/usr/bin/env rm eci_active' \
    'env -- rm eci_active' \
    'env - rm eci_active' \
    'env - - rm eci_active' \
    'env -- - rm eci_active' \
    'env FOREIGN_ASSIGNMENT=present - rm eci_active' \
    'env -v rm eci_active' \
    'env --debug rm eci_active' \
    'env -v - rm eci_active' \
    'env --debug - rm eci_active' \
    'env -v --debug rm eci_active' \
    'env --debug -v rm eci_active' \
    'env -i rm eci_active' \
    'env --ignore-environment rm eci_active' \
    'env -u NAME rm eci_active' \
    'env -uNAME rm eci_active' \
    'env --unset NAME rm eci_active' \
    'env --unset=NAME rm eci_active' \
    'env -C . rm eci_active' \
    'env -C. rm eci_active' \
    'env --chdir . rm eci_active' \
    'env --chdir=. rm eci_active' \
    "env -C /tmp rm $FOREIGN_TIMEOUT_CONTROL_INNER/eci_active" \
    'env FOREIGN_ASSIGNMENT=present rm eci_active' \
    'rm -v eci_active' \
    'rm --verbose eci_active' \
    'rm -- eci_active' \
    'rm --force eci_active' \
    'rm -r eci_active' \
    'rm -R eci_active' \
    'rm -rf eci_active' \
    'rm -fr eci_active' \
    'rm --recursive eci_active' \
    'cp -r ordinary-copy eci_active' \
    'cp -R ordinary-copy eci_active' \
    'cp -rf ordinary-copy eci_active' \
    'cp -fr ordinary-copy eci_active' \
    'cp -a ordinary-copy eci_active' \
    'cp --archive ordinary-copy eci_active' \
    'cp --recursive ordinary-copy eci_active' \
    'cp -- ordinary-copy eci_active' \
    'cp --force ordinary-copy eci_active' \
    'cp -v ordinary-copy eci_active' \
    'cp --verbose ordinary-copy eci_active' \
    'tee -- eci_active' \
    'tee --append eci_active' \
    'tee -i eci_active' \
    'tee --ignore-interrupts eci_active' \
    'tee -ai eci_active' \
    'tee -ia eci_active' \
    'tee -p eci_active' \
    'tee --append --ignore-interrupts eci_active' \
    'dd of=eci_active' \
    'dd -- of=eci_active' \
    'dd of=eci_active bs=1 count=1' \
    'dd if=/dev/zero of=eci_active' \
    'unlink eci_active' \
    'unlink -- eci_active' \
    "env -C /tmp printf '%s' marker > eci_active" \
    "printf '%s' marker < eci_active > eci_active"; do
    assert_foreign_timeout_pair deny "$tail" "$FOREIGN_TIMEOUT_CONTROL_INNER" "$FOREIGN_SESSION" "$literal_timeout_token"
  done

  # These literal launch forms have an established foreign-marker writer
  # effect.  Keep the matrix finite: core input FDs and the exact installed env
  # debug/split combinations below, not a general shell or env grammar.
  for tail in \
    'cp ordinary-copy < eci_active eci_active' \
    'cp ordinary-copy 0< eci_active eci_active' \
    'cp ordinary-copy 1< eci_active eci_active' \
    'cp ordinary-copy 2< eci_active eci_active' \
    'cp ordinary-copy 2<&0 eci_active' \
    'cp ordinary-copy 2<& 0 eci_active' \
    "cp '3'< eci_active eci_active" \
    'cp 3 < eci_active eci_active' \
    'env -vv rm eci_active' \
    'env -vvv rm eci_active' \
    'env -vS rm eci_active' \
    'env -vSrm eci_active' \
    'env -vvSrm eci_active' \
    'env --debug -vSrm eci_active'; do
    assert_foreign_timeout_pair deny "$tail" "$FOREIGN_TIMEOUT_CONTROL_INNER" "$FOREIGN_SESSION" "$literal_timeout_token"
  done

  # The timeout child is argv, not a second shell.  Unknown/malformed env
  # grammar remains ordinary, while a recognized `-u NAME` retains rm as the
  # direct child.  These use the same literal timeout path and replay shape.
  for tail in \
    'builtin rm eci_active' \
    'command rm eci_active' \
    'command -v rm eci_active' \
    'exec rm eci_active' \
    'FOREIGN_ASSIGNMENT=present rm eci_active' \
    '"FOREIGN_ASSIGNMENT=present" rm eci_active' \
    'env -u rm eci_active' \
    'env -u' \
    'env --unset' \
    'env -C /tmp rm eci_active' \
    "env -C $TMP_ROOT/nonexistent-env-chdir rm eci_active" \
    'env -C . -C . rm eci_active' \
    'env --chdir . --chdir . rm eci_active' \
    'env --unknown rm eci_active' \
    'env FOREIGN_ASSIGNMENT=$TARGET rm eci_active' \
    'env FOREIGN_ASSIGNMENT=present' \
    './env rm eci_active' \
    'rm --unknown eci_active' \
    'cp --unknown ordinary-copy eci_active' \
    'tee --unknown eci_active' \
    'dd --unknown of=eci_active' \
    'unlink eci_active ordinary' \
    'srm eci_active' \
    'dd if=eci_active' \
    'cat < eci_active'; do
    assert_foreign_timeout_pair allow "$tail" "$FOREIGN_TIMEOUT_CONTROL_INNER" "$FOREIGN_SESSION" "$literal_timeout_token"
  done

  # Boundaries around the demonstrated spellings stay ordinary. In particular,
  # do not turn quoted/escaped/non-core FD words, mixed env clusters, or
  # equals split syntax into a broad parser-owned denial surface.
  for tail in \
    'cat 2< eci_active' \
    'cp ordinary-copy 3< eci_active eci_active' \
    'cp 3< eci_active eci_active' \
    'mv eci_active 3</dev/null' \
    'cp \3< eci_active eci_active' \
    "cp ordinary-copy '2'< eci_active eci_active" \
    'cp ordinary-copy \2< eci_active eci_active' \
    'cp ordinary-copy 2 < eci_active eci_active' \
    'cp ordinary-copy 2<&1 eci_active' \
    'cp ordinary-copy 2<& 1 eci_active' \
    'env -iv rm eci_active' \
    'env -vi rm eci_active' \
    'env - -v rm eci_active' \
    'env FOREIGN_ASSIGNMENT=present -v rm eci_active' \
    'env -S rm eci_active' \
    'env --split-string rm eci_active' \
    'env -Srm eci_active' \
    'env -vS "rm eci_active"' \
    'env --split-string=rm eci_active' \
    "env -S 'rm eci_active'" \
    'env -- -v eci_active' \
    'env -- --debug eci_active' \
    'env -- -u eci_active' \
    'env -- -C eci_active'; do
    assert_foreign_timeout_pair allow "$tail" "$FOREIGN_TIMEOUT_CONTROL_INNER" "$FOREIGN_SESSION" "$literal_timeout_token"
  done

  # Every direct finite writer position must agree between a real observed
  # timeout child and its synthetic replay.  Inputs, modes, and ordinary
  # command spellings are deliberately kept in the allow cases below.
  for tail in \
    'rm -f eci_active' \
    'rm eci_active' \
    'shred eci_active' \
    'touch eci_active' \
    'touch -d 2026-01-01T00:00:00Z eci_active' \
    'touch --date=2026-01-01T00:00:00Z eci_active' \
    "sed -i.bak 's/old/new/' eci_active" \
    "sed --in-place=.bak 's/old/new/' eci_active" \
    "sed -e's/old/new/' -i ordinary-copy eci_active" \
    'sed -f ordinary-copy -i eci_active' \
    'unlink eci_active' \
    'chmod 600 eci_active' \
    'chown root eci_active' \
    'tee eci_active' \
    'dd if=/dev/zero of=eci_active' \
    'truncate -s 0 eci_active' \
    'truncate --size=0 eci_active' \
    'cp ordinary-copy eci_active' \
    'install ordinary-copy eci_active' \
    'mv eci_active ordinary-copy' \
    'mv ordinary-copy eci_active' \
    'ln -f ordinary-copy eci_active' \
    'rm --force eci_active' \
    'cp -f ordinary-copy eci_active' \
    'cp --force ordinary-copy eci_active' \
    'tee -a eci_active' \
    'tee --append eci_active' \
    'ln --force ordinary-copy eci_active' \
    'rm -- eci_active' \
    'rm eci_active -- ordinary-copy' \
    'cp -- ordinary-copy eci_active' \
    'tee -- eci_active' \
    'ln -f -- ordinary-copy eci_active' \
    "printf '%s' marker > eci_active" \
    "printf '%s' marker >> eci_active" \
    "printf '%s' marker >| eci_active" \
    "printf '%s' marker >& eci_active" \
    "printf '%s' marker &> eci_active" \
    "printf '%s' marker &>> eci_active" \
    'rm "eci_active"' \
    'env rm eci_active'; do
    assert_foreign_timeout_pair deny "$tail"
  done

  # `--` makes the leading dash an operand instead of an option.  Session
  # identity validation is shared with proof state, so both valid spellings
  # must resolve without a local first-character rule.
  assert_foreign_timeout_pair deny "rm -- $FOREIGN_UNDERSCORE_SESSION/eci_active" "$PROOF_ROOT" "$FOREIGN_UNDERSCORE_SESSION"
  assert_foreign_timeout_pair deny "rm -- $FOREIGN_DASH_SESSION/eci_active" "$PROOF_ROOT" "$FOREIGN_DASH_SESSION"

  # Read/source operands, quoted or escaped operator punctuation, dynamic
  # operands, unsupported nesting, and unknown writer/wrapper grammar are
  # ordinary execution.  A quoted literal pathname remains a concrete target
  # above; only the syntax characters themselves are data here.
  for tail in \
    'cat eci_active' \
    "printf '%s' eci_active" \
    'cp eci_active ordinary-copy' \
    'cp -a eci_active ordinary-copy' \
    'sed -f eci_active -i ordinary-copy' \
    "sed -e's/old/new/' -i ordinary-copy" \
    'truncate -s eci_active ordinary-copy' \
    "sed -i.bak 's/old/new/' ordinary-copy" \
    'rmdir eci_active' \
    'chmod eci_active ordinary-copy' \
    'ln eci_active ordinary-link' \
    "printf '%s' marker '>' eci_active" \
    "printf '%s' marker \\> eci_active" \
    "printf '%s' marker '&>' eci_active" \
    "printf '%s' marker \\&\\> eci_active" \
    'rm "$TARGET"' \
    'rm $(printf eci_active)' \
    "bash -c 'rm eci_active'" \
    'ln -s ordinary-copy eci_active' \
    'unknown-wrapper rm eci_active'; do
    assert_foreign_timeout_pair allow "$tail"
  done

  # An actual observed timeout child may not mutate a valid foreign marker.
  for role in coordinator worker; do
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
  done

  # A marker pathname used as command input is not a foreign-marker mutation.
  # Keep both a pure reader and a writer with that marker only as its source.
  # A redirect to the marker itself remains the concrete effect to deny.
  for role in coordinator worker; do
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 cat eci_active" "$role"
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 printf '%s' eci_active" "$role"
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 cp eci_active ordinary-copy" "$role"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 printf '%s' marker > eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
  done

  # Command positions matter: input, mode, and impossible-directory operands
  # stay ordinary; direct destinations and forced replacement targets do not.
  for role in coordinator worker; do
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rmdir eci_active" "$role"
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 chmod eci_active ordinary-copy" "$role"
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 ln eci_active ordinary-link" "$role"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 dd if=/dev/zero of=eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 cp ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 install ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 mv eci_active ordinary-copy" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 mv ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 ln -f ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 chmod 600 eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION"
  done

  # An invalid first marker-shaped operand is advisory; the later valid
  # foreign marker is the concrete target that must still be found.
  for role in coordinator worker; do
    assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm $missing_first_marker eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" || target_failures=1
  done

  # A structurally valid record is not live after its declared CWD disappears.
  # Keep collecting both expected RED controls before returning their failure.
  stale_foreign_cwd="$TMP_ROOT/stale-foreign-timeout-cwd"
  mkdir -p -- "$stale_foreign_cwd"
  printf '%s\n' \
    'scope: foreign timeout marker regression' \
    "cwd: $stale_foreign_cwd" \
    "session_id: $FOREIGN_SESSION" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"
  rmdir -- "$stale_foreign_cwd"
  for role in coordinator worker; do
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" "$role" || target_failures=1
  done
  printf '%s\n' \
    'scope: foreign timeout marker regression' \
    "cwd: $FOREIGN_REPO" \
    "session_id: $FOREIGN_SESSION" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"
  [ "$target_failures" -eq 0 ] || return 1

  # The current marker remains covered by its existing direct-control route.
  assert_denied_code "cd $current_timeout_inner; ./timeout 5 rm eci_active" ECI_PLAN_LIVE_CONTROL_DENIED worker \
    "path=$(realpath -e -- "$current_timeout_inner/eci_active")"

  # A pathname observation alone is advisory. Missing, malformed, and
  # path/session-owner-mismatched candidates must not become foreign-marker
  # denials for either role.
  rm -- "$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"
  for role in coordinator worker; do
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" "$role"
  done
  printf '%s\n' 'scope: malformed timeout marker' >"$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"
  for role in coordinator worker; do
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" "$role"
  done
  printf '%s\n' \
    'scope: foreign timeout marker regression' \
    "cwd: $FOREIGN_REPO" \
    'session_id: mismatched-timeout-owner' \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"
  for role in coordinator worker; do
    assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" "$role"
  done

  # A same-named ordinary file outside the proof root is not an ECI marker.
  for role in coordinator worker; do
    assert_allowed "cd $outside_timeout_inner; ./timeout 5 rm eci_active" "$role"
  done

  # Restore the valid foreign marker to distinguish a valid observed replay
  # from absent, opaque, duplicate, malformed, and prefix-mismatched facts.
  printf '%s\n' \
    'scope: foreign timeout marker regression' \
    "cwd: $FOREIGN_REPO" \
    "session_id: $FOREIGN_SESSION" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$FOREIGN_TIMEOUT_CONTROL_INNER/eci_active"
  observed_replays="$(jq -cn --arg cwd "$FOREIGN_TIMEOUT_CONTROL_INNER" '
    [{segment:1,parent_segment:2,prefix:["./timeout","5"],cwd:$cwd,command_path:"",command_path_set:false,command_path_exported:false,disposition:"observed"}]
  ')"
  opaque_replays="$(jq -c '.[0].disposition = "opaque" | .' <<<"$observed_replays")"
  duplicate_replays="$(jq -c '[.[0], .[0]]' <<<"$observed_replays")"
  malformed_replays='[{"segment":1}]'
  prefix_mismatch_replays="$(jq -c '.[0].prefix = ["./timeout", "6"] | .' <<<"$observed_replays")"
  for role in coordinator worker; do
    assert_denied_code "./timeout 5 rm eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    # The observed replay carries the child CWD, but input paths must still
    # remain ordinary while a redirect to the live marker is denied.
    assert_allowed "./timeout 5 cat eci_active" "$role" configured absent "$REPO" "$observed_replays" true
    assert_allowed "./timeout 5 printf '%s' eci_active" "$role" configured absent "$REPO" "$observed_replays" true
    assert_allowed "./timeout 5 cp eci_active ordinary-copy" "$role" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 printf '%s' marker > eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_allowed "./timeout 5 rmdir eci_active" "$role" configured absent "$REPO" "$observed_replays" true
    assert_allowed "./timeout 5 chmod eci_active ordinary-copy" "$role" configured absent "$REPO" "$observed_replays" true
    assert_allowed "./timeout 5 ln eci_active ordinary-link" "$role" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 dd if=/dev/zero of=eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 cp ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 install ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 mv eci_active ordinary-copy" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 mv ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 ln -f ordinary-copy eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_denied_code "./timeout 5 chmod 600 eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    # A synthetic observed replay must also scan beyond an invalid first
    # marker-shaped operand to the later live foreign marker.
    assert_denied_code "./timeout 5 rm $missing_first_marker eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
      "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$observed_replays" true
    assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" '[]' true
    assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" "$opaque_replays" true
    assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" "$duplicate_replays" true
    assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" "$malformed_replays" true
    assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" "$prefix_mismatch_replays" true
  done
}

checkout_invalid_mode_pair() {
  local options="$1" input="${2:-file}" path command protected_before ordinary_before index_before raw_before index_path native_status row_preserved row_ok
  for path in hooks/validate-bash.sh AGENTS.md; do
    printf '%s\n' "$path" >"$REPO/--"
    case "$input" in
      explicit) command="git checkout $options -- $path" ;;
      explicit-file) command="git checkout --pathspec-from-file=$REPO/-- $options -- $path" ;;
      ordinary-file) command="git checkout --pathspec-from-file=$REPO/-- $options" ;;
      file) command="git checkout --pathspec-from-file -- $options" ;;
    esac
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    index_path="$(git -C "$REPO" rev-parse --path-format=absolute --git-path index)"
    raw_before="$(sha256sum "$index_path")"
    row_ok=1
    assert_allowed "$command" worker || { failures=1; row_ok=0; }
    native_status=0
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/mode-invalid.out" 2>&1 || native_status=$?
    if [ "$native_status" -eq 0 ]; then
      printf 'native-invalid checkout mode unexpectedly succeeded: %s\n' "$command" >&2
      failures=1
      row_ok=0
    fi
    row_preserved=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || row_preserved=0
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$ordinary_before" ] || row_preserved=0
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || row_preserved=0
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || row_preserved=0
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || row_preserved=0
    [ "$(sha256sum "$index_path")" = "$raw_before" ] || row_preserved=0
    if [ "$row_preserved" -eq 1 ] && [ "$row_ok" -eq 1 ]; then
      printf 'invalid-mode native_exit=%s destination=%s full-state-preserved options=%s\n' "$native_status" "$path" "$options"
    else
      printf 'invalid-mode admission/native/state check failed: %s\n' "$command" >&2
      failures=1
    fi
  done
}

checkout_ambiguous_source_pair() {
  local path command protected_before ordinary_before index_before native_status row_preserved row_ok index_path raw_before
  index_path="$(git -C "$REPO" rev-parse --path-format=absolute --git-path index)"
  for path in hooks/validate-bash.sh AGENTS.md; do
    command="git checkout hooks/validate-bash.sh $path"
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    raw_before="$(sha256sum "$index_path")"
    row_ok=1
    assert_allowed "$command" worker || { failures=1; row_ok=0; }
    native_status=0
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/checkout-ambiguous.out" 2>&1 || native_status=$?
    if [ "$native_status" -eq 0 ]; then
      printf 'ambiguous checkout unexpectedly succeeded: %s\n' "$command" >&2
      failures=1
      row_ok=0
    fi
    rg -q 'both revision and filename' "$TMP_ROOT/checkout-ambiguous.out" || { failures=1; row_ok=0; }
    row_preserved=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || row_preserved=0
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$ordinary_before" ] || row_preserved=0
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || row_preserved=0
    [ "$(sha256sum "$index_path")" = "$raw_before" ] || row_preserved=0
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || row_preserved=0
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || row_preserved=0
    if [ "$row_preserved" -eq 1 ] && [ "$row_ok" -eq 1 ]; then
      printf 'ambiguous-source native_exit=%s destination=%s index/head/branch/protected-preserved\n' "$native_status" "$path"
    else
      printf 'ambiguous-source state preservation failed: %s\n' "$command" >&2
      failures=1
    fi
  done
}

checkout_invalid_effect_pair() {
  local path command protected_before ordinary_before index_before index_path raw_before native_status row_preserved row_ok
  index_path="$(git -C "$REPO" rev-parse --path-format=absolute --git-path index)"
  for path in hooks/validate-bash.sh AGENTS.md; do
    command="git checkout -z --detach $path"
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    raw_before="$(sha256sum "$index_path")"
    row_ok=1
    assert_allowed "$command" worker || { failures=1; row_ok=0; }
    native_status=0
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/checkout-invalid-effect.out" 2>&1 || native_status=$?
    if [ "$native_status" -eq 0 ]; then
      printf 'invalid checkout option unexpectedly succeeded: %s\n' "$command" >&2
      failures=1
      row_ok=0
    fi
    row_preserved=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || row_preserved=0
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$ordinary_before" ] || row_preserved=0
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || row_preserved=0
    [ "$(sha256sum "$index_path")" = "$raw_before" ] || row_preserved=0
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || row_preserved=0
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || row_preserved=0
    if [ "$row_preserved" -eq 1 ] && [ "$row_ok" -eq 1 ]; then
      printf 'invalid-effect native_exit=%s destination=%s full-state-preserved\n' "$native_status" "$path"
    else
      printf 'invalid-effect state preservation failed: %s\n' "$command" >&2
      failures=1
    fi
  done
}

checkout_unknown_syntax_pair() {
  local path command protected_before ordinary_before index_before native_status row_preserved row_ok
  for path in hooks/validate-bash.sh AGENTS.md; do
    command="git checkout --unknown-checkout-option -- $path"
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    row_ok=1
    assert_allowed "$command" worker || { failures=1; row_ok=0; }
    native_status=0
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/checkout-unknown.out" 2>&1 || native_status=$?
    if [ "$native_status" -eq 0 ]; then
      printf 'unknown checkout option unexpectedly succeeded: %s\n' "$command" >&2
      failures=1
      row_ok=0
    fi
    row_preserved=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || row_preserved=0
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$ordinary_before" ] || row_preserved=0
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || row_preserved=0
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || row_preserved=0
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || row_preserved=0
    if [ "$row_preserved" -eq 1 ] && [ "$row_ok" -eq 1 ]; then
      printf 'unknown-syntax native_exit=%s destination=%s state-preserved\n' "$native_status" "$path"
    else
      printf 'unknown-syntax state preservation failed: %s\n' "$command" >&2
      failures=1
    fi
  done
}

restore_no_staged_pair() {
  local path command protected_before ordinary_before index_before native_status index_path raw_before row_preserved row_ok
  index_path="$(git -C "$REPO" rev-parse --path-format=absolute --git-path index)"
  for path in hooks/validate-bash.sh AGENTS.md; do
    if [ "$path" = hooks/validate-bash.sh ]; then
      printf '\n# Fixture no-staged probe\n' >>"$REPO/$path"
    else
      printf 'ordinary no-staged probe\n' >"$REPO/$path"
    fi
    command="git restore --no-staged -- $path"
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    raw_before="$(sha256sum "$index_path")"
    row_ok=1
    assert_allowed "$command" worker || { failures=1; row_ok=0; }
    native_status=0
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/restore-no-staged.out" 2>&1 || native_status=$?
    if [ "$native_status" -eq 0 ]; then
      printf 'restore without a selected destination unexpectedly succeeded: %s\n' "$command" >&2
      failures=1
      row_ok=0
    fi
    rg -Fq "neither '--staged' or '--worktree' is specified" "$TMP_ROOT/restore-no-staged.out" || { failures=1; row_ok=0; }
    row_preserved=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || row_preserved=0
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$ordinary_before" ] || row_preserved=0
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || row_preserved=0
    [ "$(sha256sum "$index_path")" = "$raw_before" ] || row_preserved=0
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || row_preserved=0
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || row_preserved=0
    if [ "$row_preserved" -eq 1 ] && [ "$row_ok" -eq 1 ]; then
      printf 'restore-no-staged native_exit=%s destination=%s index/head/branch/protected-preserved\n' "$native_status" "$path"
    else
      printf 'restore-no-staged state preservation failed: %s\n' "$command" >&2
      failures=1
    fi
    git -C "$REPO" restore --source=HEAD --worktree -- "$path"
  done
}

checkout_orphan_validity_pairs() {
  local path command protected_before ordinary_before index_before index_path raw_before native_status row_preserved row_ok expected_ordinary
  index_path="$(git -C "$REPO" rev-parse --path-format=absolute --git-path index)"
  for path in hooks/validate-bash.sh AGENTS.md; do
    command="git checkout --orphan=bad..name -- $path"
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    raw_before="$(sha256sum "$index_path")"
    row_ok=1
    assert_allowed "$command" worker || { failures=1; row_ok=0; }
    native_status=0
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/checkout-orphan-invalid.out" 2>&1 || native_status=$?
    if [ "$native_status" -eq 0 ]; then
      printf 'invalid orphan name unexpectedly succeeded: %s\n' "$command" >&2
      failures=1
      row_ok=0
    fi
    row_preserved=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || row_preserved=0
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$ordinary_before" ] || row_preserved=0
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || row_preserved=0
    [ "$(sha256sum "$index_path")" = "$raw_before" ] || row_preserved=0
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || row_preserved=0
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || row_preserved=0
    if [ "$row_preserved" -eq 1 ] && [ "$row_ok" -eq 1 ]; then
      printf 'invalid-orphan native_exit=%s destination=%s full-state-preserved\n' "$native_status" "$path"
    else
      printf 'invalid-orphan state preservation failed: %s\n' "$command" >&2
      failures=1
    fi
    git -C "$REPO" restore --source=HEAD --worktree -- "$path"
  done

  command='git checkout --orphan=bad..name --no-orphan -- hooks/validate-bash.sh'
  protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
    "effect=overwrite target=$REPO/hooks/validate-bash.sh" || failures=1
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || failures=1
  command='git checkout --orphan=bad..name --no-orphan -- AGENTS.md'
  printf 'ordinary changed\n' >"$REPO/AGENTS.md"
  expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
  row_ok=1
  if assert_allowed "$command" worker; then
    (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
    if [ "$row_ok" -eq 1 ]; then printf 'cancelled-invalid-orphan restored ordinary path\n'; fi
  else failures=1; fi
}

checkout_branch_path_pair() {
  local failures=0 path command protected_before expected_ordinary row_ok index_before
  for path in hooks/validate-bash.sh AGENTS.md; do
    command="git checkout existing-other-branch -- $path"
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
    if [ "$path" = hooks/validate-bash.sh ]; then
      printf '\n# Fixture branch-path probe\n' >>"$REPO/$path"
      protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
      assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
        "effect=overwrite target=$REPO/hooks/validate-bash.sh" || failures=1
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || failures=1
    else
      printf 'ordinary changed\n' >"$REPO/AGENTS.md"
      row_ok=1
      if assert_allowed "$command" worker; then
        (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
        if [ "$row_ok" -eq 1 ]; then printf 'branch-path restored ordinary destination\n'; fi
      else failures=1; fi
    fi
    git -C "$REPO" restore --source=HEAD --worktree -- "$path"
  done
  assert_denied_code 'git checkout existing-other-branch --' ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
    'operation=repository' || failures=1

  # An explicit separator makes the same name a source ref followed by a
  # destination path, even when that source name is also a protected file.
  for path in hooks/validate-bash.sh AGENTS.md; do
    command="git checkout hooks/validate-bash.sh -- $path"
    expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
    if [ "$path" = hooks/validate-bash.sh ]; then
      printf '\n# Fixture explicit-source probe\n' >>"$REPO/$path"
      protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
      assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
        "effect=overwrite target=$REPO/hooks/validate-bash.sh" || failures=1
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || failures=1
    else
      printf 'ordinary changed\n' >"$REPO/AGENTS.md"
      protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
      index_before="$(git -C "$REPO" ls-files --stage)"
      row_ok=1
      if assert_allowed "$command" worker; then
        (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
        if [ "$row_ok" -eq 1 ]; then printf 'explicit-source branch restored ordinary destination\n'; fi
      else
        failures=1
      fi
    fi
    git -C "$REPO" restore --source=HEAD --worktree -- "$path"
  done
  [ "$failures" -eq 0 ]
}

restore_explicit_worktree_pair() {
  local options path command protected_before ordinary_before index_before expected_ordinary row_ok
  for options in '--worktree --no-staged' '--no-staged --worktree'; do
    for path in hooks/validate-bash.sh AGENTS.md; do
      if [ "$path" = hooks/validate-bash.sh ]; then
        printf '\n# Fixture explicit worktree probe\n' >>"$REPO/$path"
      else
        printf 'ordinary explicit worktree probe\n' >"$REPO/$path"
      fi
      command="git restore $options -- $path"
      protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
      ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
      index_before="$(git -C "$REPO" ls-files --stage)"
      expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
      if [ "$path" = hooks/validate-bash.sh ]; then
        row_ok=1
        assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
          "effect=overwrite target=$REPO/hooks/validate-bash.sh" || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
      else
        row_ok=1
        if assert_allowed "$command" worker; then
          (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
          [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
          [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
          [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
          [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
          [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
          if [ "$row_ok" -eq 1 ]; then
            printf 'restore-explicit-worktree restored ordinary destination options=%s\n' "$options"
          fi
        else
          failures=1
          row_ok=0
        fi
      fi
      git -C "$REPO" restore --source=HEAD --worktree -- "$path"
    done
  done
  # Short -S remains a staged-only restore and must not select worktree output.
  printf 'staged content\n' >"$REPO/AGENTS.md"
  git -C "$REPO" add -- AGENTS.md
  printf 'unstaged content\n' >"$REPO/AGENTS.md"
  protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  ordinary_before="$(git -C "$REPO" hash-object AGENTS.md)"
  command='git restore -S -- AGENTS.md'
  row_ok=1
  if assert_allowed "$command" worker; then
    (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$ordinary_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" rev-parse :AGENTS.md)" = "$(git -C "$REPO" rev-parse HEAD:AGENTS.md)" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
    if [ "$row_ok" -eq 1 ]; then printf 'restore-short-staged preserved worktree and restored only index\n'; fi
  else failures=1; row_ok=0; fi
  git -C "$REPO" restore --source=HEAD --staged --worktree -- AGENTS.md
}

checkout_matcher_fault_controls() {
  python3 - "$RUNTIME_ROOT/hooks/validate-bash.sh" "$REPO" <<'PY_NATIVE_MATCHER'
import os
from pathlib import Path
import re
import subprocess
import sys

hook, repository = sys.argv[1:]
source = Path(hook).read_text()
library = re.search(r"git_effect_python\(\).*?<<'PY'\n(.*?)\nPY", source, re.S).group(1)
block = source[source.index("git_protected_worktree_target_detail()") :]
block = re.search(r"<<'PY'\n(.*?)\nPY", block, re.S).group(1)
block = block[:block.index("if analysis is not None:")]
sys.argv = ["matcher-proof", "git checkout -- hooks/validate-bash.sh", repository,
            repository, repository, repository + "/hooks", "null", library]
namespace = {}
exec(compile(block, "actual-checkout-helper", "exec"), namespace)
if "checkout_native_names" not in namespace:
    raise AssertionError("native matcher fault controls unavailable")
context = namespace["CheckoutGitContext"](repository, (), dict(os.environ))
arguments = ["ls-files", "--cached", "--full-name", "-z", "--", "hooks/validate-bash.sh"]
index = subprocess.check_output(["git", "-C", repository, "rev-parse", "--path-format=absolute", "--git-path", "index"], text=True).strip()
raw_before = Path(index).read_bytes()
for setting, value, expected in (
    ("CHECKOUT_MATCH_TIMEOUT", 0.0, "matcher-timeout"),
    ("CHECKOUT_MATCH_BYTES", 0, "matcher-output-cap"),
    ("CHECKOUT_MATCH_NAMES", 0, "matcher-output-cap"),
):
    previous = namespace[setting]
    namespace[setting] = value
    try:
        result = namespace["checkout_native_names"](arguments, context)
        assert result.status == "unresolved" and result.reason == expected, (setting, result.status, result.reason)
        detail = namespace["checkout_detail"](["--", "hooks/validate-bash.sh"], repository, frozenset(), context=context)
        assert "effect=overwrite-unresolved" in detail and "kind=unresolved-checkout-selection" in detail
    finally:
        namespace[setting] = previous
    print("native matcher fault retained unresolved selection:", setting)
missing = namespace["CheckoutGitContext"](repository, None, None)
assert "effect=overwrite-unresolved" in namespace["checkout_detail"](["--", "AGENTS.md"], repository, frozenset(), context=missing)
for segment, unknown in (
    (["env", "GIT_WORK_TREE=$missing", "git", "checkout", "--", "AGENTS.md"], frozenset({1})),
    (["env", "GIT_DIR=$missing", "git", "checkout", "--", "AGENTS.md"], frozenset({1})),
    (["git", "-C", "$missing", "checkout", "--", "AGENTS.md"], frozenset({2})),
    (["sudo", "env", "-i", "git", "checkout", "--", "AGENTS.md"], frozenset()),
    (["sudo", "env", "-i", "sh", "-c", "git checkout -- AGENTS.md"], frozenset()),
    (["env", "-C", "$missing", "sh", "-c", "git checkout -- AGENTS.md"], frozenset({2})),
):
    detail = namespace["inspect_segment"](segment, unknown=unknown)
    assert detail and "effect=overwrite-unresolved" in detail
assert "effect=overwrite-unresolved" in namespace["checkout_detail"](["--pathspec-from-file", "$missing"], repository, frozenset({1}), context=context)
print("native matcher unavailable cwd/worktree/git-dir/wrapper/file stayed unresolved")
previous_revision = namespace["checkout_revision"]
namespace["checkout_revision"] = lambda *args, **kwargs: None
try:
    assert namespace["git_checkout_effect"](["-"], base=repository) == "repository-unresolved"
    completed = namespace["completed_checkout"](["HEAD", "--", "AGENTS.md"], frozenset(), frozenset(), context)
    assert completed.options.source_lookup_unknown
    assert namespace["checkout_detail"](["HEAD", "--", "AGENTS.md"], repository, frozenset(), context=context) is None
    assert "reason=source-lookup-protected-target" in namespace["checkout_detail"](["HEAD", "--", "hooks/validate-bash.sh"], repository, frozenset(), context=context)
finally:
    namespace["checkout_revision"] = previous_revision
assert Path(index).read_bytes() == raw_before, "read-only matcher changed the index"
print("native matcher fault context stayed unresolved; raw index preserved")
PY_NATIVE_MATCHER
}

run_checkout_native_selection_target() {
  local failures=0 command template path row_ok status protected_before index_before expected_ordinary source_tree
  # No previous checkout exists. Removing the index entry makes the bare
  # dash fail natively; restoring it proves Git's literal-path fallback.
  git -C "$REPO" rm -q -- -
  index_before="$(git -C "$REPO" ls-files --stage)"
  protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  row_ok=1
  assert_allowed 'git checkout -' worker || { failures=1; row_ok=0; }
  status=0
  (cd -- "$REPO"; git checkout -) >"$TMP_ROOT/dash-no-history.out" 2>&1 || status=$?
  [ "$status" -ne 0 ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
  [ "$row_ok" -ne 1 ] || printf 'dash no-history/no-file native_exit=%s semantic state preserved\n' "$status"
  git -C "$REPO" restore --source=HEAD --staged --worktree -- -
  index_before="$(git -C "$REPO" ls-files --stage)"
  expected_ordinary="$(git -C "$REPO" rev-parse HEAD:-)"
  printf 'dirty literal dash\n' >"$REPO/-"
  row_ok=1
  assert_allowed 'git checkout -' worker || { failures=1; row_ok=0; }
  (cd -- "$REPO"; git checkout -) >"$TMP_ROOT/dash-literal.out" 2>&1 || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" hash-object -- -)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
  [ "$row_ok" -ne 1 ] || printf 'dash literal fallback restored; semantic index/head/branch/protected preserved (stat refresh allowed)\n'

  printf 'hooks/validate-bash.sh checkout-test\nAGENTS.md checkout-test\n' >"$REPO/.gitattributes"
  printf 'ordinary literal base\n' >"$REPO/hooks/[v]alidate-bash.sh"
  git -C "$REPO" add -- .gitattributes 'hooks/[v]alidate-bash.sh'
  git -C "$REPO" commit -qm 'native selection attributes'
  before_head="$(git -C "$REPO" rev-parse HEAD)"
  for template in \
    "git checkout -- ':(icase)__UPPER__'" \
    "git --icase-pathspecs checkout -- '__UPPER__'" \
    "env GIT_ICASE_PATHSPECS=1 git checkout -- '__UPPER__'" \
    "git checkout -- ':(glob)__PATH__'" \
    "git checkout -- ':(literal)__PATH__'" \
    "git checkout -- ':(attr:checkout-test)__PATH__'" \
    "git --literal-pathspecs checkout -- '__PATH__'" \
    "git --glob-pathspecs checkout -- '__PATH__'" \
    "git --noglob-pathspecs checkout -- '__PATH__'" \
    "git -c core.ignoreCase=true checkout -- '__PATH__'" \
    "git --attr-source HEAD checkout -- ':(attr:checkout-test)__PATH__'" \
    "git --attr-source=HEAD checkout -- ':(attr:checkout-test)__PATH__'" \
    "git -C hooks checkout -- ':(top)__PATH__'" \
    "git checkout -- ':(prefix:0)__PATH__'" \
    "git checkout -- '__PATH__' ':(exclude)file.txt'" \
    "git checkout HEAD -- ':(glob)__PATH__'" \
    "git checkout HEAD -- ':(attr:checkout-test)__PATH__'" \
    "git checkout HEAD --no-overlay -- ':(icase)__PATH__'" \
    "git checkout --pathspec-from-file=$TMP_ROOT/native-selection.paths" \
    "git checkout HEAD --pathspec-from-file=$TMP_ROOT/native-selection.paths --pathspec-file-nul"; do
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
    row_ok=1
    for path in hooks/validate-bash.sh AGENTS.md; do
      command="${template//__PATH__/$path}"
      command="${command//__UPPER__/${path^^}}"
      case "$template" in
        *--pathspec-file-nul) printf '%s\0' ":(icase)${path^^}" >"$TMP_ROOT/native-selection.paths" ;;
        *) printf '%s\n' ":(icase)${path^^}" >"$TMP_ROOT/native-selection.paths" ;;
      esac
      if [ "$path" = hooks/validate-bash.sh ]; then
        assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
          "effect=overwrite target=$REPO/hooks/validate-bash.sh" || { failures=1; row_ok=0; }
      else
        printf 'ordinary dirty\n' >"$REPO/AGENTS.md"
        assert_allowed "$command" worker || { failures=1; row_ok=0; }
        (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/native-selection.out" 2>&1 || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
      fi
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
    done
    [ "$row_ok" -ne 1 ] || printf 'native selection protected denied/ordinary restored/state preserved: %s\n' "$template"
  done
  for template in \
    "git checkout -- ':(literal)hooks/[v]alidate-bash.sh'" \
    "git --literal-pathspecs checkout -- 'hooks/[v]alidate-bash.sh'" \
    "git --noglob-pathspecs checkout -- 'hooks/[v]alidate-bash.sh'"; do
    row_ok=1
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    printf 'literal ordinary dirty\n' >"$REPO/hooks/[v]alidate-bash.sh"
    assert_allowed "$template" worker || { failures=1; row_ok=0; }
    (cd -- "$REPO"; bash -c "$template") >"$TMP_ROOT/native-literal.out" 2>&1 || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object 'hooks/[v]alidate-bash.sh')" = "$(git -C "$REPO" rev-parse 'HEAD:hooks/[v]alidate-bash.sh')" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
    [ "$row_ok" -ne 1 ] || printf 'literal ordinary metacharacter path restored/protected state preserved: %s\n' "$template"
  done
  assert_denied_code "git checkout -- '*.sh'" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  printf 'ordinary wildcard dirty\n' >"$REPO/AGENTS.md"
  command="git checkout -- '*ODEX.md'"
  assert_allowed "$command" worker || failures=1
  (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/native-wildcard.out" 2>&1 || failures=1
  [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$(git -C "$REPO" rev-parse HEAD:AGENTS.md)" ] || failures=1
  for template in line nul; do
    if [ "$template" = nul ]; then
      printf ':!AGENTS.md\0' >"$TMP_ROOT/native-selection.paths"
      command="git checkout --pathspec-from-file=$TMP_ROOT/native-selection.paths --pathspec-file-nul"
    else
      printf ':!AGENTS.md\n' >"$TMP_ROOT/native-selection.paths"
      command="git checkout --pathspec-from-file=$TMP_ROOT/native-selection.paths"
    fi
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
    if [ "$template" = nul ]; then printf 'AGENTS.md\0:!hooks/validate-bash.sh\0' >"$TMP_ROOT/native-selection.paths"
    else printf 'AGENTS.md\n:!hooks/validate-bash.sh\n' >"$TMP_ROOT/native-selection.paths"; fi
    printf 'ordinary file-set dirty\n' >"$REPO/AGENTS.md"
    assert_allowed "$command" worker || failures=1
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/native-file-set.out" 2>&1 || failures=1
    [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$(git -C "$REPO" rev-parse HEAD:AGENTS.md)" ] || failures=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || failures=1
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || failures=1
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
  done
  assert_denied_code "git checkout -- '*validate-bash.sh'" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
    "effect=overwrite target=$REPO/hooks/validate-bash.sh" || failures=1
  assert_denied_code "git checkout -- ':(exclude)AGENTS.md'" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  assert_allowed "git checkout -- ':(exclude)hooks' ':(exclude)hooks.json'" worker || failures=1
  for command in "git checkout -- ':!AGENTS.md'" "git checkout -- ':^AGENTS.md'"; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  done
  assert_allowed "git checkout -- 'hooks/validate-bash.sh' ':(exclude)hooks/validate-bash.sh'" worker || failures=1
  assert_allowed "git checkout -- ':(attr:unset-test)hooks/validate-bash.sh'" worker || failures=1
  assert_allowed "git checkout -- 'HOOKS/VALIDATE-BASH.SH'" worker || failures=1
  row_ok=1
  index_before="$(git -C "$REPO" ls-files --stage)"
  protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  command="git checkout -- ':(glob,literal)hooks/validate-bash.sh'"
  assert_allowed "$command" worker || { failures=1; row_ok=0; }
  status=0
  (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/native-invalid-pathspec.out" 2>&1 || status=$?
  [ "$status" -ne 0 ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
  [ "$row_ok" -ne 1 ] || printf 'native-invalid pathspec admitted/native failure/semantic state preserved\n'
  printf '"hooks/validate-bash.sh"\n' >"$TMP_ROOT/native-selection.paths"
  for command in \
    "git checkout --pathspec-from-file=$TMP_ROOT/native-selection.paths" \
    'git checkout --pathspec-from-file=-' \
    'sudo git checkout -- AGENTS.md'; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
      'effect=overwrite-unresolved' || failures=1
  done
  source_tree="$(git -C "$REPO" ls-tree HEAD | awk '$4 != "hooks"' | git -C "$REPO" mktree)"
  assert_denied_code "git checkout $source_tree --no-overlay -- ':(icase)HOOKS/VALIDATE-BASH.SH'" \
    ECI_WORKER_GIT_OWNERSHIP_DENIED worker "effect=overwrite target=$REPO/hooks/validate-bash.sh" || failures=1
  row_ok=1
  assert_denied_code "git checkout --patch $source_tree -- hooks/validate-bash.sh" \
    ECI_WORKER_GIT_OWNERSHIP_DENIED worker "effect=overwrite target=$REPO/hooks/validate-bash.sh" || { failures=1; row_ok=0; }
  # The index already contains the source blob; Git asks separately to apply
  # the selected hunk to the dirty worktree when it cannot apply to the index.
  printf 'ordinary patch dirty\n' >"$REPO/AGENTS.md"
  command="git checkout --patch $source_tree -- AGENTS.md"
  assert_allowed "$command" worker || { failures=1; row_ok=0; }
  (cd -- "$REPO"; printf 'y\ny\n' | bash -c "$command") >"$TMP_ROOT/native-patch-overlay.out" 2>&1 || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$(git -C "$REPO" rev-parse HEAD:AGENTS.md)" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
  [ "$row_ok" -ne 1 ] || printf 'patch default no-overlay protected deletion denied/ordinary restored/state preserved\n'
  assert_allowed "git checkout $source_tree -- ':(glob)hooks/validate-bash.sh'" worker || failures=1
  status=0
  (cd -- "$REPO"; git checkout "$source_tree" -- ':(glob)hooks/validate-bash.sh') >"$TMP_ROOT/native-tree-absent.out" 2>&1 || status=$?
  [ "$status" -ne 0 ] || failures=1
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || failures=1
  printf 'ordinary dirty\n' >"$REPO/AGENTS.md"
  command="git checkout $source_tree --no-overlay -- ':(glob)AGENTS.md'"
  assert_allowed "$command" worker || failures=1
  (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/native-no-overlay.out" 2>&1 || failures=1
  [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$(git -C "$REPO" rev-parse HEAD:AGENTS.md)" ] || failures=1
  [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || failures=1
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || failures=1
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
  git -C "$REPO" checkout -q existing-other-branch
  git -C "$REPO" checkout -q "${before_branch#refs/heads/}"
  assert_denied_code 'git checkout -' ECI_WORKER_GIT_OWNERSHIP_DENIED worker 'operation=repository' || failures=1
  assert_denied_code 'git --attr-source HEAD checkout -' ECI_WORKER_GIT_OWNERSHIP_DENIED worker 'operation=repository' || failures=1
  checkout_matcher_fault_controls || failures=1
  [ "$failures" -eq 0 ]
}

run_checkout_completed_selection_target() {
  local failures=0 options template path command row_ok protected_before protected_clean index_before expected_ordinary native_status
  for options in \
    '--patch -U-2' '--patch --unified=-2' '--patch --inter-hunk-context=-2' \
    '--patch -U-1 -U-2' '--patch --inter-hunk-context=-1 --inter-hunk-context=-2' \
    '--patch -U-2 --no-patch' '--patch -U08 -U1' \
    '--patch --unified=2147483648 --unified=1' \
    'HEAD AGENTS.md' 'AGENTS.md'; do
    checkout_invalid_mode_pair "$options" explicit
  done
  for template in \
    "git checkout --orphan topic --no-orphan --pathspec-from-file=$TMP_ROOT/completed.paths" \
    "git checkout --orp topic --no-orphan --pathspec-fr=$TMP_ROOT/completed.paths" \
    'git checkout --orp hooks/validate-bash.sh --no-orphan __PATH__' \
    'git checkout --orp -- --no-orphan -- __PATH__' \
    "git checkout --orp -- --no-orphan --pathspec-fr=$TMP_ROOT/completed.paths" \
    "git checkout HEAD --pathspec-fr=$TMP_ROOT/completed.paths" \
    "git checkout --pathspec-fr=$TMP_ROOT/completed.paths HEAD" \
    "git checkout --pathspec-fr=$TMP_ROOT/completed.paths --pathspec-file-nul --no-pathspec-file-nul" \
    "git checkout --pathspec-fr=$TMP_ROOT/completed.paths --pathspec-file-nul" \
    "git checkout --pathspec-fr=$TMP_ROOT/completed.paths --pathspec-fr= -- __PATH__" \
    "git checkout --pathspec-fr= --pathspec-fr=$TMP_ROOT/completed.paths" \
    'git checkout --patch -U-2 -U1 -- __PATH__' \
    'git checkout --unified=-2 --unified=-1 -- __PATH__' \
    'git checkout --patch --inter-hunk-context=-2 --inter-hunk-context=1 -- __PATH__'; do
    row_ok=1
    protected_clean="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    case "$template" in
      *' --patch '*) printf '\n# completed-selection patch dirty\n' >>"$REPO/hooks/validate-bash.sh" ;;
    esac
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
    for path in hooks/validate-bash.sh AGENTS.md; do
      case "$template" in
        *--pathspec-file-nul) printf '%s\0' "$path" >"$TMP_ROOT/completed.paths" ;;
        *) printf '%s\n' "$path" >"$TMP_ROOT/completed.paths" ;;
      esac
      command="${template//__PATH__/$path}"
      if [ "$path" = hooks/validate-bash.sh ]; then
        if [ "$template" = 'git checkout --orp hooks/validate-bash.sh --no-orphan __PATH__' ]; then
          # This lone operand names the fixture's existing branch.
          assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
            'operation=repository' || { failures=1; row_ok=0; }
        else
          assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
            "effect=overwrite target=$REPO/hooks/validate-bash.sh" || { failures=1; row_ok=0; }
        fi
      else
        printf 'ordinary changed\n' >"$REPO/AGENTS.md"
        if assert_allowed "$command" worker; then
          if (cd -- "$REPO"; printf 'y\n' | bash -c "$command") >"$TMP_ROOT/completed-checkout.out" 2>&1; then
            native_status=0
          else
            native_status=$?
            printf 'completed-selection failed native status=%s: %s\n' "$native_status" "$command" >&2
            failures=1; row_ok=0
          fi
        else failures=1; row_ok=0; fi
        [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { printf 'completed-selection failed ordinary worktree hash: %s\n' "$command" >&2; failures=1; row_ok=0; }
      fi
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { printf 'completed-selection failed protected worktree hash: %s\n' "$command" >&2; failures=1; row_ok=0; }
      [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { printf 'completed-selection failed semantic index: %s\n' "$command" >&2; failures=1; row_ok=0; }
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { printf 'completed-selection failed HEAD: %s\n' "$command" >&2; failures=1; row_ok=0; }
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { printf 'completed-selection failed branch: %s\n' "$command" >&2; failures=1; row_ok=0; }
    done
    case "$template" in
      *' --patch '*)
        git -C "$REPO" restore --worktree -- hooks/validate-bash.sh
        [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_clean" ] || { failures=1; row_ok=0; }
        command="${template//__PATH__/hooks/validate-bash.sh}"
        assert_allowed "$command" worker || { failures=1; row_ok=0; }
        (cd -- "$REPO"; printf 'y\n' | bash -c "$command") >"$TMP_ROOT/completed-clean-patch.out" 2>&1 || { failures=1; row_ok=0; }
        rg -q '^No changes\.$' "$TMP_ROOT/completed-clean-patch.out" || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_clean" ] || { failures=1; row_ok=0; }
        [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
        ;;
    esac
    if [ "$row_ok" -eq 1 ]; then printf 'completed-selection restored ordinary path and preserved other state: %s\n' "$template"; fi
  done
  [ "$failures" -eq 0 ]
}

run_checkout_option_validity_target() {
  local failures=0 options command path expected_ordinary protected_before index_before row_ok
  run_checkout_completed_selection_target || failures=1
  git -C "$REPO" checkout -q existing-other-branch
  git -C "$REPO" checkout -q "${before_branch#refs/heads/}"
  assert_denied_code 'git checkout -' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  assert_denied_code 'git checkout - --' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  assert_denied_code 'git checkout -b dash-source -' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  assert_denied_code 'git checkout - hooks/validate-bash.sh' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  checkout_invalid_mode_pair '--merge -' explicit
  checkout_invalid_mode_pair '--ours -' explicit
  checkout_invalid_mode_pair '--patch --unified=08' explicit
  checkout_invalid_mode_pair '--patch -U08' explicit
  checkout_invalid_mode_pair '--patch --inter-hunk-context=08' explicit
  checkout_invalid_mode_pair '--pathspec-from-file= --pathspec-file-nul' explicit
  printf '%s\n' hooks/validate-bash.sh >"$TMP_ROOT/cleared-checkout.paths"
  assert_denied_code "git checkout - --pathspec-from-file=$TMP_ROOT/cleared-checkout.paths" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  for options in \
    '-U -1' '-U-1' '-U-0x1' '--unified=-0x1' '--inter-hunk-context=-0x1' \
    '--pathspec-from-file=' '--pathspec-from-file ""' \
    "--pathspec-from-file=$TMP_ROOT/cleared-checkout.paths --pathspec-from-file=" \
    '-'; do
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    row_ok=1
    assert_denied_code "git checkout $options -- hooks/validate-bash.sh" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
      "effect=overwrite target=$REPO/hooks/validate-bash.sh" || { failures=1; row_ok=0; }
    printf 'ordinary changed\n' >"$REPO/AGENTS.md"
    command="git checkout $options -- AGENTS.md"
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
    else failures=1; row_ok=0; fi
    [ "$(cat -- "$REPO/AGENTS.md")" = 'ordinary base' ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
    if [ "$row_ok" -eq 1 ]; then printf 'checkout repaired-value restoration/state checks: %s\n' "$options"; fi
  done
  printf '%s\n' AGENTS.md >"$TMP_ROOT/previous-checkout.paths"
  for command in 'git checkout - AGENTS.md' "git checkout - --pathspec-from-file=$TMP_ROOT/previous-checkout.paths"; do
    printf 'ordinary changed\n' >"$REPO/AGENTS.md"
    row_ok=1
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
    else failures=1; row_ok=0; fi
    [ "$(cat -- "$REPO/AGENTS.md")" = 'ordinary base' ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
    if [ "$row_ok" -eq 1 ]; then printf 'previous-source restored ordinary path and preserved other state: %s\n' "$command"; fi
  done
  assert_denied_code "git checkout --pathspec-from-file= --pathspec-from-file=$TMP_ROOT/cleared-checkout.paths" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  assert_denied_code 'git checkout --pathspec-from-file= existing-other-branch' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  printf 'changed dash\n' >"$REPO/-"
  command='git checkout -- -'
  if assert_allowed "$command" worker; then
    (cd -- "$REPO"; bash -c "$command") || failures=1
    [ "$(cat -- "$REPO/-")" = 'literal short path' ] || failures=1
  else failures=1; fi
  for options in \
    '--patch --merge' \
    '--patch --force' \
    '--patch --overlay' \
    '--patch --conflict=diff3' \
    '--patch --merge --no-merge --merge' \
    '--patch --force --no-force --force' \
    '--patch --overlay --no-overlay --overlay'; do
    checkout_invalid_mode_pair "$options" explicit
    checkout_invalid_mode_pair "$options" explicit-file
  done
  for options in '-z' '-h'; do
    checkout_invalid_mode_pair "$options" explicit
    checkout_invalid_mode_pair "$options" ordinary-file
  done
  checkout_invalid_effect_pair
  checkout_unknown_syntax_pair
  checkout_ambiguous_source_pair
  checkout_orphan_validity_pairs
  checkout_branch_path_pair || failures=1
  restore_no_staged_pair
  restore_explicit_worktree_pair
  # Option values and destinations after the actual separator stay data.
  for path in -z -h; do
    for command in "git checkout --pathspec-from-file $path" "git checkout --pathspec-from-file=$path"; do
      printf '%s\n' hooks/validate-bash.sh >"$REPO/$path"
      assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
        "effect=overwrite target=$REPO/hooks/validate-bash.sh" || failures=1
      printf '%s\n' AGENTS.md >"$REPO/$path"
      printf 'ordinary changed\n' >"$REPO/AGENTS.md"
      if assert_allowed "$command" worker; then
        (cd -- "$REPO"; bash -c "$command") || failures=1
        [ "$(cat -- "$REPO/AGENTS.md")" = 'ordinary base' ] || failures=1
      else failures=1; fi
    done
    assert_denied_code "git checkout -- hooks/validate-bash.sh $path" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
      "effect=overwrite target=$REPO/hooks/validate-bash.sh" || failures=1
    command="git checkout -- $path"
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || failures=1
      [ "$(cat -- "$REPO/$path")" = 'literal short path' ] || failures=1
    else failures=1; fi
  done
  expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
  for options in \
    '--patch' \
    '--patch -U8' \
    '--patch --merge --no-merge' \
    '--patch --force --no-force' \
    '--patch --overlay --no-overlay' \
    '--patch --conflict=diff3 --no-merge'; do
    printf '\n# Fixture patch edit\n' >>"$REPO/hooks/validate-bash.sh"
    protected_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    index_before="$(git -C "$REPO" ls-files --stage)"
    row_ok=1
    assert_denied_code "git checkout $options -- hooks/validate-bash.sh" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
      "effect=overwrite target=$REPO/hooks/validate-bash.sh" || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
    printf 'ordinary changed\n' >"$REPO/AGENTS.md"
    command="git checkout $options -- AGENTS.md"
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; printf 'y\n' | bash -c "$command") >"$TMP_ROOT/valid-patch.out" 2>&1 || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_before" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
      if [ "$row_ok" -eq 1 ]; then printf 'valid-patch affirmative restoration/state checks: %s\n' "$options"; fi
    else failures=1; fi
    git -C "$REPO" restore --source=HEAD --worktree -- hooks/validate-bash.sh
  done
  git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
  [ "$failures" -eq 0 ]
}

checkout_context_mode_membership_pair() {
  local failures=0 blob tree mode command native_repo protected_hash index_tree head branch large_tree number redirect padding inventory_bytes
  protected_hash="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  index_tree="$(git -C "$REPO" write-tree)"
  head="$(git -C "$REPO" rev-parse HEAD)"
  branch="$(git -C "$REPO" symbolic-ref HEAD)"
  blob="$(printf 'replacement entry\n' | git -C "$REPO" hash-object -w --stdin)"
  for mode in 100644 120000 160000; do
    {
      git -C "$REPO" ls-tree HEAD | awk '$4 != "hooks"'
      if [ "$mode" = 160000 ]; then
        printf '160000 commit %s\thooks\n' "$head"
      else
        printf '%s blob %s\thooks\n' "$mode" "$blob"
      fi
    } >"$TMP_ROOT/context-mode-tree"
    tree="$(git -C "$REPO" mktree <"$TMP_ROOT/context-mode-tree")"
    command="git checkout $tree -- hooks"
    if [ "$mode" = 160000 ]; then
      assert_allowed "$command" worker || failures=1
    else
      assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
    fi
    # A minimal tracked clone establishes native descendant replacement
    # independently of the private launcher's untracked runtime helpers.
    native_repo="$TMP_ROOT/context-native-$mode"
    git clone -q --no-hardlinks "$REPO" "$native_repo"
    git -C "$native_repo" checkout "$tree" -- hooks >"$TMP_ROOT/context-mode-$mode.log" 2>&1 || failures=1
    if [ "$mode" = 160000 ]; then
      [ "$(git -C "$native_repo" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || failures=1
    else
      [ ! -e "$native_repo/hooks/validate-bash.sh" ] || failures=1
    fi
    printf 'ordinary mode control dirty\n' >"$REPO/file.txt"
    command="git checkout $tree -- file.txt"
    if assert_allowed "$command" worker; then
      git -C "$REPO" checkout "$tree" -- file.txt >"$TMP_ROOT/context-mode-ordinary.log" 2>&1 || failures=1
      [ "$(git -C "$REPO" hash-object file.txt)" = "$(git -C "$REPO" rev-parse HEAD:file.txt)" ] || failures=1
    else failures=1; fi
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || failures=1
  done
  # Selection cost depends on selected names, not unrelated source-tree size.
  printf -v padding '%0180d' 0
  {
    git -C "$REPO" ls-tree -z HEAD
    for ((number = 0; number < 17000; number++)); do
      printf '100644 blob %s\tordinary-budget-%05d%s\0' "$blob" "$number" "$padding"
    done
    printf '100644 blob %s\tliteral[star]*:name\0' "$blob"
    printf '100644 blob %s\t:literal[star]*\0' "$blob"
    printf '100644 blob %s\tline\nname\0' "$blob"
  } >"$TMP_ROOT/context-large-tree"
  large_tree="$(git -C "$REPO" mktree -z <"$TMP_ROOT/context-large-tree")"
  inventory_bytes="$(git -C "$REPO" ls-tree -r --name-only -z "$large_tree" | wc -c)"
  [ "$inventory_bytes" -gt 1048576 ] || failures=1
  assert_denied_code "git checkout $large_tree -- hooks/validate-bash.sh" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  printf 'line\nname\0' >"$TMP_ROOT/context-literal-nul.paths"
  for command in "git checkout $large_tree -- file.txt" "git --icase-pathspecs checkout $large_tree -- ':(literal)literal[star]*:name'" \
    "git --literal-pathspecs checkout $large_tree -- ':literal[star]*'" \
    "git checkout $large_tree --pathspec-from-file=$TMP_ROOT/context-literal-nul.paths --pathspec-file-nul"; do
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/context-large-native.log" 2>&1 || failures=1
    else failures=1; fi
  done
  [ "$(git -C "$REPO" hash-object 'literal[star]*:name')" = "$blob" ] || failures=1
  [ "$(git -C "$REPO" hash-object ':literal[star]*')" = "$blob" ] || failures=1
  [ "$(git -C "$REPO" hash-object $'line\nname')" = "$blob" ] || failures=1
  git -C "$REPO" --literal-pathspecs rm -q -f -- 'literal[star]*:name' ':literal[star]*' $'line\nname'
  # Persisted core.worktree maps the invocation repository's Git directory
  # to this actual configured runtime, which deliberately has no .git now.
  redirect="$TMP_ROOT/context-invocation"
  mkdir -p -- "$redirect"
  mv -- "$REPO/.git" "$redirect/.git"
  git -C "$redirect" config core.worktree "$REPO"
  cp -- "$PROOF_ROOT/$SESSION/eci_active" "$TMP_ROOT/context-marker-before-redirect"
  awk -v cwd="$redirect" '/^cwd:/ { print "cwd: " cwd; next } { print }' \
    "$TMP_ROOT/context-marker-before-redirect" >"$PROOF_ROOT/$SESSION/eci_active"
  printf '\n# redirected dirty\n' >>"$REPO/hooks/validate-bash.sh"
  assert_denied_code 'git checkout HEAD -- hooks/validate-bash.sh' ECI_WORKER_GIT_OWNERSHIP_DENIED worker '' configured absent "$redirect" || failures=1
  git -C "$redirect" checkout HEAD -- hooks/validate-bash.sh >"$TMP_ROOT/context-redirect-protected.log" 2>&1 || failures=1
  [ "$(git -C "$redirect" hash-object "$REPO/hooks/validate-bash.sh")" = "$protected_hash" ] || failures=1
  printf 'redirected ordinary dirty\n' >"$REPO/file.txt"
  if assert_allowed 'git checkout HEAD -- file.txt' worker configured absent "$redirect"; then
    git -C "$redirect" checkout HEAD -- file.txt >"$TMP_ROOT/context-redirect-ordinary.log" 2>&1 || failures=1
    [ "$(git -C "$redirect" hash-object "$REPO/file.txt")" = "$(git -C "$redirect" rev-parse HEAD:file.txt)" ] || failures=1
  else failures=1; fi
  [ "$(git -C "$redirect" write-tree)" = "$index_tree" ] || failures=1
  [ "$(git -C "$redirect" rev-parse HEAD)" = "$head" ] || failures=1
  [ "$(git -C "$redirect" symbolic-ref HEAD)" = "$branch" ] || failures=1
  git -C "$redirect" config --unset core.worktree
  mv -- "$redirect/.git" "$REPO/.git"
  cp -- "$TMP_ROOT/context-marker-before-redirect" "$PROOF_ROOT/$SESSION/eci_active"
  [ "$failures" -eq 0 ] || return 1
  printf '%s\n' 'checkout ancestor/membership/persisted worktree: PASS'
}

checkout_context_failed() {
  printf 'checkout-context failed predicate=%s command=%q native_status=%s\n' \
    "$1" "${command:-none}" "${native_status:-not-recorded}" >&2
  failures=1
}

checkout_context_prefix_pair() {
  local failures=0 command native_status=0 protected_hash ordinary_hash index_tree head branch native_source
  protected_hash="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  ordinary_hash="$(git -C "$REPO" hash-object file.txt)"
  index_tree="$(git -C "$REPO" write-tree)"
  head="$(git -C "$REPO" rev-parse HEAD)"
  branch="$(git -C "$REPO" symbolic-ref HEAD)"
  native_source="$TMP_ROOT/prefix-native"
  git clone -q --no-hardlinks "$REPO" "$native_source"
  for command in 'git -C hooks restore -- validate-bash.sh' 'git restore -- validate-bash.sh'; do
    printf '\n# native prefix dirty\n' >>"$native_source/hooks/validate-bash.sh"
    native_status=0
    if [ "$command" = 'git restore -- validate-bash.sh' ]; then
      assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker '' configured absent "$REPO/hooks" || checkout_context_failed prefix-protected-admission
      (cd -- "$native_source/hooks"; /bin/bash -c "$command") >"$TMP_ROOT/prefix-native.log" 2>&1 || native_status=$?
    else
      assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || checkout_context_failed prefix-protected-admission
      (cd -- "$native_source"; /bin/bash -c "$command") >"$TMP_ROOT/prefix-native.log" 2>&1 || native_status=$?
    fi
    [ "$native_status" -eq 0 ] || checkout_context_failed prefix-protected-native
    [ "$(git -C "$native_source" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed prefix-protected-native-restored
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed prefix-protected-preserved
  done
  command='git -C hooks restore -- ../file.txt'
  printf 'prefix ordinary dirty\n' >"$REPO/file.txt"
  assert_allowed "$command" worker || checkout_context_failed prefix-ordinary-admission
  native_status=0
  (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/prefix-ordinary-native.log" 2>&1 || native_status=$?
  [ "$native_status" -eq 0 ] || checkout_context_failed prefix-ordinary-native
  [ "$(git -C "$REPO" hash-object file.txt)" = "$ordinary_hash" ] || checkout_context_failed prefix-ordinary-restored
  [ "$(git -C "$REPO" write-tree)" = "$index_tree" ] || checkout_context_failed prefix-index
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ] || checkout_context_failed prefix-head
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$branch" ] || checkout_context_failed prefix-branch
  [ "$failures" -eq 0 ] || return 1
  printf '%s\n' 'checkout retained invocation prefix: PASS'
}

run_checkout_context_validity_target() {
  local failures=0 command protected_hash ordinary_hash index_tree head branch native_source patch_hash left right merge_left merge_right native_status fault_hash eci_hash source_ref
  protected_hash="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  ordinary_hash="$(git -C "$REPO" hash-object file.txt)"
  index_tree="$(git -C "$REPO" write-tree)"
  head="$(git -C "$REPO" rev-parse HEAD)"
  branch="$(git -C "$REPO" symbolic-ref HEAD)"
  native_source="$TMP_ROOT/context-source-native"
  git clone -q --no-hardlinks "$REPO" "$native_source"
  # Resolve source and selection through the registered gate, with the same
  # effective config/environment and native merge-base semantics as Git.
  for command in \
    'env CHECKOUT_CONTEXT_VALUE=false git --config-env=checkout.guess=CHECKOUT_CONTEXT_VALUE checkout HEAD -- hooks/validate-bash.sh' \
    'git checkout HEAD...HEAD -- hooks/validate-bash.sh' \
    'git checkout HEAD... -- hooks/validate-bash.sh' \
    'git checkout ...HEAD -- hooks/validate-bash.sh'; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || checkout_context_failed protected-source-admission
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed protected-source-preserved
    printf '\n# native protected source dirty\n' >>"$native_source/hooks/validate-bash.sh"
    native_status=0
    (cd -- "$native_source"; /bin/bash -c "$command") >"$TMP_ROOT/context-protected-native.log" 2>&1 || { native_status=$?; checkout_context_failed protected-source-native; }
    [ "$(git -C "$native_source" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed protected-source-native-restored
    [ "$(git -C "$native_source" write-tree)" = "$index_tree" ] || checkout_context_failed protected-source-native-index
    [ "$(git -C "$native_source" rev-parse HEAD)" = "$head" ] || checkout_context_failed protected-source-native-head
  done
  for command in \
    'env CHECKOUT_CONTEXT_VALUE=false git --config-env=checkout.guess=CHECKOUT_CONTEXT_VALUE checkout HEAD -- file.txt' \
    'git checkout HEAD...HEAD -- file.txt' \
    'git checkout HEAD... -- file.txt' \
    'git checkout ...HEAD -- file.txt'; do
    printf 'ordinary dirty\n' >"$REPO/file.txt"
    if assert_allowed "$command" worker; then
      native_status=0
      (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/context-native.log" 2>&1 || { native_status=$?; checkout_context_failed ordinary-source-native; }
      [ "$(git -C "$REPO" hash-object file.txt)" = "$ordinary_hash" ] || checkout_context_failed ordinary-source-native-restored
    else checkout_context_failed ordinary-source-admission; fi
    git -C "$REPO" restore --worktree -- file.txt
  done
  left="$(printf 'left base\n' | git -C "$REPO" commit-tree "$index_tree" -p "$head")"
  right="$(printf 'right base\n' | git -C "$REPO" commit-tree "$index_tree" -p "$head")"
  merge_left="$(printf 'left merge\n' | git -C "$REPO" commit-tree "$index_tree" -p "$left" -p "$right")"
  merge_right="$(printf 'right merge\n' | git -C "$REPO" commit-tree "$index_tree" -p "$right" -p "$left")"
  for command in \
    "git checkout $merge_left...$merge_right -- hooks/validate-bash.sh" \
    'env -u CHECKOUT_CONTEXT_ABSENT git --config-env=checkout.guess=CHECKOUT_CONTEXT_ABSENT checkout HEAD -- hooks/validate-bash.sh'; do
    assert_allowed "$command" worker || checkout_context_failed invalid-source-admission
    native_status=0
    (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/context-invalid-native.log" 2>&1 || native_status=$?
    [ "$native_status" -ne 0 ] && [ -s "$TMP_ROOT/context-invalid-native.log" ] || checkout_context_failed invalid-source-native-rejection
    [ "$(git -C "$REPO" write-tree)" = "$index_tree" ] || checkout_context_failed invalid-source-index
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed invalid-source-protected
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ] || checkout_context_failed invalid-source-head
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$branch" ] || checkout_context_failed invalid-source-branch
  done
  # Inject only the source reader in the disposable registered hook. The
  # real native command still succeeds; target selection and admission must
  # distinguish known protected and ordinary destinations under that fault.
  cp -- "$RUNTIME_ROOT/hooks/validate-bash.sh" "$TMP_ROOT/context-hook-before-fault"
  awk '
    /^def checkout_source\(/ { source_reader = 1 }
    source_reader && /^    if value is None:/ {
      print "    if value in {\"HEAD\", \"hooks/validate-bash.sh\", \"AGENTS.md\"}:"
      print "        return CheckoutSource(\"unresolved\", value, None, True)"
      source_reader = 0
    }
    { print }
  ' "$TMP_ROOT/context-hook-before-fault" >"$RUNTIME_ROOT/hooks/validate-bash.sh"
  rg -q 'return CheckoutSource\("unresolved", value, None, True\)' "$RUNTIME_ROOT/hooks/validate-bash.sh" || {
    printf '%s\n' 'source-reader fault injection unavailable on selected source' >&2
    checkout_context_failed source-fault-injection
  }
  assert_denied_code 'git checkout HEAD -- hooks/validate-bash.sh' ECI_WORKER_GIT_OWNERSHIP_DENIED worker source-lookup-protected-target || checkout_context_failed source-fault-protected-admission
  printf 'source-fault ordinary dirty\n' >"$REPO/file.txt"
  if assert_allowed 'git checkout HEAD -- file.txt' worker; then
    native_status=0
    git -C "$REPO" checkout HEAD -- file.txt >"$TMP_ROOT/context-source-fault-native.log" 2>&1 || { native_status=$?; checkout_context_failed source-fault-ordinary-native; }
    [ "$(git -C "$REPO" hash-object file.txt)" = "$ordinary_hash" ] || checkout_context_failed source-fault-ordinary-restored
  else checkout_context_failed source-fault-ordinary-admission; fi
  fault_hash="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  eci_hash="$(git -C "$REPO" hash-object AGENTS.md)"
  source_ref="$(git -C "$REPO" rev-parse refs/heads/hooks/validate-bash.sh)"
  git -C "$REPO" update-ref -d refs/heads/hooks/validate-bash.sh
  command='git checkout hooks/validate-bash.sh file.txt'
  assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker source-lookup-protected-target || checkout_context_failed source-fault-leading-protected-admission
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$fault_hash" ] || checkout_context_failed source-fault-leading-protected-preserved
  printf '\n# native leading protected dirty\n' >>"$native_source/hooks/validate-bash.sh"
  printf 'native leading ordinary dirty\n' >"$native_source/file.txt"
  native_status=0
  (cd -- "$native_source"; /bin/bash -c "$command") >"$TMP_ROOT/context-leading-native.log" 2>&1 || { native_status=$?; checkout_context_failed source-fault-leading-native; }
  [ "$(git -C "$native_source" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed source-fault-leading-native-protected-restored
  [ "$(git -C "$native_source" hash-object file.txt)" = "$ordinary_hash" ] || checkout_context_failed source-fault-leading-native-ordinary-restored
  [ "$(git -C "$native_source" write-tree)" = "$index_tree" ] || checkout_context_failed source-fault-leading-native-index
  [ "$(git -C "$native_source" rev-parse HEAD)" = "$head" ] || checkout_context_failed source-fault-leading-native-head
  command='git checkout AGENTS.md file.txt'
  printf 'source-fault first ordinary dirty\n' >"$REPO/AGENTS.md"
  printf 'source-fault second ordinary dirty\n' >"$REPO/file.txt"
  assert_allowed "$command" worker || checkout_context_failed source-fault-leading-ordinary-admission
  native_status=0
  (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/context-leading-ordinary-native.log" 2>&1 || { native_status=$?; checkout_context_failed source-fault-leading-ordinary-native; }
  [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$eci_hash" ] || checkout_context_failed source-fault-leading-first-restored
  [ "$(git -C "$REPO" hash-object file.txt)" = "$ordinary_hash" ] || checkout_context_failed source-fault-leading-second-restored
  command="git checkout HEAD ':(exclude)file.txt'"
  assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker source-lookup-protected-target || checkout_context_failed source-fault-exclusion-protected-admission
  printf '\n# native source exclusion dirty\n' >>"$native_source/hooks/validate-bash.sh"
  native_status=0
  (cd -- "$native_source"; /bin/bash -c "$command") >"$TMP_ROOT/context-source-exclusion-native.log" 2>&1 || { native_status=$?; checkout_context_failed source-fault-exclusion-native; }
  [ "$(git -C "$native_source" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed source-fault-exclusion-protected-restored
  command="git checkout HEAD AGENTS.md ':(exclude)hooks'"
  printf 'source-fault exclusion ordinary dirty\n' >"$REPO/AGENTS.md"
  assert_allowed "$command" worker || checkout_context_failed source-fault-exclusion-ordinary-admission
  native_status=0
  (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/context-source-exclusion-ordinary-native.log" 2>&1 || { native_status=$?; checkout_context_failed source-fault-exclusion-ordinary-native; }
  [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$eci_hash" ] || checkout_context_failed source-fault-exclusion-ordinary-restored
  printf '%s\n' hooks/validate-bash.sh >"$TMP_ROOT/context-source-fault.paths"
  command="git checkout HEAD --pathspec-from-file=$TMP_ROOT/context-source-fault.paths"
  assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker source-lookup-protected-target || checkout_context_failed source-fault-file-protected-admission
  printf '\n# native source-file protected dirty\n' >>"$native_source/hooks/validate-bash.sh"
  native_status=0
  (cd -- "$native_source"; /bin/bash -c "$command") >"$TMP_ROOT/context-source-fault-file-protected-native.log" 2>&1 || { native_status=$?; checkout_context_failed source-fault-file-protected-native; }
  [ "$(git -C "$native_source" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed source-fault-file-protected-restored
  printf '%s\n' file.txt >"$TMP_ROOT/context-source-fault.paths"
  printf 'source-fault file ordinary dirty\n' >"$REPO/file.txt"
  assert_allowed "$command" worker || checkout_context_failed source-fault-file-ordinary-admission
  native_status=0
  (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/context-source-fault-file-native.log" 2>&1 || { native_status=$?; checkout_context_failed source-fault-file-ordinary-native; }
  [ "$(git -C "$REPO" hash-object file.txt)" = "$ordinary_hash" ] || checkout_context_failed source-fault-file-ordinary-restored
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$fault_hash" ] || checkout_context_failed source-fault-final-protected
  [ "$(git -C "$REPO" write-tree)" = "$index_tree" ] || checkout_context_failed source-fault-final-index
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ] || checkout_context_failed source-fault-final-head
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$branch" ] || checkout_context_failed source-fault-final-branch
  git -C "$REPO" update-ref refs/heads/hooks/validate-bash.sh "$source_ref"
  [ "$(git -C "$REPO" rev-parse refs/heads/hooks/validate-bash.sh)" = "$source_ref" ] || checkout_context_failed source-fault-restored-ref
  cp -- "$TMP_ROOT/context-hook-before-fault" "$RUNTIME_ROOT/hooks/validate-bash.sh"
  # Operand-free patch checkout selects the changed index/worktree domain,
  # rather than an empty set or every clean tracked protected file.
  printf '\n# context patch dirty\n' >>"$REPO/hooks/validate-bash.sh"
  patch_hash="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  for command in 'git checkout --patch' 'git checkout --patch --' 'git checkout --patch HEAD --' "git checkout --patch $head --" "git checkout --patch 'HEAD^{tree}' --"; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || checkout_context_failed protected-patch-admission
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$patch_hash" ] || checkout_context_failed protected-patch-preserved
    printf '\n# native protected patch dirty\n' >>"$native_source/hooks/validate-bash.sh"
    native_status=0
    # Explicit sources also ask to apply to the worktree when the clean index
    # cannot accept the reversed dirty-worktree hunk.
    printf 'y\ny\n' | (cd -- "$native_source"; /bin/bash -c "$command") >"$TMP_ROOT/context-protected-patch-native.log" 2>&1 || { native_status=$?; checkout_context_failed protected-patch-native; }
    [ "$(git -C "$native_source" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed protected-patch-native-restored
  done
  git -C "$REPO" restore --worktree -- hooks/validate-bash.sh
  for command in 'git checkout --patch' 'git checkout --patch HEAD --' "git checkout --patch $head --" "git checkout --patch 'HEAD^{tree}' --"; do
    printf 'ordinary patch dirty\n' >"$REPO/file.txt"
    if assert_allowed "$command" worker; then
      native_status=0
      printf 'y\ny\n' | (cd -- "$REPO"; /bin/bash -c "$command") >"$TMP_ROOT/context-patch-native.log" 2>&1 || { native_status=$?; checkout_context_failed ordinary-patch-native; }
      [ "$(git -C "$REPO" hash-object file.txt)" = "$ordinary_hash" ] || checkout_context_failed ordinary-patch-native-restored
    else checkout_context_failed ordinary-patch-admission; fi
  done
  git -C "$REPO" restore --worktree -- file.txt
  [ "$(git -C "$REPO" write-tree)" = "$index_tree" ] || checkout_context_failed final-index
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ] || checkout_context_failed final-head
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$branch" ] || checkout_context_failed final-branch
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_hash" ] || checkout_context_failed final-protected
  checkout_context_mode_membership_pair || checkout_context_failed ancestor-membership-worktree-group
  checkout_context_prefix_pair || checkout_context_failed retained-prefix-group
  [ "$failures" -eq 0 ] || return 1
  printf '%s\n' 'checkout invocation/source context: PASS'
}

# Current worker contract: every CLI command passes the registered launcher,
# then the compiled CLI performs the actual operation. Legacy pathspec/checkout
# grammar lives in explicitly selected historical native differential targets.
typed_run() {
  local command status=0
  printf -v command '%q ' "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" "$@"
  assert_allowed "$command" worker
  HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" "$@" >"$TMP_ROOT/typed-operation.log" 2>&1 || status=$?
  if [ "$status" -ne 0 ]; then cat "$TMP_ROOT/typed-operation.log" >&2; return "$status"; fi
  printf 'typed operation executed: %s\n' "$*"
}

typed_reject() {
  local command before_index before_head before_branch status=0
  before_index="$(sha256sum "$REPO/.git/index")"
  before_head="$(git -C "$REPO" rev-parse HEAD)"
  before_branch="$(git -C "$REPO" symbolic-ref HEAD)"
  printf -v command '%q ' "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" "$@"
  assert_allowed "$command" worker
  HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" "$@" >"$TMP_ROOT/typed-rejection.log" 2>&1 || status=$?
  [ "$status" -ne 0 ] && [ -s "$TMP_ROOT/typed-rejection.log" ] || {
    printf 'expected CLI rejection: %s\n' "$command" >&2; return 1;
  }
  [ "$(sha256sum "$REPO/.git/index")" = "$before_index" ]
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ]
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ]
  printf 'typed rejected with state preserved: %s\n' "$*"
}

typed_invariants() {
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$matrix_head" ]
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$matrix_branch" ]
  [ "$(git -C "$REPO" show :unrelated.txt)" = 'unrelated staged' ]
  [ "$(cat "$REPO/unrelated.txt")" = 'unrelated worktree' ]
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$matrix_protected" ]
}


run_worker_git_native() {
  local command family marker="$TMP_ROOT/native-helper.marker" helper="$TMP_ROOT/native-helper"
  local native_repo="$TMP_ROOT/native-bisect" original_head
  local clone="$TMP_ROOT/native-clone" fake_git="$TMP_ROOT/native-fake-git"
  local clone_marker="$TMP_ROOT/native-fake-git.marker"
  for command in \
    "git clone -q --no-hardlinks $FOREIGN_REPO $clone" \
    "command git clone -q --no-hardlinks $FOREIGN_REPO $clone" \
    "env -- /usr/bin/git clone -q --no-hardlinks $FOREIGN_REPO $clone"; do
    assert_allowed "$command" worker
  done
  [ ! -e "$clone" ]
  git clone -q --no-hardlinks "$FOREIGN_REPO" "$clone"
  [ "$(cat "$clone/file.txt")" = base ]
  mkdir -p "$fake_git"
  printf '#!/bin/sh\nprintf entered > %q\n' "$clone_marker" >"$fake_git/git"
  chmod 755 "$fake_git/git"
  assert_denied_code "$fake_git/git clone $FOREIGN_REPO $TMP_ROOT/fake-clone" ECI_GIT_EXECUTION_CONTEXT_DENIED worker
  [ ! -e "$clone_marker" ] && [ ! -e "$TMP_ROOT/fake-clone" ]
  for command in \
    'git show --unknown-option HEAD' \
    'git diff --unknown-option --output=report' \
    'git log --unknown-option --output=report' \
    'git bisect run false' \
    'git branch --delete topic' \
    'git remote add native-example /missing' \
    'git tag native-example' \
    'git push --dry-run' \
    'git --help' \
    'git --version' \
    'git worktree list' \
    'git worktree move --help' \
    'git worktree remove --help' \
    'git worktree prune --help' \
    'git worktree repair --help' \
    'git worktree lock --help' \
    'git worktree unlock --help' \
    'git restore --staged -- file.txt' \
    'git restore --source HEAD --worktree -- file.txt' \
    'git apply --index --cached missing.patch' \
    "git -C $FOREIGN_REPO diff --output=$TMP_ROOT/foreign-native-report" \
    "git -C $FOREIGN_REPO log --oneline -1" \
    "git -C $FOREIGN_REPO status --short" \
    "git -C $FOREIGN_REPO status --short; printf ok" \
    "git --git-dir=$FOREIGN_REPO/.git --work-tree=$FOREIGN_REPO show HEAD:file.txt"; do
    assert_allowed "$command" worker
  done
  for family in add rm mv reset checkout switch commit update-index read-tree merge rebase cherry-pick revert am stash pull; do
    assert_denied_code "git $family --help" ECI_WORKER_GIT_OWNERSHIP_DENIED worker eci-worker-git
  done
  assert_denied_code 'git worktree add --help' ECI_WORKER_GIT_OWNERSHIP_DENIED worker eci-worker-git
  for command in \
    'git log && git add -- file.txt' \
    'git diff --output=report; git reset -- file.txt' \
    'git show HEAD:file.txt && env git commit --help' \
    'if true; then git log; git add -- file.txt; fi' \
    '(git diff; git reset -- file.txt)' \
    'bash -c "git log; git add -- file.txt"'; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker eci-worker-git
  done
  assert_denied_code "git status; rm -- $PROOF_ROOT/$SESSION/eci_active" ECI_PLAN_LIVE_CONTROL_DENIED worker
  printf 'native changed\n' >"$REPO/file.txt"
  command="git diff --output=$TMP_ROOT/native-report -- file.txt"
  assert_allowed "$command" worker
  git -C "$REPO" diff --output="$TMP_ROOT/native-report" -- file.txt
  [ -s "$TMP_ROOT/native-report" ]
  printf '#!/bin/sh\nprintf entered > %q\n' "$marker" >"$helper"
  chmod 755 "$helper"
  git -C "$REPO" config diff.external "$helper"
  assert_allowed 'git diff --ext-diff -- file.txt' worker
  [ ! -e "$marker" ]
  git -C "$REPO" diff --ext-diff -- file.txt
  [ "$(cat "$marker")" = entered ]
  git -C "$REPO" config --unset diff.external
  git -C "$REPO" restore --worktree -- file.txt
  mkdir -p "$native_repo"
  git -C "$native_repo" init -q
  git -C "$native_repo" -c user.name=Fixture -c user.email=fixture@example.invalid commit --allow-empty -qm 'bisect initial'
  original_head="$(git -C "$native_repo" rev-parse HEAD)"
  command="git -C $native_repo bisect start"
  assert_allowed "$command" worker
  git -C "$native_repo" bisect start
  [ -f "$native_repo/.git/BISECT_START" ]
  assert_allowed "git -C $native_repo bisect reset" worker
  git -C "$native_repo" bisect reset
  [ ! -e "$native_repo/.git/BISECT_START" ]
  [ "$(git -C "$native_repo" rev-parse HEAD)" = "$original_head" ]
  printf '%s\n' 'worker Git native default/family/compound admission: PASS'
}

run_worker_git_restore_proof_aliases() {
  local alias="$REPO/restore-alias" target="$REPO/restore-referent" proof="$REPO/proof-referent"
  local tree head command binary="$RUNTIME_ROOT/bin/eci-worker-git"
  mkdir -p -- "$alias" "$target" "$proof"
  printf 'base\n' >"$alias/file"
  printf 'sentinel\n' >"$target/file"
  printf 'live\n' >"$proof/eci_active"
  git -C "$REPO" add -- restore-alias/file proof-referent/eci_active
  git -C "$REPO" -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm 'restore alias fixture'
  rm -- "$alias/file"
  rmdir -- "$alias"
  ln -s restore-referent "$alias"
  git -C "$REPO" restore --source=HEAD --worktree -- restore-alias/file
  [ -d "$alias" ] && [ ! -L "$alias" ]
  [ "$(cat "$target/file")" = sentinel ]
  rm -- "$alias/file"
  rmdir -- "$alias"
  ln -s restore-referent "$alias"
  tree="$(git -C "$REPO" write-tree)"
  head="$(git -C "$REPO" rev-parse HEAD)"
  if "$binary" --repo "$REPO" restore --source head --destination worktree -- restore-alias/file >"$TMP_ROOT/alias-refusal.log" 2>&1; then
    printf '%s\n' 'restore replaced unnamed alias' >&2
    return 1
  fi
  rg -q 'restore-alias/file.*ancestor symlink.*restore-alias.*move' "$TMP_ROOT/alias-refusal.log"
  [ -L "$alias" ]
  "$binary" --repo "$REPO" restore --source head --destination index -- restore-alias/file
  command="mv -- '$alias' '$alias.saved-alias'"
  assert_allowed "$command" worker
  mv -- "$alias" "$alias.saved-alias"
  "$binary" --repo "$REPO" restore --source head --destination worktree -- restore-alias/file
  [ -L "$alias.saved-alias" ] && [ "$(cat "$alias/file")" = base ] && [ "$(cat "$target/file")" = sentinel ]
  ln -s proof-referent "$REPO/proof-alias"
  if ECI_PROOF_ROOT="$REPO/proof-alias" "$binary" --repo "$REPO" remove --destination worktree -- proof-referent/eci_active >"$TMP_ROOT/proof-refusal.log" 2>&1; then
    printf '%s\n' 'physical proof root alias lost protection' >&2
    return 1
  fi
  [ "$(cat "$proof/eci_active")" = live ]
  [ "$(git -C "$REPO" write-tree)" = "$tree" ] && [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ]
  printf '%s\n' 'worker Git native restore/proof aliases and registered owned hatch: PASS'
}

run_worker_git_index_aliases() {
  local saved_repo="$REPO" kind oid head_before outside="$TMP_ROOT/alias-outside"
  REPO="$TMP_ROOT/alias-index-repo"
  mkdir -p -- "$REPO/dir" "$outside"
  git -C "$REPO" init -q
  git -C "$REPO" config user.email normal-git-test@example.invalid
  git -C "$REPO" config user.name 'Normal Git Test'
  printf 'base\n' >"$REPO/dir/file"
  printf 'unrelated\n' >"$REPO/unrelated"
  git -C "$REPO" add -- dir/file unrelated
  git -C "$REPO" commit -qm 'alias baseline'
  printf 'changed\n' >"$REPO/dir/file"
  git -C "$REPO" diff -- dir/file >"$TMP_ROOT/alias.patch"
  oid="$(git -C "$REPO" hash-object -w dir/file)"
  rm -- "$REPO/dir/file"
  rmdir -- "$REPO/dir"
  printf 'outside sentinel\n' >"$outside/file"
  chmod 640 "$outside/file"
  ln -s "$outside" "$REPO/dir"
  head_before="$(git -C "$REPO" rev-parse HEAD)"
  printf '%s\n' 'scope: typed index aliases' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  git -C "$REPO" ls-files --stage -z -- unrelated >"$TMP_ROOT/alias-unrelated-before"
  for kind in restore unstage remove stage-hunks; do
    git -C "$REPO" restore --source=HEAD --staged -- dir/file
    if [ "$kind" != stage-hunks ]; then
      git -C "$REPO" update-index --add --cacheinfo "100644,$oid,dir/file"
    fi
    case "$kind" in
      restore) typed_run restore --source head --destination index -- dir/file ;;
      unstage) typed_run unstage -- dir/file ;;
      remove) typed_run remove --destination index -- dir/file ;;
      stage-hunks) typed_run stage-hunks --patch-file "$TMP_ROOT/alias.patch" -- dir/file ;;
    esac
    if [ "$kind" = remove ]; then
      [ -z "$(git -C "$REPO" ls-files --stage -- dir/file)" ]
    elif [ "$kind" = stage-hunks ]; then
      [ "$(git -C "$REPO" show :dir/file)" = changed ]
    else
      [ "$(git -C "$REPO" show :dir/file)" = base ]
    fi
    [ "$(readlink "$REPO/dir")" = "$outside" ]
    [ "$(cat "$outside/file")" = 'outside sentinel' ]
    [ "$(stat -c %a "$outside/file")" = 640 ]
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$head_before" ]
    git -C "$REPO" ls-files --stage -z -- unrelated >"$TMP_ROOT/alias-unrelated-after"
    cmp -- "$TMP_ROOT/alias-unrelated-before" "$TMP_ROOT/alias-unrelated-after"
    typed_reject stage-content -- dir/file
    typed_reject stage-removals -- dir/file
    typed_reject restore --source head --destination worktree -- dir/file
    typed_reject remove --destination worktree -- dir/file
    typed_reject move -- dir/file moved
    git -C "$REPO" update-index --add --cacheinfo "100644,$oid,dir/file"
    git -C "$REPO" restore --source=HEAD --staged -- dir/file
    [ "$(git -C "$REPO" show :dir/file)" = base ]
    [ "$(cat "$outside/file")" = 'outside sentinel' ]
  done
  REPO="$saved_repo"
  printf '%s\n' 'scope: typed matrix' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  printf '%s\n' 'worker Git registered logical index aliases and physical denial pairs: PASS'
}

run_worker_git_mutation_repairs() {
  local saved_repo="$REPO" source_oid index_path pointer_before command status=0 conflict_oid kind directory_head
  REPO="$TMP_ROOT/typed-repair-repo"
  mkdir -p -- "$REPO/hooks" "$REPO/dir"
  git -C "$REPO" init -q
  git -C "$REPO" config user.email normal-git-test@example.invalid
  git -C "$REPO" config user.name 'Normal Git Test'
  printf 'base\n' >"$REPO/file.txt"
  printf 'unrelated\n' >"$REPO/unrelated.txt"
  printf 'base\n' >"$REPO/index-only.txt"
  printf 'child\n' >"$REPO/dir/child"
  printf 'live hook\n' >"$REPO/hooks/validate-bash.sh"
  git -C "$REPO" add -- file.txt unrelated.txt index-only.txt dir/child hooks/validate-bash.sh
  git -C "$REPO" commit -qm 'repair baseline'
  printf '%s\n' 'scope: typed repairs' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  source_oid="$(git -C "$REPO" rev-parse HEAD)"
  typed_reject stage-content -- dir
  typed_reject remove --destination worktree -- file.txt dir
  typed_reject remove --destination index -- dir
  typed_reject restore --source head --destination index -- dir
  typed_reject unstage -- dir
  typed_reject move -- dir moved
  typed_reject remove --destination worktree -- file.txt missing
  [ "$(cat "$REPO/file.txt")" = base ]
  [ "$(cat "$REPO/dir/child")" = child ]
  # An ordinary source path can be a configured live hook's resolved referent.
  # Keep the registered Bash launcher intact while changing the private Edit hook.
  mv -- "$RUNTIME_ROOT/hooks/validate-edit-write.sh" "$TMP_ROOT/saved-edit-hook"
  ln -s "$REPO/file.txt" "$RUNTIME_ROOT/hooks/validate-edit-write.sh"
  typed_reject remove --destination worktree -- file.txt
  [ "$(cat "$REPO/file.txt")" = base ]
  mv -- "$RUNTIME_ROOT/hooks" "$REPO/aliased-hooks"
  ln -s "$REPO/aliased-hooks" "$RUNTIME_ROOT/hooks"
  typed_reject remove --destination worktree -- aliased-hooks/validate-edit-write.sh
  [ -L "$REPO/aliased-hooks/validate-edit-write.sh" ]
  ln -s "$REPO/file.txt" "$REPO/aliased-hooks/ordinary-link"
  typed_run remove --destination worktree -- aliased-hooks/ordinary-link
  [ "$(cat "$REPO/file.txt")" = base ]
  rm -- "$RUNTIME_ROOT/hooks"
  mv -- "$REPO/aliased-hooks" "$RUNTIME_ROOT/hooks"
  rm -- "$RUNTIME_ROOT/hooks/validate-edit-write.sh"
  mv -- "$TMP_ROOT/saved-edit-hook" "$RUNTIME_ROOT/hooks/validate-edit-write.sh"
  # Gitlinks may mutate .gitmodules or move implicit administrative state.
  git -C "$REPO" update-index --add --cacheinfo "160000,$source_oid,sub"
  git -C "$REPO" commit -qm 'gitlink fixture'
  printf 'metadata sentinel\n' >"$REPO/.gitmodules"
  typed_reject remove --destination both -- sub
  typed_reject restore --source head --destination worktree -- sub
  typed_reject move -- sub destination
  [ "$(cat "$REPO/.gitmodules")" = 'metadata sentinel' ]
  typed_run remove --destination index -- sub
  [ "$(cat "$REPO/.gitmodules")" = 'metadata sentinel' ]
  directory_head="$(git -C "$REPO" rev-parse HEAD)"
  git -C "$REPO" ls-files --stage -z -- file.txt unrelated.txt dir/child hooks/validate-bash.sh >"$TMP_ROOT/directory-unrelated-before"
  for kind in restore unstage remove stage-hunks; do
    printf 'changed\n' >"$REPO/index-only.txt"
    git -C "$REPO" diff -- index-only.txt >"$TMP_ROOT/index-only.patch"
    [ "$kind" = stage-hunks ] || git -C "$REPO" add -- index-only.txt
    rm -- "$REPO/index-only.txt"
    mkdir -- "$REPO/index-only.txt"
    printf 'untouched\n' >"$REPO/index-only.txt/child"
    case "$kind" in
      restore) typed_run restore --source head --destination index -- index-only.txt ;;
      unstage) typed_run unstage -- index-only.txt ;;
      remove) typed_run remove --destination index -- index-only.txt ;;
      stage-hunks) typed_run stage-hunks --patch-file "$TMP_ROOT/index-only.patch" -- index-only.txt ;;
    esac
    if [ "$kind" = remove ]; then
      [ -z "$(git -C "$REPO" ls-files --stage -- index-only.txt)" ]
    elif [ "$kind" = stage-hunks ]; then
      [ "$(git -C "$REPO" show :index-only.txt)" = changed ]
    else
      [ "$(git -C "$REPO" show :index-only.txt)" = base ]
    fi
    [ "$(cat "$REPO/index-only.txt/child")" = untouched ]
    typed_reject stage-content -- index-only.txt
    typed_reject stage-removals -- index-only.txt
    typed_reject restore --source head --destination worktree -- index-only.txt
    typed_reject remove --destination worktree -- index-only.txt
    [ "$(cat "$REPO/index-only.txt/child")" = untouched ]
    git -C "$REPO" ls-files --stage -z -- file.txt unrelated.txt dir/child hooks/validate-bash.sh >"$TMP_ROOT/directory-unrelated-after"
    cmp -- "$TMP_ROOT/directory-unrelated-before" "$TMP_ROOT/directory-unrelated-after"
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$directory_head" ]
    rm -- "$REPO/index-only.txt/child"
    rmdir -- "$REPO/index-only.txt"
    git -C "$REPO" restore --source=HEAD --staged --worktree -- index-only.txt
  done
  printf '%s\n' 'worker Git untouched index-only directory registered pairs: PASS'
  # A hunk can coexist with unrelated complete conflict stage records.
  conflict_oid="$(git -C "$REPO" rev-parse :unrelated.txt)"
  git -C "$REPO" update-index --force-remove unrelated.txt
  printf '100644 %s 1\tunrelated.txt\n100644 %s 2\tunrelated.txt\n100644 %s 3\tunrelated.txt\n' "$conflict_oid" "$conflict_oid" "$conflict_oid" |
    git -C "$REPO" update-index --index-info
  git -C "$REPO" ls-files --stage -z -- unrelated.txt >"$TMP_ROOT/conflict-before"
  printf 'selected hunk\n' >"$REPO/file.txt"
  git -C "$REPO" diff -- file.txt >"$TMP_ROOT/repair.patch"
  typed_run stage-hunks --patch-file "$TMP_ROOT/repair.patch" -- file.txt
  [ "$(git -C "$REPO" show :file.txt)" = 'selected hunk' ]
  git -C "$REPO" ls-files --stage -z -- unrelated.txt >"$TMP_ROOT/conflict-after"
  cmp -- "$TMP_ROOT/conflict-before" "$TMP_ROOT/conflict-after"
  # The linked worktree's pointer itself must remain a protected leaf.
  git -C "$REPO" worktree add -q -b repair-linked "$TMP_ROOT/typed-linked"
  REPO="$TMP_ROOT/typed-linked"
  printf '%s\n' 'scope: linked pointer repair' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  pointer_before="$(cat "$REPO/.git")"
  index_path="$(git -C "$REPO" rev-parse --path-format=absolute --git-path index)"
  cp -- "$index_path" "$TMP_ROOT/linked-index-before"
  printf -v command '%q ' "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" remove --destination worktree -- .git
  assert_allowed "$command" worker
  status=0
  HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" remove --destination worktree -- .git >"$TMP_ROOT/linked-pointer-rejection.log" 2>&1 || status=$?
  [ "$status" -ne 0 ] && [ "$(cat "$REPO/.git")" = "$pointer_before" ]
  cmp -- "$index_path" "$TMP_ROOT/linked-index-before"
  REPO="$saved_repo"
  printf '%s\n' 'scope: typed matrix' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  printf '%s\n' 'worker Git mutation repair registered pairs: PASS'
}

run_worker_git_cli_edges() {
  local saved_repo head hook_hash mode tree_oid status object blob
  saved_repo="$REPO"
  head="$(git -C "$REPO" rev-parse HEAD)"
  hook_hash="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  # Native skip-worktree state does not expand literal selection or waive
  # live-target protection. Content staging remains native index behavior.
  git -C "$REPO" update-index --skip-worktree file.txt
  # Git itself excludes skip-worktree entries from this fixed restore form;
  # native-invalid selection remains an ordinary CLI diagnostic, not a gate.
  status=0
  git -C "$REPO" restore --source=HEAD --worktree -- file.txt >"$TMP_ROOT/skip-native.log" 2>&1 || status=$?
  [ "$status" -ne 0 ] && [ -s "$TMP_ROOT/skip-native.log" ]
  typed_reject restore --source head --destination worktree -- file.txt
  typed_reject restore --source head --destination worktree -- hooks/validate-bash.sh
  git -C "$REPO" update-index --no-skip-worktree file.txt
  # A source/index gitlink at the protected ancestor must never recursively
  # rewrite live hooks. Both source/tree and index modes retain exact denial.
  cp "$REPO/.git/index" "$TMP_ROOT/edge-index"
  git -C "$REPO" rm -q --cached -- hooks/validate-bash.sh
  blob="$(printf 'ancestor replacement\n' | git -C "$REPO" hash-object -w --stdin)"
  for mode in 100644 120000 160000; do
    object="$blob"; [ "$mode" != 160000 ] || object="$head"
    git -C "$REPO" update-index --add --cacheinfo "$mode,$object,hooks"
    tree_oid="$(git -C "$REPO" write-tree)"
    typed_reject restore --source index --destination worktree -- hooks
    typed_reject restore --source "$tree_oid" --destination both -- hooks
    git -C "$REPO" update-index --force-remove hooks
  done
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$hook_hash" ]
  cp "$TMP_ROOT/edge-index" "$REPO/.git/index"
  # A leaf symlink is a literal entry: restoring/removing it changes the link,
  # and preserves its protected referent. Ancestor aliases were denied above.
  ln -s hooks/validate-bash.sh "$REPO/ordinary-link"
  typed_run stage-content -- ordinary-link
  [ "$(git -C "$REPO" ls-files --stage -- ordinary-link | cut -d' ' -f1)" = 120000 ]
  rm "$REPO/ordinary-link"
  typed_run restore --source index --destination worktree -- ordinary-link
  [ "$(readlink "$REPO/ordinary-link")" = hooks/validate-bash.sh ]
  typed_run remove --destination both -- ordinary-link
  [ ! -L "$REPO/ordinary-link" ]
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$hook_hash" ]
  # Unborn unstage removes only selected prepared content, retaining the
  # other prepared blob and every live file, without inventing a HEAD.
  REPO="$TMP_ROOT/typed-unborn"
  mkdir -p "$REPO"; git -C "$REPO" init -q
  printf '%s\n' 'scope: typed unborn matrix' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  printf 'unborn selected\n' >"$REPO/selected"
  printf 'unborn retained\n' >"$REPO/retained"
  typed_run stage-content -- selected retained
  typed_run unstage -- selected
  if git -C "$REPO" cat-file -e :selected 2>/dev/null; then return 1; fi
  [ "$(git -C "$REPO" show :retained)" = 'unborn retained' ]
  [ "$(cat "$REPO/selected")" = 'unborn selected' ]
  if git -C "$REPO" rev-parse --verify HEAD >/dev/null 2>&1; then return 1; fi
  REPO="$saved_repo"
  printf '%s\n' 'scope: typed matrix' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$head" ]
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$hook_hash" ]
  run_worker_git_mutation_repairs
  run_worker_git_index_aliases
  printf '%s\n' 'worker Git mode/unborn registered edges: PASS'
}

run_worker_git_cli_matrix() {
  local matrix_head matrix_branch matrix_protected source_oid path destination source hook_hash redirect original_repo native_status command i
  # Establish ordinary and unrelated staged/worktree states once; each bounded
  # operation preserves HEAD, branch, live hook and unrelated content.
  git -C "$REPO" restore --worktree -- file.txt
  for path in unrelated.txt removed.txt move.txt ':(glob)*' '*.txt' 'HOOKS' 'space name' '-n'; do
    printf 'ordinary base\n' >"$REPO/$path"
    git -C "$REPO" --literal-pathspecs add -- "$path"
  done
  git -C "$REPO" commit -qm 'matrix baseline'
  matrix_head="$(git -C "$REPO" rev-parse HEAD)"
  matrix_branch="$(git -C "$REPO" symbolic-ref HEAD)"
  matrix_protected="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  source_oid="$(git -C "$REPO" rev-parse HEAD^{tree})"
  printf 'unrelated staged\n' >"$REPO/unrelated.txt"
  git -C "$REPO" add -- unrelated.txt
  printf 'unrelated worktree\n' >"$REPO/unrelated.txt"
  printf 'content staged\n' >"$REPO/file.txt"
  typed_run stage-content -- file.txt
  [ "$(git -C "$REPO" show :file.txt)" = 'content staged' ]; typed_invariants
  typed_run unstage -- file.txt
  [ "$(git -C "$REPO" show :file.txt)" = base ]
  [ "$(cat "$REPO/file.txt")" = 'content staged' ]; typed_invariants
  # Literal selectors select only the named entry, including case and dash.
  for path in ':(glob)*' '*.txt' 'HOOKS' 'space name' '-n'; do
    printf 'literal changed\n' >"$REPO/$path"
    typed_run stage-content -- "$path"
    [ "$(git -C "$REPO" show ":$path")" = 'literal changed' ]
    typed_run restore --source head --destination both -- "$path"
    [ "$(cat "$REPO/$path")" = 'ordinary base' ]; typed_invariants
  done
  # Fixed source/destination matrix: index reads only restore the worktree.
  for source in head "$source_oid"; do
    for destination in worktree index both; do
      printf 'destination dirty\n' >"$REPO/file.txt"
      typed_run stage-content -- file.txt
      typed_run restore --source "$source" --destination "$destination" -- file.txt
      if [ "$destination" != index ]; then [ "$(cat "$REPO/file.txt")" = base ]; fi
      if [ "$destination" != worktree ]; then [ "$(git -C "$REPO" show :file.txt)" = base ]; fi
      typed_invariants
    done
  done
  printf 'index source\n' >"$REPO/file.txt"; typed_run stage-content -- file.txt
  printf 'worktree source\n' >"$REPO/file.txt"
  typed_run restore --source index --destination worktree -- file.txt
  [ "$(cat "$REPO/file.txt")" = 'index source' ]; typed_invariants
  typed_run restore --source head --destination both -- file.txt
  # A protected file supports content/index operations; worktree effects deny.
  printf '\n# protected dirty\n' >>"$REPO/hooks/validate-bash.sh"
  matrix_protected="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  typed_run stage-content -- hooks/validate-bash.sh
  [ "$(git -C "$REPO" rev-parse :hooks/validate-bash.sh)" = "$matrix_protected" ]; typed_invariants
  typed_run unstage -- hooks/validate-bash.sh
  typed_run restore --source head --destination index -- hooks/validate-bash.sh
  typed_run remove --destination index -- hooks/validate-bash.sh
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$matrix_protected" ]
  typed_run restore --source head --destination index -- hooks/validate-bash.sh
  for source in head index "$source_oid"; do
    typed_reject restore --source "$source" --destination worktree -- hooks/validate-bash.sh
    typed_invariants
  done
  typed_reject restore --source head --destination both -- hooks/validate-bash.sh
  typed_reject remove --destination worktree -- hooks
  typed_reject remove --destination both -- hooks/validate-bash.sh
  typed_reject move -- hooks/validate-bash.sh moved-hook
  typed_reject move -- file.txt hooks/validate-bash.sh
  typed_invariants
  # Protection is based on physical ancestors and provider paths, not a name.
  ln -s hooks "$REPO/hook-alias"
  typed_reject restore --source head --destination worktree -- hook-alias/validate-bash.sh
  ln -s "$FOREIGN_REPO" "$REPO/outside-alias"
  typed_reject stage-content -- outside-alias/file.txt
  typed_reject stage-content -- .git/index
  typed_reject stage-content -- ../file.txt
  typed_reject stage-content -- /etc/hosts
  typed_reject stage-content -- hooks
  typed_reject restore --source index --destination both -- file.txt
  typed_reject restore --source missing-ref --destination worktree -- file.txt
  typed_reject restore --source 0000000000000000000000000000000000000000 --destination both -- file.txt
  typed_reject restore --source head --destination worktree -- absent.txt
  typed_reject stage-content -- absent.txt
  typed_reject stage-removals -- file.txt
  typed_reject move -- file.txt move.txt
  typed_reject commit --amend
  typed_reject commit --message invalid -- file.txt
  typed_invariants
  # Content staging never sweeps deletions; removal staging requires absence.
  rm -- "$REPO/removed.txt"
  typed_run stage-content -- file.txt
  git -C "$REPO" cat-file -e :removed.txt
  typed_run stage-removals -- removed.txt
  if git -C "$REPO" cat-file -e :removed.txt 2>/dev/null; then return 1; fi
  typed_run restore --source head --destination both -- removed.txt; typed_invariants
  for destination in index worktree both; do
    typed_run remove --destination "$destination" -- removed.txt
    if [ "$destination" != index ]; then [ ! -e "$REPO/removed.txt" ]; else [ -f "$REPO/removed.txt" ]; fi
    if [ "$destination" != worktree ]; then
      if git -C "$REPO" cat-file -e :removed.txt 2>/dev/null; then return 1; fi
    else git -C "$REPO" cat-file -e :removed.txt; fi
    typed_run restore --source head --destination both -- removed.txt; typed_invariants
  done
  typed_run move -- move.txt moved.txt
  [ ! -e "$REPO/move.txt" ] && [ -f "$REPO/moved.txt" ]
  [ "$(git -C "$REPO" show :moved.txt)" = 'ordinary base' ]; typed_invariants
  typed_run move -- moved.txt move.txt; typed_invariants
  # Native patch selection proves complete changed-name validation and keeps
  # worktree content untouched, including unlisted second-file rejection.
  typed_run restore --source head --destination both -- file.txt
  printf 'hunk changed\n' >"$REPO/file.txt"
  git -C "$REPO" diff -- file.txt >"$TMP_ROOT/one.patch"
  printf 'second patch\n' >"$REPO/removed.txt"
  git -C "$REPO" diff -- file.txt removed.txt >"$TMP_ROOT/two.patch"
  typed_reject stage-hunks --patch-file "$TMP_ROOT/two.patch" -- file.txt
  typed_run stage-hunks --patch-file "$TMP_ROOT/one.patch" -- file.txt
  [ "$(git -C "$REPO" show :file.txt)" = 'hunk changed' ]
  [ "$(cat "$REPO/file.txt")" = 'hunk changed' ]; typed_invariants
  typed_run restore --source head --destination both -- file.txt removed.txt
  # A large tracked inventory does not broaden a narrow literal selection.
  for ((i=0; i<1200; i++)); do printf 'inventory\n' >"$REPO/inventory-$i"; done
  git -C "$REPO" add -- 'inventory-*'
  printf 'large ordinary\n' >"$REPO/file.txt"
  typed_run stage-content -- file.txt
  [ "$(git -C "$REPO" show :file.txt)" = 'large ordinary' ]; typed_invariants
  typed_run restore --source head --destination both -- file.txt
  git -C "$REPO" reset -q HEAD -- 'inventory-*'
  # The marker must match the physical callback CWD. An inactive control
  # separately proves that it makes no active ECI admission claim.
  assert_allowed 'git add -- file.txt' worker configured absent "$FOREIGN_REPO"
  original_repo="$REPO"; redirect="$TMP_ROOT/typed-redirect"
  mkdir -p "$redirect"
  cp "$REPO/file.txt" "$redirect/file.txt"
  mkdir -p "$redirect/hooks"; cp "$REPO/hooks/validate-bash.sh" "$redirect/hooks/validate-bash.sh"
  git -C "$REPO" config core.worktree "$redirect"
  REPO="$(realpath -e "$redirect")"
  printf '%s\n' 'scope: typed redirected worktree' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  # --repo continues to designate the existing Git directory owner, whose
  # persisted worktree resolves to the physical callback's directory.
  printf -v command '%q ' "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$original_repo" restore --source head --destination worktree -- file.txt
  assert_allowed "$command" worker
  HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$original_repo" restore --source head --destination worktree -- file.txt
  [ "$(cat "$REPO/file.txt")" = base ]
  git --git-dir="$original_repo/.git" config --unset core.worktree
  REPO="$original_repo"
  printf '%s\n' 'scope: typed matrix' "cwd: $REPO" "session_id: $SESSION" >"$PROOF_ROOT/$SESSION/eci_active"
  typed_invariants
  # Native-invalid behavior is independent native component evidence; it is
  # never reinterpreted as permission for worker native mutations.
  hook_hash="$(sha256sum "$REPO/.git/index")"
  native_status=0
  git -C "$REPO" checkout --definitely-invalid -- file.txt >"$TMP_ROOT/native-invalid.log" 2>&1 || native_status=$?
  [ "$native_status" -ne 0 ] && [ -s "$TMP_ROOT/native-invalid.log" ]
  [ "$(sha256sum "$REPO/.git/index")" = "$hook_hash" ]; typed_invariants
  printf 'helper dirty\n' >"$REPO/file.txt"

  typed_run restore --source head --destination both -- file.txt
  for command in 'git status --short' 'git diff -- file.txt' 'git log --oneline -1' 'git show HEAD:file.txt'; do assert_allowed "$command" worker; done
  assert_allowed "git diff --output=$TMP_ROOT/inspection.out" worker
  assert_allowed "git -C $FOREIGN_REPO status --short" worker
  assert_allowed 'git add -- file.txt' coordinator
  assert_denied_code 'git add .' ECI_BROAD_DESTRUCTIVE_DENIED coordinator
  assert_denied_code 'git reset --hard' ECI_BROAD_DESTRUCTIVE_DENIED coordinator
  for command in 'git rebase topic' 'git merge topic' 'git checkout -- file.txt' 'git restore -- file.txt' 'git rm -- file.txt' 'git mv file.txt next.txt' 'git diff && env git add -- file.txt'; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker
  done
  # Prepared-index commit intentionally includes the unrelated prepared blob;
  # it preserves the unrelated live worktree and has no implicit -a behavior.
  printf 'prepared commit\n' >"$REPO/file.txt"; typed_run stage-content -- file.txt
  printf 'unstaged commit\n' >"$REPO/file.txt"
  typed_run commit --message 'typed prepared checkpoint'
  [ "$(git -C "$REPO" show HEAD:file.txt)" = 'prepared commit' ]
  [ "$(cat "$REPO/file.txt")" = 'unstaged commit' ]
  [ "$(git -C "$REPO" show HEAD:unrelated.txt)" = 'unrelated staged' ]
  [ "$(cat "$REPO/unrelated.txt")" = 'unrelated worktree' ]
  [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$matrix_branch" ]
  [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$matrix_protected" ]
  git -C "$REPO" diff --cached --quiet
  run_worker_git_cli_edges
  printf '%s\n' 'worker Git typed-operation registered matrix: PASS'
}

run_effect_aware_git_target() {
  local command failures=0 before_head before_branch protected_base protected_changed
  # Protection binds to the configured runtime, so make that complete copied
  # runtime the actual repository instead of using a lookalike sibling path.
  REPO="$(realpath -e -- "$RUNTIME_ROOT")"
  git -C "$REPO" init -q
  git -C "$REPO" config user.email normal-git-test@example.invalid
  git -C "$REPO" config user.name 'Normal Git Test'
  printf 'base\n' >"$REPO/file.txt"
  printf 'ordinary base\n' >"$REPO/AGENTS.md"
  printf 'literal short path\n' >"$REPO/-z"
  printf 'literal short path\n' >"$REPO/-h"
  printf 'literal short path\n' >"$REPO/-"
  printf '%s\n' 'scope: effect-aware Git regression' "cwd: $REPO" \
    "session_id: $SESSION" 'created_utc: 2026-08-28T00:00:00Z' >"$PROOF_ROOT/$SESSION/eci_active"
  git -C "$REPO" add -- hooks/validate-bash.sh hooks.json AGENTS.md file.txt -z -h -
  git -C "$REPO" commit -qm 'effect fixture'
  protected_base="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  git -C "$REPO" branch hooks/validate-bash.sh
  git -C "$REPO" branch existing-other-branch
  git -C "$REPO" update-ref refs/remotes/origin/file.txt HEAD
  git -C "$REPO" update-ref refs/remotes/origin/remote-topic HEAD
  git -C "$REPO" config remote.origin.url "$FOREIGN_REPO"
  git -C "$REPO" config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'
  before_head="$(git -C "$REPO" rev-parse HEAD)"
  before_branch="$(git -C "$REPO" symbolic-ref HEAD)"
  if [[ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = worker-git-cli-slice || "${NORMAL_GIT_ADMISSION_TARGET:-full}" = worker-git-cli-matrix || "${NORMAL_GIT_ADMISSION_TARGET:-full}" = worker-git-cli-edges || "${NORMAL_GIT_ADMISSION_TARGET:-full}" = worker-git-native || "${NORMAL_GIT_ADMISSION_TARGET:-full}" = worker-git-index-aliases || "${NORMAL_GIT_ADMISSION_TARGET:-full}" = full ]]; then
    (
      cd -- "$SOURCE_ROOT/hooks/lib/eci-worker-git-go"
      env GOWORK=off CGO_ENABLED=0 /usr/lib/go-1.26/bin/go build -mod=readonly -trimpath -buildvcs=false -o "$RUNTIME_ROOT/bin/eci-worker-git" .
    )
    assert_denied_code 'git add -- file.txt' ECI_WORKER_GIT_OWNERSHIP_DENIED worker eci-worker-git || return 1
    assert_allowed 'git diff -- file.txt' worker || return 1
    assert_allowed 'git log --oneline -1' worker || return 1
    assert_allowed 'git show HEAD:file.txt' worker || return 1
    assert_denied_code 'git diff -- file.txt && git add -- file.txt' ECI_WORKER_GIT_OWNERSHIP_DENIED worker eci-worker-git || return 1
    assert_denied_code 'env git add -- file.txt' ECI_WORKER_GIT_OWNERSHIP_DENIED worker eci-worker-git || return 1
    mkdir -p -- "$TMP_ROOT/typed-fixture-init"
    assert_allowed "git -C $TMP_ROOT/typed-fixture-init init -q" worker || return 1
    git -C "$TMP_ROOT/typed-fixture-init" init -q
    [ -d "$TMP_ROOT/typed-fixture-init/.git" ] || return 1
    assert_denied_code "\"$RUNTIME_ROOT/bin/eci-worker-git\" --repo \"$FOREIGN_REPO\" stage-content -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED worker || return 1
    command="\"$RUNTIME_ROOT/bin/eci-worker-git\" --repo \"$REPO\" stage-content -- file.txt"
    assert_allowed "$command" worker || return 1
    printf 'typed stage changed\n' >"$REPO/file.txt"
    HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" stage-content -- file.txt
    [ "$(git -C "$REPO" show :file.txt)" = 'typed stage changed' ] || return 1
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || return 1
    command="\"$RUNTIME_ROOT/bin/eci-worker-git\" --repo \"$REPO\" unstage -- file.txt"
    assert_allowed "$command" worker || return 1
    HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" unstage -- file.txt
    [ "$(git -C "$REPO" show :file.txt)" = base ] || return 1
    [ "$(cat "$REPO/file.txt")" = 'typed stage changed' ] || return 1
    command="\"$RUNTIME_ROOT/bin/eci-worker-git\" --repo \"$REPO\" restore --source head --destination worktree -- hooks/validate-bash.sh"
    assert_allowed "$command" worker || return 1
    if HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" restore --source head --destination worktree -- hooks/validate-bash.sh \
      >"$TMP_ROOT/typed-protected.log" 2>&1; then
      printf '%s\n' 'typed CLI admitted protected restoration' >&2
      return 1
    fi
    rg -q 'protected live target' "$TMP_ROOT/typed-protected.log" || return 1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || return 1
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || return 1
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || return 1
    git -C "$REPO" diff --cached --quiet || return 1
    printf '%s\n' 'worker Git native referral/typed-operation slice: PASS'
    run_worker_git_native
    [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" != worker-git-native ] || return 0
    if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = worker-git-index-aliases ]; then
      run_worker_git_index_aliases
      return 0
    fi
    run_worker_git_restore_proof_aliases
    if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = worker-git-cli-edges ]; then
      run_worker_git_cli_edges
    elif [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" != worker-git-cli-slice ]; then
      run_worker_git_cli_matrix
    fi
    return 0
  fi
  assert_denied_code 'git restore -- hooks/validate-bash.sh' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || return 1
  if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = checkout-context-prefix ]; then
    checkout_context_prefix_pair
    return $?
  fi
  if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = checkout-context-validity ]; then
    run_checkout_context_validity_target
    return $?
  fi
  if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = checkout-branch-source-validity ]; then
    checkout_branch_path_pair
    return $?
  fi
  if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = checkout-native-selection ]; then
    run_checkout_native_selection_target
    return $?
  fi
  if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = checkout-completed-selection ]; then
    run_checkout_completed_selection_target
    return $?
  fi
  if [ "${NORMAL_GIT_ADMISSION_TARGET:-full}" = checkout-option-validity ]; then
    run_checkout_option_validity_target
    return $?
  fi
  run_checkout_native_selection_target || failures=1
  run_checkout_option_validity_target || failures=1
  run_checkout_context_validity_target || failures=1

  # A verified checkout tree source is not an explicit destination and must
  # not prevent loading file-selected destinations. Protected forms are
  # checked only; admitted ordinary forms execute through real Git.
  printf '%s\n' hooks/validate-bash.sh >"$TMP_ROOT/protected-checkout.paths"
  printf '%s\n' AGENTS.md >"$TMP_ROOT/ordinary-checkout.paths"
  for command in \
    "git checkout HEAD --pathspec-from-file=$TMP_ROOT/protected-checkout.paths" \
    "git checkout 'HEAD^{tree}' --pathspec-from-file=$TMP_ROOT/protected-checkout.paths" \
    "git checkout HEAD --pathspec-from-file $TMP_ROOT/protected-checkout.paths" \
    "git checkout --pathspec-from-file=$TMP_ROOT/protected-checkout.paths HEAD" \
    "git checkout --pathspec-from-file=$TMP_ROOT/protected-checkout.paths" \
    'git checkout --orphan=-scratch --no-orphan -- hooks/validate-bash.sh'; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  done
  for command in \
    "git checkout HEAD --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths" \
    "git checkout HEAD --pathspec-from-file $TMP_ROOT/ordinary-checkout.paths" \
    "git checkout 'HEAD^{tree}' --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths" \
    "git checkout --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths" \
    'git checkout --orphan=-scratch --no-orphan -- AGENTS.md'; do
    printf 'ordinary changed\n' >"$REPO/AGENTS.md"
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || failures=1
      [ "$(cat -- "$REPO/AGENTS.md")" = 'ordinary base' ] || failures=1
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
      git -C "$REPO" diff --cached --quiet || failures=1
    else failures=1; fi
  done
  command="git checkout HEAD AGENTS.md --pathspec-from-file=$TMP_ROOT/protected-checkout.paths"
  if assert_allowed "$command" worker; then
    if (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/explicit-file-conflict.out" 2>&1; then
      printf 'checkout with explicit and file destinations unexpectedly succeeded\n' >&2
      failures=1
    fi
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
    git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
  else failures=1; fi

  # Pathspec-file assignment, cancellation and replacement use final state.
  # Option-looking filenames remain consumed values, including a literal --.
  for filename in --no-pathspec-from-file --detach --; do
    printf '%s\n' hooks/validate-bash.sh >"$REPO/$filename"
  done
  for command in \
    "git checkout --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths --no-pathspec-from-file existing-other-branch" \
    "git checkout --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths --no-pathspec-from-file existing-other-branch --" \
    'git checkout --pathspec-from-file= --no-pathspec-from-file existing-other-branch' \
    "git checkout --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths --pathspec-file-nul --no-pathspec-from-file --no-pathspec-file-nul existing-other-branch" \
    "git checkout --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths --no-pathspec-from-file -- hooks/validate-bash.sh" \
    'git checkout --pathspec-from-file= --no-pathspec-from-file -- hooks/validate-bash.sh' \
    'git checkout --pathspec-from-file -- --no-pathspec-from-file -- hooks/validate-bash.sh' \
    "git checkout --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths --no-pathspec-from-file --pathspec-from-file=$TMP_ROOT/protected-checkout.paths" \
    "git checkout --pathspec-from-file= --no-pathspec-from-file --pathspec-from-file=$TMP_ROOT/protected-checkout.paths" \
    "git checkout --pathspec-from-file -- --no-pathspec-from-file --pathspec-from-file=$TMP_ROOT/protected-checkout.paths" \
    'git checkout --pathspec-from-file --no-pathspec-from-file' \
    'git checkout --pathspec-from-file --detach' \
    'git checkout --pathspec-from-file --'; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  done
  for command in \
    "git checkout --pathspec-from-file=$TMP_ROOT/protected-checkout.paths --no-pathspec-from-file -- AGENTS.md" \
    'git checkout --pathspec-from-file= --no-pathspec-from-file -- AGENTS.md' \
    'git checkout --pathspec-from-file -- --no-pathspec-from-file -- AGENTS.md' \
    "git checkout --pathspec-from-file=$TMP_ROOT/protected-checkout.paths --no-pathspec-from-file --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths" \
    "git checkout --pathspec-from-file= --no-pathspec-from-file --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths" \
    "git checkout --pathspec-from-file -- --no-pathspec-from-file --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths"; do
    printf 'ordinary changed\n' >"$REPO/AGENTS.md"
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || failures=1
      [ "$(cat -- "$REPO/AGENTS.md")" = 'ordinary base' ] || failures=1
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
      git -C "$REPO" diff --cached --quiet || failures=1
    else failures=1; fi
  done
  # NUL mode without a surviving file is native-invalid. A surviving file
  # combined with actual explicit paths is likewise invalid and transparent.
  for command in \
    "git checkout --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths --pathspec-file-nul --no-pathspec-from-file existing-other-branch" \
    "git checkout --pathspec-from-file=$TMP_ROOT/protected-checkout.paths --no-pathspec-from-file --pathspec-from-file=$TMP_ROOT/ordinary-checkout.paths -- hooks/validate-bash.sh"; do
    if assert_allowed "$command" worker; then
      if (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/final-file-invalid.out" 2>&1; then
        printf 'native-invalid final file state unexpectedly succeeded: %s\n' "$command" >&2
        failures=1
      fi
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
      git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
    else failures=1; fi
  done

  # The literal filename -- is a consumed value, not an option boundary.
  # Each row checks registered protection separately from native effects in
  # this disposable runtime, then exercises an admitted ordinary counterpart.
  literal_checkout_pair() {
    local command="$1" format="$2" stage="${3:-}" path mode base second third
    local expected_hook expected_ordinary index_before hook_before native_status row_ok
    if [ -n "$stage" ]; then
      for path in hooks/validate-bash.sh AGENTS.md; do
        base="$(git -C "$REPO" rev-parse "HEAD:$path")"
        if [ "$path" = hooks/validate-bash.sh ]; then
          mode=100755
          second="$base"
          third="$( { git -C "$REPO" show "HEAD:$path"; printf '\n# Fixture stage three\n'; } | git -C "$REPO" hash-object -w --stdin)"
        else
          mode=100644
          second="$(printf 'ordinary stage two\n' | git -C "$REPO" hash-object -w --stdin)"
          third="$(printf 'ordinary stage three\n' | git -C "$REPO" hash-object -w --stdin)"
        fi
        printf '0 %040d\t%s\n%s %s 1\t%s\n%s %s 2\t%s\n%s %s 3\t%s\n' \
          0 "$path" "$mode" "$base" "$path" "$mode" "$second" "$path" "$mode" "$third" "$path" |
          git -C "$REPO" update-index --index-info
      done
      expected_hook="$(git -C "$REPO" rev-parse ":$stage:hooks/validate-bash.sh")"
      expected_ordinary="$(git -C "$REPO" rev-parse ":$stage:AGENTS.md")"
    else
      expected_hook="$protected_base"
      expected_ordinary="$(git -C "$REPO" rev-parse HEAD:AGENTS.md)"
    fi
    index_before="$(git -C "$REPO" ls-files --stage)"
    if [ "$format" = nul ]; then
      printf 'hooks/validate-bash.sh\0' >"$REPO/--"
    else
      printf '%s\n' hooks/validate-bash.sh >"$REPO/--"
    fi
    printf '\n# Fixture owned temporary edit\n' >>"$REPO/hooks/validate-bash.sh"
    row_ok=1
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
      "effect=overwrite target=$REPO/hooks/validate-bash.sh" || { failures=1; row_ok=0; }
    # This deliberate native overwrite is confined to the disposable fixture;
    # it does not execute an admitted production command or touch live source.
    native_status=0
    (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/literal-native.out" 2>&1 || native_status=$?
    [ "$native_status" -eq 0 ] || { cat -- "$TMP_ROOT/literal-native.out" >&2; failures=1; row_ok=0; }
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$expected_hook" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
    if [ "$row_ok" -eq 1 ]; then
      printf 'literal-file protected native_exit=%s restored_hash=%s stage=%s command=%s\n' \
        "$native_status" "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" "${stage:-0}" "$command"
    fi
    hook_before="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
    if [ "$format" = nul ]; then
      printf 'AGENTS.md\0' >"$REPO/--"
    else
      printf '%s\n' AGENTS.md >"$REPO/--"
    fi
    printf 'ordinary changed\n' >"$REPO/AGENTS.md"
    row_ok=1
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" hash-object AGENTS.md)" = "$expected_ordinary" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$hook_before" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" ls-files --stage)" = "$index_before" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || { failures=1; row_ok=0; }
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || { failures=1; row_ok=0; }
      if [ "$row_ok" -eq 1 ]; then
        printf 'literal-file ordinary restored_hash=%s index/head/branch/protected-preserved command=%s\n' \
          "$(git -C "$REPO" hash-object AGENTS.md)" "$command"
      fi
    else failures=1; row_ok=0; fi
    if [ -n "$stage" ]; then
      git -C "$REPO" restore --source=HEAD --staged --worktree -- hooks/validate-bash.sh AGENTS.md
    fi
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
    git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
  }
  for command in \
    'git checkout --pathspec-from-file --' \
    'git checkout --pathspec-from-file -- --no-pathspec-file-nul' \
    'git checkout --pathspec-from-file -- --pathspec-file-nul --no-pathspec-file-nul' \
    'git checkout --pathspec-from-file -- HEAD' \
    'git checkout --pathspec-from-file -- HEAD --' \
    "git checkout --pathspec-from-file -- 'HEAD^{tree}'" \
    'git checkout HEAD --pathspec-from-file --'; do
    literal_checkout_pair "$command" line
  done
  literal_checkout_pair 'git checkout --pathspec-from-file -- --pathspec-file-nul --no-pathspec-file-nul --pathspec-file-nul' nul
  literal_checkout_pair 'git checkout --pathspec-from-file -- -2' line 2
  literal_checkout_pair 'git checkout --pathspec-from-file -- -3' line 3

  # Effective checkout modes are validated by Git before any path restore.
  # Invalid rows remain advisory for both destinations and preserve raw as
  # well as semantic index state in this private runtime.

  for options in \
    '-2 HEAD' \
    "-3 'HEAD^{tree}'" \
    '-2 -f' \
    '-3 --merge' \
    '-2 --conflict=diff3' \
    '--force --merge' \
    '--merge HEAD' \
    '--conflict=diff3 HEAD' \
    '-l' \
    '-l HEAD' \
    '-t' \
    '--track' \
    '--no-track' \
    '--track=direct' \
    '--track=inherit' \
    '--track=bogus' \
    '--track HEAD' \
    '--track=direct HEAD' \
    '--track --no-track' \
    '--no-merge --conflict=diff3 HEAD' \
    '--merge --conflict=diff3 --no-conflict HEAD' \
    '--force --no-force --force -2'; do
    checkout_invalid_mode_pair "$options"
  done
  literal_checkout_pair 'git checkout --pathspec-from-file -- -23' line 3
  literal_checkout_pair 'git checkout --pathspec-from-file -- --theirs --ours' line 2
  literal_checkout_pair 'git checkout --pathspec-from-file -- --ours --theirs' line 3
  literal_checkout_pair 'git checkout --pathspec-from-file -- -q -2' line 2
  literal_checkout_pair 'git checkout --pathspec-from-file -- --force --no-force -2' line 2
  literal_checkout_pair 'git checkout --pathspec-from-file -- --merge --no-merge -2' line 2
  literal_checkout_pair 'git checkout --pathspec-from-file -- --conflict=diff3 --no-conflict -2' line 2
  literal_checkout_pair 'git checkout --pathspec-from-file -- --conflict=diff3 --no-merge HEAD' line

  # Unknown short flags retain the target reader's validity guard; the
  # shared normalizer owns long identity. Native-invalid mode/value/conflict
  # forms remain advisory and preserve the complete tracked state.
  printf '%s\n' hooks/validate-bash.sh >"$REPO/--"
  for command in \
    'git checkout --pathspec-from-file -- --bogus' \
    'git checkout --pathspec-from-file -- -z' \
    'git checkout --pathspec-from-file -- --patch' \
    'git checkout --pathspec-from-file -- --detach=bogus' \
    'git checkout --pathspec-from-file -- --unified=bogus' \
    'git checkout --pathspec-from-file -- --unified=1' \
    'git checkout --pathspec-from-file -- --conflict=bogus' \
    'git checkout --pathspec-from-file -- AGENTS.md' \
    'git checkout --pathspec-from-file -- HEAD -- AGENTS.md'; do
    if assert_allowed "$command" worker; then
      if (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/literal-invalid.out" 2>&1; then
        printf 'literal-file native-invalid control unexpectedly succeeded: %s\n' "$command" >&2
        failures=1
      fi
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
      git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
    else failures=1; fi
  done

  # Git's negative prepass uses reverse candidates. Standard wildcard
  # mappings still permit guessing even with a matching source negative.
  git -C "$REPO" config --add remote.origin.fetch '^refs/heads/wip/*'
  assert_denied_code 'git checkout --guess remote-topic' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  assert_denied_code 'git -c checkout.guess=true checkout remote-topic' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  git -C "$REPO" config --add remote.origin.fetch '^refs/heads/remote-topic'
  assert_denied_code 'git checkout --guess remote-topic' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  # An exact positive contributes its source to the negative prepass, so
  # the matching negative prevents native guessing without changing state.
  git -C "$REPO" config --replace-all remote.origin.fetch 'refs/heads/remote-topic:refs/remotes/origin/remote-topic'
  git -C "$REPO" config --add remote.origin.fetch '^refs/heads/remote-topic'
  command='git checkout --guess remote-topic'
  if assert_allowed "$command" worker; then
    if (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/excluded-remote.out" 2>&1; then
      printf 'checkout guessed an excluded remote branch\n' >&2
      failures=1
    fi
    [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
    git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
  else failures=1; fi
  git -C "$REPO" config --replace-all remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'

  # These operations are checked for denial only, never executed.
  for command in \
    'git restore --recurse-submodules hooks/validate-bash.sh' \
    'git restore --staged --work -- hooks/validate-bash.sh' \
    'git restore --no-worktree --worktree -- hooks/validate-bash.sh' \
    'git checkout --orp topic HEAD' \
    'git checkout --no-orphan --orphan topic HEAD' \
    'git checkout HEAD hooks/validate-bash.sh' \
    'git checkout existing-other-branch --' \
    'git checkout --guess remote-topic' \
    'git checkout --no-guess --guess remote-topic' \
    'git -c checkout.guess=true checkout remote-topic' \
    'git reset -- file.txt' \
    'git commit --amend --no-edit' \
    'git branch --delete existing-other-branch'; do
    assert_denied_code "$command" ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  done

  # Disabling the default worktree effect leaves a Git-invalid restore, not
  # a protected overwrite. The actual error must preserve every tracked state.
  command='git restore --no-worktree -- hooks/validate-bash.sh'
  if assert_allowed "$command" worker; then
    if (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/no-worktree.out" 2>&1; then
      printf 'restore with no destination unexpectedly succeeded\n' >&2
      failures=1
    fi
    rg -Fq "neither '--staged' or '--worktree' is specified" "$TMP_ROOT/no-worktree.out" || failures=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
    [ "$(git -C "$REPO" rev-parse :hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
    [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
    git -C "$REPO" diff --cached --quiet || failures=1
  else failures=1; fi

  # A tracked path sharing a branch name is ambiguous without an explicit
  # separator. Admit Git's own error and prove it has no branch/worktree effect.
  for command in \
    'git checkout hooks/validate-bash.sh AGENTS.md' \
    'git checkout --guess file.txt' \
    'git checkout --no-guess --guess file.txt' \
    'git -c checkout.guess=true checkout file.txt'; do
    if assert_allowed "$command" worker; then
      if (cd -- "$REPO"; bash -c "$command") >"$TMP_ROOT/ambiguous-checkout.out" 2>&1; then
        printf 'ambiguous checkout unexpectedly succeeded: %s\n' "$command" >&2
        failures=1
      fi
      if [ "$command" = 'git checkout hooks/validate-bash.sh AGENTS.md' ]; then
        rg -q 'both revision and filename' "$TMP_ROOT/ambiguous-checkout.out" || failures=1
      else
        rg -q 'could be both a local file and a tracking branch' "$TMP_ROOT/ambiguous-checkout.out" || failures=1
      fi
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
      [ "$(cat -- "$REPO/file.txt")" = base ] || failures=1
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
      git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
    else failures=1; fi
  done

  # A protected-looking source revision is an input. Execute admitted forms
  # and prove the ordinary destination is restored without touching the hook.
  for command in \
    'git restore -s hooks/validate-bash.sh -- AGENTS.md' \
    'git restore -qs hooks/validate-bash.sh -- AGENTS.md' \
    'git restore -Wqs hooks/validate-bash.sh -- AGENTS.md' \
    'git restore -qshooks/validate-bash.sh -- AGENTS.md' \
    'git checkout hooks/validate-bash.sh -- AGENTS.md' \
    'git checkout --orphan topic --no-orphan -- AGENTS.md'; do
    printf 'ordinary changed\n' >"$REPO/AGENTS.md"
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || failures=1
      [ "$(cat -- "$REPO/AGENTS.md")" = 'ordinary base' ] || failures=1
      [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
      [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1
    else
      failures=1
    fi
  done

  git -C "$REPO" config checkout.guess false
  assert_denied_code 'git checkout --guess remote-topic' ECI_WORKER_GIT_OWNERSHIP_DENIED worker || failures=1
  for command in \
    'git checkout file.txt' \
    'git checkout --no-guess file.txt' \
    'git checkout --guess --no-guess file.txt' \
    'git -c checkout.guess=false checkout file.txt' \
    'git -c checkout.guess=true checkout --no-guess file.txt'; do
    printf 'changed\n' >"$REPO/file.txt"
    if assert_allowed "$command" worker; then
      (cd -- "$REPO"; bash -c "$command") || failures=1
      [ "$(cat -- "$REPO/file.txt")" = base ] || failures=1
      [ "$(git -C "$REPO" symbolic-ref HEAD)" = "$before_branch" ] || failures=1
    else
      failures=1
    fi
  done
  [ "$(git -C "$REPO" rev-parse HEAD)" = "$before_head" ] || failures=1

  # Normal owned staging, index-only restore, and checkpoint commit remain
  # executable. Index-only restore must preserve the edited protected file.
  printf '\n# Owned fixture checkpoint\n' >>"$REPO/hooks/validate-bash.sh"
  protected_changed="$(git -C "$REPO" hash-object hooks/validate-bash.sh)"
  if assert_allowed 'git add -- hooks/validate-bash.sh' worker; then
    git -C "$REPO" add -- hooks/validate-bash.sh
    [ "$(git -C "$REPO" rev-parse :hooks/validate-bash.sh)" = "$protected_changed" ] || failures=1
  else failures=1; fi
  if assert_allowed 'git restore --staged -- hooks/validate-bash.sh' worker; then
    git -C "$REPO" restore --staged -- hooks/validate-bash.sh
    [ "$(git -C "$REPO" rev-parse :hooks/validate-bash.sh)" = "$protected_base" ] || failures=1
    [ "$(git -C "$REPO" hash-object hooks/validate-bash.sh)" = "$protected_changed" ] || failures=1
  else failures=1; fi
  if assert_allowed 'git add -- hooks/validate-bash.sh' worker &&
     assert_allowed "git commit -qm 'owned checkpoint'" worker; then
    git -C "$REPO" add -- hooks/validate-bash.sh
    git -C "$REPO" commit -qm 'owned checkpoint'
    [ "$(git -C "$REPO" rev-parse HEAD:hooks/validate-bash.sh)" = "$protected_changed" ] || failures=1
    git -C "$REPO" diff --quiet && git -C "$REPO" diff --cached --quiet || failures=1
  else failures=1; fi
  [ "$failures" -eq 0 ]
}

case "${NORMAL_GIT_ADMISSION_TARGET:-full}" in
  git-run-once)
    for role in coordinator worker; do
      denial=ECI_BROAD_DESTRUCTIVE_DENIED
      [ "$role" != worker ] || denial=ECI_WORKER_GIT_OWNERSHIP_DENIED
      assert_denied_code "git -C $REPO reset --hard" "$denial" "$role"
      command="\"$RUNTIME_ROOT/bin/eci-worker-git\" --repo \"$REPO\" run-once --reason 'user approved fixture reset' --user-authorized -- reset --hard"
      assert_allowed "$command" "$role"
      assert_denied_code "$command; git -C $REPO reset --hard" "$denial" "$role"
      assert_denied_code "$command; rm $FOREIGN_TIMEOUT_CONTROL_INNER/eci_active" ECI_PLAN_LIVE_CONTROL_DENIED "$role"
      printf 'modified\n' >"$REPO/file.txt"
      HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
        "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" run-once --reason 'user approved fixture reset' --user-authorized -- reset --hard
      [ "$(cat -- "$REPO/file.txt")" = base ]
      assert_denied_code "git -C $REPO reset --hard" "$denial" "$role"
    done
    printf 'unchanged\n' >"$REPO/file.txt"
    if HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" run-once --reason '' --user-authorized -- reset --hard; then exit 1; fi
    if HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" run-once --reason 'fixture' -- reset --hard; then exit 1; fi
    [ "$(cat -- "$REPO/file.txt")" = unchanged ]
    status=0
    HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
      "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" run-once --reason 'approved fixture exit check' --user-authorized -- -c 'alias.failure=!exit 37' failure || status=$?
    [ "$status" -eq 37 ]
    jq -se 'length == 5 and all(.[]; .route == "run-once")' "$HOME_ROOT/.cache/eci/eci-worker-git.jsonl" >/dev/null
    printf '%s\n' 'normal Git admission run-once target: PASS'
    exit 0
    ;;
  full|worker-git-cli-matrix|worker-git-cli-edges|worker-git-native|worker-git-index-aliases)
    run_effect_aware_git_target
    printf '%s\n' 'normal Git admission current worker contract: PASS'
    exit 0
    ;;
  historical-native-full)
    [ -n "${NORMAL_GIT_ADMISSION_HOOK_SOURCE:-}" ] || {
      printf '%s\n' 'historical-native-full requires explicit NORMAL_GIT_ADMISSION_HOOK_SOURCE from the historical revision' >&2
      exit 64
    }
    printf '%s\n' 'historical native differential evidence; not current worker admission policy'
    ;;
  effect-aware-git|checkout-option-validity|checkout-branch-source-validity|checkout-completed-selection|checkout-native-selection|checkout-context-validity|checkout-context-prefix|worker-git-cli-slice)
    if [ "${NORMAL_GIT_ADMISSION_TARGET}" != worker-git-cli-slice ]; then
      [ -n "${NORMAL_GIT_ADMISSION_HOOK_SOURCE:-}" ] || {
        printf '%s\n' 'historical native component requires explicit NORMAL_GIT_ADMISSION_HOOK_SOURCE; current worker proof is full or worker-git-cli-matrix' >&2
        exit 64
      }
      printf '%s\n' 'historical native differential component; not current worker admission policy'
    fi
    run_effect_aware_git_target
    if [ "${NORMAL_GIT_ADMISSION_TARGET}" = checkout-branch-source-validity ]; then
      printf '%s\n' 'normal Git admission explicit branch source target: PASS'
    else
      printf '%s\n' 'normal Git admission effect-aware-git target: PASS'
    fi
    exit 0
    ;;
  compound-handoff-boundary)
    run_compound_handoff_boundary_target
    printf '%s\n' 'normal Git admission compound-handoff-boundary target: PASS'
    exit 0
    ;;
  foreign-timeout-marker)
    run_foreign_timeout_marker_target
    printf '%s\n' 'normal Git admission foreign-timeout-marker target: PASS'
    exit 0
    ;;
  *)
    printf 'unknown normal Git admission target: %s\n' "${NORMAL_GIT_ADMISSION_TARGET}" >&2
    exit 64
    ;;
esac

# Producers' normal commits need no approval artifact or review receipt.
assert_allowed "git commit -qm --all" worker

# Command spelling, `-C`, environment setup, and punctuation are not an
# accidental mistake by themselves.  The target remains this active repo.
assert_denied_code "git -C $REPO commit -am 'ordinary commit'" ECI_BROAD_DESTRUCTIVE_DENIED worker
assert_allowed "env GIT_EDITOR=true /usr/bin/git -C $REPO commit --allow-empty -m 'ordinary commit'" worker
assert_denied_code "printf prepare && git -C $REPO commit -m 'ordinary commit' -- file.txt" ECI_GIT_COMMIT_STAGING_DENIED worker
[ ! -e "$REPO/.git-commit-approved-once" ]
[ ! -e "$PROOF_ROOT/$SESSION/eci-required-critics.json" ]
[ ! -e "$PROOF_ROOT/$SESSION/eci-commit-admitted" ]

# Targeted index changes are ordinary repository work.
assert_allowed "git -C $REPO add -- file.txt"
assert_allowed "git -C $REPO restore --staged -- file.txt"
assert_allowed "git -C $REPO reset -- file.txt"

# A worker's local index may stage explicit same-repository paths. The private
# fixture removes the live temporary bypass above, so these exercise the real
# planner and target resolver rather than an early exit.
assert_allowed "git add -- hooks.json" worker
assert_allowed "git add README.md" worker

# Worker native Git preserves inspection options and repository context.
run_worker_git_inspection() {
  local command="$1"
  assert_allowed "$command" worker
}
for worker_git_read in \
  "git status --short" \
  "git --no-pager status --short" \
  "git -c color.ui=false status --short" \
  "git --git-dir=.git status --short" \
  "git -C $REPO status --short" \
  "env -- git -C $REPO status --short" \
  "command git -C $REPO status --short" \
  "git -C $REPO log -1 --oneline" \
  "git -C $REPO diff --stat" \
  "git -C $REPO rev-parse --show-toplevel" \
  "git branch --list 'main*'" \
  "git branch --contains HEAD" \
  "systemd-run --working-directory=. --setenv=GIT_DIR=.git --setenv=GIT_WORK_TREE=. git status --short" \
  "systemd-run --working-directory $REPO --setenv GIT_DIR=.git --setenv GIT_WORK_TREE=. git log -1 --oneline"; do
  run_worker_git_inspection "$worker_git_read"
done
assert_allowed "git -C $FOREIGN_REPO status --short" worker
assert_allowed "systemd-run --working-directory=$FOREIGN_REPO git status --short" worker
assert_allowed "systemd-run --working-directory=$REPO --setenv=GIT_WORK_TREE=$FOREIGN_REPO git status --short" worker
assert_allowed "git -C $REPO diff --output=$TMP_ROOT/git-inspection.out" worker

# A timeout-wrapped Git child is exposed only after the exact callback-PATH
# timeout executable launches the planner's harmless replacement child. The
# launch fixture therefore keeps normal named-path work ordinary and preserves
# the resolved foreign and broad target boundaries for both roles.
CALLBACK_PATH="$FAKE_TIMEOUT_LAUNCH_DIR:$BASE_CALLBACK_PATH"
for role in coordinator worker; do
  assert_allowed "timeout --signal TERM 5 git add -- hooks.json" "$role"
  assert_denied_code "timeout --signal TERM 5 git -C $FOREIGN_REPO add -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED "$role" \
    "active_repo=$REPO target_repo=$FOREIGN_REPO"
  assert_denied_code "timeout --signal TERM 5 git add ." ECI_BROAD_DESTRUCTIVE_DENIED "$role" \
    "effect=whole-worktree-staging target=$REPO selector=."
  assert_denied_code "timeout --signal TERM 5 env git -C $FOREIGN_REPO add -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED "$role" \
    "active_repo=$REPO target_repo=$FOREIGN_REPO"
  assert_denied_code "timeout --signal TERM 5 env git add ." ECI_BROAD_DESTRUCTIVE_DENIED "$role" \
    "effect=whole-worktree-staging target=$REPO selector=."
  assert_allowed "timeout --signal '\$TIMEOUT_SIGNAL' 5 git add ." "$role"
done
# Conditional prefixes poison replay state: the direct timeout remains
# ordinary instead of borrowing an observation from the callback context.
assert_allowed "printf prepare && timeout --signal TERM 5 git add ." worker
# Timeout does not turn history acceptance into local index work after a
# positive launch observation.
assert_denied_code "timeout --signal TERM 5 git rebase topic" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
  "token=rebase"

# State-only semicolon prefixes carry the actual shell CWD and PATH into the
# observed timeout record. The copied launcher must preserve the outer callback
# CWD for active-scope checks while Git resolves its child from this state.
STATE_CHILD="$REPO/timeout-state-child"
mkdir -p -- "$STATE_CHILD"
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$STATE_CHILD/timeout"
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$FOREIGN_REPO/timeout"
chmod 755 -- "$STATE_CHILD/timeout" "$FOREIGN_REPO/timeout"
CALLBACK_PATH="$FAKE_TIMEOUT_ACCEPT_DIR:$BASE_CALLBACK_PATH"
for role in coordinator worker; do
  assert_denied_code "cd $STATE_CHILD; ./timeout --signal TERM 5 git add :/" ECI_BROAD_DESTRUCTIVE_DENIED "$role" \
    "effect=whole-worktree-staging target=$REPO selector=:/"
  assert_denied_code "cd $STATE_CHILD; cd $FOREIGN_REPO; ./timeout --signal TERM 5 git add ." ECI_GIT_CROSS_SCOPE_DENIED "$role" \
    "active_repo=$REPO target_repo=$FOREIGN_REPO"
done
# A later absolute cd back to the outer repository is also modeled, not a
# stale reuse of the first transition's directory.
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$REPO/timeout"
chmod 755 -- "$REPO/timeout"
for role in coordinator worker; do
  assert_denied_code "cd $STATE_CHILD; cd $REPO; ./timeout --signal TERM 5 git add ." ECI_BROAD_DESTRUCTIVE_DENIED "$role" \
    "effect=whole-worktree-staging target=$REPO selector=."
done

# An observed timeout child resolves its relative writer target from the
# semicolon-modeled CWD. The real inner control file remains denied, while an
# outer hardlink to that control file does not falsely deny an inner ordinary
# eci_active name during recursive segment validation.
TIMEOUT_CONTROL_INNER="$PROOF_ROOT/$SESSION"
TIMEOUT_ORDINARY_INNER="$TMP_ROOT/timeout-ordinary-inner"
OUTER_CONTROL_ALIAS="$REPO/eci_active"
mkdir -p -- "$TIMEOUT_ORDINARY_INNER"
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$TIMEOUT_CONTROL_INNER/timeout"
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$TIMEOUT_ORDINARY_INNER/timeout"
printf '%s\n' ordinary >"$TIMEOUT_ORDINARY_INNER/eci_active"
chmod 755 -- "$TIMEOUT_CONTROL_INNER/timeout" "$TIMEOUT_ORDINARY_INNER/timeout"
TIMEOUT_CONTROL_MARKER="$(realpath -e -- "$TIMEOUT_CONTROL_INNER/eci_active")"
CALLBACK_PATH="$FAKE_TIMEOUT_ACCEPT_DIR:$BASE_CALLBACK_PATH"
assert_denied_code "cd $TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" ECI_PLAN_LIVE_CONTROL_DENIED worker \
  "path=$TIMEOUT_CONTROL_MARKER"
ln -- "$TIMEOUT_CONTROL_INNER/eci_active" "$OUTER_CONTROL_ALIAS"
assert_allowed "cd $TIMEOUT_ORDINARY_INNER; ./timeout 5 rm eci_active" worker configured absent "$REPO"
# Literal branch reachability is carried into the recursive segment route:
# a live inline writer remains denied, while the dead split-body writer is
# retained structurally but skipped by both planner and adapter replay.
assert_denied_code "if true; then; rm $TIMEOUT_CONTROL_MARKER; fi" ECI_PLAN_LIVE_CONTROL_DENIED worker \
  "worker-proof-control"
assert_allowed "if false; then; rm $TIMEOUT_CONTROL_MARKER; fi" worker configured absent "$REPO"
# Spoofed inherited replay/CWD variables must not replace the planner-owned
# ordinary-inner fact or redirect the child back to the callback repository.
ECI_COMPOUND_SEGMENT_VALIDATION=true ECI_COMPOUND_CWD="$REPO" \
ECI_COMPOUND_COMMAND_PATH="$REPO" \
  assert_allowed "cd $TIMEOUT_ORDINARY_INNER; ./timeout 5 rm eci_active" worker configured absent "$REPO"
# Top-level JSON metadata is callback input, not planner provenance. Even a
# shape-valid forged segment fact must not move direct ownership resolution to
# the ordinary inner directory without the parent-only handoff channel.
assert_denied_code "rm eci_active" ECI_PLAN_LIVE_CONTROL_DENIED worker \
  "path=$TIMEOUT_CONTROL_INNER/eci_active" configured absent "$REPO" "[]" true false "$TIMEOUT_ORDINARY_INNER"
rm -- "$OUTER_CONTROL_ALIAS"

# An unsupported cd form leaves the segment CWD unknown. Harmless paths remain
# ordinary, while the unresolved control basename takes the targeted worker
# ownership route instead of borrowing the callback CWD.
assert_denied_code "cd -P $TIMEOUT_ORDINARY_INNER; ./timeout 5 rm eci_active" ECI_CONTROL_OWNER_REQUIRED worker \
  "resolved=<unknown-cwd>"
# Conditional, background, and harmless unknown-CWD segments remain ordinary;
# they cannot inherit a stale callback directory for control resolution.
assert_allowed "cd $TIMEOUT_ORDINARY_INNER || printf ordinary" worker configured absent "$REPO"
assert_allowed "false && cd $TIMEOUT_ORDINARY_INNER; printf ordinary" worker configured absent "$REPO"
assert_allowed "printf ordinary & printf still-ordinary" worker configured absent "$REPO"
assert_allowed "cd -P $TIMEOUT_ORDINARY_INNER; printf ordinary" worker configured absent "$REPO"

# A planner-observed timeout child runs from its replay CWD.  That must catch
# a foreign active marker for either role without changing the outer callback
# scope anchor.  A nonlaunching timeout and invalid replay facts remain
# ordinary because they do not establish a child write.
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$FOREIGN_TIMEOUT_CONTROL_INNER/timeout"
chmod 755 -- "$FOREIGN_TIMEOUT_CONTROL_INNER/timeout"
for role in coordinator worker; do
  assert_denied_code "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
    "foreign_session=$FOREIGN_SESSION"
done
cp -- "$FAKE_TIMEOUT_ACCEPT_DIR/timeout" "$FOREIGN_TIMEOUT_CONTROL_INNER/timeout"
chmod 755 -- "$FOREIGN_TIMEOUT_CONTROL_INNER/timeout"
for role in coordinator worker; do
  assert_allowed "cd $FOREIGN_TIMEOUT_CONTROL_INNER; ./timeout 5 rm eci_active" "$role"
done

FOREIGN_TIMEOUT_OBSERVED_REPLAYS="$(jq -cn --arg cwd "$FOREIGN_TIMEOUT_CONTROL_INNER" '
  [{segment:1,parent_segment:2,prefix:["./timeout","5"],cwd:$cwd,command_path:"",command_path_set:false,command_path_exported:false,disposition:"observed"}]
')"
FOREIGN_TIMEOUT_OPAQUE_REPLAYS="$(jq -c '.[0].disposition = "opaque" | .' <<<"$FOREIGN_TIMEOUT_OBSERVED_REPLAYS")"
FOREIGN_TIMEOUT_DUPLICATE_REPLAYS="$(jq -c '[.[0], .[0]]' <<<"$FOREIGN_TIMEOUT_OBSERVED_REPLAYS")"
FOREIGN_TIMEOUT_PREFIX_MISMATCH_REPLAYS="$(jq -c '.[0].prefix = ["./timeout", "6"] | .' <<<"$FOREIGN_TIMEOUT_OBSERVED_REPLAYS")"
for role in coordinator worker; do
  assert_denied_code "./timeout 5 rm eci_active" ECI_CROSS_SESSION_ACTIVE_MARKER_DENIED "$role" \
    "foreign_session=$FOREIGN_SESSION" configured absent "$REPO" "$FOREIGN_TIMEOUT_OBSERVED_REPLAYS" true
  assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" '[]' true
  assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" "$FOREIGN_TIMEOUT_OPAQUE_REPLAYS" true
  assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" "$FOREIGN_TIMEOUT_DUPLICATE_REPLAYS" true
  assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" '[{"segment":1}]' true
  assert_allowed "./timeout 5 rm eci_active" "$role" configured absent "$REPO" "$FOREIGN_TIMEOUT_PREFIX_MISMATCH_REPLAYS" true
done

# A recursive callback consumes only planner-shaped replay records. A scalar
# replay entry is advisory metadata, so an otherwise ordinary compound stays
# ordinary instead of becoming an internal planner denial.
assert_allowed 'printf ordinary; printf still-ordinary' coordinator configured absent "$REPO" '[1]' true

# Recursive validation must not source an inherited BASH_ENV. The poison file
# records each shell that sources it and exports the historical compound flag;
# the parent callback contributes one record, while the child must contribute
# none because the recursive adapter removes BASH_ENV and ECI_COMPOUND_*.
BASH_ENV_SENTINEL="$TMP_ROOT/compound-bash-env.sourced"
BASH_ENV_POISON="$TMP_ROOT/compound-bash-env.sh"
printf 'printf "%%s\\n" "\$BASHPID" >> %q\nexport ECI_COMPOUND_SEGMENT_VALIDATION=true\n' \
  "$BASH_ENV_SENTINEL" >"$BASH_ENV_POISON"
BASH_ENV="$BASH_ENV_POISON" assert_allowed 'printf ordinary; printf still-ordinary' coordinator configured absent "$REPO"
[ "$(wc -l <"$BASH_ENV_SENTINEL")" -eq 1 ] || {
  printf 'recursive validation sourced inherited BASH_ENV: %s\n' "$BASH_ENV_SENTINEL" >&2
  exit 1
}

# PATH state is likewise literal and ordered: assignments, export changes,
# set-empty, unset, and a known nonlaunch cannot borrow the callback PATH.
for role in coordinator worker; do
  CALLBACK_PATH="$FAKE_TIMEOUT_ACCEPT_DIR:$BASE_CALLBACK_PATH"
  assert_denied_code "PATH=$FAKE_TIMEOUT_LAUNCH_DIR; timeout --signal TERM 5 git add ." ECI_BROAD_DESTRUCTIVE_DENIED "$role" \
    "effect=whole-worktree-staging target=$REPO selector=."
  assert_denied_code "export PATH=$FAKE_TIMEOUT_LAUNCH_DIR; timeout --signal TERM 5 git add ." ECI_BROAD_DESTRUCTIVE_DENIED "$role" \
    "effect=whole-worktree-staging target=$REPO selector=."
  assert_denied_code "PATH=$FAKE_TIMEOUT_LAUNCH_DIR; export -n PATH; timeout --signal TERM 5 git add ." ECI_BROAD_DESTRUCTIVE_DENIED "$role" \
    "effect=whole-worktree-staging target=$REPO selector=."
  CALLBACK_PATH="$FAKE_TIMEOUT_LAUNCH_DIR:$BASE_CALLBACK_PATH"
  assert_allowed "PATH=; timeout --signal TERM 5 git add ." "$role"
  assert_allowed "unset PATH; timeout --signal TERM 5 git add ." "$role"
  assert_allowed "PATH=$FAKE_TIMEOUT_ACCEPT_DIR; timeout --signal TERM 5 git add ." "$role"
  assert_allowed "PATH=$FAKE_TIMEOUT_ACCEPT_DIR && timeout --signal TERM 5 git add ." "$role"
  assert_allowed 'PATH=$TIMEOUT_PATH; timeout --signal TERM 5 git add .' "$role"
  assert_allowed "printf harmless; timeout --signal TERM 5 git add ." "$role"
done
CALLBACK_PATH="$FAKE_TIMEOUT_LAUNCH_DIR:$BASE_CALLBACK_PATH"

# The probe keeps inherited callback variables other than its explicitly bound
# PWD and PATH. A fake timeout requiring this variable distinguishes an actual
# child launch from an executable that merely accepts the timeout prefix.
CALLBACK_PATH="$FAKE_TIMEOUT_REQUIRED_DIR:$BASE_CALLBACK_PATH"
assert_denied_code "timeout --signal TERM 5 git rebase topic" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
  "token=rebase" configured present
assert_allowed "timeout --signal TERM 5 git rebase topic" worker configured absent

# Shell PATH lookup retains callback-CWD empty and relative components. The
# test invokes the copied hook through /bin/bash so the raw callback PATH is
# not consumed by the harness before the hook captures it.
mkdir -p -- "$REPO/bin"
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$REPO/timeout"
cp -- "$FAKE_TIMEOUT_LAUNCH_DIR/timeout" "$REPO/bin/timeout"
chmod 755 -- "$REPO/timeout" "$REPO/bin/timeout"
for CALLBACK_PATH in "$TMP_ROOT/missing:bin" : . bin; do
  assert_denied_code "timeout --signal TERM 5 git rebase topic" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
    "token=rebase" raw
done

# Explicitly empty PATH differs from an empty component: bare timeout remains
# opaque when PATH is empty or unset, while a direct absolute or ./timeout
# spelling launches because the replacement child is an absolute executable.
assert_allowed "timeout --signal TERM 5 git rebase topic" worker empty
assert_allowed "timeout --signal TERM 5 git rebase topic" worker unset
assert_denied_code "$FAKE_TIMEOUT_LAUNCH_DIR/timeout --signal TERM 5 git rebase topic" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
  "token=rebase" empty
assert_denied_code "$FAKE_TIMEOUT_LAUNCH_DIR/timeout --signal TERM 5 git rebase topic" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
  "token=rebase" unset
assert_denied_code "./timeout --signal TERM 5 git rebase topic" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
  "token=rebase" empty
assert_denied_code "./timeout --signal TERM 5 git rebase topic" ECI_WORKER_GIT_OWNERSHIP_DENIED worker \
  "token=rebase" unset

# An executable that accepts the same prefix but does not start its child must
# leave foreign and broad Git forms ordinary. This is an E2E A/B check against
# the launching executable above, not a timeout option-name table.
CALLBACK_PATH="$FAKE_TIMEOUT_ACCEPT_DIR:$BASE_CALLBACK_PATH"
for role in coordinator worker; do
  assert_allowed "timeout --signal TERM 5 git -C $FOREIGN_REPO add -- file.txt" "$role"
  assert_allowed "timeout --signal TERM 5 git add ." "$role"
  assert_allowed "timeout --signal TERM 5 env git -C $FOREIGN_REPO add -- file.txt" "$role"
  assert_allowed "timeout --signal TERM 5 env git add ." "$role"
  assert_allowed "timeout --signal 0 5 git add ." "$role"
done
# A nonlaunching timeout is opaque to each worker recognizer. It must not
# reconstruct a direct Git child or a piped branch mutation from argv alone.
assert_allowed "timeout --signal TERM 5 git rebase topic" worker
assert_allowed "timeout --signal TERM 5 git merge topic" worker
assert_allowed "printf prepare | timeout --signal TERM 5 git branch --delete topic" worker

# A fake timeout that rejects its signal and a structurally malformed prefix
# both have no observed child launch. They remain ordinary runtime behavior.
CALLBACK_PATH="$FAKE_TIMEOUT_INVALID_DIR:$BASE_CALLBACK_PATH"
for role in coordinator worker; do
  assert_allowed "timeout --signal invalid-signal 5 git add ." "$role"
done
CALLBACK_PATH="$FAKE_TIMEOUT_LAUNCH_DIR:$BASE_CALLBACK_PATH"
assert_allowed "timeout not-a-duration git add ." worker
assert_allowed "timeout --not-a-timeout-option 5 git add ." worker
assert_allowed "chronic git add ." worker
CALLBACK_PATH="$BASE_CALLBACK_PATH"

# Preserve only resolved accidental-risk boundaries.
assert_denied_code "git -C $REPO reset --hard" ECI_BROAD_DESTRUCTIVE_DENIED
assert_denied_code "env GIT_EDITOR=true /usr/bin/git -C $REPO reset --hard" ECI_BROAD_DESTRUCTIVE_DENIED
assert_denied_code "git -C $FOREIGN_REPO add -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED
# The same resolved foreign target stays cross-scope through ordinary wrapper
# and sequencing spellings. Punctuation is not the boundary; the target is.
assert_denied_code "env GIT_EDITOR=true /usr/bin/git -C $FOREIGN_REPO add -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED
assert_denied_code "printf prepare && git -C $FOREIGN_REPO add -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED
# A target that does not resolve to a repository has no known cross-scope
# mutation. Let Git report its ordinary runtime error instead of manufacturing
# an ECI denial from incomplete target information.
assert_allowed "git -C $NON_REPO add -- file.txt"

# The worker keeps the same concrete target/effect boundary: foreign staging,
# a known whole-worktree selector, and a working-tree reset remain denied.
assert_denied_code "git -C $FOREIGN_REPO add -- file.txt" ECI_GIT_CROSS_SCOPE_DENIED worker \
  "active_repo=$REPO target_repo=$FOREIGN_REPO"
assert_denied_code "git add ." ECI_BROAD_DESTRUCTIVE_DENIED worker \
  "effect=whole-worktree-staging target=$REPO"
assert_denied_code "git reset --hard" ECI_BROAD_DESTRUCTIVE_DENIED worker \
  "effect=reset-working-tree target=$REPO"

run_effect_aware_git_target
printf '%s\n' 'normal Git admission: PASS'
