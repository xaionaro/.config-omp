# shellcheck shell=bash
# Bash version guard for the ECI hook entry points. Sourced first, before any other
# library, as `. "<hooks>/lib/eci-bash-guard.sh" EVENT`. It is written for bash 3.2,
# the macOS /bin/bash, so it parses and runs where the hook libraries do not.
# On bash older than 4.4 it writes one decision naming the requirement and exits the
# hook with status 0:
#   PreToolUse                    a deny with the message as permissionDecisionReason
#   SessionStart, UserPromptSubmit the message as additionalContext
#   any other event               the message on stderr
# Example: bash 3.2.57 running validate-bash.sh gets
#   {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",...}}

if [ "${BASH_VERSINFO[0]:-0}" -lt 4 ] || { [ "${BASH_VERSINFO[0]:-0}" -eq 4 ] && [ "${BASH_VERSINFO[1]:-0}" -lt 4 ]; }; then
  eci_bash_guard_message="[ECI_HOOK_BASH_TOO_OLD] ECI hooks need bash >= 4.4, but this hook ran under bash ${BASH_VERSION:-unknown}, the first bash on omp's PATH. Install a newer bash (macOS: brew install bash) and put it first on PATH for omp, then retry."
  case "${1:-}" in
    PreToolUse)
      printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"%s"}}\n' "$eci_bash_guard_message"
      ;;
    SessionStart|UserPromptSubmit)
      printf '{"hookSpecificOutput":{"hookEventName":"%s","additionalContext":"%s"}}\n' "$1" "$eci_bash_guard_message"
      ;;
    *)
      printf '%s\n' "$eci_bash_guard_message" >&2
      ;;
  esac
  exit 0
fi
