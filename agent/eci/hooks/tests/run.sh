#!/usr/bin/env bash

set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
FIXTURES="$ROOT/hooks/tests/fixtures"
if ! command -v strace >/dev/null 2>&1; then
  printf '%s\n' "FAIL setup strace is required"
  exit 1
fi
TMP_ROOT=""
if ! TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-hooks-tests.XXXXXX")"; then
  printf '%s\n' "FAIL setup could not create temporary test root"
  exit 1
fi
export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export XDG_STATE_HOME="$TMP_ROOT/xdg-state"
mkdir -p "$XDG_CONFIG_HOME/eci"
chmod 700 "$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/eci"
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"
chmod 600 "$XDG_CONFIG_HOME/eci/command-gate-mode"
# Tests isolate HOME; the published Go helper lives under the real $HOME/tmp/eci-runtime.
mkdir -p "$TMP_ROOT/home/tmp" || exit 1
ln -s -- "$HOME/tmp/eci-runtime" "$TMP_ROOT/home/tmp/eci-runtime" || exit 1

PASS_COUNT=0
FAIL_COUNT=0
XFAIL_COUNT=0
XPASS_COUNT=0
TODO_COUNT=0
CASE_RECORDED=0

cleanup() {
  local status=$?
  trap - EXIT
  if [ -n "${TMP_ROOT:-}" ] && [ -d "$TMP_ROOT" ]; then
    rm -f "$(subagent_transcript_path 2>/dev/null || true)" 2>/dev/null || true
    rm -rf "$TMP_ROOT"
  fi
  exit "$status"
}

trap cleanup EXIT

note() {
  printf '%s\n' "$*"
}

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  CASE_RECORDED=1
  note "PASS $1"
}

fail() {
  FAIL_COUNT=$((FAIL_COUNT + 1))
  CASE_RECORDED=1
  note "FAIL $1"
  if [ "${2:-}" ]; then
    note "     $2"
  fi
}

xfail() {
  XFAIL_COUNT=$((XFAIL_COUNT + 1))
  CASE_RECORDED=1
  note "XFAIL $1"
}

xpass() {
  XPASS_COUNT=$((XPASS_COUNT + 1))
  CASE_RECORDED=1
  note "XPASS $1"
  if [ "${2:-}" ]; then
    note "      $2"
  fi
}

todo() {
  TODO_COUNT=$((TODO_COUNT + 1))
  CASE_RECORDED=1
  note "TODO $1"
}

run_case() {
  local name="$1"
  shift
  CASE_RECORDED=0
  if "$@"; then
    [ "$CASE_RECORDED" -eq 1 ] || pass "$name"
  else
    [ "$CASE_RECORDED" -eq 1 ] || fail "$name"
  fi
}

run_xfail() {
  local name="$1"
  local future_check="$2"
  local current_bad_check="$3"
  shift
  shift
  shift
  if "$future_check" "$@"; then
    xpass "$name" "expected failure passed; flip this case to PASS in this runner"
  elif "$current_bad_check" "$@"; then
    xfail "$name"
  else
    fail "$name" "unexpected failure mode; not counted as XFAIL"
  fi
}

json_field_equals() {
  local file="$1"
  local expr="$2"
  local want="$3"
  local got
  got="$(jq -r "$expr" "$file" 2>/dev/null || true)"
  [ "$got" = "$want" ]
}

json_field_contains() {
  local file="$1"
  local expr="$2"
  local needle="$3"
  local got
  got="$(jq -r "$expr" "$file" 2>/dev/null || true)"
  case "$got" in
    *"$needle"*) return 0 ;;
    *) return 1 ;;
  esac
}

json_field_not_contains() {
  ! json_field_contains "$@"
}

run_hook() {
  local outfile="$1"
  local script="$2"
  local fixture="$3"
  shift 3
  env -u ECI_HOME -u ECI_ROLE "$@" bash "$script" <"$fixture" 2>"$outfile.err" |
    cat >"$outfile"
}

is_pretool_deny() {
  json_field_equals "$1" '.hookSpecificOutput.permissionDecision // empty' "deny"
}

is_edit_routing_deny() {
  is_pretool_deny "$1" &&
    json_field_contains "$1" '.hookSpecificOutput.permissionDecisionReason // empty' "Delegate repository edits" &&
    json_field_contains "$1" '.hookSpecificOutput.permissionDecisionReason // empty' "This denial governs edit routing only" &&
    json_field_not_contains "$1" '.hookSpecificOutput.permissionDecisionReason // empty' "Never stop" &&
    json_field_not_contains "$1" '.hookSpecificOutput.permissionDecisionReason // empty' "teardown"
}

expect_no_output() {
  [ ! -s "$1" ]
}

fresh_proof_root() {
  local root="$TMP_ROOT/proof-$1"
  rm -rf "$root"
  mkdir -p "$root" || return 1
  printf '%s\n' "$root"
}

subagent_transcript_path() {
  printf '%s/home/.omp/agent/sessions/eci-hooks-test-subagent.jsonl\n' "$TMP_ROOT"
}

with_cwd_fixture() {
  local src="$1"
  local dst="$2"
  jq --arg cwd "$ROOT" '.cwd = $cwd' "$src" >"$dst"
}

with_cwd_path() {
  local src="$1"
  local dst="$2"
  local cwd="$3"
  jq --arg cwd "$cwd" '.cwd = $cwd' "$src" >"$dst"
}

make_git_repo() {
  local name="$1"
  local repo="$TMP_ROOT/git-$name"
  mkdir -p "$repo" || return 1
  git -C "$repo" init -q || return 1
  git -C "$repo" config user.email "hooks-test@example.invalid" || return 1
  git -C "$repo" config user.name "Hooks Test" || return 1
  printf 'base\n' >"$repo/file.txt"
  git -C "$repo" add file.txt || return 1
  git -C "$repo" commit -qm "initial" || return 1
  printf '%s\n' "$repo"
}

test_prompt_state_is_silent_records_head_and_clears_bypass() {
  local proof_root out expected
  proof_root="$(fresh_proof_root prompt-head)"
  mkdir -p "$proof_root/reviewer/t00-session"
  touch "$proof_root/reviewer/t00-session/bypass"
  expected="$(git -C "$ROOT" rev-parse HEAD)"
  out="$TMP_ROOT/prompt-head.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$FIXTURES/user-prompt-submit.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out" &&
    [ "$(cat "$proof_root/reviewer/t00-session/prompt_head" 2>/dev/null || true)" = "$expected" ] &&
    [ ! -e "$proof_root/reviewer/t00-session/bypass" ]
}

test_prompt_state_marks_side_prompt() {
  local proof_root out marker cwd_marker_count cwd_marker
  proof_root="$(fresh_proof_root prompt-side)"
  out="$TMP_ROOT/prompt-side.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$FIXTURES/user-prompt-side.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  marker="$proof_root/side-stop/sessions/t00-session/side_stop"
  cwd_marker_count=$(find "$proof_root/side-stop/cwd" -mindepth 2 -maxdepth 2 -name side_stop 2>/dev/null | wc -l)
  cwd_marker=$(find "$proof_root/side-stop/cwd" -mindepth 2 -maxdepth 2 -name side_stop 2>/dev/null | head -n1)
  expect_no_output "$out" &&
    [ -f "$marker" ] &&
    grep -q '^command: /side$' "$marker" &&
    grep -q '^parent_session_id: t00-session$' "$marker" &&
    [ "$cwd_marker_count" -eq 1 ] &&
    grep -q '^command: /side$' "$cwd_marker" &&
    grep -q '^parent_session_id: t00-session$' "$cwd_marker"
}

test_prompt_state_skips_state_for_invalid_session() {
  local proof_root out
  proof_root="$(fresh_proof_root prompt-invalid)"
  out="$TMP_ROOT/prompt-invalid.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$FIXTURES/user-prompt-invalid-session.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out" &&
    [ ! -e "$proof_root/reviewer/../bad/prompt_head" ] &&
    [ "$(find "$proof_root/reviewer" -name prompt_head 2>/dev/null | wc -l)" -eq 0 ]
}

test_prompt_state_keeps_session_eci_marker_silent() {
  local proof_root out marker
  proof_root="$(fresh_proof_root prompt-eci)"
  marker="$proof_root/t00-session/eci_active"
  mkdir -p "$(dirname "$marker")" || return 1
  printf 'scope: prompt state test\n' >"$marker"
  out="$TMP_ROOT/prompt-eci.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$FIXTURES/user-prompt-submit.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out" &&
    [ -f "$marker" ] &&
    grep -q "scope: prompt state test" "$marker"
}

