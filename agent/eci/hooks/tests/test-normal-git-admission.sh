#!/usr/bin/env bash

set -Eeuo pipefail

SOURCE_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$SOURCE_ROOT/hooks/tests/require-go.sh"
. "$SOURCE_ROOT/hooks/tests/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-normal-git-admission.XXXXXX")"
TMP_ROOT="$(eci_realpath_existing "$TMP_ROOT")"
trap 'chmod -R -- u+w "$TMP_ROOT"; rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM
trap 'printf "fixture failed: target=%s line=%s\n" "${NORMAL_GIT_ADMISSION_TARGET:-full}" "$LINENO" >&2' ERR

# Exercise a complete private runtime, removing only the exact development
# bridge when present. Never change the live runtime.
HOME_ROOT="$TMP_ROOT/home"
RUNTIME_ROOT="$HOME_ROOT/.omp/agent/eci"
mkdir -p -- "$RUNTIME_ROOT" "$TMP_ROOT/config/eci" "$TMP_ROOT/state"
cp -a -- "$SOURCE_ROOT/hooks" "$RUNTIME_ROOT"
cp -- "$SOURCE_ROOT/hooks.json" "$RUNTIME_ROOT/hooks.json"
mkdir -p -- "$RUNTIME_ROOT/bin"
cp -- "$SOURCE_ROOT/bin/eci-runtime-sync" "$RUNTIME_ROOT/bin/eci-runtime-sync"
# Refresh only this private fixture's planner receipt. This keeps the test on
# the real hook path while avoiding a source build for every Git probe.
PLANNER_DIR="$RUNTIME_ROOT/hooks/lib/eci-command-plan-go"
(
  cd -- "$PLANNER_DIR"
  "$ECI_TEST_GO" build -trimpath -buildvcs=false -o eci-command-plan .
)
chmod 755 -- "$PLANNER_DIR/eci-command-plan"
planner_go_mod_sha="$(eci_sha256_hex "$PLANNER_DIR/go.mod")"
planner_main_sha="$(eci_sha256_hex "$PLANNER_DIR/main.go")"
planner_classifier_sha="$(eci_sha256_hex "$PLANNER_DIR/classifier.go")"
planner_binary_sha="$(eci_sha256_hex "$PLANNER_DIR/eci-command-plan")"
planner_binary_size="$(eci_stat size "$(eci_realpath_existing "$PLANNER_DIR/eci-command-plan")")"
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

# Publish and receipt the planner pair that the build call verifies in this bin/, and the
# verifier eci-verexec that runs the planner: eci-command-plan is the binary built above;
# eci-safe-import and eci-verexec are built from their modules.
cp -- "$PLANNER_DIR/eci-command-plan" "$RUNTIME_ROOT/bin/eci-command-plan"
(
  cd -- "$RUNTIME_ROOT/hooks/lib/eci-safe-import-go"
  "$ECI_TEST_GO" build -trimpath -buildvcs=false -o "$RUNTIME_ROOT/bin/eci-safe-import" .
)
(
  cd -- "$RUNTIME_ROOT/hooks/lib/eci-verexec-go"
  CGO_ENABLED=0 "$ECI_TEST_GO" build -trimpath -buildvcs=false -o "$RUNTIME_ROOT/bin/eci-verexec" .
)
for planner_tool in eci-command-plan eci-safe-import eci-verexec; do
  printf '%s  %s\n' "$(eci_sha256_hex "$RUNTIME_ROOT/bin/$planner_tool")" "$planner_tool" >"$RUNTIME_ROOT/bin/$planner_tool.sha256"
done

# Build the disposable runtime from its copied sources. Clean archives contain
# neither ignored executables nor optional planner provenance.
source "$RUNTIME_ROOT/hooks/lib/eci-runtime-sync.sh"
HOME="$HOME_ROOT" eci_runtime_build_missing "$RUNTIME_ROOT" \
  eci-command-gate-mode eci-command-plan eci-safe-import eci-worker-git
source_hook_sha="$(eci_sha256_hex "$RUNTIME_ROOT/hooks/validate-bash.sh")"
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
exercised_hook_sha="$(eci_sha256_hex "$RUNTIME_ROOT/hooks/validate-bash.sh")"
printf 'registered_hook source_sha256=%s exercised_sha256=%s\n' "$source_hook_sha" "$exercised_hook_sha"
BASH_LAUNCHER="$(jq -er '.hooks.PreToolUse[] | select(.matcher == "^Bash$") | .hooks[] | select(.type == "command") | .command' "$RUNTIME_ROOT/hooks.json")"
[ "$BASH_LAUNCHER" = 'bash "$HOME/.omp/agent/eci/hooks/validate-bash.sh"' ] || {
  printf 'unexpected copied Bash launcher: %s\n' "$BASH_LAUNCHER" >&2
  exit 1
}

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
REPO="$(eci_realpath_existing "$REPO")"
FOREIGN_REPO="$(eci_realpath_existing "$FOREIGN_REPO")"
mkdir -p -- "$NON_REPO"
NON_REPO="$(eci_realpath_existing "$NON_REPO")"

