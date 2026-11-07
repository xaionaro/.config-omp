#!/usr/bin/env bash

# Shared coordinator cleanup capability parser.  This is intentionally kept
# provider-neutral and hard-linked into the hook tree.  The
# caller must have completed the compiled plan; this route adds only the live
# provider-home, temporary-root, and inode checks that the planner cannot do.
# The check itself lives in the eci-hook-helper `cleanup-route` mode.
ECI_SHARED_CLEANUP_ROUTE_DETAIL=""
eci_shared_cleanup_route() {
  [ "${hook_is_subagent:-false}" != true ] || return 1
  local allow_private_session_child="${2:-false}" detail helper
  case "$allow_private_session_child" in
    true|false) ;;
    *) return 1 ;;
  esac
  helper="$(. "${BASH_SOURCE[0]%/*}/eci-native-binary.sh" && eci_runtime_hook_helper_path)" || {
    ECI_SHARED_CLEANUP_ROUTE_DETAIL="coordinator-cleanup-route reason=cleanup route helper is unavailable"
    return 1
  }
  detail="$("$helper" cleanup-route "$1" "$allow_private_session_child")" && {
    ECI_SHARED_CLEANUP_ROUTE_DETAIL=""
    return 0
  }
  ECI_SHARED_CLEANUP_ROUTE_DETAIL="${detail:-coordinator-cleanup-route reason=command is outside the bounded cleanup grammar}"
  return 1
}