test_prompt_state_config_is_wired_without_probe() {
  jq -e '
    ([.hooks.UserPromptSubmit[]?.hooks[]?.command]
      | any(test("hooks/prompt-task-reminder\\.sh"))) and
    ([.hooks.PostToolUse[]?.hooks[]?.command] | length == 0) and
    ([.. | objects | .command? // empty]
      | all(contains("/hooks/tests/skill-event-probe.sh") | not))
  ' "$ROOT/hooks.json" >/dev/null
}

test_prompt_state_keeps_nontrivial_governance_task_silent() {
  prompt_state_prompt_stays_silent "prompt-nontrivial-governance-silent" \
    "Update AGENTS.md and hooks/prompt-task-reminder.sh to enforce governance routing."
}

test_prompt_state_keeps_eci_typo_silent() {
  prompt_state_prompt_stays_silent "prompt-eci-typo-silent" \
    "Fix a typo in AGENTS.md"
}

test_prompt_state_keeps_hooks_path_governance_task_silent() {
  prompt_state_prompt_stays_silent "prompt-hooks-path-silent" \
    "Fix hooks/stop-gate.sh to enforce ECI teardown."
}

test_prompt_state_leaves_react_hook_prompt_silent() {
  local proof_root input out
  proof_root="$(fresh_proof_root prompt-react-hook-silent)"
  input="$TMP_ROOT/prompt-react-hook-silent.json"
  jq '.prompt = "Fix React hook useUser."' "$FIXTURES/user-prompt-submit.json" >"$input"
  out="$TMP_ROOT/prompt-react-hook-silent.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_prompt_state_leaves_react_app_hooks_path_silent() {
  prompt_state_prompt_stays_silent "prompt-react-app-hooks-path-silent" \
    "Fix hooks/useUser.ts in the React app."
}

test_prompt_state_leaves_react_src_hooks_path_silent() {
  prompt_state_prompt_stays_silent "prompt-react-src-hooks-path-silent" \
    "Fix src/hooks/useUser.ts to correct stale state."
}

test_prompt_state_leaves_express_routing_prompt_silent() {
  local proof_root input out
  proof_root="$(fresh_proof_root prompt-express-routing-silent)"
  input="$TMP_ROOT/prompt-express-routing-silent.json"
  jq '.prompt = "Write routing for the Express app."' "$FIXTURES/user-prompt-submit.json" >"$input"
  out="$TMP_ROOT/prompt-express-routing-silent.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_prompt_state_leaves_email_rule_prompt_silent() {
  local proof_root input out
  proof_root="$(fresh_proof_root prompt-email-rule-silent)"
  input="$TMP_ROOT/prompt-email-rule-silent.json"
  jq '.prompt = "Add a rule to the email filter."' "$FIXTURES/user-prompt-submit.json" >"$input"
  out="$TMP_ROOT/prompt-email-rule-silent.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

prompt_state_prompt_stays_silent() {
  local tag="$1"
  local prompt_text="$2"
  local proof_root input out
  proof_root="$(fresh_proof_root "$tag")"
  input="$TMP_ROOT/$tag.json"
  jq --arg prompt "$prompt_text" '.prompt = $prompt' "$FIXTURES/user-prompt-submit.json" >"$input"
  out="$TMP_ROOT/$tag.out"

  run_hook "$out" "$ROOT/hooks/prompt-task-reminder.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_prompt_state_keeps_system_prompt_task_silent() {
  prompt_state_prompt_stays_silent "prompt-system-prompt-silent" \
    "Improve the system prompt."
}

test_prompt_state_keeps_subagent_stop_hook_prompt_task_silent() {
  prompt_state_prompt_stays_silent "prompt-subagent-stop-hook-silent" \
    "Update the subagent prompt for Stop-hook handling."
}

test_prompt_state_keeps_stop_hook_prompt_task_silent() {
  prompt_state_prompt_stays_silent "prompt-stop-hook-prompt-silent" \
    "Tighten the Stop-hook prompt."
}

test_prompt_state_keeps_eci_review_task_silent() {
  prompt_state_prompt_stays_silent "prompt-eci-review-silent" \
    "Review AGENTS.md."
}

test_prompt_state_keeps_hook_behavior_audit_task_silent() {
  prompt_state_prompt_stays_silent "prompt-hook-behavior-audit-silent" \
    "Audit hook behavior."
}

test_prompt_state_keeps_hook_tests_task_silent() {
  prompt_state_prompt_stays_silent "prompt-hook-tests-silent" \
    "Add hook tests for stop gate behavior."
}

test_prompt_state_keeps_task_routing_task_silent() {
  prompt_state_prompt_stays_silent "prompt-task-routing-silent" \
    "Fix task routing for non-trivial requests."
}

test_prompt_state_keeps_agent_routing_protocol_task_silent() {
  prompt_state_prompt_stays_silent "prompt-agent-routing-protocol-silent" \
    "Fix agent routing so non-trivial tasks use the right protocol."
}

test_prompt_state_keeps_nontrivial_tasks_use_eci_question_silent() {
  prompt_state_prompt_stays_silent "prompt-nontrivial-tasks-use-eci-question-silent" \
    "What should we fix so non-trivial tasks use ECI at least?"
}

test_prompt_state_keeps_ensure_nontrivial_tasks_use_eci_silent() {
  prompt_state_prompt_stays_silent "prompt-ensure-nontrivial-tasks-use-eci-silent" \
    "Ensure non-trivial tasks use ECI at least."
}

test_prompt_state_keeps_route_nontrivial_tasks_to_eci_silent() {
  prompt_state_prompt_stays_silent "prompt-route-nontrivial-tasks-to-eci-silent" \
    "Route non-trivial tasks to ECI."
}

test_prompt_state_keeps_routing_nontrivial_tasks_use_eci_silent() {
  prompt_state_prompt_stays_silent "prompt-routing-nontrivial-tasks-use-eci-silent" \
    "Fix routing so non-trivial tasks use ECI at least."
}

test_prompt_state_keeps_eci_routing_nontrivial_tasks_silent() {
  prompt_state_prompt_stays_silent "prompt-eci-routing-nontrivial-tasks-silent" \
    "Fix ECI routing for non-trivial tasks."
}

test_prompt_state_keeps_make_nontrivial_tasks_use_eci_silent() {
  prompt_state_prompt_stays_silent "prompt-make-nontrivial-tasks-use-eci-silent" \
    "Make non-trivial tasks use ECI at least."
}

test_prompt_state_keeps_caveman_switch_task_silent() {
  prompt_state_prompt_stays_silent "prompt-caveman-switch-silent" \
    "OK, we switched from caveman to ponytail previously. Switch it back to caveman."
}

test_prompt_state_keeps_plain_stop_gate_task_silent() {
  prompt_state_prompt_stays_silent "prompt-stop-gate-silent" \
    "Fix stop gate behavior."
}

test_prompt_state_keeps_hyphen_stop_gate_task_silent() {
  prompt_state_prompt_stays_silent "prompt-hyphen-stop-gate-silent" \
    "Fix stop-gate behavior."
}

test_prompt_state_keeps_stop_gate_script_task_silent() {
  prompt_state_prompt_stays_silent "prompt-stop-gate-script-silent" \
    "Update stop-gate.sh."
}

test_prompt_state_keeps_stop_checklist_path_task_silent() {
  prompt_state_prompt_stays_silent "prompt-stop-checklist-path-silent" \
    "Update hooks/stop-checklist.md."
}

test_prompt_state_keeps_stop_checklist_task_silent() {
  prompt_state_prompt_stays_silent "prompt-stop-checklist-silent" \
    "Update stop checklist."
}

test_prompt_state_keeps_eci_config_task_silent() {
  prompt_state_prompt_stays_silent "prompt-eci-config-silent" \
    "Update ECI config.toml."
}

test_prompt_state_leaves_cli_installation_instructions_silent() {
  prompt_state_prompt_stays_silent "prompt-cli-instructions-silent" \
    "Write installation instructions for the CLI."
}

test_prompt_state_leaves_onboarding_email_instructions_silent() {
  prompt_state_prompt_stays_silent "prompt-email-instructions-silent" \
    "Add instructions to the onboarding email."
}

test_prompt_state_leaves_react_hook_behavior_prompt_silent() {
  prompt_state_prompt_stays_silent "prompt-react-hook-behavior-silent" \
    "Fix React hook behavior in useUser."
}

test_prompt_state_leaves_react_hook_behavior_audit_silent() {
  prompt_state_prompt_stays_silent "prompt-react-hook-behavior-audit-silent" \
    "Audit hook behavior in React useUser."
}

test_prompt_state_leaves_react_hook_tests_silent() {
  prompt_state_prompt_stays_silent "prompt-react-hook-tests-silent" \
    "Add hook tests for React useUser."
}

test_prompt_state_leaves_express_routing_rule_prompt_silent() {
  prompt_state_prompt_stays_silent "prompt-express-routing-rule-silent" \
    "Add a routing rule to the Express app."
}

test_prompt_state_leaves_rust_config_silent() {
  prompt_state_prompt_stays_silent "prompt-rust-config-silent" \
    "Update config.toml for the Rust app."
}

test_prompt_state_leaves_web_app_hooks_json_silent() {
  prompt_state_prompt_stays_silent "prompt-web-app-hooks-json-silent" \
    "Modify hooks.json for my web app."
}

test_prompt_state_leaves_caveman_story_silent() {
  prompt_state_prompt_stays_silent "prompt-caveman-story-silent" \
    "Write a caveman story."
}

test_runtime_hook_probe_historical_evidence_is_sanitized() {
  jq -e -s '
    length == 2 and
    all(type == "object") and
    all(keys_unsorted | all(IN("hook_event_name", "session_id", "cwd", "tool_name", "tool_input_keys", "observed"))) and
    any(
      .hook_event_name == "UserPromptSubmit" and
      .cwd == "<eci-home>" and
      (.session_id | test("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"))
    ) and
    any(
      .hook_event_name == "PostToolUse" and
      .tool_name == "Bash" and
      (.tool_input_keys | type == "array" and index("command") != null)
    ) and
    ([
      paths(scalars) as $p
      | {
          key: ($p[-1] | tostring),
          value: (getpath($p) | tostring)
        }
      | select(
          (.key | test("(?i)(password|passwd|secret|token|api[_-]?key|access[_-]?key|credential|auth|bearer|cookie|private[_-]?key)")) or
          (.value | test("(?i)(sk-[A-Za-z0-9]|ghp_|xox[baprs]-|AKIA[0-9A-Z]{16}|BEGIN (RSA |OPENSSH |DSA |EC |)PRIVATE KEY|Bearer[[:space:]]+[A-Za-z0-9._=-]+|password|passwd|secret|token|api[_-]?key|access[_-]?key|credential|cookie)"))
        )
    ] | length == 0)
  ' "$FIXTURES/runtime-hook-probe-evidence.jsonl" >/dev/null
}

test_session_snapshot_saves_baseline_and_clears_legacy_skip() {
  local proof_root out
  proof_root="$(fresh_proof_root session)"
  mkdir -p "$proof_root/t00-session"
  touch "$proof_root/t00-session/skip_stop"
  ECI_SESSION_ID=t00-session ECI_PROOF_ROOT="$proof_root" "$ROOT/bin/skip-stop" on >/dev/null 2>&1 || return 1
  env -u ECI_SESSION_ID ECI_PROOF_ROOT="$proof_root" "$ROOT/bin/skip-stop" on >/dev/null 2>&1 || return 1
  out="$TMP_ROOT/session-snapshot.out"

  run_hook "$out" "$ROOT/hooks/session-snapshot.sh" "$FIXTURES/session-start.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  [ -s "$proof_root/t00-session/baseline_head" ] &&
    [ ! -e "$proof_root/t00-session/skip_stop" ] &&
    [ -e "$proof_root/skip-stop/sessions/t00-session/skip_stop" ] &&
    [ "$(find "$proof_root/skip-stop/cwd" -mindepth 2 -maxdepth 2 -name skip_stop 2>/dev/null | wc -l)" -eq 1 ] &&
    json_field_contains "$out" '.hookSpecificOutput.additionalContext // empty' "AGENTS.md"
}

test_session_snapshot_skips_ephemeral_threads() {
  local proof_root out
  proof_root="$(fresh_proof_root session-ephemeral)"
  out="$TMP_ROOT/session-snapshot-ephemeral.out"

  run_hook "$out" "$ROOT/hooks/session-snapshot.sh" "$FIXTURES/session-start-ephemeral.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out" &&
    [ ! -e "$proof_root/t00-side/baseline_head" ]
}

test_session_snapshot_preserves_fresh_markers_in_old_state_dirs() {
  local proof_root out skip_cwd
  proof_root="$(fresh_proof_root session-old-markers)"
  ECI_SESSION_ID=t00-session ECI_PROOF_ROOT="$proof_root" "$ROOT/bin/skip-stop" on >/dev/null 2>&1 || return 1
  env -u ECI_SESSION_ID ECI_PROOF_ROOT="$proof_root" "$ROOT/bin/skip-stop" on >/dev/null 2>&1 || return 1

  skip_cwd=$(find "$proof_root/skip-stop/cwd" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -n1)
  [ -n "$skip_cwd" ] || return 1
  touch -t 202001010000 "$proof_root/skip-stop/sessions/t00-session" "$skip_cwd" "$proof_root/t00-session"

  out="$TMP_ROOT/session-snapshot-old-markers.out"
  run_hook "$out" "$ROOT/hooks/session-snapshot.sh" "$FIXTURES/session-start.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  [ -e "$proof_root/skip-stop/sessions/t00-session/skip_stop" ] &&
    [ -e "$skip_cwd/skip_stop" ]
}

test_side_session_start_is_silent_and_binds_stop_bypass() {
  local proof_root prompt_out prompt_input start_input start_out child_marker cwd_marker
  proof_root="$(fresh_proof_root session-side)"
  prompt_out="$TMP_ROOT/session-side-prompt.out"
  prompt_input="$TMP_ROOT/session-side-prompt.json"

  jq --arg cwd "$ROOT" '.session_id = "t00-parent" | .cwd = $cwd' "$FIXTURES/user-prompt-side.json" >"$prompt_input"
  run_hook "$prompt_out" "$ROOT/hooks/prompt-task-reminder.sh" "$prompt_input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  start_input="$TMP_ROOT/session-side-start.json"
  jq --arg cwd "$ROOT" '.session_id = "t00-side" | .cwd = $cwd' "$FIXTURES/session-start.json" >"$start_input"
  start_out="$TMP_ROOT/session-side-start.out"
  run_hook "$start_out" "$ROOT/hooks/session-snapshot.sh" "$start_input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  child_marker="$proof_root/side-stop/sessions/t00-side/side_stop"
  cwd_marker=$(find "$proof_root/side-stop/cwd" -mindepth 2 -maxdepth 2 -name side_stop 2>/dev/null | head -n1)
  [ -n "$cwd_marker" ] || return 1
  touch -t 202001010000 "$cwd_marker"

  expect_no_output "$start_out" &&
    [ -f "$child_marker" ] &&
    grep -q '^parent_session_id: t00-parent$' "$child_marker"
}

test_eci_gate_allows_code_apply_patch() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-code)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-code.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-apply-patch-code.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_skips_invalid_session_id() {
  local proof_root input out
  proof_root="$(fresh_proof_root eci-invalid-session)"
  mkdir -p "$proof_root/../bad"
  printf 'scope: bad\n' >"$proof_root/../bad/eci_active"
  input="$TMP_ROOT/eci-invalid-session.json"
  jq '.session_id = "../bad"' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-invalid-session.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_denies_malformed_identity_with_active_marker() {
  local proof_root input out
  proof_root="$(fresh_proof_root eci-malformed-active)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: malformed active identity\n' >"$proof_root/t00-session/eci_active"
  input="$TMP_ROOT/eci-malformed-active.json"
  jq '.session_id = []' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-malformed-active.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' 'malformed hook identity' || return 1

  jq '.tool_name = ""' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" || return 1

  jq '.cwd = "bad\ncwd"' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out"
}

test_eci_gate_allows_edits_without_routing_message() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-message)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-message.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-apply-patch-code.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_ignores_code_apply_patch_from_cwd_state() {
  local proof_root input out cwd_dir
  proof_root="$(fresh_proof_root eci-code-cwd)"
  cwd_dir="$(ECI_PROOF_ROOT="$proof_root" bash -c '. "$0/hooks/lib/eci-proof-state.sh"; eci_ensure_cwd_state_dir eci "$1"' "$ROOT" "$ROOT")" || return 1
  printf 'scope: stale cwd state\n' >"$cwd_dir/eci_active"

  input="$TMP_ROOT/eci-code-cwd.json"
  jq --arg cwd "$ROOT" '.cwd = $cwd' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-code-cwd.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_ignores_eci_role_for_normal_edit() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-role-spoof)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-role-spoof.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-apply-patch-code.json" \
    ECI_PROOF_ROOT="$proof_root" ECI_ROLE="eci-implementer" || return 1

  expect_no_output "$out"
}

write_subagent_transcript() {
  local path="$1"
  mkdir -p "$(dirname "$path")" || return 1
  cat >"$path" <<'JSON'
{"timestamp":"2026-05-04T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}}}
JSON
}

write_first_record_scoped_subagent_transcript() {
  local path="$1"
  local record_bytes="$2"
  local include_newline="${3:-yes}"
  local include_history="${4:-yes}"
  local parent_session_id="${5:-parent-session}"
  local prefix suffix newline_bytes padding_length history_prefix history_suffix
  prefix="{\"type\":\"session_meta\",\"payload\":{\"source\":{\"subagent\":{\"thread_spawn\":{\"parent_thread_id\":\"$parent_session_id\"}}}},\"padding\":\""
  suffix='"}'
  newline_bytes=1
  [ "$include_newline" = yes ] || newline_bytes=0
  padding_length=$((record_bytes - ${#prefix} - ${#suffix} - newline_bytes))
  [ "$padding_length" -ge 0 ] || return 1
  mkdir -p "$(dirname "$path")" || return 1
  printf '%s%*s%s' "$prefix" "$padding_length" '' "$suffix" | tr ' ' x >"$path"
  [ "$include_newline" = yes ] && printf '\n' >>"$path"
  if [ "$include_history" = yes ]; then
    history_prefix='{"padding":"'
    history_suffix='","sentinel":"DEEP_SECOND_RECORD_SENTINEL"}'
    printf '%s%*s%s\n' "$history_prefix" 1048576 '' "$history_suffix" | tr ' ' y >>"$path"
  fi
  [ "$record_bytes" -eq "$(head -c "$record_bytes" "$path" | wc -c)" ]
}

trace_first_record_helper() {
  local helper="$1"
  local transcript="$2"
  local output="$3"
  local trace="$4"

  strace -f -qq -s 8192 -e trace=read -P "$transcript" -o "$trace" \
    env -u ECI_HOME HOME="$TMP_ROOT/home" bash -c '. "$1"; "$2" "$3"' \
    bash "$ROOT/hooks/lib/eci-proof-state.sh" "$helper" \
    "$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')" \
    >"$output" 2>"$output.err"
}

trace_read_bytes() {
  local trace="$1"

  awk '$NF ~ /^[0-9]+$/ { total += $NF } END { print total + 0 }' "$trace"
}

trace_is_first_record_scoped() {
  local trace="$1"
  local transcript="$2"
  local first_record_bytes maximum_read_bytes total_bytes read_bytes
  first_record_bytes="$(sed -n '1{p;q;}' "$transcript" | wc -c)"
  maximum_read_bytes=$((first_record_bytes + 8192))
  [ "$maximum_read_bytes" -le 73728 ] || maximum_read_bytes=73728
  total_bytes="$(wc -c <"$transcript")"
  read_bytes="$(trace_read_bytes "$trace")"

  [ -s "$trace" ] &&
    [ "$read_bytes" -le "$maximum_read_bytes" ] &&
    { [ "$first_record_bytes" -eq "$total_bytes" ] || [ "$read_bytes" -lt "$total_bytes" ]; } &&
    ! grep -Fq 'DEEP_SECOND_RECORD_SENTINEL' "$trace"
}

invoke_state_helper() {
  local helper="$1"
  local input="$2"
  local output="$3"

  env -u ECI_HOME HOME="$TMP_ROOT/home" bash -c '. "$1"; "$2" "$3"' \
    bash "$ROOT/hooks/lib/eci-proof-state.sh" "$helper" "$input" \
    >"$output" 2>"$output.err"
}

test_subagent_helper_reads_oversize_metadata_prefix() {
  local label record_bytes transcript output trace
  for label in short boundary large; do
    case "$label" in
      short) record_bytes=256 ;;
      boundary) record_bytes=4096 ;;
      large) record_bytes=1048576 ;;
    esac
    transcript="$TMP_ROOT/home/.omp/agent/sessions/first-record-subagent-$label.jsonl"
    output="$TMP_ROOT/first-record-subagent-$label.out"
    trace="$TMP_ROOT/first-record-subagent-$label.strace"
    write_first_record_scoped_subagent_transcript "$transcript" "$record_bytes" || return 1
    trace_first_record_helper eci_hook_is_subagent_context \
      "$transcript" "$output" "$trace" || return 1
    [ ! -s "$output" ] && trace_is_first_record_scoped "$trace" "$transcript" || return 1
  done
}

test_parent_helper_reads_oversize_metadata_prefix() {
  local transcript output trace status=0
  transcript="$TMP_ROOT/home/.omp/agent/sessions/first-record-parent-large.jsonl"
  output="$TMP_ROOT/first-record-parent-large.out"
  trace="$TMP_ROOT/first-record-parent-large.strace"
  write_first_record_scoped_subagent_transcript "$transcript" 1048576 || return 1

  # Records whose newline is beyond the 64 KiB+1 budget are rejected by design.
  trace_first_record_helper eci_hook_parent_session_id \
    "$transcript" "$output" "$trace" || status=$?
  [ "$status" -eq 2 ] && [ ! -s "$output" ] && trace_is_first_record_scoped "$trace" "$transcript"
}

test_first_record_helpers_reject_oversize_without_final_newline() {
  local transcript input output
  transcript="$TMP_ROOT/home/.omp/agent/sessions/first-record-no-final-newline.jsonl"
  output="$TMP_ROOT/first-record-no-final-newline.out"
  write_first_record_scoped_subagent_transcript "$transcript" 1048576 no no || return 1
  input="$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')"

  if invoke_state_helper eci_hook_is_subagent_context "$input" "$output"; then
    return 1
  fi
  if invoke_state_helper eci_hook_parent_session_id "$input" "$output"; then
    return 1
  fi
  [ ! -s "$output" ]
}

test_first_record_helpers_preserve_boundary_behavior() {
  local dir transcript input output
  dir="$TMP_ROOT/home/.omp/agent/sessions"
  mkdir -p "$dir" || return 1

  transcript="$dir/boundary-subagent.jsonl"
  write_subagent_transcript "$transcript" || return 1
  input="$(jq -cn --arg transcript "$transcript" --arg sid child-session \
    '{transcript_path: $transcript, session_id: $sid}')"
  output="$TMP_ROOT/boundary-subagent.out"
  invoke_state_helper eci_hook_is_subagent_context "$input" "$output" || return 1
  invoke_state_helper eci_hook_parent_session_id "$input" "$output" || return 1
  [ "$(cat "$output")" = parent-session ] || return 1

  transcript="$dir/boundary-no-final-newline.jsonl"
  printf '%s' "$(cat "$dir/boundary-subagent.jsonl")" >"$transcript"
  input="$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')"
  invoke_state_helper eci_hook_is_subagent_context "$input" "$output" || return 1
  invoke_state_helper eci_hook_parent_session_id "$input" "$output" || return 1
  [ "$(cat "$output")" = parent-session ] || return 1

  transcript="$dir/boundary-empty.jsonl"
  : >"$transcript"
  input="$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')"
  if invoke_state_helper eci_hook_is_subagent_context "$input" "$output"; then return 1; fi
  invoke_state_helper eci_hook_parent_session_id "$input" "$output" || true
  [ ! -s "$output" ] || return 1

  transcript="$dir/boundary-malformed.jsonl"
  printf '%s\n' '{malformed' >"$transcript"
  input="$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')"
  if invoke_state_helper eci_hook_is_subagent_context "$input" "$output"; then return 1; fi
  invoke_state_helper eci_hook_parent_session_id "$input" "$output" || true
  [ ! -s "$output" ] || return 1

  transcript="$dir/boundary-missing.jsonl"
  input="$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')"
  if invoke_state_helper eci_hook_is_subagent_context "$input" "$output"; then return 1; fi
  if invoke_state_helper eci_hook_parent_session_id "$input" "$output"; then return 1; fi

  transcript="$dir/boundary-main.jsonl"
  write_main_transcript "$transcript" || return 1
  input="$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')"
  if invoke_state_helper eci_hook_is_subagent_context "$input" "$output"; then return 1; fi
  invoke_state_helper eci_hook_parent_session_id "$input" "$output" || true
  [ ! -s "$output" ] || return 1

  transcript="$dir/boundary-invalid-parent.jsonl"
  printf '%s\n' '{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"../bad"}}}}}' >"$transcript"
  input="$(jq -cn --arg transcript "$transcript" --arg sid child-session \
    '{transcript_path: $transcript, session_id: $sid}')"
  invoke_state_helper eci_hook_allowed_session_ids "$input" "$output" || return 1
  [ "$(cat "$output")" = child-session ] || return 1

  transcript="$TMP_ROOT/outside-restricted-session-path.jsonl"
  write_subagent_transcript "$transcript" || return 1
  input="$(jq -cn --arg transcript "$transcript" '{transcript_path: $transcript}')"
  if invoke_state_helper eci_hook_is_subagent_context "$input" "$output"; then return 1; fi
  if invoke_state_helper eci_hook_parent_session_id "$input" "$output"; then return 1; fi
  [ ! -s "$output" ]
}

omp_status_of() {
  local path="$1" input
  input="$(jq -cn --arg t "$path" '{transcript_path: $t}')" || return 1
  invoke_state_helper eci_hook_subagent_status "$input" "$TMP_ROOT/omp-status.out" || return 1
  cat "$TMP_ROOT/omp-status.out"
}

omp_root() {
  printf '%s\n' "$TMP_ROOT/home/.omp/agent/sessions"
}

OMP_TITLE_LINE='{"type":"title","title":"Session"}'
OMP_HEADER_LINE='{"type":"session","version":3,"id":"aaaa-main","cwd":"/work"}'
OMP_PARENT_HEADER_LINE='{"type":"session","version":3,"id":"bbbb-child","cwd":"/work","parentSession":"aaaa-main"}'
OMP_MESSAGE_LINE='{"type":"message","message":{"role":"user","content":"hello"}}'
OMP_INIT_LINE='{"type":"session_init","customType":"session_init","data":{}}'

test_omp_status_main_title_first_depth_two() {
  local path
  path="$(omp_root)/-proj-/main.jsonl"
  mkdir -p "$(dirname "$path")" || return 1
  printf '%s\n' "$OMP_TITLE_LINE" "$OMP_HEADER_LINE" "$OMP_MESSAGE_LINE" >"$path"
  [ "$(omp_status_of "$path")" = main ]
}

test_omp_status_session_init_line_four_is_subagent() {
  local path
  path="$(omp_root)/-proj-/parent/child.jsonl"
  mkdir -p "$(dirname "$path")" || return 1
  printf '%s\n' "$OMP_HEADER_LINE" "$OMP_TITLE_LINE" "$OMP_MESSAGE_LINE" "$OMP_INIT_LINE" >"$path"
  [ "$(omp_status_of "$path")" = subagent ]
}

test_omp_status_parent_session_header_is_subagent() {
  local path
  path="$(omp_root)/-proj-/parentonly.jsonl"
  mkdir -p "$(dirname "$path")" || return 1
  printf '%s\n' "$OMP_PARENT_HEADER_LINE" "$OMP_MESSAGE_LINE" >"$path"
  [ "$(omp_status_of "$path")" = subagent ]
}

test_omp_status_depth_three_is_subagent() {
  local path
  path="$(omp_root)/-proj-/parent/nosignal.jsonl"
  mkdir -p "$(dirname "$path")" || return 1
  printf '%s\n' "$OMP_HEADER_LINE" "$OMP_MESSAGE_LINE" >"$path"
  [ "$(omp_status_of "$path")" = subagent ]
}

test_omp_status_fails_closed() {
  local root dir outside
  root="$(omp_root)"
  dir="$root/-proj-"
  mkdir -p "$dir" || return 1

  [ "$(omp_status_of "$dir/missing.jsonl")" = unknown ] || return 1

  printf '%s\n' "$OMP_HEADER_LINE" "$OMP_MESSAGE_LINE" >"$dir/real.jsonl"
  ln -s "$dir/real.jsonl" "$dir/link.jsonl" || return 1
  [ "$(omp_status_of "$dir/link.jsonl")" = unknown ] || return 1

  outside="$TMP_ROOT/outside-omp"
  mkdir -p "$outside" || return 1
  printf '%s\n' "$OMP_HEADER_LINE" "$OMP_MESSAGE_LINE" >"$outside/main.jsonl"
  [ "$(omp_status_of "$outside/main.jsonl")" = unknown ] || return 1

  : >"$dir/empty.jsonl"
  [ "$(omp_status_of "$dir/empty.jsonl")" = unknown ] || return 1

  printf '%s\n' "$OMP_HEADER_LINE" '{malformed' >"$dir/malformed.jsonl"
  [ "$(omp_status_of "$dir/malformed.jsonl")" = unknown ]
}

test_omp_status_large_main_is_main() {
  local path filler
  path="$(omp_root)/-proj-/large.jsonl"
  mkdir -p "$(dirname "$path")" || return 1
  filler="$(head -c 70000 /dev/zero | tr '\0' x)" || return 1
  printf '%s\n' "$OMP_TITLE_LINE" "$OMP_HEADER_LINE" \
    "$(jq -cn --arg t "$filler" '{type: "message", message: {role: "user", content: $t}}')" \
    "$OMP_MESSAGE_LINE" >"$path"
  [ "$(stat -c %s "$path")" -gt 65536 ] || return 1
  [ "$(omp_status_of "$path")" = main ]
}

write_main_transcript() {
  local path="$1"
  mkdir -p "$(dirname "$path")" || return 1
  cat >"$path" <<'JSON'
{"timestamp":"2026-05-04T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":"cli"}}
JSON
}