SESSION='normal-git-session'
PROOF_ROOT="$TMP_ROOT/proof"
mkdir -p -- "$PROOF_ROOT/$SESSION"
printf '%s\n' \
  'scope: normal Git admission regression' \
  "cwd: $REPO" \
  "session_id: $SESSION" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$PROOF_ROOT/$SESSION/eci_active"
MAIN_TRANSCRIPT="$HOME_ROOT/.omp/agent/sessions/normal-git/main.jsonl"
mkdir -p -- "$(dirname -- "$MAIN_TRANSCRIPT")"
printf '%s\n' \
  "{\"type\":\"session\",\"version\":3,\"id\":\"$SESSION\",\"cwd\":\"$REPO\"}" \
  '{"type":"message","role":"user","content":"normal git"}' >"$MAIN_TRANSCRIPT"

KIMI_PROOF_ROOT_RAW="$TMP_ROOT/kimi-proof"
mkdir -p -- "$KIMI_PROOF_ROOT_RAW"
KIMI_PROOF_ROOT="$(eci_realpath_existing "$KIMI_PROOF_ROOT_RAW")"
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
  compound_physical="$(eci_realpath_existing "$compound_cwd" 2>/dev/null || true)"
  local compound_handoff_token=normal-git-test-handoff
  local fd9_mode="${10:-none}" fd9_token="${11:-$compound_handoff_token}"
  local subagent=false
  local transcript="${CALLBACK_TRANSCRIPT-}"
  [ -n "${CALLBACK_TRANSCRIPT+x}" ] || [ "$role" != coordinator ] || transcript="$MAIN_TRANSCRIPT"
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
    --arg transcript "$transcript" \
    --arg command_path "$CALLBACK_PATH" \
    --argjson timeout_replays "$timeout_replays" \
    --argjson compound_validation "$compound_replay" \
    '({session_id:$session,tool_name:"Bash",cwd:$cwd,timeout_replays:$timeout_replays,tool_input:{command:$command}} + (if $transcript == "" then {} else {transcript_path:$transcript} end)) |
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
  printf '%s\n' 'scope: malformed Kimi marker' >"$KIMI_FOREIGN_MARKER"
  assert_allowed "rm $KIMI_FOREIGN_MARKER" worker
  printf '%s\n' \
    "cwd: $FOREIGN_REPO" \
    "session_id: $KIMI_FOREIGN_SESSION" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$KIMI_FOREIGN_MARKER"
  assert_allowed "rm $KIMI_FOREIGN_MARKER" worker

  # A foreign marker pathname without a recognized live record is not itself
  # a live-control effect. Keep the exact proof/session spelling visible to
  # the adapter so the generic reserved-name route cannot blanket-deny these
  # missing, malformed and mismatched observations.
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

  local replay
  replay="$(jq -cn --arg cwd "$inner" '[{segment:1,parent_segment:2,prefix:["./timeout","5"],cwd:$cwd,command_path:"",command_path_set:false,command_path_exported:false,disposition:"observed"}]')"
  output="$(run_hook "./timeout 5 rm eci_active" worker configured absent "$REPO" "$replay" true true "$inner")"
  [ ! -s "$output" ] || {
    printf 'valid compound handoff was denied: %s\n' "$(cat -- "$output")" >&2
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

assert_worker_git_route_denied() {
  local command="$1" token="$2" output

  output="$(run_hook "$command" worker configured absent "$REPO" '[]' false true "$REPO")"
  jq -e --arg token "token=$token," '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_WORKER_GIT_OWNERSHIP_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=worker-git-ownership")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("role=worker")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-git-ownership")) and
    (.hookSpecificOutput.permissionDecisionReason | contains($token))
  ' "$output" >/dev/null || {
    printf 'expected worker Git route denial for token %s: %q\n' "$token" "$command" >&2
    cat -- "$output" >&2
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
    "path=$(eci_realpath_existing "$current_timeout_inner/eci_active")"

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

# Current worker contract: every CLI command passes the registered launcher,
# then the compiled CLI performs the actual operation.
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
  before_index="$(eci_sha256 "$REPO/.git/index")"
  before_head="$(git -C "$REPO" rev-parse HEAD)"
  before_branch="$(git -C "$REPO" symbolic-ref HEAD)"
  printf -v command '%q ' "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" "$@"
  assert_allowed "$command" worker
  HOME="$HOME_ROOT" ECI_HOME="$RUNTIME_ROOT" ECI_PROOF_ROOT="$PROOF_ROOT" \
    "$RUNTIME_ROOT/bin/eci-worker-git" --repo "$REPO" "$@" >"$TMP_ROOT/typed-rejection.log" 2>&1 || status=$?
  [ "$status" -ne 0 ] && [ -s "$TMP_ROOT/typed-rejection.log" ] || {
    printf 'expected CLI rejection: %s\n' "$command" >&2; return 1;
  }
  [ "$(eci_sha256 "$REPO/.git/index")" = "$before_index" ]
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
  assert_denied_code "$fake_git/git clone $FOREIGN_REPO $TMP_ROOT/fake-clone" ECI_WORKER_SCRIPT_TARGET_DENIED worker
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
    assert_worker_git_route_denied "git $family --help" "$family"
  done
  assert_allowed 'git worktree add --help' worker
  assert_worker_git_route_denied 'git log && git add -- file.txt' add
  assert_worker_git_route_denied 'git diff --output=report; git reset -- file.txt' reset
  assert_worker_git_route_denied 'git show HEAD:file.txt && env git commit --help' commit
  assert_worker_git_route_denied 'if true; then git log; git add -- file.txt; fi' add
  assert_worker_git_route_denied '(git diff; git reset -- file.txt)' reset
  assert_worker_git_route_denied 'bash -c "git log; git add -- file.txt"' add
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
    [ "$(eci_stat mode "$outside/file")" = 640 ]
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
  original_repo="$REPO"; redirect="$TMP_ROOT/typed-redirect"
  mkdir -p "$redirect"
  cp "$REPO/file.txt" "$redirect/file.txt"
  mkdir -p "$redirect/hooks"; cp "$REPO/hooks/validate-bash.sh" "$redirect/hooks/validate-bash.sh"
  git -C "$REPO" config core.worktree "$redirect"
  REPO="$(eci_realpath_existing "$redirect")"
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
  hook_hash="$(eci_sha256 "$REPO/.git/index")"
  native_status=0
  git -C "$REPO" checkout --definitely-invalid -- file.txt >"$TMP_ROOT/native-invalid.log" 2>&1 || native_status=$?
  [ "$native_status" -ne 0 ] && [ -s "$TMP_ROOT/native-invalid.log" ]
  [ "$(eci_sha256 "$REPO/.git/index")" = "$hook_hash" ]; typed_invariants
  printf 'helper dirty\n' >"$REPO/file.txt"

  typed_run restore --source head --destination both -- file.txt
  for command in 'git status --short' 'git diff -- file.txt' 'git log --oneline -1' 'git show HEAD:file.txt'; do assert_allowed "$command" worker; done
  assert_allowed "git diff --output=$TMP_ROOT/inspection.out" worker
  assert_allowed "git -C $FOREIGN_REPO status --short" worker
  assert_allowed 'git add -- file.txt' coordinator
  assert_denied_code 'git add -A' ECI_BROAD_DESTRUCTIVE_DENIED coordinator
  assert_denied_code 'git reset --hard' ECI_BROAD_DESTRUCTIVE_DENIED coordinator
  for command in 'git rebase topic' 'git merge topic' 'git checkout -- file.txt' 'git rm -- file.txt' 'git mv file.txt next.txt' 'git diff && env git add -- file.txt'; do
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
  local command before_head before_branch protected_base
  # Protection binds to the configured runtime, so make that complete copied
  # runtime the actual repository instead of using a lookalike sibling path.
  REPO="$(eci_realpath_existing "$RUNTIME_ROOT")"
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
  (
    cd -- "$SOURCE_ROOT/hooks/lib/eci-worker-git-go"
    env GOWORK=off CGO_ENABLED=0 "$ECI_TEST_GO" build -mod=readonly -trimpath -buildvcs=false -o "$RUNTIME_ROOT/bin/eci-worker-git" .
  )
  assert_worker_git_route_denied 'git add -- file.txt' add || return 1
  assert_allowed 'git diff -- file.txt' worker || return 1
  assert_allowed 'git log --oneline -1' worker || return 1
  assert_allowed 'git show HEAD:file.txt' worker || return 1
  assert_worker_git_route_denied 'git diff -- file.txt && git add -- file.txt' add || return 1
  assert_worker_git_route_denied 'env git add -- file.txt' add || return 1
  mkdir -p -- "$TMP_ROOT/typed-fixture-init"
  assert_allowed "git -C $TMP_ROOT/typed-fixture-init init -q" worker || return 1
  git -C "$TMP_ROOT/typed-fixture-init" init -q
  [ -d "$TMP_ROOT/typed-fixture-init/.git" ] || return 1
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
}

case "${NORMAL_GIT_ADMISSION_TARGET:-full}" in
  git-run-once)
    for role in coordinator worker; do
      denial=ECI_BROAD_DESTRUCTIVE_DENIED
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
  full|worker-git-cli-matrix|worker-git-cli-edges|worker-git-native|worker-git-index-aliases|worker-git-cli-slice)
    run_effect_aware_git_target
    printf '%s\n' 'normal Git admission current worker contract: PASS'
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
