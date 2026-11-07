#!/usr/bin/env bash

set -euo pipefail

ECI_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KIMI_ROOT="${KIMI_CODE_HOME:-${HOME:?}/.kimi-code}"
tmp_parent="${ECI_TMPDIR:-${HOME:?}/tmp}"
eci_device="$(stat -Lc '%d' -- "$ECI_ROOT" 2>/dev/null || true)"
tmp_device="$(stat -Lc '%d' -- "$tmp_parent" 2>/dev/null || true)"
if [ -z "$eci_device" ] || [ "$eci_device" != "$tmp_device" ]; then
  # Hardlink identity cases must be created on the same filesystem as the
  # provider source.  The fallback is fixture setup only; every production
  # hardlink assertion below remains strict.
  tmp_parent="$ECI_ROOT"
fi
TMP_ROOT="$(mktemp -d "$tmp_parent/.eci-command-plan.XXXXXX")"
HELPER_HARDLINK="$ECI_ROOT/hooks/lib/.eci-command-plan-test-helper-$BASHPID"
ECI_WORKER_SESSION_ID="11111111-1111-4111-8111-111111111111"
ECI_WORKER_TRANSCRIPT=""
cleanup_command_plan_test() {
  rm -f -- "$HELPER_HARDLINK"
  if [ -n "$ECI_WORKER_TRANSCRIPT" ]; then
    rm -f -- "$ECI_WORKER_TRANSCRIPT"
  fi
  rm -rf -- "$TMP_ROOT"
}
trap cleanup_command_plan_test EXIT
export XDG_CONFIG_HOME="$TMP_ROOT/xdg-config"
export XDG_STATE_HOME="$TMP_ROOT/xdg-state"
mkdir -p "$XDG_CONFIG_HOME/eci"
chmod 700 "$XDG_CONFIG_HOME" "$XDG_CONFIG_HOME/eci"
printf '%s\n' enforcing >"$XDG_CONFIG_HOME/eci/command-gate-mode"
chmod 600 "$XDG_CONFIG_HOME/eci/command-gate-mode"

make_marker() {
  local proof_root="$1" session_id="$2" cwd="$3"
  mkdir -p "$proof_root/$session_id"
  printf '%s\n' \
    'scope: command-plan regression' \
    "cwd: $cwd" \
    "session_id: $session_id" \
    'created_utc: 2026-08-21T00:00:00Z' \
    >"$proof_root/$session_id/eci_active"
}

run_hook() {
  local provider="$1" role="$2" active="$3" command="$4" output="$5"
  local root hook proof_root session_id cwd kimi_home now_ms wire transcript
  kimi_home="$KIMI_ROOT"
  transcript=""
  case "$provider" in
    eci)
      root="$ECI_ROOT"
      session_id="eci-$role-$active"
      if [ "$role" = worker ]; then
        session_id="$ECI_WORKER_SESSION_ID"
        if [ -z "$ECI_WORKER_TRANSCRIPT" ]; then
          ECI_WORKER_TRANSCRIPT="$(mktemp "$ECI_ROOT/sessions/eci-command-plan-test-worker-$BASHPID.XXXXXX.jsonl")"
          printf '%s\n' \
            '{"timestamp":"2026-08-21T00:00:00.000Z","type":"session_meta","payload":{"id":"11111111-1111-4111-8111-111111111111","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}}}' \
            >"$ECI_WORKER_TRANSCRIPT"
        fi
        transcript="$ECI_WORKER_TRANSCRIPT"
      fi
      ;;
    kimi)
      root="$KIMI_ROOT"
      session_id="session_11111111-1111-4111-8111-111111111111"
      if [ "$role" = worker ]; then
        kimi_home="$TMP_ROOT/kimi-worker-home"
        wire="$kimi_home/sessions/2026-08-21/$session_id/agents/main/wire.jsonl"
        mkdir -p "$(dirname "$wire")"
        now_ms="$(( $(date +%s%N) / 1000000 ))"
        printf '%s\n' \
          '{"protocol_version":"1.5"}' \
          "{\"event\":{\"type\":\"tool.call\",\"name\":\"Agent\",\"toolCallId\":\"agent-call\",\"time\":$((now_ms - 5000))}}" \
          >"$wire"
      fi
      ;;
    *) return 2 ;;
  esac
  hook="$root/hooks/validate-bash.sh"
  proof_root="$TMP_ROOT/$provider-$role-$active-proof"
  cwd="$ECI_ROOT"
  mkdir -p "$proof_root"
  if [ "$active" = active ]; then
    make_marker "$proof_root" "$session_id" "$cwd"
  fi
  if [ -n "$transcript" ]; then
    jq -cn --arg session_id "$session_id" --arg cwd "$cwd" \
      --arg transcript "$transcript" --arg command "$command" \
      '{session_id:$session_id,tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}'
  else
    jq -cn --arg session_id "$session_id" --arg cwd "$cwd" --arg command "$command" \
      '{session_id:$session_id,tool_name:"Bash",cwd:$cwd,tool_input:{command:$command}}'
  fi |
    HOME="$HOME" ECI_HOME="$ECI_ROOT" KIMI_CODE_HOME="$kimi_home" \
      ECI_PROOF_ROOT="$proof_root" KIMI_PROOF_ROOT="$proof_root" \
      ECI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" \
      KIMI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" \
      ECI_ROLE="$([ "$role" = worker ] && printf subagent || printf coordinator)" \
      KIMI_ROLE="$([ "$role" = worker ] && printf subagent || printf coordinator)" \
      PATH="$root/bin:$PATH" bash "$hook" >"$output"
}