write_main_transcript_with_subagent_call() {
  local path="$1"
  mkdir -p "$(dirname "$path")" || return 1
  cat >"$path" <<'JSON'
{"timestamp":"2026-05-04T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-session","source":"cli"}}
{"timestamp":"2026-05-04T00:00:01.000Z","type":"user","message":{"content":"use a subagent"}}
{"timestamp":"2026-05-04T00:00:02.000Z","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","arguments":"{\"agent_type\":\"explorer\"}"}}
JSON
}

main_reviewer_transcript_path() {
  printf '%s/home/.omp/agent/sessions/eci-hooks-test-main-reviewer.jsonl\n' "$TMP_ROOT"
}

install_reviewer_transcript_fixture() {
  local fixture="$1"
  local path="$2"
  mkdir -p "$(dirname "$path")" || return 1
  cp "$fixture" "$path"
}

redaction_fixture_value() {
  case "$1" in
    openai-api-key) printf '%s%s%s' 'sk-' 'test' 'SECRET1234567890' ;;
    password) printf '%s%s' 'hunter' '2' ;;
    bearer-token) printf '%s%s' 'bearer' 'SECRET987654321' ;;
    github-token) printf '%s%s%s' 'gh' 'p_' 'SECRETtoken1234567890' ;;
    slack-token) printf '%s%s%s' 'xo' 'xb-' '1234567890-secretvalue' ;;
    aws-access-key) printf '%s%s' 'AK' 'IAABCDEFGHIJKLMNOP' ;;
    google-api-key) printf '%s%s%s' 'AI' 'za' 'SyA1234567890abcdefghijklmnopqrstu' ;;
    private-key-begin) printf '%s%s %s %s %s%s' '-----' 'BEGIN' 'OPENSSH' 'PRIVATE' 'KEY' '-----' ;;
    private-key-material) printf '%s%s%s' 'private' '-key-' 'material' ;;
    private-key-end) printf '%s%s %s %s %s%s' '-----' 'END' 'OPENSSH' 'PRIVATE' 'KEY' '-----' ;;
    private-key-block)
      printf '%s\n%s\n%s' \
        "$(redaction_fixture_value private-key-begin)" \
        "$(redaction_fixture_value private-key-material)" \
        "$(redaction_fixture_value private-key-end)"
      ;;
    *) return 1 ;;
  esac
}

file_lacks_values() {
  local file="$1"
  local value
  shift

  for value in "$@"; do
    [ -n "$value" ] || continue
    if grep -Fq -- "$value" "$file"; then
      return 1
    fi
  done
}

