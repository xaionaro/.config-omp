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
ECI_MAIN_TRANSCRIPT_DIR="${HOME:?}/.omp/agent/sessions/-eci-command-plan-$BASHPID-"
cleanup_command_plan_test() {
  rm -f -- "$HELPER_HARDLINK"
  if [ -n "$ECI_WORKER_TRANSCRIPT" ]; then
    rm -f -- "$ECI_WORKER_TRANSCRIPT"
  fi
  rm -rf -- "$TMP_ROOT" "$ECI_MAIN_TRANSCRIPT_DIR"
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
          # agent/eci/sessions is untracked runtime state; a fresh checkout lacks it.
          mkdir -p -- "$ECI_ROOT/sessions"
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
  if [ "$role" = coordinator ]; then
    # The planner takes the callback role from its transcript: coordinator calls
    # (both providers run the omp planner) carry a main omp transcript (session
    # header, no parentSession); without one the hook fails closed to the worker role.
    transcript="$ECI_MAIN_TRANSCRIPT_DIR/$session_id.jsonl"
    if [ ! -e "$transcript" ]; then
      mkdir -p -- "$ECI_MAIN_TRANSCRIPT_DIR"
      jq -cn --arg id "$session_id" '{type:"session",version:3,id:$id,cwd:"/work"}' >"$transcript"
      chmod 0600 -- "$transcript"
    fi
  fi
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

# Both providers' hooks run the omp planner, whose provider root is
# $HOME/.omp/agent/eci. A coordinator running the Kimi peer's eci-active selects a
# script outside that root and the approved roots, so both trees deny it with
# ECI_COORDINATOR_SCRIPT_TARGET_DENIED predicate=script-target (hook_effects.go
# :1158-1183 HEAD, :1106-1131 575bb81; probes of both 575bb81 rows, provider codex
# and kimi, measured the same denial). The omp eci-active target is the lifecycle route.
assert_lifecycle_route() {
  local provider="$1" target_provider="$2" command="$3"
  if [ "$target_provider" = kimi ]; then
    assert_plan_denied "$provider" coordinator active "$command" \
      ECI_COORDINATOR_SCRIPT_TARGET_DENIED
    jq -e '.hookSpecificOutput.permissionDecisionReason | contains("predicate=script-target")' \
      "$TMP_ROOT/output.json" >/dev/null
  else
    assert_allowed "$provider" coordinator active "$command"
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

      assert_lifecycle_route "$provider" "$target_provider" "$target status"
      # Retired (group a): the NAME=value prefix denial ECI_PLAN_SYNTAX_DENIED for the
      # lifecycle command. The pre-migration planner allows a NAME=value prefix in hook
      # mode; lifecycle deferral is non-hook only (575bb81 classifier.go:935).
      assert_lifecycle_route "$provider" "$target_provider" "$fixed"
      assert_lifecycle_route "$provider" "$target_provider" \
        "env -- $identity_name=$session_id $target status"

      # Retired (group b): lifecycle argument denials (ECI_LIFECYCLE_ARGUMENTS_DENIED),
      # identity-route denials (ECI_LIFECYCLE_IDENTITY_DENIED), and the status output
      # line `session_id:`. Pre-migration source 575bb81 declares the first two codes in
      # contract.go and emits neither; lifecycle routes are deferred to the provider
      # (6ab0b42 citation). Its status verb prints only the lifecycle-managed line
      # (bin/eci-active:51-53, :70-71), which HEAD also prints.
      assert_allowed "$provider" coordinator active 'env FOO=x novel-finite-tool'
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
      # Retired, both providers (group c): `env | sort` and `env -S` denials. Both
      # planners (575bb81 and 3ee2a18) allow them for kimi hook input in every role and
      # marker state (~/kprobe/kimi-probe.tsv). Only environment-command
      # (eci-hook-helper-go/environment_command.go) emits their codes, and no hook calls it.
      assert_allowed "$provider" "$role" "$active" 'printf escaped\;operator'
      # Retired (group a): the worker-active newline denial ECI_COMMAND_SYNTAX_DENIED.
      # Pre-migration source 575bb81 declares that code in contract.go only and emits
      # it nowhere. The allowance stays for the other role and activity combinations.
      if [ "$role" != worker ] || [ "$active" != active ]; then
        assert_allowed "$provider" "$role" "$active" $'printf first\nprintf second'
      fi
      # Retired (group a): the plan-syntax denials for `printf "$(date)"`, `printf }`
      # and the malformed compound list (including `VALUE=x novel-finite-tool`), and
      # the dynamic-launch and wrapper denials for `xargs`, `node -e` and `command`.
      # The pre-migration planner allows each of these inputs in hook mode, so the
      # source emits none of them for these inputs.
      if [ "$active" != active ]; then
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
  provider_root="$ECI_ROOT"
  [ "$provider" = kimi ] && provider_root="$KIMI_ROOT"
  assert_allowed "$provider" worker active 'novel-worker-fast-path --flag value'
  provider_helper_alias="$TMP_ROOT/$provider-worker-absolute-helper-hardlink"
  ln -- "$provider_root/hooks/lib/eci-environment-command.sh" "$provider_helper_alias"
  assert_allowed "$provider" worker active "cat $provider_helper_alias"
  # Group b: worker `status` of the provider's eci-active. The eci row is retired:
  # status is a read-only lifecycle verb (classifier.go !isLifecycleReadOnlyInvocation).
  # The kimi worker callback runs with KIMI_CODE_HOME=$TMP_ROOT/kimi-worker-home
  # (run_hook), so the row targets the eci-active of that home. A target under
  # $KIMI_ROOT lies outside every root the worker callback names and gets
  # ECI_WORKER_SCRIPT_TARGET_DENIED instead. 575bb81 and 3ee2a18 both give this denial.
  if [ "$provider" = kimi ]; then
    kimi_worker_control="$TMP_ROOT/kimi-worker-home/bin/eci-active"
    mkdir -p -- "${kimi_worker_control%/*}"
    cp -- "$KIMI_ROOT/bin/eci-active" "$kimi_worker_control"
    assert_denied "$provider" worker active \
      "$kimi_worker_control status" ECI_CONTROL_OWNER_REQUIRED
    jq -e '.hookSpecificOutput.permissionDecisionReason | contains("predicate=worker-control-script")' \
      "$TMP_ROOT/output.json" >/dev/null
  fi
  # Retired, both providers (group c): the worker `env` denial. Both planners allow
  # bare env for kimi hook input (~/kprobe/kimi-probe.tsv).
  # Retired (group d): the eci worker `git commit -m worker-fast-path` allowance,
  # which the pre-migration planner denies with ECI_WORKER_GIT_OWNERSHIP_DENIED.
  # Retired (group a): the `bash -c` dynamic-launch denial, which the pre-migration
  # planner allows in hook mode.
  assert_allowed "$provider" worker active './tools/eci-review-gate.sh verify'
  # Retired (group e): the worker review-gate denials ECI_WORKER_REVIEW_GATE_DENIED.
  # Pre-migration source 575bb81 declares that code in contract.go only, and its
  # planner allows the worker invocation in hook mode.
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
  # Retired (group d): the worker `git archive HEAD` gate denial, and the git branch
  # and git remote set-url ECI_WORKER_GIT_OWNERSHIP_DENIED denials. The pre-migration
  # planner allows all three in hook mode. Rebase stays: the source emits
  # ECI_WORKER_GIT_OWNERSHIP_DENIED for it. Worktree add is allowed under the accepted
  # WORKERGIT ruling D (disable-the-denial; TEST-DISPOSITION "Worker worktree add").
  assert_denied "$provider" worker active 'git rebase topic' \
    ECI_WORKER_GIT_OWNERSHIP_DENIED
  assert_allowed "$provider" worker active 'git worktree add /tmp/eci-worker-tree HEAD'
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
  rm -f -- "$HELPER_HARDLINK"
  ln -- "$ECI_ROOT/hooks/lib/eci-environment-command.sh" "$HELPER_HARDLINK"
  # Retired, both providers (group f): worker `cat` of goal_state and of its symlink
  # and hardlink. Built from 575bb81 and 3ee2a18 and fed kimi hook input, both
  # planners allow all three reads in every role and marker state
  # (~/kprobe/kimi-probe.tsv). Live-control inspection covers write operands only.
  # Quotes: TD-WITHHELD-QUOTES.md "test-eci-command-plan.sh (Job PORT, kimi c/f)".
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
# Retired (groups c, g, h): the `printf ok; env` plan-segment denials, the
# ECI_TMPDIR_SYSTEM_ROOT denial for `printf ok; rm -f /tmp/x`, and the compound
# retention of a direct `rm -f` denial. Pre-migration source 575bb81 emits no
# ECI_TMPDIR_SYSTEM_ROOT, and its planner allows each of these inputs in hook mode,
# so there is no source route to retain.

printf 'ECI command-plan tests passed\n'