assert_allowed() {
  local provider="$1" role="$2" active="$3" command="$4" output
  output="$TMP_ROOT/output.json"
  run_hook "$provider" "$role" "$active" "$command" "$output"
  if [ -s "$output" ]; then
    printf 'expected allow: provider=%s role=%s active=%s command=%q\n' \
      "$provider" "$role" "$active" "$command" >&2
    cat "$output" >&2
    return 1
  fi
}

assert_denied() {
  local provider="$1" role="$2" active="$3" command="$4" code="$5" output
  output="$TMP_ROOT/output.json"
  run_hook "$provider" "$role" "$active" "$command" "$output"
  jq -e --arg code "[$code]" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("phase=PreToolUse")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'expected %s denial: provider=%s role=%s active=%s command=%q\n' \
      "$code" "$provider" "$role" "$active" "$command" >&2
    cat "$output" >&2
    return 1
  }
}

assert_gate_denied() {
  local provider="$1" role="$2" active="$3" command="$4" output
  output="$TMP_ROOT/output.json"
  run_hook "$provider" "$role" "$active" "$command" "$output"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("phase=PreToolUse")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'expected gate denial: provider=%s role=%s active=%s command=%q\n' \
      "$provider" "$role" "$active" "$command" >&2
    cat "$output" >&2
    return 1
  }
}

# assert_compound_keeps_direct_denial proves that a parser-attested compound
# cannot skip the named route selected by one of its direct segments.
#
# Example: `printf ok; rm -f target` must retain rm's direct-route denial,
# rather than inheriting printf's generic allow.
assert_compound_keeps_direct_denial() {
  local provider="$1" role="$2" active="$3" prefix="$4" segment="$5"
  local direct_output compound_output direct_code
  direct_output="$TMP_ROOT/direct-segment-output.json"
  compound_output="$TMP_ROOT/compound-segment-output.json"
  run_hook "$provider" "$role" "$active" "$segment" "$direct_output"
  if ! direct_code="$(jq -r '
    .hookSpecificOutput.permissionDecisionReason |
    capture("\\[(?<code>ECI_[A-Z0-9_]+)\\]").code
  ' "$direct_output" 2>/dev/null)" || [ -z "$direct_code" ]; then
    printf 'expected direct named-route denial: provider=%s role=%s active=%s command=%q\n' \
      "$provider" "$role" "$active" "$segment" >&2
    cat "$direct_output" >&2
    return 1
  fi
  run_hook "$provider" "$role" "$active" "$prefix; $segment" "$compound_output"
  jq -e --arg code "[$direct_code]" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_COMMAND_NONLITERAL_DENIED]") | not)
  ' "$compound_output" >/dev/null || {
    printf 'expected compound to retain direct %s denial: provider=%s role=%s active=%s command=%q\n' \
      "$direct_code" "$provider" "$role" "$active" "$prefix; $segment" >&2
    cat "$compound_output" >&2
    return 1
  }
}