test_eci_gate_allows_spawned_agent_transcript_payload() {
  local proof_root input out transcript
  proof_root="$(fresh_proof_root eci-subagent-transcript)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  transcript="$(subagent_transcript_path)"
  write_subagent_transcript "$transcript" || return 1
  input="$TMP_ROOT/eci-subagent-transcript.json"
  jq --arg transcript "$transcript" '.transcript_path = $transcript' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-subagent-transcript.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_allows_oversize_spawned_agent_transcript_payload() {
  local proof_root input out transcript
  proof_root="$(fresh_proof_root eci-oversize-subagent-transcript)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  transcript="$TMP_ROOT/home/.omp/agent/sessions/eci-oversize-subagent.jsonl"
  write_first_record_scoped_subagent_transcript "$transcript" 1048576 yes no t00-session || return 1
  input="$TMP_ROOT/eci-oversize-subagent-transcript.json"
  jq --arg transcript "$transcript" '.transcript_path = $transcript' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-oversize-subagent-transcript.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_allows_spawned_agent_through_sessions_root_symlink() {
  local proof_root input out sessions_root sessions_target transcript
  proof_root="$(fresh_proof_root eci-subagent-sessions-symlink)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  sessions_root="$TMP_ROOT/home/.omp/agent/sessions"
  sessions_target="$TMP_ROOT/eci-subagent-sessions-target"
  mkdir -p "$TMP_ROOT/home/.omp/agent/eci" "$sessions_target"
  ln -s "$sessions_target" "$sessions_root"
  transcript="$sessions_root/child.jsonl"
  write_subagent_transcript "$transcript" || return 1
  input="$TMP_ROOT/eci-subagent-sessions-symlink.json"
  jq --arg transcript "$transcript" '.transcript_path = $transcript' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-subagent-sessions-symlink.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_allows_split_root_logical_child_transcript() {
  local proof_root input out physical_sessions logical_eci transcript
  proof_root="$(fresh_proof_root eci-split-root-logical-child)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  physical_sessions="$TMP_ROOT/home/.omp/agent/sessions"
  logical_eci="$TMP_ROOT/logical-eci"
  mkdir -p "$physical_sessions" "$logical_eci"
  ln -s "$physical_sessions" "$logical_eci/sessions"
  transcript="$logical_eci/sessions/child.jsonl"
  write_subagent_transcript "$transcript" || return 1
  input="$TMP_ROOT/eci-split-root-logical-child.json"
  jq --arg transcript "$transcript" '.transcript_path = $transcript' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-split-root-logical-child.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_HOME="$logical_eci" ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_blocks_main_transcript_payload() {
  local proof_root input out transcript
  proof_root="$(fresh_proof_root eci-main-transcript)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  transcript="$TMP_ROOT/home/.omp/agent/sessions/eci-hooks-test-main.jsonl"
  write_main_transcript "$transcript" || return 1
  input="$TMP_ROOT/eci-main-transcript.json"
  jq --arg transcript "$transcript" '.transcript_path = $transcript' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-main-transcript.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1

  is_pretool_deny "$out"
}

test_eci_gate_blocks_split_root_logical_main_transcript() {
  local proof_root input out physical_sessions logical_eci transcript
  proof_root="$(fresh_proof_root eci-split-root-logical-main)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  physical_sessions="$TMP_ROOT/home/.omp/agent/sessions"
  logical_eci="$TMP_ROOT/logical-eci"
  mkdir -p "$physical_sessions" "$logical_eci"
  ln -s "$physical_sessions" "$logical_eci/sessions"
  transcript="$logical_eci/sessions/main.jsonl"
  write_main_transcript "$transcript" || return 1
  input="$TMP_ROOT/eci-split-root-logical-main.json"
  jq --arg transcript "$transcript" '.transcript_path = $transcript' "$FIXTURES/eci-apply-patch-code.json" >"$input"
  out="$TMP_ROOT/eci-split-root-logical-main.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_HOME="$logical_eci" ECI_PROOF_ROOT="$proof_root" || return 1

  is_pretool_deny "$out"
}

test_reviewer_backend_parser_accepts_no_credential_backends() {
  (
    . "$ROOT/hooks/lib/reviewer-backend.sh"
    ECI_STOP_REVIEWER="" parse_reviewer_env ECI_STOP_REVIEWER &&
      [ "$REVIEWER_BACKEND" = "" ] &&
      ECI_STOP_REVIEWER="ollama:http://127.0.0.1:11434:qwen3:4b" parse_reviewer_env ECI_STOP_REVIEWER &&
      [ "$REVIEWER_BACKEND" = "ollama" ] &&
      [ "$REVIEWER_OLLAMA_HOST" = "http://127.0.0.1:11434" ] &&
      [ "$REVIEWER_OLLAMA_MODEL" = "qwen3:4b" ] &&
      ECI_STOP_REVIEWER="opencode-zen:https://zen.example:nemotron" parse_reviewer_env ECI_STOP_REVIEWER &&
      [ "$REVIEWER_BACKEND" = "opencode-zen" ] &&
      [ "$REVIEWER_OPENCODE_HOST" = "https://zen.example" ] &&
      [ "$REVIEWER_OPENCODE_MODEL" = "nemotron" ]
  )
}

test_reviewer_backend_parser_rejects_credential_backends() {
  (
    . "$ROOT/hooks/lib/reviewer-backend.sh"
    ! ECI_STOP_REVIEWER="claude" parse_reviewer_env ECI_STOP_REVIEWER &&
      ! ECI_STOP_REVIEWER="github-copilot:gpt-4.1" parse_reviewer_env ECI_STOP_REVIEWER &&
      ! ECI_STOP_REVIEWER="eci-as-role:reviewer" parse_reviewer_env ECI_STOP_REVIEWER &&
      ! ECI_STOP_REVIEWER="eci:eci" parse_reviewer_env ECI_STOP_REVIEWER &&
      ! ECI_STOP_REVIEWER="shell:agent" parse_reviewer_env ECI_STOP_REVIEWER
  )
}

test_reviewer_backend_parser_reports_structured_diagnostics() {
  local out status
  out="$TMP_ROOT/reviewer-backend-diagnostic.err"
  status=0
  (
    . "$ROOT/hooks/lib/reviewer-backend.sh"
    ECI_STOP_REVIEWER="claude" parse_reviewer_env ECI_STOP_REVIEWER
  ) 2>"$out" || status=$?
  [ "$status" -eq 1 ] || return 1
  grep -q '^\[ECI_REVIEWER_BACKEND_UNSUPPORTED\] ECI gate denied' "$out" || return 1
  grep -q 'phase=Stop' "$out" || return 1
  grep -q 'operation=external-review-config' "$out" || return 1
  grep -q 'subject=env=ECI_STOP_REVIEWER,backend=claude' "$out" || return 1
  grep -q 'reason: reviewer-backend: unknown ECI_STOP_REVIEWER=claude' "$out" || return 1
  grep -q 'remediation:' "$out" || return 1

  status=0
  (
    . "$ROOT/hooks/lib/reviewer-backend.sh"
    ECI_STOP_REVIEWER="ollama:not-a-backend-spec" parse_reviewer_env ECI_STOP_REVIEWER
  ) 2>"$out" || status=$?
  [ "$status" -eq 1 ] || return 1
  grep -q '^\[ECI_REVIEWER_BACKEND_MALFORMED\] ECI gate denied' "$out" || return 1
  grep -q 'reason: reviewer-backend: malformed ECI_STOP_REVIEWER=' "$out"
}

test_reviewer_schema_matches_rules() {
  jq -e '
    (.required | index("assistant_tail_quote")) and
    (.required | index("passes_completed")) and
    (.required | index("verdict")) and
    (.required | index("violations")) and
    (.properties.passes_completed.items.enum | index("tail")) and
    (.properties.passes_completed.items.enum | index("tools")) and
    (.properties.passes_completed.items.enum | index("checklist")) and
    (.properties.passes_completed.items.enum | index("agreements"))
  ' "$ROOT/hooks/lib/reviewer-schema.json" >/dev/null &&
    grep -q 'passes_completed' "$ROOT/hooks/reviewer-rules.md" &&
    grep -q 'assistant_tail_quote' "$ROOT/hooks/reviewer-rules.md"
}

test_compose_reviewer_prompt_uses_eci_sources() {
  local out
  out="$TMP_ROOT/reviewer-prompt.out"

  (
    . "$ROOT/hooks/lib/compose-reviewer-prompt.sh"
    compose_reviewer_prompt "$ROOT/hooks/reviewer-rules.md" >"$out"
  ) || return 1

  grep -q '# AGENTS.md' "$out" &&
    grep -q '# stop-checklist.md' "$out" &&
    grep -q '^# Response$' "$out" &&
    grep -q '^## Evidence$' "$out" &&
    ! grep -Eq '[.]claude' "$out"
}

test_compose_reviewer_prompt_reports_structured_diagnostics() {
  local out status
  out="$TMP_ROOT/reviewer-prompt-diagnostic.err"
  status=0
  (
    . "$ROOT/hooks/lib/compose-reviewer-prompt.sh"
    compose_reviewer_prompt "$TMP_ROOT/missing-reviewer-rules.md" >/dev/null
  ) 2>"$out" || status=$?
  [ "$status" -eq 1 ] || return 1
  grep -q '^\[ECI_REVIEW_PROMPT_WRAPPER_MISSING\] ECI gate denied' "$out" || return 1
  grep -q 'phase=Stop' "$out" || return 1
  grep -q 'operation=external-review-config' "$out" || return 1
  grep -q 'subject=path=' "$out" || return 1
  grep -q 'reason: compose_reviewer_prompt: wrapper not found:' "$out" || return 1
  grep -q 'remediation:' "$out"
}

test_reviewer_filter_keeps_user_history_agreement_rules() {
  local body
  body="$TMP_ROOT/reviewer-filter-user-history.md"
  cat >"$body" <<'EOF'
## USER_HISTORY

<entry>USER: Always run bash hooks/tests/run.sh before stopping.</entry>

## CURRENT_TURN

<entry>ASSISTANT: I skipped it.</entry>
<entry>ASSISTANT: I skipped it because the database migration failed.</entry>
EOF

  (
    . "$ROOT/hooks/lib/reviewer-filter.sh"
    REVIEWER_FILTER_CORPUS_FILES="$ROOT/hooks/stop-checklist.md"
    kept=$(filter_violations '{"verdict":"fail","violations":[{"rule":"Always run bash hooks/tests/run.sh before stopping.","evidence":"I skipped it."}]}' "$body")
    paraphrased=$(filter_violations '{"verdict":"fail","violations":[{"rule":"You must execute the requested hook test suite before ending the turn.","evidence":"I skipped it."}]}' "$body")
    fabricated=$(filter_violations '{"verdict":"fail","violations":[{"rule":"Always whistle three times before stopping.","evidence":"I skipped it."}]}' "$body")
    current_copy=$(filter_violations '{"verdict":"fail","violations":[{"rule":"I skipped it because the database migration failed.","evidence":"I skipped it because the database migration failed."}]}' "$body")
    [ "$(printf '%s' "$kept" | jq -r '.verdict')" = "fail" ] &&
      [ "$(printf '%s' "$kept" | jq '.violations | length')" = "1" ] &&
      [ "$(printf '%s' "$paraphrased" | jq -r '.verdict')" = "fail" ] &&
      [ "$(printf '%s' "$paraphrased" | jq '.violations | length')" = "1" ] &&
      [ "$(printf '%s' "$fabricated" | jq -r '.verdict')" = "pass" ] &&
      [ "$(printf '%s' "$fabricated" | jq '.violations | length')" = "0" ] &&
      [ "$(printf '%s' "$current_copy" | jq -r '.verdict')" = "pass" ] &&
      [ "$(printf '%s' "$current_copy" | jq '.violations | length')" = "0" ]
  )
}

test_system_reviewer_slices_sanitized_eci_transcript() {
  local proof_root input out transcript body
  proof_root="$(fresh_proof_root reviewer-slice)"
  transcript="$(main_reviewer_transcript_path)"
  install_reviewer_transcript_fixture "$FIXTURES/reviewer-main-transcript.jsonl" "$transcript" || return 1
  input="$TMP_ROOT/reviewer-slice.json"
  jq --arg cwd "$ROOT" --arg transcript "$transcript" \
    '.cwd = $cwd | .transcript_path = $transcript' "$FIXTURES/stop-basic.json" >"$input"
  out="$TMP_ROOT/reviewer-slice.out"
  body="$TMP_ROOT/reviewer-slice-body.md"

  run_hook "$out" "$ROOT/hooks/system-prompt-reviewer.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" \
    ECI_STOP_REVIEWER="ollama:http://127.0.0.1:11434:qwen3:4b" \
    ECI_REVIEWER_FAKE_RESULT='{"assistant_tail_quote":"Done.","passes_completed":["tail","tools","checklist","agreements"],"verdict":"pass","violations":[]}' \
    ECI_REVIEWER_DEBUG_BODY_PATH="$body" || return 1

  expect_no_output "$out" &&
    grep -q '## USER_HISTORY' "$body" &&
    grep -q 'Earlier request: inspect the hook config.' "$body" &&
    grep -q '## CURRENT_TURN' "$body" &&
    grep -q 'Current request: implement the ECI reviewer hook.' "$body" &&
    grep -q 'TOOL_RESULT:' "$body" &&
    ! grep -q 'Earlier response should not appear in USER_HISTORY.' "$body"
}

test_system_reviewer_skips_vcs_when_eci_active() {
  local proof_root input out transcript body marker
  proof_root="$(fresh_proof_root reviewer-eci-vcs-skip)"
  transcript="$(main_reviewer_transcript_path)"
  install_reviewer_transcript_fixture "$FIXTURES/reviewer-main-transcript.jsonl" "$transcript" || return 1
  marker="$proof_root/t00-session/eci_active"
  mkdir -p "$(dirname "$marker")" || return 1
  printf 'scope: reviewer skip\n' >"$marker"
  input="$TMP_ROOT/reviewer-eci-vcs-skip.json"
  jq --arg cwd "$ROOT" --arg transcript "$transcript" \
    '.cwd = $cwd | .transcript_path = $transcript' "$FIXTURES/stop-basic.json" >"$input"
  out="$TMP_ROOT/reviewer-eci-vcs-skip.out"
  body="$TMP_ROOT/reviewer-eci-vcs-skip-body.md"

  run_hook "$out" "$ROOT/hooks/system-prompt-reviewer.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" \
    ECI_STOP_REVIEWER="ollama:http://127.0.0.1:11434:qwen3:4b" \
    ECI_REVIEWER_FAKE_RESULT='{"assistant_tail_quote":"Done.","passes_completed":["tail","tools","checklist","agreements"],"verdict":"pass","violations":[]}' \
    ECI_REVIEWER_DEBUG_BODY_PATH="$body" || return 1

  expect_no_output "$out" &&
    grep -q '## VCS_STATUS' "$body" &&
    grep -q 'skipped: ECI active' "$body" &&
    grep -q '## DIFF' "$body" &&
    grep -q 'skipped: same reason as VCS_STATUS' "$body"
}

write_fake_ps_with_secrets() {
  local dir="$1"
  local openai_key password bearer_token aws_access_key github_token
  mkdir -p "$dir" || return 1
  openai_key="$(redaction_fixture_value openai-api-key)" || return 1
  password="$(redaction_fixture_value password)" || return 1
  bearer_token="$(redaction_fixture_value bearer-token)" || return 1
  aws_access_key="$(redaction_fixture_value aws-access-key)" || return 1
  github_token="$(redaction_fixture_value github-token)" || return 1

  cat >"$dir/ps" <<SH
#!/usr/bin/env bash
cat <<'PS'
101 1 10 S python worker.py --api-key=$openai_key --password $password Authorization: Bearer $bearer_token
102 1 11 S node service.js AWS_SECRET_ACCESS_KEY=$aws_access_key token=$github_token
103 1 12 S $ROOT/hooks/system-prompt-reviewer.sh --token should_skip
104 1 13 S ./service --safe flag
PS
SH
  chmod +x "$dir/ps"
}

test_system_reviewer_redacts_background_process_secrets() {
  local proof_root input out transcript body fake_bin background
  local openai_key password bearer_token aws_access_key github_token
  proof_root="$(fresh_proof_root reviewer-redacts-processes)"
  transcript="$(main_reviewer_transcript_path)"
  install_reviewer_transcript_fixture "$FIXTURES/reviewer-main-transcript.jsonl" "$transcript" || return 1
  input="$TMP_ROOT/reviewer-redacts-processes.json"
  jq --arg cwd "$ROOT" --arg transcript "$transcript" \
    '.cwd = $cwd | .transcript_path = $transcript' "$FIXTURES/stop-basic.json" >"$input"
  out="$TMP_ROOT/reviewer-redacts-processes.out"
  body="$TMP_ROOT/reviewer-redacts-processes-body.md"
  background="$TMP_ROOT/reviewer-redacts-processes-background.md"
  fake_bin="$TMP_ROOT/fake-ps-bin"
  write_fake_ps_with_secrets "$fake_bin" || return 1
  openai_key="$(redaction_fixture_value openai-api-key)" || return 1
  password="$(redaction_fixture_value password)" || return 1
  bearer_token="$(redaction_fixture_value bearer-token)" || return 1
  aws_access_key="$(redaction_fixture_value aws-access-key)" || return 1
  github_token="$(redaction_fixture_value github-token)" || return 1

  run_hook "$out" "$ROOT/hooks/system-prompt-reviewer.sh" "$input" \
    PATH="$fake_bin:$PATH" ECI_PROOF_ROOT="$proof_root" \
    ECI_STOP_REVIEWER="ollama:http://127.0.0.1:11434:qwen3:4b" \
    ECI_REVIEWER_FAKE_RESULT='{"assistant_tail_quote":"Done.","passes_completed":["tail","tools","checklist","agreements"],"verdict":"pass","violations":[]}' \
    ECI_REVIEWER_DEBUG_BODY_PATH="$body" || return 1

  awk '/## BACKGROUND_PROCESSES/{flag=1; next} flag{print}' "$body" >"$background"
  expect_no_output "$out" &&
    grep -q '## BACKGROUND_PROCESSES' "$body" &&
    grep -q './service --safe flag' "$background" &&
    grep -q '\[REDACTED\]' "$background" &&
    file_lacks_values "$background" "$openai_key" "$password" "$bearer_token" "$aws_access_key" "$github_token" &&
    ! grep -Eq 'should_skip|system-prompt-reviewer\.sh' "$background"
}

test_system_reviewer_renders_response_item_tool_events() {
  local proof_root input out transcript body
  proof_root="$(fresh_proof_root reviewer-response-item)"
  transcript="$(main_reviewer_transcript_path)"
  install_reviewer_transcript_fixture "$FIXTURES/reviewer-response-item-transcript.jsonl" "$transcript" || return 1
  input="$TMP_ROOT/reviewer-response-item.json"
  jq --arg cwd "$ROOT" --arg transcript "$transcript" \
    '.cwd = $cwd | .transcript_path = $transcript' "$FIXTURES/stop-basic.json" >"$input"
  out="$TMP_ROOT/reviewer-response-item.out"
  body="$TMP_ROOT/reviewer-response-item-body.md"

  run_hook "$out" "$ROOT/hooks/system-prompt-reviewer.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" \
    ECI_STOP_REVIEWER="ollama:http://127.0.0.1:11434:qwen3:4b" \
    ECI_REVIEWER_FAKE_RESULT='{"assistant_tail_quote":"Done.","passes_completed":["tail","tools","checklist","agreements"],"verdict":"pass","violations":[]}' \
    ECI_REVIEWER_DEBUG_BODY_PATH="$body" || return 1

  expect_no_output "$out" &&
    grep -Fq 'ASSISTANT: [tool_use=functions.exec_command input={"command":"sed -n' "$body" &&
    grep -q 'TOOL_RESULT:' "$body" &&
    grep -q 'hooks/system-prompt-reviewer.sh' "$body"
}

test_stop_reviewer_timeout_and_hook_wiring() {
  jq -e '
    ([.hooks.PreToolUse[]? | select(.matcher == "^Bash$") | .hooks[]?
      | select((.command // "") | test("/validate-bash\\.sh"))]
      | length == 1 and all(.timeout == 75))
  ' "$ROOT/hooks.json" >/dev/null
}

test_admission_inputs_are_bounded() {
  (cd "$ROOT/hooks/lib/eci-hook-helper-go" && /usr/lib/go-1.26/bin/go test -count=1 ./... >/dev/null)
}

test_hash_fails_open_without_sha256_or_python() {
  local bin_dir out
  bin_dir="$TMP_ROOT/hash-no-sha-no-python-bin"
  out="$TMP_ROOT/hash-no-sha-no-python.out"
  mkdir -p "$bin_dir" || return 1

  if PATH="$bin_dir" /bin/bash -c '. "$1"; eci_hash_string value' \
      bash "$ROOT/hooks/lib/eci-proof-state.sh" >"$out" 2>/dev/null; then
    return 1
  fi
  [ ! -s "$out" ]
}

test_hook_tests_leave_no_python_bytecode_cache() {
  [ -z "$(find "$ROOT/hooks" -type d -name __pycache__ -print -quit)" ]
}

test_eci_gate_allows_markdown_only_apply_patch() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-markdown)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-markdown.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-apply-patch-markdown.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_allows_markdown_only_edit_payloads() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-markdown-edit)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"

  out="$TMP_ROOT/eci-edit-markdown.out"
  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-edit-markdown.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" || return 1

  out="$TMP_ROOT/eci-multiedit-markdown.out"
  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-multiedit-markdown.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out"
}

test_eci_gate_allows_markdown_only_write_payload() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-markdown-write)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-write-markdown.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-write-markdown.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  expect_no_output "$out"
}

test_eci_gate_denies_code_write_payload() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-code-write)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-write-code.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-write-code.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  is_edit_routing_deny "$out"
}

test_eci_gate_denies_direct_write_to_active_control_file() {
  local proof_root input out control
  proof_root="$(fresh_proof_root eci-control-write)"
  control="$proof_root/t00-session/eci_active"
  mkdir -p "$proof_root/t00-session" || return 1
  input="$TMP_ROOT/eci-control-write.json"
  out="$TMP_ROOT/eci-control-write.out"
  jq -n --arg cwd "$ROOT" --arg path "$control" \
    '{session_id:"t00-session",cwd:$cwd,tool_name:"Write",tool_input:{file_path:$path,content:"scope: forged\n"}}' \
    >"$input" || return 1
  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "ECI_CONTROL_OWNER_REQUIRED"
}

test_eci_gate_allows_current_session_high_level_log_and_anchor() {
  local proof_root input out log anchor ledger_dir
  proof_root="$(fresh_proof_root eci-anchor-edit)"
  ledger_dir="$TMP_ROOT/home/.agent/context/sessions/t00-session"
  mkdir -p "$ledger_dir" || return 1
  log="$ledger_dir/high_level_log.jsonl"
  anchor="$ledger_dir/high_level_log.anchor"
  printf '%s\n' '{"event":"decision","summary":"coordinator ledger update"}' >"$log"
  printf '%s\n' 'coordinator anchor update' >"$anchor"
  input="$TMP_ROOT/eci-anchor-edit.json"
  jq -n --arg cwd "$ROOT" --arg path "$log" \
    '{session_id:"t00-session",cwd:$cwd,tool_name:"Write",tool_input:{file_path:$path,content:"{\"event\":\"decision\",\"summary\":\"coordinator ledger update\"}\n"}}' >"$input"
  out="$TMP_ROOT/eci-anchor-edit.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" || return 1

  jq -n --arg cwd "$ROOT" --arg path "$anchor" \
    '{session_id:"t00-session",cwd:$cwd,tool_name:"Write",tool_input:{file_path:$path,content:"coordinator anchor update"}}' >"$input"
  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out"
}

test_eci_gate_denies_code_notebookedit_payload() {
  local proof_root input out
  proof_root="$(fresh_proof_root eci-code-notebook)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  input="$TMP_ROOT/eci-notebook.json"
  jq -n '{session_id:"t00-session",tool_name:"NotebookEdit",tool_input:{notebook_path:"src/file.ipynb"}}' >"$input"
  out="$TMP_ROOT/eci-notebook.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$input" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  is_edit_routing_deny "$out"
}

test_eci_gate_denies_mixed_markdown_code_patch() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-mixed)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-mixed.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-apply-patch-mixed.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  is_edit_routing_deny "$out"
}

test_eci_gate_blocks_multiedit_stdin() {
  local proof_root out
  proof_root="$(fresh_proof_root eci-multiedit)"
  mkdir -p "$proof_root/t00-session"
  printf 'scope: test\n' >"$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/eci-multiedit.out"

  run_hook "$out" "$ROOT/hooks/eci-active-gate.sh" "$FIXTURES/eci-multiedit.json" \
    ECI_PROOF_ROOT="$proof_root" || return 1

  is_edit_routing_deny "$out"
}

