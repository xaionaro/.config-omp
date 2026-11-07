#!/usr/bin/env bash
# Hook scratch + error helpers.
. "${BASH_SOURCE[0]%/*}/eci-diagnostic.sh"

eci_init_tmp() {
  local home="${HOME:-}" requested target canonical inherited inherited_canonical
  if [ -z "$home" ]; then
    printf '%s\n' "$(eci_diagnostic_reason "ECI_TMPDIR_HOME_MISSING" "PreToolUse" "tmp-init" "hook=eci" "HOME is unset; a home-scoped temporary directory cannot be selected" "set HOME to the user's home directory and retry")" >&2
    return 1
  fi

  requested="${ECI_TMPDIR:-$home/tmp}"
  target="$requested"
  canonical="$(realpath -m -- "$target" 2>/dev/null || true)"
  if [ -z "$canonical" ]; then
    canonical="$target"
  fi
  # A caller's system temporary directory is not a command-scope error.  The
  # hook selects its own home-scoped scratch directory when useful, without
  # making the caller repair an environment variable first.
  if [ "$canonical" = /tmp ] || [[ "$canonical" == /tmp/* ]]; then
    target="$home/tmp"
  fi
  inherited="${TMPDIR:-}"
  if [ -n "$inherited" ]; then
    inherited_canonical="$(realpath -m -- "$inherited" 2>/dev/null || true)"
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

_eci_fail_open_emit() {
  local hook_name="$1" line="$2" exit_code="$3" cmd="$4"
  printf '%s\n' "$(eci_diagnostic_reason "ECI_HOOK_FAIL_OPEN" "PreToolUse" "hook-fail-open" "hook=$hook_name,line=$line,exit=$exit_code,command=$cmd" "hook command failed; failing open" "inspect the failing command and available temporary-directory/disk state before retrying")" >&2
}

eci_install_fail_open_trap() {
  local name="${1:-${BASH_SOURCE[1]##*/}}"
  # shellcheck disable=SC2064
  trap '_eci_fail_open_emit "'"$name"'" "$LINENO" "$?" "$BASH_COMMAND"; exit 0' ERR
}