assert_plan_denied() {
  local provider="$1" role="$2" active="$3" command="$4" code="$5" output
  output="$TMP_ROOT/output.json"
  run_hook "$provider" "$role" "$active" "$command" "$output"
  jq -e --arg code "[$code]" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("provider=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("role=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("marker=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("segment=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("argv_index=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("byte_offset=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("token=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("path=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("predicate=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("rejected segment=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'expected compiler-specific %s denial: provider=%s role=%s active=%s command=%q\n' \
      "$code" "$provider" "$role" "$active" "$command" >&2
    cat "$output" >&2
    return 1
  }
}

assert_lifecycle_identity_denied() {
  local provider="$1" command="$2" token="$3" argv_index="$4" byte_offset="$5"
  local predicate="$6" expected_fragment="$7" observed_fragment="$8" output
  output="$TMP_ROOT/output.json"
  run_hook "$provider" coordinator active "$command" "$output"
  jq -e \
    --arg token "$token" \
    --arg argv_index "$argv_index" \
    --arg byte_offset "$byte_offset" \
    --arg predicate "$predicate" \
    --arg expected_fragment "$expected_fragment" \
    --arg observed_fragment "$observed_fragment" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_PLAN_LIFECYCLE_IDENTITY_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=plan-segment")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("provider=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("role=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("marker=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("rejected command=")) and
    (.hookSpecificOutput.permissionDecisionReason | contains($expected_fragment)) and
    (.hookSpecificOutput.permissionDecisionReason | contains($observed_fragment)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:"))
  ' "$output" >/dev/null || {
    printf 'expected lifecycle identity denial: provider=%s command=%q\n' \
      "$provider" "$command" >&2
    cat "$output" >&2
    return 1
  }
}

assert_lifecycle_route_identity_denied() {
  local provider="$1" command="$2" expected_fragment="$3" observed_fragment="$4" output
  output="$TMP_ROOT/output.json"
  run_hook "$provider" coordinator active "$command" "$output"
  jq -e \
    --arg expected_fragment "$expected_fragment" \
    --arg observed_fragment "$observed_fragment" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIFECYCLE_IDENTITY_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=eci-lifecycle")) and
    (.hookSpecificOutput.permissionDecisionReason | contains($expected_fragment)) and
    (.hookSpecificOutput.permissionDecisionReason | contains($observed_fragment)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_PLAN_LIFECYCLE_IDENTITY_DENIED") | not)
  ' "$output" >/dev/null || {
    printf 'expected lifecycle-route identity denial: provider=%s command=%q\n' \
      "$provider" "$command" >&2
    cat "$output" >&2
    return 1
  }
}

assert_lifecycle_arguments_denied() {
  local provider="$1" command="$2" output
  output="$TMP_ROOT/output.json"
  run_hook "$provider" coordinator active "$command" "$output"
  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("[ECI_LIFECYCLE_ARGUMENTS_DENIED]")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("operation=eci-lifecycle")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("reason:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("remediation:")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_PLAN_LIFECYCLE_IDENTITY_DENIED") | not)
  ' "$output" >/dev/null || {
    printf 'expected lifecycle arguments denial: provider=%s command=%q\n' \
      "$provider" "$command" >&2
    cat "$output" >&2
    return 1
  }
}

assert_lifecycle_status_executes() {
  local provider="$1" target_provider="$2" target="$3" identity_name="$4"
  local session_id proof_root output
  case "$provider" in
    eci) session_id="eci-coordinator-active" ;;
    kimi) session_id="session_11111111-1111-4111-8111-111111111111" ;;
    *) return 2 ;;
  esac
  proof_root="$TMP_ROOT/$provider-coordinator-active-proof"
  output="$TMP_ROOT/$provider-$target_provider-status.txt"
  if ! env "$identity_name=$session_id" ECI_PROOF_ROOT="$proof_root" KIMI_PROOF_ROOT="$proof_root" \
    "$target" status >"$output" 2>&1; then
    printf 'expected lifecycle status execution: provider=%s target_provider=%s session=%s\n' \
      "$provider" "$target_provider" "$session_id" >&2
    cat "$output" >&2
    return 1
  fi
  if ! grep -F -- "session_id: $session_id" "$output" >/dev/null; then
    printf 'expected active lifecycle status output: provider=%s target_provider=%s session=%s\n' \
      "$provider" "$target_provider" "$session_id" >&2
    cat "$output" >&2
    return 1
  fi
}