eci_gate_matcher() {
  jq -er '
    [
      .hooks.PreToolUse[]?
      | select(any(.hooks[]?; (.command // "") | test("/eci-active-gate\\.sh")))
      | .matcher
      | strings
    ] | join("|")
  ' "$ROOT/hooks.json"
}

matcher_has_tool() {
  local matcher="$1"
  local tool="$2"
  jq -e -n --arg matcher "$matcher" --arg tool "$tool" '
    $matcher | test("(^|[^A-Za-z0-9_])" + $tool + "([^A-Za-z0-9_]|$)")
  ' >/dev/null
}

test_multiedit_hook_config_is_wired() {
  local matcher
  matcher="$(eci_gate_matcher)" || return 1
  matcher_has_tool "$matcher" "MultiEdit"
}

test_notebookedit_hook_config_is_wired() {
  local matcher
  matcher="$(eci_gate_matcher)" || return 1
  matcher_has_tool "$matcher" "NotebookEdit"
}

test_validate_apply_patch_allows_vendorish_path() {
  local out
  out="$TMP_ROOT/apply-patch-vendorish.out"
  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$FIXTURES/validate-apply-patch-vendorish.json" || return 1
  expect_no_output "$out"
}

test_validate_edit_write_allows_vendorish_path() {
  local out
  out="$TMP_ROOT/edit-write-vendorish.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$FIXTURES/validate-edit-write-vendorish-edit.json" || return 1
  expect_no_output "$out"
}

test_validate_edit_write_allows_regular_repo_edit() {
  local repo input out
  repo="$TMP_ROOT/regular-repo-$$"
  mkdir -p "$repo/.git"
  echo "package x" >"$repo/file.go"
  input="$TMP_ROOT/edit-regular-eci.json"
  jq -n --arg fp "$repo/file.go" '{tool_name:"Edit",tool_input:{file_path:$fp,old_string:"x",new_string:"y"}}' >"$input"
  out="$TMP_ROOT/edit-regular-eci.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" || return 1
  expect_no_output "$out"
}

test_validate_edit_write_allows_notebookedit_same_sid_proof() {
  local proof_root input out
  proof_root="$(fresh_proof_root edit-write-notebook-same)"
  mkdir -p "$proof_root/t00-session"
  input="$TMP_ROOT/edit-write-notebook-same.json"
  jq -n --arg fp "$TMP_ROOT/home/.agent/context/sessions/t00-session/project-understanding.yaml" \
    '{session_id:"t00-session",tool_name:"NotebookEdit",tool_input:{notebook_path:$fp,new_string:"x"}}' >"$input"
  out="$TMP_ROOT/edit-write-notebook-same.out"

  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out"
}

test_validate_edit_write_allows_aliased_proof_dir() {
  local proof_root input out alias_dir
  proof_root="$(fresh_proof_root edit-write-alias)"
  alias_dir="$TMP_ROOT/home/.agent/context/sessions/mission-alias"
  mkdir -p "$alias_dir"
  printf 'session_id: t00-session\n' >"$alias_dir/.eci-proof-alias"
  input="$TMP_ROOT/edit-write-alias.json"
  jq -n --arg fp "$alias_dir/project-understanding.yaml" \
    '{session_id:"t00-session",tool_name:"Write",tool_input:{file_path:$fp,content:"schema: project-understanding/v1\nsections:\n  context:\n    title: \"Context\"\nrecords: {}\n"}}' >"$input"
  out="$TMP_ROOT/edit-write-alias.out"

  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out"
}

test_validate_edit_write_blocks_symlinked_alias_dir() {
  local proof_root input out alias_dir alias_target
  proof_root="$(fresh_proof_root edit-write-symlink-alias)"
  alias_target="$TMP_ROOT/edit-write-symlink-alias-target"
  mkdir -p "$alias_target"
  alias_dir="$proof_root/mission-alias"
  ln -s "$alias_target" "$alias_dir"
  input="$TMP_ROOT/edit-write-symlink-alias.json"
  jq -n --arg fp "$alias_dir/project-understanding.yaml" \
    '{session_id:"t00-session",tool_name:"Write",tool_input:{file_path:$fp,content:"x"}}' >"$input"
  out="$TMP_ROOT/edit-write-symlink-alias.out"

  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "unsafe session-scoped edit path"
}

test_validate_edit_write_blocks_relative_control_path() {
  local proof_root input out marker relative_path
  proof_root="$(fresh_proof_root edit-write-relative-control)"
  mkdir -p "$proof_root/t00-session"
  marker="$proof_root/t00-session/eci_active"
  printf '%s\n' 'scope: relative control path' >"$marker"
  relative_path="$(realpath --relative-to="$ROOT" "$marker")"
  input="$TMP_ROOT/edit-write-relative-control.json"
  jq -n --arg cwd "$ROOT" --arg fp "$relative_path" \
    '{session_id:"t00-session",cwd:$cwd,tool_name:"Write",tool_input:{file_path:$fp,content:"forged"}}' >"$input"
  out="$TMP_ROOT/edit-write-relative-control.out"

  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "session-scoped"
}

test_validate_edit_write_blocks_notebookedit_other_sid_proof() {
  local proof_root input out
  proof_root="$(fresh_proof_root edit-write-notebook-other)"
  mkdir -p "$TMP_ROOT/home/.agent/context/sessions/other-session"
  input="$TMP_ROOT/edit-write-notebook-other.json"
  jq -n --arg fp "$TMP_ROOT/home/.agent/context/sessions/other-session/project-understanding.yaml" \
    '{session_id:"t00-session",tool_name:"NotebookEdit",tool_input:{notebook_path:$fp,new_string:"x"}}' >"$input"
  out="$TMP_ROOT/edit-write-notebook-other.out"

  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "other-session"
}

test_validate_apply_patch_allows_aliased_proof_dir() {
  local proof_root input out alias_dir patch_path patch_text
  proof_root="$(fresh_proof_root apply-patch-alias)"
  alias_dir="$TMP_ROOT/home/.agent/context/sessions/mission-alias"
  mkdir -p "$alias_dir"
  printf 'session_id: t00-session\n' >"$alias_dir/.eci-proof-alias"
  patch_path="$alias_dir/project-understanding.yaml"
  printf '%s\n' 'schema: project-understanding/v1' 'sections:' '  context:' '    title: "Context"' 'records: {}' >"$patch_path"
  patch_text="$(printf '*** Begin Patch\n*** Update File: %s\n@@\n-    title: "Context"\n+    title: "Current context"\n*** End Patch\n' "$patch_path")"
  input="$TMP_ROOT/apply-patch-alias.json"
  jq -n --arg patch "$patch_text" \
    '{session_id:"t00-session",tool_name:"apply_patch",tool_input:{patch:$patch}}' >"$input"
  out="$TMP_ROOT/apply-patch-alias.out"

  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out"
}

test_validate_apply_patch_blocks_symlinked_alias_dir() {
  local proof_root input out alias_dir alias_target patch_path patch_text
  proof_root="$(fresh_proof_root apply-patch-symlink-alias)"
  alias_target="$TMP_ROOT/apply-patch-symlink-alias-target"
  mkdir -p "$alias_target"
  alias_dir="$proof_root/mission-alias"
  ln -s "$alias_target" "$alias_dir"
  patch_path="$alias_dir/project-understanding.yaml"
  patch_text="$(printf '*** Begin Patch\n*** Add File: %s\n+test\n*** End Patch\n' "$patch_path")"
  input="$TMP_ROOT/apply-patch-symlink-alias.json"
  jq -n --arg patch "$patch_text" \
    '{session_id:"t00-session",tool_name:"apply_patch",tool_input:{patch:$patch}}' >"$input"
  out="$TMP_ROOT/apply-patch-symlink-alias.out"

  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "unsafe session-scoped patch path"
}

test_validate_apply_patch_blocks_relative_control_path() {
  local proof_root input out marker relative_path patch_text
  proof_root="$(fresh_proof_root apply-patch-relative-control)"
  mkdir -p "$proof_root/t00-session"
  marker="$proof_root/t00-session/eci_active"
  printf '%s\n' 'scope: relative control path' >"$marker"
  relative_path="$(realpath --relative-to="$ROOT" "$marker")"
  patch_text="$(printf '*** Begin Patch\n*** Update File: %s\n-scope: relative control path\n+scope: forged\n*** End Patch\n' "$relative_path")"
  input="$TMP_ROOT/apply-patch-relative-control.json"
  jq -n --arg cwd "$ROOT" --arg patch "$patch_text" \
    '{session_id:"t00-session",cwd:$cwd,tool_name:"apply_patch",tool_input:{patch:$patch}}' >"$input"
  out="$TMP_ROOT/apply-patch-relative-control.out"

  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "session-scoped"
}

test_validate_apply_patch_blocks_other_sid_proof() {
  local proof_root input out patch_path patch_text
  proof_root="$(fresh_proof_root apply-patch-other-session)"
  mkdir -p "$TMP_ROOT/home/.agent/context/sessions/other-session"
  patch_path="$TMP_ROOT/home/.agent/context/sessions/other-session/project-understanding.yaml"
  patch_text="$(printf '*** Begin Patch\n*** Add File: %s\n+test\n*** End Patch\n' "$patch_path")"
  input="$TMP_ROOT/apply-patch-other-session.json"
  jq -n --arg patch "$patch_text" \
    '{session_id:"t00-session",tool_name:"apply_patch",tool_input:{patch:$patch}}' >"$input"
  out="$TMP_ROOT/apply-patch-other-session.out"

  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "other-session"
}

test_validate_apply_patch_blocks_uuid_sid_despite_alias_marker() {
  local proof_root input out uuid_dir patch_path patch_text
  proof_root="$(fresh_proof_root apply-patch-uuid-alias)"
  uuid_dir="$TMP_ROOT/home/.agent/context/sessions/019e55e6-6d85-7181-b197-43aa5708609b"
  mkdir -p "$uuid_dir"
  printf 'session_id: t00-session\n' >"$uuid_dir/.eci-proof-alias"
  patch_path="$uuid_dir/project-understanding.yaml"
  patch_text="$(printf '*** Begin Patch\n*** Add File: %s\n+test\n*** End Patch\n' "$patch_path")"
  input="$TMP_ROOT/apply-patch-uuid-alias.json"
  jq -n --arg patch "$patch_text" \
    '{session_id:"t00-session",tool_name:"apply_patch",tool_input:{patch:$patch}}' >"$input"
  out="$TMP_ROOT/apply-patch-uuid-alias.out"

  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "019e55e6-6d85-7181-b197-43aa5708609b"
}

ledger_basenames() {
  printf '%s\n' \
    "project-understanding.yaml" \
    "high_level_log.jsonl" \
    "high_level_log.anchor" \
    "latest-status-report.md" \
    "latest-status-report.yaml" \
    "forecasts.yaml" \
    "eci_active" \
    "eci_wait" \
    "eci_user_owned_wait.md" \
    "eci-required-critics.json" \
    "eci-required-critics.commit.1.ledger" \
    "eci-critic-identities.ledger" \
    "eci-acceptance-anchor" \
    "eci-acceptance-transaction" \
    "eci-teardown-complete" \
    "eci-prewrite-admitted.commit.1" \
    "baseline_head" \
    "baseline_head.binding" \
    "ate_nested_eci_active" \
    "ate_nested_eci_completion"
}

test_validate_bash_allows_subagent_low_level_work() {
  local proof_root transcript repo input out command tag failures=0
  proof_root="$(fresh_proof_root bash-subagent-low-level)"
  transcript="$(subagent_transcript_path)"
  write_subagent_transcript "$transcript" || return 1
  repo="$(make_git_repo bash-subagent-low-level)" || return 1
  printf 'next\n' >"$repo/file.txt"

  while IFS=$'\t' read -r tag command; do
    [ -n "$tag" ] || continue
    input="$TMP_ROOT/bash-subagent-low-level-$tag.json"
    out="$TMP_ROOT/bash-subagent-low-level-$tag.out"
    jq --arg cwd "$repo" --arg transcript "$transcript" --arg command "$command" \
      '.session_id = "t00-session" | .cwd = $cwd | .transcript_path = $transcript | .tool_input.command = $command' \
      "$FIXTURES/validate-bash-go-test-bare.json" >"$input" || return 1

    run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
    if ! expect_no_output "$out"; then
      note "$tag expected no hook output"
      cat "$out"
      failures=$((failures + 1))
    fi
  done <<EOF
mkdir	mkdir -p build/output
rg	rg -n ledger_basenames $ROOT/hooks/tests/run.sh
git-commit	git add file.txt && git commit -m low-level-work
EOF

  [ "$failures" -eq 0 ]
}

write_direct_ledger_input() {
  local tool="$1"
  local path="$2"
  local output="$3"
  local content="new"
  if [ "${path##*/}" = project-understanding.yaml ]; then
    content=$'schema: project-understanding/v1\nsections:\n  context:\n    title: "Context"\nrecords: {}\n'
  fi

  case "$tool" in
    Edit)
      jq -n --arg fp "$path" \
        '{session_id:"t00-session",tool_name:"Edit",tool_input:{file_path:$fp,old_string:"old",new_string:"new"}}' >"$output"
      ;;
    Write)
      jq -n --arg fp "$path" --arg content "$content" \
        '{session_id:"t00-session",tool_name:"Write",tool_input:{file_path:$fp,content:$content}}' >"$output"
      ;;
    MultiEdit)
      jq -n --arg fp "$path" \
        '{session_id:"t00-session",tool_name:"MultiEdit",tool_input:{file_path:$fp,edits:[{old_string:"old",new_string:"new"}]}}' >"$output"
      ;;
    NotebookEdit)
      jq -n --arg fp "$path" \
        '{session_id:"t00-session",tool_name:"NotebookEdit",tool_input:{notebook_path:$fp,new_string:"new"}}' >"$output"
      ;;
    *) return 1 ;;
  esac
}

write_apply_patch_input() {
  local path="$1"
  local output="$2"
  local patch_text

  patch_text="$(printf '*** Begin Patch\n*** Update File: %s\n@@\n-old\n+new\n*** End Patch\n' "$path")"
  jq -n --arg patch "$patch_text" \
    '{session_id:"t00-session",tool_name:"apply_patch",tool_input:{patch:$patch}}' >"$output"
}

test_validate_direct_and_apply_block_subagent_ledger_paths() {
  local proof_root ledger_dir transcript ledger tool input out tag
  proof_root="$(fresh_proof_root direct-subagent-ledger)"
  ledger_dir="$TMP_ROOT/home/.agent/context/sessions/t00-session"
  mkdir -p "$ledger_dir" || return 1
  transcript="$(subagent_transcript_path)"
  write_subagent_transcript "$transcript" || return 1

  while IFS= read -r ledger; do
    for tool in Edit Write MultiEdit NotebookEdit; do
      tag="direct-subagent-ledger-${tool}-${ledger//[^A-Za-z0-9]/-}"
      input="$TMP_ROOT/$tag.json"
      write_direct_ledger_input "$tool" "$ledger" "$input" || return 1
      jq --arg cwd "$ledger_dir" --arg transcript "$transcript" \
        '.cwd = $cwd | .transcript_path = $transcript' "$input" >"$input.tmp" &&
        mv "$input.tmp" "$input" || return 1
      out="$TMP_ROOT/$tag.out"

      run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
      is_pretool_deny "$out" &&
        json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "Only the main thread" || {
          printf '%s\n' "$tag expected ledger write denial" >&2
          cat "$out" >&2
          return 1
        }
    done

    tag="apply-subagent-ledger-${ledger//[^A-Za-z0-9]/-}"
    input="$TMP_ROOT/$tag.json"
    write_apply_patch_input "$ledger" "$input" || return 1
    jq --arg cwd "$ledger_dir" --arg transcript "$transcript" \
      '.cwd = $cwd | .transcript_path = $transcript' "$input" >"$input.tmp" &&
      mv "$input.tmp" "$input" || return 1
    out="$TMP_ROOT/$tag.out"

    run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
    is_pretool_deny "$out" &&
      json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "Only the main thread" || {
        printf '%s\n' "$tag expected ledger write denial" >&2
        cat "$out" >&2
        return 1
      }
  done < <(ledger_basenames)
}

test_validate_direct_and_apply_allow_main_ledger_paths() {
  local proof_root ledger_dir ledger tool input out tag
  proof_root="$(fresh_proof_root direct-main-ledger)"
  ledger_dir="$TMP_ROOT/home/.agent/context/sessions/t00-session"
  mkdir -p "$ledger_dir" || return 1

  while IFS= read -r ledger; do
    for tool in Edit Write MultiEdit NotebookEdit; do
      tag="direct-main-ledger-${tool}-${ledger//[^A-Za-z0-9]/-}"
      input="$TMP_ROOT/$tag.json"
      write_direct_ledger_input "$tool" "$ledger" "$input" || return 1
      jq --arg cwd "$ledger_dir" '.cwd = $cwd' "$input" >"$input.tmp" &&
        mv "$input.tmp" "$input" || return 1
      out="$TMP_ROOT/$tag.out"

      run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
      expect_no_output "$out" || {
        printf '%s\n' "$tag expected no denial" >&2
        cat "$out" >&2
        return 1
      }
    done

    tag="apply-main-ledger-${ledger//[^A-Za-z0-9]/-}"
    input="$TMP_ROOT/$tag.json"
    write_apply_patch_input "$ledger" "$input" || return 1
    jq --arg cwd "$ledger_dir" '.cwd = $cwd' "$input" >"$input.tmp" &&
      mv "$input.tmp" "$input" || return 1
    out="$TMP_ROOT/$tag.out"

    run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" || return 1
    expect_no_output "$out" || {
      printf '%s\n' "$tag expected no denial" >&2
      cat "$out" >&2
      return 1
    }
  done < <(ledger_basenames)
}

