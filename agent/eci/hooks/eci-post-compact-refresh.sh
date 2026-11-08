#!/usr/bin/env bash
# TODO: Remove ECI marker discovery from this no-op callback; native goals own lifecycle.
case "${BASH_SOURCE[0]}" in */*) . "${BASH_SOURCE[0]%/*}/lib/eci-bash-guard.sh" PostCompact ;; *) . ./lib/eci-bash-guard.sh PostCompact ;; esac

# PostCompact hook: validate the event without touching session state.
# The provider does not accept event-specific output fields for PostCompact, so
# every path emits the provider-valid empty object and keeps diagnostics off
# stdout.

set -euo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/lib/eci-proof-state.sh"

postcompact_noop() {
  # PostCompact always requires one valid JSON object on stdout, including
  # malformed, inactive, or out-of-scope input. Keep diagnostics off stdout.
  printf '%s\n' '{}'
  exit 0
}

input=$(cat)
if ! printf '%s' "$input" | jq -e '
  type == "object" and
  (.session_id? | type) == "string" and
  (.cwd? | type) == "string" and
  (.hook_event_name? | type) == "string" and
  .hook_event_name == "PostCompact" and
  ((has("trigger") | not) or ((.trigger | type) == "string" and (.trigger == "manual" or .trigger == "auto")))
' >/dev/null 2>&1; then
  postcompact_noop
fi

session_id=$(printf '%s' "$input" | jq -r '.session_id' 2>/dev/null || true)
cwd=$(printf '%s' "$input" | jq -r '.cwd' 2>/dev/null || true)
[ -n "$cwd" ] || postcompact_noop

eci_valid_session_id "$session_id" || postcompact_noop
case "$cwd" in
  *[![:print:]]*) postcompact_noop ;;
esac

root="$(eci_proof_root 2>/dev/null || true)"
proof_dir="$root/$session_id"
marker="$proof_dir/eci_active"
nested_marker="$proof_dir/ate_nested_eci_active"

eci_proof_root_is_safe || postcompact_noop
eci_session_dir_is_safe "$root" "$session_id" || postcompact_noop
[ -d "$proof_dir" ] && [ ! -L "$proof_dir" ] || postcompact_noop

nested_marker_is_active() {
  local bytes line_count writer
  local -a lines=()

  [ -f "$nested_marker" ] && [ ! -L "$nested_marker" ] || return 1
  # A nested marker is only meaningful under the validated outer ECI owner;
  # this prevents a stale/forged ATE file from activating refresh on its own.
  eci_marker_is_valid_for_cwd "$marker" "$(eci_canonical_cwd "$cwd")" || return 1
  bytes="$(wc -c <"$nested_marker" 2>/dev/null || true)"
  case "$bytes" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$bytes" -le 1024 ] || return 1
  [ "$(tail -c 1 -- "$nested_marker" 2>/dev/null | od -An -t x1 | tr -d '[:space:]')" = 0a ] || return 1
  line_count="$(awk 'END { print NR + 0 }' "$nested_marker" 2>/dev/null || printf '0')"
  # Legacy three-line nested markers lack ownership/authentication and must not
  # keep a session in an active refresh state.
  [ "$line_count" -eq 8 ] || return 1
  mapfile -t lines <"$nested_marker" || return 1
  [ "${#lines[@]}" -eq 8 ] || return 1
  [[ "${lines[0]}" == "outer_session_id: $session_id" ]] || return 1
  [[ "${lines[1]}" == "outer_marker: $proof_dir/eci_active" ]] || return 1
  [ "${lines[2]}" = 'owner: ate' ] || return 1
  [[ "${lines[3]}" == writer_session_id:\ * ]] || return 1
  writer="${lines[3]#writer_session_id: }"
  eci_valid_session_id "$writer" || return 1
  [[ "${lines[4]}" =~ ^acceptance_version:[[:space:]]*[1-9][0-9]*$ ]] || return 1
  [[ "${lines[5]}" =~ ^step:[[:space:]]*[0-9]+$ ]] || return 1
  [[ "${lines[6]}" =~ ^iteration:[[:space:]]*[0-9]+$ ]] || return 1
  [ "${lines[7]}" = 'state: active' ] || return 1
  return 0
}

direct_marker_is_active() {
  [ -f "$marker" ] && [ ! -L "$marker" ] || return 1
  eci_marker_is_valid_for_cwd "$marker" "$(eci_canonical_cwd "$cwd")"
}

if ! direct_marker_is_active && ! nested_marker_is_active; then
  postcompact_noop
fi

postcompact_noop