run_lifecycle_identity_matrix() {
  local provider target_provider target identity_name wrong_name session_id fixed
  for provider in eci kimi; do
    case "$provider" in
      eci) session_id="eci-coordinator-active" ;;
      kimi) session_id="session_11111111-1111-4111-8111-111111111111" ;;
    esac
    for target_provider in eci kimi; do
      case "$target_provider" in
        eci)
          target="$ECI_ROOT/bin/eci-active"
          identity_name=ECI_SESSION_ID
          wrong_name=KIMI_SESSION_ID
          ;;
        kimi)
          target="$KIMI_ROOT/bin/eci-active"
          identity_name=KIMI_SESSION_ID
          wrong_name=ECI_SESSION_ID
          ;;
      esac
      fixed="env $identity_name=$session_id $target status"

      assert_allowed "$provider" coordinator active "$target status"
      assert_plan_denied "$provider" coordinator active \
        "$identity_name=$session_id $target status" ECI_PLAN_SYNTAX_DENIED
      assert_allowed "$provider" coordinator active "$fixed"
      assert_allowed "$provider" coordinator active \
        "env -- $identity_name=$session_id $target status"

      assert_lifecycle_route_identity_denied "$provider" \
        "env $wrong_name=$session_id $target status" \
        "expected_name=$identity_name" "observed_name=$wrong_name"
      assert_lifecycle_route_identity_denied "$provider" \
        "env -- $wrong_name=$session_id $target status" \
        "expected_name=$identity_name" "observed_name=$wrong_name"
      assert_allowed "$provider" coordinator active "$fixed"

      assert_lifecycle_route_identity_denied "$provider" \
        "env $identity_name=wrong-session $target status" \
        "expected=$identity_name=$session_id" "observed=$identity_name=wrong-session"
      assert_lifecycle_route_identity_denied "$provider" \
        "env -- $identity_name=wrong-session $target status" \
        "expected=$identity_name=$session_id" "observed=$identity_name=wrong-session"
      assert_lifecycle_route_identity_denied "$provider" \
        "env $identity_name= $target status" \
        "expected=$identity_name=$session_id" "observed=$identity_name="
      assert_lifecycle_route_identity_denied "$provider" \
        "env -- $identity_name= $target status" \
        "expected=$identity_name=$session_id" "observed=$identity_name="
      assert_allowed "$provider" coordinator active "$fixed"

      assert_lifecycle_route_identity_denied "$provider" \
        "env $target status" "expected_name=$identity_name" 'observed_name=<none>'
      assert_lifecycle_route_identity_denied "$provider" \
        "env -- $target status" "expected_name=$identity_name" 'observed_name=<none>'
      assert_allowed "$provider" coordinator active "$fixed"

      assert_lifecycle_arguments_denied "$provider" "$target unknown-verb"
      assert_lifecycle_arguments_denied "$provider" "$target status extra"
      assert_allowed "$provider" coordinator active 'env FOO=x novel-finite-tool'
      assert_lifecycle_status_executes \
        "$provider" "$target_provider" "$target" "$identity_name"
    done
  done
}

assert_eci_transcript_worker_allowed() {
  local command="$1" eci_home proof_root session_id transcript output
  eci_home="$TMP_ROOT/eci-transcript-worker-home"
  proof_root="$TMP_ROOT/eci-transcript-worker-proof"
  session_id="11111111-1111-4111-8111-111111111111"
  transcript="$eci_home/sessions/worker.jsonl"
  output="$TMP_ROOT/eci-transcript-worker-output.json"
  mkdir -p "$(dirname "$transcript")" "$proof_root"
  printf '%s\n' \
    '{"timestamp":"2026-08-21T00:00:00.000Z","type":"session_meta","payload":{"id":"11111111-1111-4111-8111-111111111111","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Test","agent_role":"default"}}}}}' \
    >"$transcript"
  make_marker "$proof_root" "$session_id" "$ECI_ROOT"
  jq -cn --arg session_id "$session_id" --arg cwd "$ECI_ROOT" \
    --arg transcript "$transcript" --arg command "$command" \
    '{session_id:$session_id,tool_name:"Bash",cwd:$cwd,transcript_path:$transcript,tool_input:{command:$command}}' |
    env -u ECI_HOOK_IS_SUBAGENT -u ECI_ROLE \
      ECI_PROOF_ROOT="$proof_root" ECI_HOME="$eci_home" \
      KIMI_CODE_HOME="$KIMI_ROOT" PATH="$ECI_ROOT/bin:$PATH" \
      bash "$ECI_ROOT/hooks/validate-bash.sh" >"$output"
  if [ -s "$output" ]; then
    printf 'expected transcript-authenticated ECI worker allow: command=%q\n' \
      "$command" >&2
    cat "$output" >&2
    return 1
  fi
}