test_validate_bash_allows_write_into_submodule() {
  # Submodule write blocking is the file-edit tools' job (Edit/Write/MultiEdit
  # via validate-edit-write.sh). Bash is intentionally NOT gated on submodule
  # paths, so normal script/build workflows in submodules still work.
  local repo sub input out
  repo="$TMP_ROOT/sup-repo-bash-$$"
  sub="$repo/sub"
  mkdir -p "$repo/.git" "$sub"
  printf 'gitdir: %s/modules/sub\n' "$repo/.git" >"$sub/.git"
  input="$TMP_ROOT/bash-submod-eci.json"
  jq -n --arg cmd "echo y > $sub/file.go" '{tool_name:"Bash",tool_input:{command:$cmd}}' >"$input"
  out="$TMP_ROOT/bash-submod-eci.out"
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" || return 1
  local decision
  decision=$(jq -r '.hookSpecificOutput.permissionDecision // "no-decision"' <"$out" 2>/dev/null)
  [ -z "$decision" ] && decision="no-decision"

  if [ "$decision" != "deny" ]; then
    pass "validate-bash allows writes into a git submodule"
  else
    fail "validate-bash allows writes into a git submodule" \
      "decision=$decision; expected non-deny. Output: $(head -c 300 "$out")"
  fi
}

test_validate_edit_write_blocks_direct_edit_local_gomod_replace() {
  local out
  out="$TMP_ROOT/edit-write-gomod-edit.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$FIXTURES/validate-edit-write-gomod-local-edit.json" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "local relative replace"
}

test_validate_edit_write_blocks_direct_write_local_gomod_replace() {
  local out
  out="$TMP_ROOT/edit-write-gomod-write.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$FIXTURES/validate-edit-write-gomod-local-write.json" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "local relative replace"
}

test_validate_edit_write_blocks_direct_multiedit_local_gomod_replace() {
  local out
  out="$TMP_ROOT/edit-write-gomod-multiedit.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$FIXTURES/validate-edit-write-gomod-local-multiedit.json" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "local relative replace"
}

test_validate_edit_write_allows_unrelated_non_plan_edit() {
  local out
  out="$TMP_ROOT/edit-write-allow-unrelated.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$FIXTURES/validate-edit-write-allow-unrelated-edit.json" || return 1
  expect_no_output "$out"
}

test_validate_edit_write_allows_remote_write_gomod_replace() {
  local out
  out="$TMP_ROOT/edit-write-allow-remote-write.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$FIXTURES/validate-edit-write-allow-remote-write.json" || return 1
  expect_no_output "$out"
}

test_validate_edit_write_allows_remote_multiedit_gomod_replace() {
  local out
  out="$TMP_ROOT/edit-write-allow-remote-multiedit.out"
  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$FIXTURES/validate-edit-write-allow-remote-multiedit.json" || return 1
  expect_no_output "$out"
}

test_edit_write_hook_config_is_split_and_preserves_gates() {
  jq -e '
    def commands_for($matcher):
      [.hooks.PreToolUse[]? | select(.matcher == $matcher) | .hooks[]?.command];
    def has($commands; $suffix):
      any($commands[]?; contains($suffix));

    (commands_for("^apply_patch$") as $apply
      | commands_for("^(Edit|Write|MultiEdit|NotebookEdit)$") as $direct
      | has($apply; "/validate-apply-patch.sh") and
        (has($apply; "/validate-edit-write.sh") | not) and
        has($apply; "/security-reminder.py") and
        has($apply; "/eci-active-gate.sh") and
        has($apply; "/ate-orchestrator-gate.sh") and
        has($direct; "/validate-edit-write.sh") and
        (has($direct; "/validate-apply-patch.sh") | not) and
        has($direct; "/security-reminder.py") and
        has($direct; "/eci-active-gate.sh") and
        has($direct; "/ate-orchestrator-gate.sh"))
  ' "$ROOT/hooks.json" >/dev/null
}

test_ate_gate_allows_markdown_edits_for_lead() {
  local out
  out="$TMP_ROOT/ate-markdown-lead.out"
  run_hook "$out" "$ROOT/hooks/ate-orchestrator-gate.sh" "$FIXTURES/eci-apply-patch-markdown.json" \
    ECI_ROLE=lead || return 1
  expect_no_output "$out"
}

test_ate_gate_allows_markdown_edits_for_coordinator() {
  local out
  out="$TMP_ROOT/ate-markdown-coordinator.out"
  run_hook "$out" "$ROOT/hooks/ate-orchestrator-gate.sh" "$FIXTURES/eci-apply-patch-markdown.json" \
    ECI_ROLE=coordinator || return 1
  expect_no_output "$out"
}

test_validate_apply_patch_blocks_local_gomod_replace_from_patch() {
  local out
  out="$TMP_ROOT/apply-patch-gomod.out"
  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$FIXTURES/validate-apply-patch-gomod.json" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "local relative replace"
}

test_validate_apply_patch_blocks_local_gomod_replace_on_move_destination() {
  local out
  out="$TMP_ROOT/apply-patch-gomod-move.out"
  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$FIXTURES/validate-apply-patch-gomod-move.json" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "local relative replace"
}

test_validate_apply_patch_blocks_block_form_local_gomod_replace() {
  local out
  out="$TMP_ROOT/apply-patch-gomod-block.out"
  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$FIXTURES/validate-apply-patch-gomod-block.json" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "local relative replace"
}

test_validate_apply_patch_allows_remote_gomod_replace() {
  local out
  out="$TMP_ROOT/apply-patch-gomod-remote.out"
  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$FIXTURES/validate-apply-patch-gomod-remote.json" || return 1
  expect_no_output "$out"
}

test_validate_apply_patch_allows_unrelated_non_plan_edit() {
  local out
  out="$TMP_ROOT/apply-patch-unrelated.out"
  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$FIXTURES/validate-apply-patch-unrelated.json" || return 1
  expect_no_output "$out"
}

test_validate_bash_marks_shell_activity() {
  local proof_root input out
  proof_root="$(fresh_proof_root activity-bash)"
  input="$TMP_ROOT/activity-bash.json"
  jq --arg cwd "$ROOT" '.session_id = "t00-session" | .cwd = $cwd' "$FIXTURES/validate-bash-go-test-redirect.json" >"$input"
  out="$TMP_ROOT/activity-bash.out"

  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" &&
    [ -s "$proof_root/activity/sessions/t00-session/shell" ]
}

test_validate_bash_skips_read_only_shell_activity() {
  local proof_root input out command
  proof_root="$(fresh_proof_root activity-bash-read-only)"
  command='rg -n "eci-proof|ECI_PROOF_ROOT|proof_dir" . -S'
  input="$TMP_ROOT/activity-bash-read-only.json"
  jq --arg cwd "$ROOT" --arg command "$command" \
    '.session_id = "t00-session" | .cwd = $cwd | .tool_input.command = $command' \
    "$FIXTURES/validate-bash-go-test-bare.json" >"$input"
  out="$TMP_ROOT/activity-bash-read-only.out"

  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" &&
    [ ! -e "$proof_root/activity/sessions/t00-session/shell" ]
}

test_validate_bash_skips_read_only_shell_chain_activity() {
  local proof_root input out command
  proof_root="$(fresh_proof_root activity-bash-read-chain)"
  command="sed -n '1,90p' hooks/stop-gate.sh && sed -n '360,430p' hooks/stop-gate.sh && sed -n '660,745p' hooks/stop-gate.sh"
  input="$TMP_ROOT/activity-bash-read-chain.json"
  jq --arg cwd "$ROOT" --arg command "$command" \
    '.session_id = "t00-session" | .cwd = $cwd | .tool_input.command = $command' \
    "$FIXTURES/validate-bash-go-test-bare.json" >"$input"
  out="$TMP_ROOT/activity-bash-read-chain.out"

  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" &&
    [ ! -e "$proof_root/activity/sessions/t00-session/shell" ]
}

test_validate_bash_marks_redirected_read_shell_activity() {
  local proof_root input out command
  proof_root="$(fresh_proof_root activity-bash-read-redirect)"
  command="sed -n '1,5p' AGENTS.md > /tmp/eci-read-output.txt"
  input="$TMP_ROOT/activity-bash-read-redirect.json"
  jq --arg cwd "$ROOT" --arg command "$command" \
    '.session_id = "t00-session" | .cwd = $cwd | .tool_input.command = $command' \
    "$FIXTURES/validate-bash-go-test-bare.json" >"$input"
  out="$TMP_ROOT/activity-bash-read-redirect.out"

  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" &&
    [ -s "$proof_root/activity/sessions/t00-session/shell" ]
}

test_validate_bash_allows_main_eci_active_off() {
  local input out transcript command
  transcript="$TMP_ROOT/home/.omp/agent/sessions/eci-hooks-test-main-eci-off.jsonl"
  write_main_transcript "$transcript" || return 1
  command="$ROOT/bin/eci-active off /tmp/eci-disengage.md"
  input="$TMP_ROOT/bash-main-eci-off.json"
  jq --arg cwd "$ROOT" --arg transcript "$transcript" --arg command "$command" \
    '.cwd = $cwd | .transcript_path = $transcript | .tool_input.command = $command' \
    "$FIXTURES/validate-bash-go-test-bare.json" >"$input"
  out="$TMP_ROOT/bash-main-eci-off.out"

  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" HOME="$TMP_ROOT/home" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_main_eci_user_wait_state() {
  local input out transcript command
  transcript="$TMP_ROOT/home/.omp/agent/sessions/eci-hooks-test-main-eci-wait.jsonl"
  write_main_transcript "$transcript" || return 1
  command="~/.omp/agent/eci/bin/eci-active wait /tmp/eci-user-owned-wait.md"
  input="$TMP_ROOT/bash-main-eci-wait.json"
  jq --arg cwd "$ROOT" --arg transcript "$transcript" --arg command "$command" \
    '.cwd = $cwd | .transcript_path = $transcript | .tool_input.command = $command' \
    "$FIXTURES/validate-bash-go-test-bare.json" >"$input"
  out="$TMP_ROOT/bash-main-eci-wait.out"

  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" HOME="$TMP_ROOT/home" || return 1
  expect_no_output "$out"
}

test_validate_bash_ignores_quoted_eci_user_wait_text() {
  local input out transcript command
  transcript="$(subagent_transcript_path)"
  write_subagent_transcript "$transcript" || return 1
  for command in \
    "printf '%s\\n' 'eci-active wait /tmp/eci-user-owned-wait.md'" \
    "rg -n 'eci-active resume' hooks/validate-bash.sh"; do
    input="$TMP_ROOT/bash-subagent-eci-wait-quoted.json"
    jq --arg cwd "$ROOT" --arg transcript "$transcript" --arg command "$command" \
      '.cwd = $cwd | .transcript_path = $transcript | .tool_input.command = $command' \
      "$FIXTURES/validate-bash-go-test-bare.json" >"$input"
    out="$TMP_ROOT/bash-subagent-eci-wait-quoted.out"
    run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" HOME="$TMP_ROOT/home" || return 1
    expect_no_output "$out" || return 1
  done
}

test_validate_bash_allows_redirect_to_vendor_path() {
  local out
  out="$TMP_ROOT/bash-vendor-redirect.out"
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$FIXTURES/validate-bash-vendor-redirect.json" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_in_place_imports_path() {
  local out
  out="$TMP_ROOT/bash-imports-sed.out"
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$FIXTURES/validate-bash-imports-sed.json" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_vendor_test_with_tmp_redirect() {
  local out
  out="$TMP_ROOT/bash-vendor-go-test.out"
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$FIXTURES/validate-bash-vendor-go-test.json" || return 1
  expect_no_output "$out"
}

run_validate_bash_command() {
  local command="$1"
  local tag="$2"
  local input out

  input="$TMP_ROOT/${tag}.json"
  out="$TMP_ROOT/${tag}.out"
  jq --arg cwd "$ROOT" --arg command "$command" \
    '.cwd = $cwd | .tool_input.command = $command' \
    "$FIXTURES/validate-bash-go-test-bare.json" >"$input" || return 1
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" || return 1
  printf '%s\n' "$out"
}

is_make_memory_cap_deny() {
  is_pretool_deny "$1" &&
    json_field_contains "$1" '.hookSpecificOutput.permissionDecisionReason // empty' \
      "make must be run with a finite memory cap"
}

test_validate_bash_blocks_direct_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "make test" "bash-make-direct")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_env_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "env make test" "bash-make-env")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_sudo_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "sudo make test" "bash-make-sudo")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_command_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "command -- make test" "bash-make-command")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_exec_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "exec -- make test" "bash-make-exec")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_time_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "time make test" "bash-make-time")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_time_format_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command \
    "/usr/bin/time -f '%M' make test" \
    "bash-make-time-format")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_nice_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "nice make test" "bash-make-nice")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_timeout_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "timeout 30s make test" "bash-make-timeout")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_bash_c_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "bash -c 'make test'" "bash-make-bash-c")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_bash_lc_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "bash -lc 'make test'" "bash-make-bash-lc")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_sh_c_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "sh -c 'make test'" "bash-make-sh-c")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_xargs_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command "printf test | xargs make" "bash-make-xargs")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_find_exec_make_without_memory_cap() {
  local out
  out="$(run_validate_bash_command \
    "find . -maxdepth 0 -exec make test \\;" \
    "bash-make-find-exec")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_allows_systemd_run_property_capped_make() {
  local out
  out="$(run_validate_bash_command \
    "systemd-run --scope --property=MemoryMax=1G make test" \
    "bash-make-systemd-property")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_systemd_run_short_property_capped_make() {
  local out
  out="$(run_validate_bash_command \
    "systemd-run --scope -p MemoryMax=1G make test" \
    "bash-make-systemd-short-property")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_systemd_run_unit_capped_make() {
  local out
  out="$(run_validate_bash_command \
    "systemd-run --scope -u build -p MemoryMax=1G make test" \
    "bash-make-systemd-unit-capped")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_prlimit_as_capped_make() {
  local out
  out="$(run_validate_bash_command \
    "prlimit --as=1073741824 make test" \
    "bash-make-prlimit-as")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_prlimit_attached_v_capped_make() {
  local out
  out="$(run_validate_bash_command \
    "prlimit -v1048576 make test" \
    "bash-make-prlimit-v")" || return 1
  expect_no_output "$out"
}

test_validate_bash_blocks_systemd_run_without_memorymax_for_make() {
  local out
  out="$(run_validate_bash_command \
    "systemd-run --scope make test" \
    "bash-make-systemd-uncapped")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_systemd_run_unit_without_memorymax_for_make() {
  local out
  out="$(run_validate_bash_command \
    "systemd-run --scope -u build make test" \
    "bash-make-systemd-unit-uncapped")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_systemd_run_memoryhigh_for_make() {
  local out
  out="$(run_validate_bash_command \
    "systemd-run --scope -p MemoryHigh=1G make test" \
    "bash-make-systemd-memoryhigh")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_blocks_prlimit_unlimited_cap_for_make() {
  local out
  out="$(run_validate_bash_command \
    "prlimit --as=unlimited make test" \
    "bash-make-prlimit-unlimited")" || return 1
  is_make_memory_cap_deny "$out"
}

