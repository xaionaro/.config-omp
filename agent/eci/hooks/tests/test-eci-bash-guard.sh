#!/usr/bin/env bash
# hooks/lib/eci-bash-guard.sh: under bash >= 4.4 sourcing it is a no-op; under an
# older bash (ECI_TEST_OLD_BASH, for example a bash 3.2 build like the macOS /bin/bash)
# every hook entry point the omp adapters run answers with one decision naming the
# requirement: a PreToolUse deny, SessionStart/UserPromptSubmit additionalContext, or
# a stderr line for PostCompact, each with exit 0.
set -euo pipefail
umask 077

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-bash-guard.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
fail() { printf 'eci-bash-guard: FAIL %s\n' "$*" >&2; exit 1; }

code=ECI_HOOK_BASH_TOO_OLD
out="$(bash -c '. "$1/hooks/lib/eci-bash-guard.sh" PreToolUse; printf continued' _ "$ROOT")"
[ "$out" = continued ] || fail "guard is not a no-op under bash $BASH_VERSION: $out"

# Each entry point the omp adapters run (agent/hooks-disabled) sources the guard first.
entries='validate-bash.sh:PreToolUse pretooluse-edit-dispatch.sh:PreToolUse eci-edit-gate.sh:PreToolUse session-snapshot.sh:SessionStart prompt-task-reminder.sh:UserPromptSubmit eci-post-compact-refresh.sh:PostCompact'
for entry in $entries; do
  script="${entry%%:*}" event="${entry#*:}"
  guard_line="$(grep -n 'lib/eci-bash-guard.sh' "$ROOT/hooks/$script" | head -n 1)" || fail "$script does not source the guard"
  [ "${guard_line%%:*}" -le 3 ] || fail "$script sources the guard at line ${guard_line%%:*}, after other code"
  case "$guard_line" in *" $event "*) ;; *) fail "$script passes the wrong event: $guard_line" ;; esac
done

old="${ECI_TEST_OLD_BASH:-}"
if [ -z "$old" ]; then
  printf 'eci-bash-guard: PASS (old-bash axis not run: ECI_TEST_OLD_BASH unset)\n'
  exit 0
fi
"$old" -c '[ "${BASH_VERSINFO[0]}" -lt 4 ] || { [ "${BASH_VERSINFO[0]}" -eq 4 ] && [ "${BASH_VERSINFO[1]}" -lt 4 ]; }' ||
  fail "ECI_TEST_OLD_BASH=$old is not older than bash 4.4"
payload='{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}'
for entry in $entries; do
  script="${entry%%:*}" event="${entry#*:}"
  rc=0
  stdout="$(printf '%s' "$payload" | HOME="$TMP_ROOT" "$old" "$ROOT/hooks/$script" 2>"$TMP_ROOT/err")" || rc=$?
  [ "$rc" = 0 ] || fail "$script exited $rc under $old"
  case "$event" in
    PreToolUse)
      jq -e --arg code "$code" '.hookSpecificOutput.hookEventName == "PreToolUse" and .hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | startswith("[" + $code + "]"))' <<<"$stdout" >/dev/null ||
        fail "$script: no bash-version deny: $stdout"
      ;;
    SessionStart|UserPromptSubmit)
      jq -e --arg event "$event" --arg code "$code" '.hookSpecificOutput.hookEventName == $event and (.hookSpecificOutput.additionalContext | startswith("[" + $code + "]"))' <<<"$stdout" >/dev/null ||
        fail "$script: no bash-version context: $stdout"
      ;;
    *)
      [ -z "$stdout" ] || fail "$script: unexpected stdout $stdout"
      grep -q "^\[$code\]" "$TMP_ROOT/err" || fail "$script: no bash-version stderr line"
      ;;
  esac
done
printf 'eci-bash-guard: PASS (old bash %s)\n' "$("$old" -c 'printf %s "$BASH_VERSION"')"