run_lifecycle_identity_matrix
if [ "${1:-}" = lifecycle ]; then
  printf 'ECI lifecycle identity tests passed\n'
  exit 0
fi

for provider in eci kimi; do
  for role in coordinator worker; do
    for active in inactive active; do
      assert_allowed "$provider" "$role" "$active" 'novel-finite-tool --flag value'
      assert_allowed "$provider" "$role" "$active" 'adb devices -l'
      assert_allowed "$provider" "$role" "$active" 'printf "quoted && | ;"'
      assert_allowed "$provider" "$role" "$active" 'printf ""'
      assert_allowed "$provider" "$role" "$active" \
        'printf one && printf two || printf three; printf four | sha256sum'
      assert_allowed "$provider" "$role" "$active" \
        'printf 1; printf 2; printf 3; printf 4; printf 5; printf 6; printf 7; printf 8'
      assert_denied "$provider" "$role" "$active" 'env | sort' \
        ECI_ENVIRONMENT_ENUMERATION_DENIED
      assert_denied "$provider" "$role" "$active" 'env -S "novel-finite-tool"' \
        ECI_ENVIRONMENT_OPTION_DENIED
      assert_allowed "$provider" "$role" "$active" 'printf escaped\;operator'
      if [ "$role" = worker ] && [ "$active" = active ]; then
        assert_denied "$provider" "$role" "$active" \
          $'printf first\nprintf second' ECI_COMMAND_SYNTAX_DENIED
      else
        assert_allowed "$provider" "$role" "$active" $'printf first\nprintf second'
      fi
      if [ "$active" = active ]; then
        assert_plan_denied "$provider" "$role" "$active" 'printf "$(date)"' \
          ECI_PLAN_SYNTAX_DENIED
        assert_plan_denied "$provider" "$role" "$active" 'printf }' \
          ECI_PLAN_SYNTAX_DENIED
        for malformed in \
          'printf first &&' \
          '&& printf second' \
          'printf first || || printf second' \
          'printf first > output' \
          '( printf grouped )' \
          'printf background &' \
          'printf value # comment' \
          'VALUE=x novel-finite-tool'; do
          assert_plan_denied "$provider" "$role" "$active" "$malformed" ECI_PLAN_SYNTAX_DENIED
        done
        assert_plan_denied "$provider" "$role" "$active" \
          'xargs novel-finite-tool' ECI_PLAN_DYNAMIC_LAUNCH_DENIED
        assert_plan_denied "$provider" "$role" "$active" \
          'node -e "console.log(1)"' ECI_PLAN_DYNAMIC_LAUNCH_DENIED
        assert_plan_denied "$provider" "$role" "$active" \
          'command command command command command command command command command novel-finite-tool' \
          ECI_PLAN_WRAPPER_DENIED
        assert_plan_denied "$provider" "$role" "$active" \
          'command' ECI_PLAN_WRAPPER_DENIED
      else
        assert_allowed "$provider" "$role" "$active" \
          'python3 -c "print(1)"'
      fi
    done
  done
done

for provider in eci kimi; do
  # Worker plan_status=0 must take the plain finite fast path, while the
  # ownership-shaped control, environment, Git, and wrapper forms remain on
  # their protected routes for both provider validators.
  worker_control_target="$ECI_ROOT/bin/eci-active"
  provider_root="$ECI_ROOT"
  [ "$provider" = kimi ] && worker_control_target="$KIMI_ROOT/bin/eci-active" && provider_root="$KIMI_ROOT"
  assert_allowed "$provider" worker active 'novel-worker-fast-path --flag value'
  provider_helper_alias="$TMP_ROOT/$provider-worker-absolute-helper-hardlink"
  ln -- "$provider_root/hooks/lib/eci-environment-command.sh" "$provider_helper_alias"
  assert_allowed "$provider" worker active "cat $provider_helper_alias"
  assert_denied "$provider" worker active \
    "$worker_control_target status" ECI_CONTROL_OWNER_REQUIRED
  assert_denied "$provider" worker active 'env' ECI_ENVIRONMENT_ENUMERATION_DENIED
  if [ "$provider" = eci ]; then
    assert_allowed "$provider" worker active 'git commit -m worker-fast-path'
  fi
  assert_denied "$provider" worker active \
    "bash -c 'printf protected-wrapper'" ECI_PLAN_DYNAMIC_LAUNCH_DENIED
  assert_allowed "$provider" worker active './tools/eci-review-gate.sh verify'
  assert_denied "$provider" worker active \
    "$ECI_ROOT/hooks/eci-review-gate.sh commit command-plan-session" \
    ECI_WORKER_REVIEW_GATE_DENIED
  assert_denied "$provider" worker active \
    "bash $ECI_ROOT/hooks/eci-review-gate.sh commit command-plan-session" \
    ECI_WORKER_REVIEW_GATE_DENIED