test_validate_bash_allows_non_make_command_with_make_substring() {
  local out
  out="$(run_validate_bash_command "cmake --version" "bash-make-substring")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_printf_make_word() {
  local out
  out="$(run_validate_bash_command "printf make" "bash-make-printf-word")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_echo_make_word() {
  local out
  out="$(run_validate_bash_command "echo make" "bash-make-echo-word")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_command_v_make_lookup() {
  local out
  out="$(run_validate_bash_command "command -v make" "bash-make-command-v")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_find_name_make_print() {
  local out
  out="$(run_validate_bash_command \
    "find . -name make -print" \
    "bash-make-find-name-print")" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_xargs_echo_make_word() {
  local out
  out="$(run_validate_bash_command \
    "printf make | xargs echo" \
    "bash-make-xargs-echo-word")" || return 1
  expect_no_output "$out"
}

test_validate_bash_denied_make_does_not_mark_activity() {
  local proof_root input out touched_count
  proof_root="$(fresh_proof_root bash-denied-make-no-activity)"
  input="$TMP_ROOT/bash-denied-make-no-activity.json"
  out="$TMP_ROOT/bash-denied-make-no-activity.out"
  jq --arg cwd "$ROOT" \
    '.session_id = "t00-session" | .cwd = $cwd | .tool_input.command = "make test"' \
    "$FIXTURES/validate-bash-go-test-bare.json" >"$input" || return 1

  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  touched_count="$(find "$proof_root/touched-repos/sessions/t00-session" -type f 2>/dev/null | wc -l)"

  is_make_memory_cap_deny "$out" &&
    [ ! -e "$proof_root/activity/sessions/t00-session/shell" ] &&
    [ "$touched_count" -eq 0 ]
}

test_validate_apply_patch_marks_edit_activity() {
  local proof_root input out
  proof_root="$(fresh_proof_root activity-apply-patch)"
  input="$TMP_ROOT/activity-apply-patch.json"
  jq --arg cwd "$ROOT" '.session_id = "t00-session" | .cwd = $cwd' "$FIXTURES/eci-apply-patch-markdown.json" >"$input"
  out="$TMP_ROOT/activity-apply-patch.out"

  run_hook "$out" "$ROOT/hooks/validate-apply-patch.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" &&
    [ -s "$proof_root/activity/sessions/t00-session/edit" ]
}

test_validate_edit_write_marks_edit_activity() {
  local proof_root input out
  proof_root="$(fresh_proof_root activity-edit-write)"
  input="$TMP_ROOT/activity-edit-write.json"
  jq --arg cwd "$ROOT" '.session_id = "t00-session" | .cwd = $cwd' "$FIXTURES/validate-edit-write-allow-unrelated-edit.json" >"$input"
  out="$TMP_ROOT/activity-edit-write.out"

  run_hook "$out" "$ROOT/hooks/validate-edit-write.sh" "$input" ECI_PROOF_ROOT="$proof_root" || return 1
  expect_no_output "$out" &&
    [ -s "$proof_root/activity/sessions/t00-session/edit" ]
}

test_validate_bash_blocks_bare_go_test() {
  local out
  out="$TMP_ROOT/bash-go-test-bare.out"
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$FIXTURES/validate-bash-go-test-bare.json" || return 1
  is_pretool_deny "$out" &&
    json_field_contains "$out" '.hookSpecificOutput.permissionDecisionReason // empty' "captured to a file"
}

test_validate_bash_allows_redirected_go_test() {
  local out
  out="$TMP_ROOT/bash-go-test-redirect.out"
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$FIXTURES/validate-bash-go-test-redirect.json" || return 1
  expect_no_output "$out"
}

test_validate_bash_allows_go_test_tee() {
  local out
  out="$TMP_ROOT/bash-go-test-tee.out"
  run_hook "$out" "$ROOT/hooks/validate-bash.sh" "$FIXTURES/validate-bash-go-test-tee.json" || return 1
  expect_no_output "$out"
}

test_security_reminder_explicit_opt_in_sees_workflow_move_destination() {
  local proof_root out helper
  proof_root="$(fresh_proof_root security-workflow-move)"
  helper="$(source "$ROOT/hooks/lib/eci-runtime-sync.sh" && eci_runtime_hook_helper_path)" || return 1
  out="$TMP_ROOT/security-workflow-move.out"
  env -u ECI_ROLE ENABLE_SECURITY_REMINDER=1 ECI_PROOF_ROOT="$proof_root" "$helper" security-reminder \
    <"$FIXTURES/security-workflow-move.json" >"$out" 2>"$out.err" || return 1
  expect_no_output "$out" &&
    json_field_contains "$out.err" '.systemMessage // empty' "GitHub Actions workflow"
}

test_pretooluse_edit_dispatch_separates_advisory_and_envelope() {
  local proof_root allowed_input denied_input allowed_out denied_out
  proof_root="$(fresh_proof_root edit-dispatch-envelope)"
  allowed_input="$TMP_ROOT/edit-dispatch-allowed.json"
  denied_input="$TMP_ROOT/edit-dispatch-denied.json"
  allowed_out="$TMP_ROOT/edit-dispatch-allowed.out"
  denied_out="$TMP_ROOT/edit-dispatch-denied.out"
  jq -cn --arg cwd "$ROOT" '{
    session_id: "t00-session",
    cwd: $cwd,
    tool_name: "Edit",
    tool_input: {
      file_path: "docs/notes/t00.md",
      old_string: "old",
      new_string: "child_process.exec(unsafe)"
    }
  }' >"$allowed_input" || return 1

  run_hook "$allowed_out" "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$allowed_input" \
    ECI_PROOF_ROOT="$proof_root" ENABLE_SECURITY_REMINDER=1 || return 1
  expect_no_output "$allowed_out" || return 1
  json_field_contains "$allowed_out.err" '.systemMessage // empty' "shell exec with dynamic input" || return 1

  mkdir -p "$proof_root/t00-session" || return 1
  jq -cn --arg cwd "$ROOT" --arg path "$proof_root/t00-session/eci_active" '{
    session_id: "t00-session",
    cwd: $cwd,
    tool_name: "Write",
    tool_input: {
      file_path: $path,
      content: "scope: forged\n"
    }
  }' >"$denied_input" || return 1

  run_hook "$denied_out" "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$denied_input" \
    ECI_PROOF_ROOT="$proof_root" ENABLE_SECURITY_REMINDER=1 || return 1
  [ "$(wc -l <"$denied_out" | tr -d '[:space:]')" -eq 1 ] || return 1
  jq -e '
    type == "object" and
    (keys | sort) == ["hookSpecificOutput"] and
    .hookSpecificOutput.hookEventName == "PreToolUse" and
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_CONTROL_OWNER_REQUIRED")) and
    (has("systemMessage") | not)
  ' "$denied_out" >/dev/null
}

write_mixed_eci_report() {
  local report="$1"

  cat >"$report" <<'EOF'
## ECI completion certificate

clean-pass: fixture ECI completion proof.
hard-escalation: retired marker must keep ECI active.

## Stop checklist walkthrough

- Fixture stop checklist item was reviewed.

## Incomplete compliance

- None.
EOF
}

test_audit_sync_checker_ok() {
  local out
  out="$TMP_ROOT/audit-sync-ok.out"
  bash "$ROOT/hooks/check-audit-sync.sh" >"$out" 2>"$out.err" || return 1
  grep -q "check-audit-sync: OK" "$out"
}

test_audit_sync_checker_direct_exec_ok() {
  local out
  out="$TMP_ROOT/audit-sync-direct-ok.out"
  "$ROOT/hooks/check-audit-sync.sh" >"$out" 2>"$out.err" || return 1
  grep -q "check-audit-sync: OK" "$out"
}

test_audit_sync_checker_detects_drift() {
  local hook_dir out status
  hook_dir="$TMP_ROOT/audit-sync-drift"
  mkdir -p "$hook_dir" || return 1
  cp "$ROOT/hooks/check-audit-sync.sh" "$hook_dir/check-audit-sync.sh" || return 1
  cp "$ROOT/hooks/stop-verification.md" "$hook_dir/stop-verification.md" || return 1
  sed '/rescanned:/d' "$ROOT/hooks/stop-checklist.md" >"$hook_dir/stop-checklist.md"
  out="$TMP_ROOT/audit-sync-drift.out"

  bash "$hook_dir/check-audit-sync.sh" >"$out" 2>"$out.err"
  status=$?

  [ "$status" -ne 0 ] &&
    grep -q "rescanned:" "$out"
}

test_audit_sync_checker_fails_when_synced_file_missing() {
  local hook_dir out status
  hook_dir="$TMP_ROOT/audit-sync-missing"
  mkdir -p "$hook_dir" || return 1
  cp "$ROOT/hooks/check-audit-sync.sh" "$hook_dir/check-audit-sync.sh" || return 1
  cp "$ROOT/hooks/stop-verification.md" "$hook_dir/stop-verification.md" || return 1
  out="$TMP_ROOT/audit-sync-missing.out"

  bash "$hook_dir/check-audit-sync.sh" >"$out" 2>"$out.err"
  status=$?

  [ "$status" -ne 0 ] &&
    grep -q "missing required file" "$out.err"
}

run_case "prompt state is silent and records HEAD" \
  test_prompt_state_is_silent_records_head_and_clears_bypass
run_case "prompt state marks /side prompts" \
  test_prompt_state_marks_side_prompt
run_case "prompt state skips state writes for invalid session" \
  test_prompt_state_skips_state_for_invalid_session
run_case "prompt state keeps session ECI marker silent" \
  test_prompt_state_keeps_session_eci_marker_silent
run_case "prompt state config is wired without temporary probe" \
  test_prompt_state_config_is_wired_without_probe
run_case "prompt state keeps nontrivial governance task silent" \
  test_prompt_state_keeps_nontrivial_governance_task_silent
run_case "prompt state keeps ECI typo silent" \
  test_prompt_state_keeps_eci_typo_silent
run_case "prompt state keeps hooks path governance task silent" \
  test_prompt_state_keeps_hooks_path_governance_task_silent
run_case "prompt state leaves React hook prompt silent" \
  test_prompt_state_leaves_react_hook_prompt_silent
run_case "prompt state leaves React app hooks path silent" \
  test_prompt_state_leaves_react_app_hooks_path_silent
run_case "prompt state leaves React src hooks path silent" \
  test_prompt_state_leaves_react_src_hooks_path_silent
run_case "prompt state leaves Express routing prompt silent" \
  test_prompt_state_leaves_express_routing_prompt_silent
run_case "prompt state leaves email rule prompt silent" \
  test_prompt_state_leaves_email_rule_prompt_silent
run_case "prompt state keeps system prompt task silent" \
  test_prompt_state_keeps_system_prompt_task_silent
run_case "prompt state keeps subagent Stop-hook prompt task silent" \
  test_prompt_state_keeps_subagent_stop_hook_prompt_task_silent
run_case "prompt state keeps Stop-hook prompt task silent" \
  test_prompt_state_keeps_stop_hook_prompt_task_silent
run_case "prompt state keeps AGENTS.md review task silent" \
  test_prompt_state_keeps_eci_review_task_silent
run_case "prompt state keeps hook behavior audit task silent" \
  test_prompt_state_keeps_hook_behavior_audit_task_silent
run_case "prompt state keeps hook tests task silent" \
  test_prompt_state_keeps_hook_tests_task_silent
run_case "prompt state keeps task routing task silent" \
  test_prompt_state_keeps_task_routing_task_silent
run_case "prompt state keeps agent routing protocol task silent" \
  test_prompt_state_keeps_agent_routing_protocol_task_silent
run_case "prompt state keeps nontrivial tasks use ECI question silent" \
  test_prompt_state_keeps_nontrivial_tasks_use_eci_question_silent
run_case "prompt state keeps ensure nontrivial tasks use ECI silent" \
  test_prompt_state_keeps_ensure_nontrivial_tasks_use_eci_silent
run_case "prompt state keeps route nontrivial tasks to ECI silent" \
  test_prompt_state_keeps_route_nontrivial_tasks_to_eci_silent
run_case "prompt state keeps routing nontrivial tasks use ECI silent" \
  test_prompt_state_keeps_routing_nontrivial_tasks_use_eci_silent
run_case "prompt state keeps ECI routing nontrivial tasks silent" \
  test_prompt_state_keeps_eci_routing_nontrivial_tasks_silent
run_case "prompt state keeps make nontrivial tasks use ECI silent" \
  test_prompt_state_keeps_make_nontrivial_tasks_use_eci_silent
run_case "prompt state keeps caveman switch task silent" \
  test_prompt_state_keeps_caveman_switch_task_silent
run_case "prompt state keeps plain stop gate task silent" \
  test_prompt_state_keeps_plain_stop_gate_task_silent
run_case "prompt state keeps hyphen stop gate task silent" \
  test_prompt_state_keeps_hyphen_stop_gate_task_silent
run_case "prompt state keeps stop gate script task silent" \
  test_prompt_state_keeps_stop_gate_script_task_silent
run_case "prompt state keeps stop checklist path task silent" \
  test_prompt_state_keeps_stop_checklist_path_task_silent
run_case "prompt state keeps stop checklist task silent" \
  test_prompt_state_keeps_stop_checklist_task_silent
run_case "prompt state keeps ECI config task silent" \
  test_prompt_state_keeps_eci_config_task_silent
run_case "prompt state leaves CLI installation instructions silent" \
  test_prompt_state_leaves_cli_installation_instructions_silent
run_case "prompt state leaves onboarding email instructions silent" \
  test_prompt_state_leaves_onboarding_email_instructions_silent
run_case "prompt state leaves React hook behavior prompt silent" \
  test_prompt_state_leaves_react_hook_behavior_prompt_silent
run_case "prompt state leaves React hook behavior audit silent" \
  test_prompt_state_leaves_react_hook_behavior_audit_silent
run_case "prompt state leaves React hook tests silent" \
  test_prompt_state_leaves_react_hook_tests_silent
run_case "prompt state leaves Express routing rule prompt silent" \
  test_prompt_state_leaves_express_routing_rule_prompt_silent
run_case "prompt state leaves Rust config silent" \
  test_prompt_state_leaves_rust_config_silent
run_case "prompt state leaves web app hooks.json silent" \
  test_prompt_state_leaves_web_app_hooks_json_silent
run_case "prompt state leaves caveman story silent" \
  test_prompt_state_leaves_caveman_story_silent
run_case "runtime hook probe historical evidence is sanitized" \
  test_runtime_hook_probe_historical_evidence_is_sanitized
run_case "session snapshot saves baseline and clears legacy skip_stop" \
  test_session_snapshot_saves_baseline_and_clears_legacy_skip
run_case "session snapshot skips ephemeral threads" \
  test_session_snapshot_skips_ephemeral_threads
run_case "session snapshot preserves fresh markers in old state dirs" \
  test_session_snapshot_preserves_fresh_markers_in_old_state_dirs
run_case "session snapshot refresh and active-marker cleanup safety" \
  "$ROOT/hooks/tests/test-session-snapshot-refresh.sh"
run_case "portable runtime file-lock selection and lifecycle" \
  "$ROOT/hooks/tests/test-eci-file-lock.sh"
run_case "direct lifecycle visibility accepts semantic current markers" \
  bash "$ROOT/hooks/tests/test-eci-lifecycle-visibility.sh"
run_case "ECI lifecycle source authority outside command validation" \
  bash "$ROOT/hooks/tests/test-lifecycle-source-authority.sh"
run_case "provider lifecycle dispatcher authority" \
  bash "$ROOT/hooks/tests/test-eci-provider-dispatch.sh"
run_case "planner-maintenance runtime receipt binding" \
  bash "$ROOT/hooks/tests/test-eci-maintain-planner.sh"
run_case "planner provenance uses a descriptor-pinned execution handoff" \
  bash "$ROOT/hooks/tests/test-eci-planner-fd-handoff.sh"
# TODO: Legacy on-based fixtures in mixed authority/command tests and unchanged
# session-snapshot/PostCompact hooks still need migration. Keep their generic
# checks and fixtures; the focused retirement tests below cover the new contract.
run_case "retired ECI lifecycle CLI leaves coordination marker-free" \
  bash "$ROOT/hooks/tests/test-eci-marker-retirement-cli.sh"
run_case "Stop hook retirement and deferred hook migrations" \
  bash "$ROOT/hooks/tests/test-retired-eci-marker-hooks.sh"
run_case "ECI runtime hooks use one canonical source root" \
  bash "$ROOT/hooks/tests/test-eci-runtime-single-source.sh"
run_case "PreToolUse copied-hook smoke" \
  bash "$ROOT/hooks/tests/test-stop-ordinary-continuation.sh"
run_case "Non-Stop command and edit marker scope" \
  bash "$ROOT/hooks/tests/test-eci-marker-scope.sh"
run_case "Edit marker overflow remains session scoped" \
  bash "$ROOT/hooks/tests/test-eci-fast-path.sh"
run_case "PostCompact ECI refresh and policy contracts" \
  "$ROOT/hooks/tests/test-eci-post-compact-refresh.sh"
run_case "PreToolUse configured-chain latency stays local and sub-second" \
  bash "$ROOT/hooks/tests/test-pretooluse-latency.sh"
run_case "configured PreToolUse consumers reach enabled guards" \
  bash "$ROOT/hooks/tests/test-pretooluse-configured-consumers.sh"
run_case "staged Go module and workspace policy" \
  bash "$ROOT/hooks/tests/test-pre-commit-go-mod.sh"
run_case "Go module hook parity" \
  bash "$ROOT/hooks/tests/test-go-mod-hook-parity.sh"
run_case "design versus implementation policy boundary" \
  bash "$ROOT/hooks/tests/test-policy-design-boundary.sh"
