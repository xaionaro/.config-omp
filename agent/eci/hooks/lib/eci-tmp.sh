#!/usr/bin/env bash
# Hook scratch + error helpers.
. "${BASH_SOURCE[0]%/*}/eci-diagnostic.sh" || return 1

eci_init_tmp() {
  local home="${HOME:-}" requested target canonical inherited inherited_canonical="" batched=""
  local -a canonical_lines=()
  if [ -z "$home" ]; then
    printf '%s\n' "$(eci_diagnostic_reason "ECI_TMPDIR_HOME_MISSING" "PreToolUse" "tmp-init" "hook=eci" "HOME is unset; a home-scoped temporary directory cannot be selected" "set HOME to the user's home directory and retry")" >&2
    return 1
  fi

  requested="${ECI_TMPDIR:-$home/tmp}"
  target="$requested"
  inherited="${TMPDIR:-}"
  # Both names in one realpath call when there are two and neither has a
  # newline; a failure or any other line count takes the one-name calls.
  if [ -n "$inherited" ]; then
    case "$target$inherited" in
      *$'\n'*) ;;
      *)
        if batched="$(realpath -m -- "$target" "$inherited" 2>/dev/null)" &&
          mapfile -t canonical_lines <<<"$batched" && [ "${#canonical_lines[@]}" -eq 2 ] &&
          [ -n "${canonical_lines[0]}" ] && [ -n "${canonical_lines[1]}" ]; then
          canonical="${canonical_lines[0]}"
          inherited_canonical="${canonical_lines[1]}"
        else
          batched=""
        fi
        ;;
    esac
  fi
  [ -n "$batched" ] || canonical="$(realpath -m -- "$target" 2>/dev/null || true)"
  if [ -z "$canonical" ]; then
    canonical="$target"
  fi
  # A caller's system temporary directory is not a command-scope error.  The
  # hook selects its own home-scoped scratch directory when useful, without
  # making the caller repair an environment variable first.
  if [ "$canonical" = /tmp ] || [[ "$canonical" == /tmp/* ]]; then
    target="$home/tmp"
  fi
  if [ -n "$inherited" ]; then
    [ -n "$batched" ] || inherited_canonical="$(realpath -m -- "$inherited" 2>/dev/null || true)"
    if [ "$inherited_canonical" = /tmp ] || [[ "$inherited_canonical" == /tmp/* ]]; then
      target="$home/tmp"
    fi
  fi

  export TMPDIR="$target"
  if ! mkdir -p "$target" 2>/dev/null || [ ! -w "$target" ]; then
    printf '%s\n' "$(eci_diagnostic_reason "ECI_TMPDIR_UNWRITABLE" "PreToolUse" "tmp-init" "hook=eci,target=$target,requested=$requested,tmpdir=$TMPDIR" "home-scoped temporary directory is unwritable" "make the configured temporary directory writable or set ECI_TMPDIR to a writable directory under the home directory")" >&2
    return 1
  fi
  return 0
}

_eci_fail_closed_constant() {
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_FAIL_CLOSED] hook %s failed after its gate decision point and denied the tool call; jq is unavailable or failed; inspect the hook log and available temporary-directory/disk state, then retry"}}\n' "$1"
}

# Constant deny for a hook whose ECI library cannot load.  The marker state is
# unknown, so it fails closed; it needs no library to print.
_eci_lib_unavailable_constant() {
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] hook %s cannot load its ECI hook library; the native install is incomplete or damaged; reinstall the ECI hooks and retry"}}\n' "$1"
}

# Up-front jq gate for PreToolUse validators.  Without jq no JSON deny can be
# built, so while an active marker exists (eci_marker_active_or_unknown, which
# needs no jq) a constant printf deny naming the hook and jq is printed.  With
# no marker the ordinary path exits 0.  Callers source proof-state first.
eci_require_jq_or_deny() {
  local hook_name="$1"
  if command -v jq >/dev/null 2>&1; then
    return 0
  fi
  if eci_marker_active_or_unknown; then
    printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_JQ_MISSING] hook %s denied the tool call while an active marker exists because jq is not installed; install jq and retry"}}\n' "$hook_name"
  fi
  exit 0
}

_eci_fail_closed_emit() {
  local hook_name="$1" line="$2" exit_code="$3" cmd="$4" reason
  reason="$(eci_diagnostic_reason "ECI_HOOK_FAIL_CLOSED" "PreToolUse" "hook-fail-closed" "hook=$hook_name,line=$line,exit=$exit_code,command=$cmd" "hook command failed after its gate decision point; denying the tool call" "inspect the failing command and available temporary-directory/disk state, then retry")" ||
    reason="[ECI_HOOK_FAIL_CLOSED] hook $hook_name failed after its gate decision point and denied the tool call; inspect the hook log and available temporary-directory/disk state, then retry"
  if ! jq -n --arg reason "$reason" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}' 2>/dev/null; then
    _eci_fail_closed_constant "$hook_name"
  fi
}

# Gate hooks (PreToolUse) fail closed: an unexpected error emits an explicit
# deny JSON first, and the hook exits 0 only after that JSON is printed.
# With set -E the ERR trap is inherited by command substitutions and subshells.
# A subshell failure must not print a deny into a captured value, so it exits
# with its own status and the parent's || or errexit handles it.  The busy
# flag stops a failure inside the handler from re-entering it.
_eci_fail_closed_busy=""
_eci_fail_closed_trap() {
  local hook_name="$1" line="$2" exit_code="$3" cmd="$4"
  if [ "$BASHPID" != "$$" ]; then
    exit "$exit_code"
  fi
  # Without the proof-state lib the marker state is unknown: fail closed.
  if ! declare -F eci_marker_active_or_unknown >/dev/null 2>&1; then
    _eci_lib_unavailable_constant "$hook_name"
    exit 0
  fi
  # Only an active (or unknown) marker turns an unexpected error into a deny;
  # with no marker the hook exits 0 as the source does.
  eci_marker_active_or_unknown || exit 0
  if [ -n "$_eci_fail_closed_busy" ]; then
    _eci_fail_closed_constant "$hook_name"
    exit 0
  fi
  _eci_fail_closed_busy=1
  _eci_fail_closed_emit "$hook_name" "$line" "$exit_code" "$cmd"
  exit 0
}

eci_install_fail_closed_trap() {
  local name="${1:-${BASH_SOURCE[1]##*/}}"
  # shellcheck disable=SC2064
  trap '_eci_fail_closed_trap "'"$name"'" "$LINENO" "$?" "$BASH_COMMAND"' ERR
}

_eci_advisory_emit() {
  local hook_name="$1" line="$2" exit_code="$3" cmd="$4"
  printf '%s\n' "$(eci_diagnostic_reason "ECI_HOOK_FAIL_OPEN" "PreToolUse" "hook-fail-open" "hook=$hook_name,line=$line,exit=$exit_code,command=$cmd" "hook command failed; failing open" "inspect the failing command and available temporary-directory/disk state before retrying")" >&2
}

# Advisory subshell failures propagate their status, as in the fail-closed trap.
_eci_advisory_trap() {
  local hook_name="$1" line="$2" exit_code="$3" cmd="$4"
  if [ "$BASHPID" != "$$" ]; then
    exit "$exit_code"
  fi
  _eci_advisory_emit "$hook_name" "$line" "$exit_code" "$cmd"
  exit 0
}

# Non-gate bookkeeping hooks (for example SessionStart) have no permission
# decision to deny with; they keep the advisory diagnostic and exit 0.
eci_install_advisory_fail_open_trap() {
  local name="${1:-${BASH_SOURCE[1]##*/}}"
  # shellcheck disable=SC2064
  trap '_eci_advisory_trap "'"$name"'" "$LINENO" "$?" "$BASH_COMMAND"' ERR
}