done

assert_eci_transcript_worker_allowed \
  'unlink hooks/lib/__pycache__/generated.pyc'

for provider in eci kimi; do
  assert_allowed "$provider" worker active \
    "cat $ECI_ROOT/hooks/lib/eci-environment-command.sh"
  assert_allowed "$provider" worker active \
    "git diff --binary -- hooks/validate-bash.sh | sha256sum"
  assert_allowed "$provider" worker active \
    "git rev-parse HEAD && git -C $ECI_ROOT rev-parse HEAD && git status --short --untracked-files=all"
  assert_allowed "$provider" worker active \
    "ps -eo pid,ppid,etimes,stat,args | rg validate-bash"
  assert_gate_denied "$provider" worker active 'git archive HEAD'
  for git_mutation in \
    'git rebase topic' \
    'git worktree add /tmp/eci-worker-tree HEAD' \
    'git branch feature' \
    'git remote set-url origin https://example.invalid/repo.git'; do
    assert_denied "$provider" worker active "$git_mutation" \
      ECI_WORKER_GIT_OWNERSHIP_DENIED
  done
  assert_denied "$provider" worker active 'git reset --hard HEAD' \
    ECI_BROAD_DESTRUCTIVE_DENIED

  proof_root="$TMP_ROOT/$provider-worker-active-proof"
  if [ "$provider" = kimi ]; then
    session_id="session_11111111-1111-4111-8111-111111111111"
  else
    session_id="$ECI_WORKER_SESSION_ID"
  fi
  session_root="$proof_root/$session_id"
  printf '%s\n' pending >"$session_root/goal_state"
  ln -s -- "$session_root/goal_state" "$TMP_ROOT/$provider-live-symlink"
  ln -- "$session_root/goal_state" "$TMP_ROOT/$provider-live-hardlink"
  rm -f -- "$HELPER_HARDLINK"
  ln -- "$ECI_ROOT/hooks/lib/eci-environment-command.sh" "$HELPER_HARDLINK"
  assert_plan_denied "$provider" worker active \
    "cat $session_root/goal_state" ECI_PLAN_LIVE_CONTROL_DENIED
  assert_denied "$provider" worker active \
    "cat $TMP_ROOT/$provider-live-symlink" ECI_PLAN_LIVE_CONTROL_DENIED
  assert_plan_denied "$provider" worker active \
    "cat $TMP_ROOT/$provider-live-hardlink" ECI_PLAN_LIVE_CONTROL_DENIED
  assert_allowed "$provider" worker active \
    "cat $HELPER_HARDLINK"

  unrelated="$proof_root/unrelated"
  mkdir -p "$unrelated"
  index=0
  while [ "$index" -lt 4100 ]; do
    printf '%s\n' unrelated >"$unrelated/entry-$index"
    index=$((index + 1))
  done
  assert_allowed "$provider" worker active \
    "cat $HELPER_HARDLINK"
done

assert_allowed eci coordinator active 'go test ./pkg/chathandler/platform/youtube -count=1'
assert_allowed eci coordinator active 'id'
assert_allowed eci coordinator active 'go test ./pkg/chathandler/platform/youtube -count=1; id'
assert_allowed eci coordinator active \
  "sed -n '1p' hooks/validate-bash.sh && printf ok && sed -n '1p' hooks/validate-bash.sh"
assert_plan_denied eci coordinator active 'printf ok; env' ECI_ENVIRONMENT_ENUMERATION_DENIED
assert_plan_denied eci worker active 'printf ok; env' ECI_ENVIRONMENT_ENUMERATION_DENIED
assert_denied eci coordinator active 'printf ok; rm -f /tmp/x' ECI_TMPDIR_SYSTEM_ROOT
assert_compound_keeps_direct_denial eci coordinator active 'printf ok' \
  "rm -f $TMP_ROOT/not-a-generated-cleanup-target"

printf 'ECI command-plan tests passed\n'