run_case "workflow skill routing and modular policy graph" \
  bash "$ROOT/hooks/tests/test-workflow-skill-routing.sh"
run_case "ECI edit and legacy control-path ownership" \
  bash "$ROOT/hooks/tests/test-eci-edit-control-paths.sh"
run_case "validate-bash ECI classifier contracts" \
  bash "$ROOT/hooks/tests/test-validate-bash-classifier.sh"
run_case "ECI aggregate commit tree CAS" \
  bash "$ROOT/hooks/tests/test-eci-aggregate-commit-tree-cas.sh"
run_case "ECI protected-first finite command-plan contracts" \
  bash "$ROOT/hooks/tests/test-eci-command-plan.sh"
run_case "ECI command syntax gating contracts" \
  bash "$ROOT/hooks/tests/test-eci-command-syntax-gating.sh"
run_case "side session start is silent and binds stop bypass" \
  test_side_session_start_is_silent_and_binds_stop_bypass
run_case "ECI gate allows code apply_patch when marker exists" \
  test_eci_gate_allows_code_apply_patch
run_case "ECI gate skips invalid session id" \
  test_eci_gate_skips_invalid_session_id
run_case "ECI gate denies malformed identity with active marker" \
  test_eci_gate_denies_malformed_identity_with_active_marker
run_case "ECI gate allows edits without routing message" \
  test_eci_gate_allows_edits_without_routing_message
run_case "ECI gate ignores code apply_patch from cwd marker" \
  test_eci_gate_ignores_code_apply_patch_from_cwd_state
run_case "ECI gate ignores ECI_ROLE for a normal edit" \
  test_eci_gate_ignores_eci_role_for_normal_edit
run_case "subagent helper reads oversize metadata prefix" \
  test_subagent_helper_reads_oversize_metadata_prefix
run_case "parent helper reads oversize metadata prefix" \
  test_parent_helper_reads_oversize_metadata_prefix
run_case "first-record helpers reject oversize record without final newline" \
  test_first_record_helpers_reject_oversize_without_final_newline
run_case "first-record helpers preserve boundary behavior" \
  test_first_record_helpers_preserve_boundary_behavior
run_case "omp status: main title-first depth two" \
  test_omp_status_main_title_first_depth_two
run_case "omp status: session_init on line four is subagent" \
  test_omp_status_session_init_line_four_is_subagent
run_case "omp status: parentSession header alone is subagent" \
  test_omp_status_parent_session_header_is_subagent
run_case "omp status: depth three without signals is subagent" \
  test_omp_status_depth_three_is_subagent
run_case "omp status: fails closed on missing, symlink, outside, empty, malformed" \
  test_omp_status_fails_closed
run_case "omp status: main transcript larger than 64 KiB is main" \
  test_omp_status_large_main_is_main
run_case "ECI gate allows spawned-agent transcript payload" \
  test_eci_gate_allows_spawned_agent_transcript_payload
run_case "ECI gate allows oversize spawned-agent transcript payload" \
  test_eci_gate_allows_oversize_spawned_agent_transcript_payload
run_case "ECI gate allows spawned-agent transcript through sessions-root symlink" \
  test_eci_gate_allows_spawned_agent_through_sessions_root_symlink
run_case "ECI gate allows split-root logical child transcript" \
  test_eci_gate_allows_split_root_logical_child_transcript
run_case "ECI gate blocks main transcript payload" \
  test_eci_gate_blocks_main_transcript_payload
run_case "ECI gate blocks split-root logical main transcript" \
  test_eci_gate_blocks_split_root_logical_main_transcript
run_case "reviewer backend parser accepts no-credential backends" \
  test_reviewer_backend_parser_accepts_no_credential_backends
run_case "reviewer backend parser rejects credential backends" \
  test_reviewer_backend_parser_rejects_credential_backends
run_case "reviewer backend parser reports structured diagnostics" \
  test_reviewer_backend_parser_reports_structured_diagnostics
run_case "reviewer schema matches reviewer rules" \
  test_reviewer_schema_matches_rules
run_case "reviewer prompt composition uses ECI sources" \
  test_compose_reviewer_prompt_uses_eci_sources
run_case "reviewer prompt composition reports structured diagnostics" \
  test_compose_reviewer_prompt_reports_structured_diagnostics
run_case "reviewer filter keeps user-history agreement rules" \
  test_reviewer_filter_keeps_user_history_agreement_rules
run_case "system reviewer slices sanitized ECI transcript" \
  test_system_reviewer_slices_sanitized_eci_transcript
run_case "system reviewer skips VCS context when ECI active" \
  test_system_reviewer_skips_vcs_when_eci_active
run_case "system reviewer redacts background process secrets" \
  test_system_reviewer_redacts_background_process_secrets
run_case "system reviewer renders response_item tool events" \
  test_system_reviewer_renders_response_item_tool_events
run_case "reviewer timeout and hook wiring are configured" \
  test_stop_reviewer_timeout_and_hook_wiring
run_case "admission inputs are bounded" \
  test_admission_inputs_are_bounded
run_case "hook tests leave no Python bytecode cache" \
  test_hook_tests_leave_no_python_bytecode_cache
run_case "ECI gate allows markdown-only apply_patch while marker exists" \
  test_eci_gate_allows_markdown_only_apply_patch
run_case "ECI gate allows markdown-only Edit and MultiEdit payloads" \
  test_eci_gate_allows_markdown_only_edit_payloads
run_case "ECI gate allows markdown-only Write payload" \
  test_eci_gate_allows_markdown_only_write_payload
run_case "ECI gate denies code Write payload" \
  test_eci_gate_denies_code_write_payload
run_case "ECI gate denies direct Write to active control file" \
  test_eci_gate_denies_direct_write_to_active_control_file
run_case "ECI gate allows current-session high-level log and anchor updates" \
  test_eci_gate_allows_current_session_high_level_log_and_anchor
run_case "ECI gate denies code NotebookEdit payload" \
  test_eci_gate_denies_code_notebookedit_payload
run_case "ECI gate denies mixed markdown/code apply_patch" \
  test_eci_gate_denies_mixed_markdown_code_patch
run_case "ECI gate blocks MultiEdit JSON stdin when marker exists" \
  test_eci_gate_blocks_multiedit_stdin
run_case "hooks.json edit matcher includes MultiEdit" \
  test_multiedit_hook_config_is_wired
run_case "hooks.json edit matcher includes NotebookEdit" \
  test_notebookedit_hook_config_is_wired
run_case "hooks.json splits apply_patch and direct edit validators while preserving gates" \
  test_edit_write_hook_config_is_split_and_preserves_gates
run_case "ATE gate allows markdown edits for lead role" \
  test_ate_gate_allows_markdown_edits_for_lead
run_case "ATE gate allows markdown edits for coordinator role" \
  test_ate_gate_allows_markdown_edits_for_coordinator
run_case "validate-apply-patch allows vendorish paths" \
  test_validate_apply_patch_allows_vendorish_path
run_case "validate-edit-write allows vendorish paths" \
  test_validate_edit_write_allows_vendorish_path
run_case "validate-edit-write allows edits in a regular non-submodule repo" \
  test_validate_edit_write_allows_regular_repo_edit
run_case "validate-edit-write allows NotebookEdit on own proof dir" \
  test_validate_edit_write_allows_notebookedit_same_sid_proof
run_case "validate-edit-write allows aliased proof dir" \
  test_validate_edit_write_allows_aliased_proof_dir
run_case "validate-edit-write blocks symlinked alias dir" \
  test_validate_edit_write_blocks_symlinked_alias_dir
run_case "validate-edit-write blocks relative control path" \
  test_validate_edit_write_blocks_relative_control_path
run_case "validate-edit-write blocks NotebookEdit on another session proof dir" \
  test_validate_edit_write_blocks_notebookedit_other_sid_proof
run_case "validate-apply-patch allows aliased proof dir" \
  test_validate_apply_patch_allows_aliased_proof_dir
run_case "validate-apply-patch blocks symlinked alias dir" \
  test_validate_apply_patch_blocks_symlinked_alias_dir
run_case "validate-apply-patch blocks relative control path" \
  test_validate_apply_patch_blocks_relative_control_path
run_case "validate-apply-patch blocks edits to another session proof dir" \
  test_validate_apply_patch_blocks_other_sid_proof
run_case "validate-apply-patch blocks UUID proof dir despite alias marker" \
  test_validate_apply_patch_blocks_uuid_sid_despite_alias_marker
run_case "validate-bash allows subagent low-level work" \
  test_validate_bash_allows_subagent_low_level_work
run_case "validate direct/apply blocks subagent ledger paths" \
  test_validate_direct_and_apply_block_subagent_ledger_paths
run_case "validate direct/apply allows main-thread ledger paths" \
  test_validate_direct_and_apply_allow_main_ledger_paths
run_case "validate-bash allows writes into a git submodule" \
  test_validate_bash_allows_write_into_submodule
run_case "validate-edit-write blocks direct Edit go.mod local replace" \
  test_validate_edit_write_blocks_direct_edit_local_gomod_replace
run_case "validate-edit-write blocks direct Write go.mod local replace" \
  test_validate_edit_write_blocks_direct_write_local_gomod_replace
run_case "validate-edit-write blocks direct MultiEdit go.mod local replace" \
  test_validate_edit_write_blocks_direct_multiedit_local_gomod_replace
run_case "validate-edit-write allows unrelated non-plan Edit" \
  test_validate_edit_write_allows_unrelated_non_plan_edit
run_case "validate-edit-write allows remote Write go.mod replace" \
  test_validate_edit_write_allows_remote_write_gomod_replace
run_case "validate-edit-write allows remote MultiEdit go.mod replace" \
  test_validate_edit_write_allows_remote_multiedit_gomod_replace
run_case "validate-apply-patch parses tool_input.patch go.mod replaces" \
  test_validate_apply_patch_blocks_local_gomod_replace_from_patch
run_case "validate-apply-patch blocks go.mod replace on Move to destination" \
  test_validate_apply_patch_blocks_local_gomod_replace_on_move_destination
run_case "validate-apply-patch blocks block-form go.mod local replace" \
  test_validate_apply_patch_blocks_block_form_local_gomod_replace
run_case "validate-apply-patch allows remote go.mod replace" \
  test_validate_apply_patch_allows_remote_gomod_replace
run_case "validate-apply-patch allows unrelated non-plan edit" \
  test_validate_apply_patch_allows_unrelated_non_plan_edit
run_case "validate-bash marks shell activity" \
  test_validate_bash_marks_shell_activity
run_case "validate-bash skips read-only shell activity" \
  test_validate_bash_skips_read_only_shell_activity
run_case "validate-bash skips read-only shell chain activity" \
  test_validate_bash_skips_read_only_shell_chain_activity
run_case "validate-bash marks redirected read shell activity" \
  test_validate_bash_marks_redirected_read_shell_activity
run_case "validate-bash allows main eci-active off" \
  test_validate_bash_allows_main_eci_active_off
run_case "validate-bash allows main ECI user wait state" \
  test_validate_bash_allows_main_eci_user_wait_state
run_case "validate-bash ignores quoted ECI user wait text" \
  test_validate_bash_ignores_quoted_eci_user_wait_text
run_case "validate-bash allows redirect to vendor paths" \
  test_validate_bash_allows_redirect_to_vendor_path
run_case "validate-bash allows in-place imports paths" \
  test_validate_bash_allows_in_place_imports_path
run_case "validate-bash allows vendor test with tmp redirect" \
  test_validate_bash_allows_vendor_test_with_tmp_redirect
run_case "validate-bash blocks direct make without memory cap" \
  test_validate_bash_blocks_direct_make_without_memory_cap
run_case "validate-bash blocks env make without memory cap" \
  test_validate_bash_blocks_env_make_without_memory_cap
run_case "validate-bash blocks sudo make without memory cap" \
  test_validate_bash_blocks_sudo_make_without_memory_cap
run_case "validate-bash blocks command make without memory cap" \
  test_validate_bash_blocks_command_make_without_memory_cap
run_case "validate-bash blocks exec make without memory cap" \
  test_validate_bash_blocks_exec_make_without_memory_cap
run_case "validate-bash blocks time make without memory cap" \
  test_validate_bash_blocks_time_make_without_memory_cap
run_case "validate-bash blocks time format make without memory cap" \
  test_validate_bash_blocks_time_format_make_without_memory_cap
run_case "validate-bash blocks nice make without memory cap" \
  test_validate_bash_blocks_nice_make_without_memory_cap
run_case "validate-bash blocks timeout make without memory cap" \
  test_validate_bash_blocks_timeout_make_without_memory_cap
run_case "validate-bash blocks bash -c make without memory cap" \
  test_validate_bash_blocks_bash_c_make_without_memory_cap
run_case "validate-bash blocks bash -lc make without memory cap" \
  test_validate_bash_blocks_bash_lc_make_without_memory_cap
run_case "validate-bash blocks sh -c make without memory cap" \
  test_validate_bash_blocks_sh_c_make_without_memory_cap
run_case "validate-bash blocks xargs make without memory cap" \
  test_validate_bash_blocks_xargs_make_without_memory_cap
run_case "validate-bash blocks find -exec make without memory cap" \
  test_validate_bash_blocks_find_exec_make_without_memory_cap
run_case "validate-bash allows systemd-run property capped make" \
  test_validate_bash_allows_systemd_run_property_capped_make
run_case "validate-bash allows systemd-run short property capped make" \
  test_validate_bash_allows_systemd_run_short_property_capped_make
run_case "validate-bash allows systemd-run unit capped make" \
  test_validate_bash_allows_systemd_run_unit_capped_make
run_case "validate-bash allows prlimit --as capped make" \
  test_validate_bash_allows_prlimit_as_capped_make
run_case "validate-bash allows prlimit attached -v capped make" \
  test_validate_bash_allows_prlimit_attached_v_capped_make
run_case "validate-bash blocks systemd-run without MemoryMax for make" \
  test_validate_bash_blocks_systemd_run_without_memorymax_for_make
run_case "validate-bash blocks systemd-run unit without MemoryMax for make" \
  test_validate_bash_blocks_systemd_run_unit_without_memorymax_for_make
run_case "validate-bash blocks systemd-run MemoryHigh-only for make" \
  test_validate_bash_blocks_systemd_run_memoryhigh_for_make
run_case "validate-bash blocks prlimit unlimited cap for make" \
  test_validate_bash_blocks_prlimit_unlimited_cap_for_make
run_case "validate-bash allows non-make command with make substring" \
  test_validate_bash_allows_non_make_command_with_make_substring
run_case "validate-bash allows printf make word" \
  test_validate_bash_allows_printf_make_word
run_case "validate-bash allows echo make word" \
  test_validate_bash_allows_echo_make_word
run_case "validate-bash allows command -v make lookup" \
  test_validate_bash_allows_command_v_make_lookup
run_case "validate-bash allows find name make print" \
  test_validate_bash_allows_find_name_make_print
run_case "validate-bash allows xargs echo make word" \
  test_validate_bash_allows_xargs_echo_make_word
run_case "validate-bash denied make does not mark shell activity or touched repo" \
  test_validate_bash_denied_make_does_not_mark_activity
run_case "validate-apply-patch marks edit activity" \
  test_validate_apply_patch_marks_edit_activity
run_case "validate-edit-write marks edit activity" \
  test_validate_edit_write_marks_edit_activity
run_case "validate-bash blocks bare go test" \
  test_validate_bash_blocks_bare_go_test
run_case "validate-bash allows redirected go test" \
  test_validate_bash_allows_redirected_go_test
run_case "validate-bash allows go test piped to tee" \
  test_validate_bash_allows_go_test_tee
run_case "explicit security reminder sees workflow Move to destination" \
  test_security_reminder_explicit_opt_in_sees_workflow_move_destination
run_case "security reminder is explicit opt-in" \
  bash "$ROOT/hooks/tests/test-security-reminder-opt-in.sh"
run_case "edit dispatch keeps advisory output outside the provider envelope" \
  test_pretooluse_edit_dispatch_separates_advisory_and_envelope
run_case "audit sync checker reports ok" \
  test_audit_sync_checker_ok
run_case "audit sync checker supports direct exec" \
  test_audit_sync_checker_direct_exec_ok
run_case "audit sync checker detects drift" \
  test_audit_sync_checker_detects_drift
run_case "audit sync checker fails when synced files are missing" \
  test_audit_sync_checker_fails_when_synced_file_missing

note "SUMMARY pass=$PASS_COUNT fail=$FAIL_COUNT xfail=$XFAIL_COUNT xpass=$XPASS_COUNT todo=$TODO_COUNT"

if [ "$FAIL_COUNT" -ne 0 ] || [ "$XPASS_COUNT" -ne 0 ]; then
  exit 1
fi
