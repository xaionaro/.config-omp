#!/usr/bin/env bash
# TODO: Replace the retired ECI marker activity check with ordinary callback bookkeeping.
# PreToolUse hook: validate file edits made through apply_patch.

set -Eeuo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! . "$HOOK_DIR/lib/eci-proof-state.sh" || ! . "$HOOK_DIR/lib/eci-tmp.sh" || ! . "$HOOK_DIR/lib/eci-diagnostic.sh"; then
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] hook validate-apply-patch cannot load its ECI hook library; the native install is incomplete or damaged; reinstall the ECI hooks and retry"}}'
  exit 0
fi
# Every lib function this hook calls must be defined; a partial load would let
# a missing marker check fall through to a silent exit.
for fn in eci_init_tmp eci_require_jq_or_deny eci_install_fail_closed_trap eci_diagnostic_value eci_diagnostic_reason eci_diagnostic_code_for_reason eci_marker_active_or_unknown eci_hook_subagent_status eci_resolve_hook_path eci_lexical_hook_path eci_session_path_is_unsafe eci_path_is_eci_control_alias eci_path_is_session_ledger_file eci_path_is_eci_control_file eci_path_is_git_approval_file eci_path_owner_session_id eci_hook_allowed_session_ids eci_session_owner_allowed eci_path_is_under_proof_root eci_context_session_rest eci_proof_root eci_note_touched_repo eci_mark_activity _eci_fail_closed_constant _eci_lib_unavailable_constant; do
  declare -F "$fn" >/dev/null 2>&1 || { printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] hook validate-apply-patch cannot load ECI library function %s; reinstall the ECI hooks and retry"}}\n' "$fn"; exit 0; }
done
eci_init_tmp || true
eci_require_jq_or_deny validate-apply-patch
eci_install_fail_closed_trap validate-apply-patch

input=$(cat)
session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null || true)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null || true)

patch_text=$(printf '%s' "$input" | jq -r '.tool_input.command // .tool_input.patch // .tool_input.input // empty' 2>/dev/null || true)

# Thread ownership is resolved once.  "unknown" means the transcript could not
# be verified; subagent-guarded control and ledger edits fail closed on it.
eci_hook_subagent_status "$input"
hook_subagent_status="$ECI_HOOK_SUBAGENT_STATUS"

deny() {
  local reason="${1:-unspecified patch validation denial}"
  if [[ "$reason" != \[ECI_* ]]; then
    local subject
    subject="tool=apply_patch,path=$(eci_diagnostic_value "${patch_paths:-<unparsed>}"),session=$(eci_diagnostic_value "${session_id:-<missing>}"),cwd=$(eci_diagnostic_value "${cwd:-<missing>}")"
    reason="$(eci_diagnostic_reason "$(eci_diagnostic_code_for_reason "$reason")" "PreToolUse" "patch-validation" "$subject" "$reason" "correct the reported patch target or route the change through the approved source/coordinator path, then retry")"
  fi
  jq -n --arg reason "$reason" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: $reason
    }
  }'
  exit 0
}

# An apply_patch callback with no patch text cannot be checked for targets.
# While an active marker exists it is denied; with no marker the ordinary path exits 0.
if [ -z "$patch_text" ]; then
  if eci_marker_active_or_unknown; then
    deny "[ECI_PATCH_TEXT_EMPTY] patch text is empty while an active marker exists; the patch cannot be checked for session-scoped targets, so it is denied"
  fi
  exit 0
fi

patch_paths=$(printf '%s\n' "$patch_text" | awk '
  /^\*\*\* (Add|Update|Delete) File: / {
    sub(/^\*\*\* (Add|Update|Delete) File: /, "")
    print
  }
  /^\*\*\* Move to: / {
    sub(/^\*\*\* Move to: /, "")
    print
  }
')

# Session-scoped patch targets must not traverse symlinks.  Resolved paths are
# checked separately so an in-repo symlink cannot launder a proof-root target.
while IFS= read -r path; do
  [ -n "$path" ] || continue
  lexical_path="$(eci_lexical_hook_path "${cwd:-$PWD}" "$path" 2>/dev/null || true)"
  resolved_path="$(eci_resolve_hook_path "${cwd:-$PWD}" "$path" 2>/dev/null || true)"
  if eci_session_path_is_unsafe "$lexical_path"; then
    deny "$(eci_diagnostic_reason "ECI_PROOF_PATH_ESCAPE_DENIED" "PreToolUse" "patch-validation" "$path" "Refusing unsafe session-scoped patch path ${path##*/}: proof-root and context-root ancestors and final components must not be symlinks." "use the current session's canonical path")"
  fi
  if [ "$resolved_path" != "$lexical_path" ] && eci_session_path_is_unsafe "$resolved_path"; then
    deny "$(eci_diagnostic_reason "ECI_PROOF_PATH_ESCAPE_DENIED" "PreToolUse" "patch-validation" "$path" "Refusing unsafe resolved session-scoped patch path ${path##*/}: resolved proof-root and context-root ancestors and final components must not be symlinks." "use the current session's canonical path")"
  fi
  if [[ "$path" == go.mod || "$path" == */go.mod ]] &&
     printf '%s\n' "$patch_text" | grep -Eq '^\+.*=>[[:space:]]*(\.\./|\./)'; then
    deny "$(eci_diagnostic_reason "ECI_GOMOD_LOCAL_REPLACE_DENIED" "PreToolUse" "patch-validation" "$path" "Do not add local relative replace directives to go.mod. Use a workspace, module proxy, or explicit user-approved local override." "use a workspace, module proxy, or explicit user-approved local override")"
  fi
done <<<"$patch_paths"

if [ "$hook_subagent_status" != main ]; then
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    resolved_path="$(eci_resolve_hook_path "${cwd:-$PWD}" "$path" 2>/dev/null || true)"
    lexical_path="$(eci_lexical_hook_path "${cwd:-$PWD}" "$path" 2>/dev/null || true)"
    control_alias=false
    if [ -n "$lexical_path" ] && eci_path_is_eci_control_alias "$lexical_path"; then
      control_alias=true
    elif [ -n "$resolved_path" ] && [ "$resolved_path" != "$lexical_path" ] &&
          eci_path_is_eci_control_alias "$resolved_path"; then
      control_alias=true
    fi
    if { eci_path_is_session_ledger_file "$resolved_path" ||
         eci_path_is_eci_control_file "$lexical_path" ||
         eci_path_is_eci_control_file "$resolved_path" ||
         [ "$control_alias" = true ] ||
         eci_path_is_git_approval_file "$lexical_path" ||
         eci_path_is_git_approval_file "$resolved_path"; }; then
      deny "$(eci_diagnostic_reason "ECI_CONTROL_OWNER_REQUIRED" "PreToolUse" "patch-validation" "$path" "Only the main thread may modify coordinator-owned ECI or authorization state." "route coordinator-owned ECI or authorization changes through the main/orchestrator")"
    fi
  done <<<"$patch_paths"
fi

ownership_busy=""
ownership_failure_deny() {
  local code="${1:-1}"
  # Under set -E a subshell failure exits with its own status, so no deny JSON
  # is printed into a captured value.  The busy flag stops re-entry.
  if [ "$BASHPID" != "$$" ]; then
    exit "$code"
  fi
  # With no active (or unknown) marker the failure exits 0, as the source does.
  if ! declare -F eci_marker_active_or_unknown >/dev/null 2>&1; then
    _eci_lib_unavailable_constant validate-apply-patch
    exit 0
  fi
  eci_marker_active_or_unknown || exit 0
  if [ -n "$ownership_busy" ]; then
    _eci_fail_closed_constant validate-apply-patch
    exit 0
  fi
  ownership_busy=1
  deny "ownership check failed; failing closed for session-scoped path safety (file=${BASH_SOURCE[0]},line=${BASH_LINENO[0]:-unknown},command=${BASH_COMMAND})"
}
trap 'ownership_failure_deny "$?"' ERR
# Session ownership applies to every target under a proof or context root.  An
# owner that resolves must match an allowed session; a session-scoped target
# with no resolvable session id is denied rather than allowed through.
while IFS= read -r path; do
  [ -n "$path" ] || continue
  lexical_owner_path="$(eci_lexical_hook_path "${cwd:-$PWD}" "$path" 2>/dev/null || true)"
  resolved_owner_path="$(eci_resolve_hook_path "${cwd:-$PWD}" "$path" 2>/dev/null || true)"
  # Ordinary repository paths have no session owner to resolve.  Skip the
  # metadata probe unless the target is under a proof or context root.
  if ! eci_path_is_under_proof_root "$lexical_owner_path" &&
     ! eci_path_is_under_proof_root "${resolved_owner_path:-}" &&
     ! eci_context_session_rest "$lexical_owner_path" >/dev/null &&
     ! eci_context_session_rest "${resolved_owner_path:-}" >/dev/null; then
    continue
  fi
  owner_probe="${lexical_owner_path:-$path}"
  owner_session_id="$(eci_path_owner_session_id "$owner_probe" 2>/dev/null || true)"
  [ -n "$owner_session_id" ] || owner_session_id="$(eci_path_owner_session_id "${resolved_owner_path:-$path}" 2>/dev/null || true)"
  [ -n "$owner_session_id" ] || { eci_marker_active_or_unknown && deny "$(eci_diagnostic_reason "ECI_SESSION_OWNER_UNRESOLVED" "PreToolUse" "apply-patch-validation" "$path" "Session-scoped file ${path##*/} has no resolvable session owner; refusing fail-open on a session-scoped path." "use the current session's canonical path")"; continue; }
  eci_hook_allowed_session_ids "$input" "$hook_subagent_status"
  allowed_session_ids=("${ECI_HOOK_ALLOWED_SESSION_IDS[@]}")
  if [ "${#allowed_session_ids[@]}" -eq 0 ]; then
    deny "$(eci_diagnostic_reason "ECI_SESSION_REQUIRED" "PreToolUse" "patch-validation" "$path" "Session-scoped file ${path##*/} requires a current session id; none resolved. Refusing fail-open on a session-scoped path." "use the current session's canonical path")"
  fi
  if ! eci_session_owner_allowed "$owner_session_id" "${allowed_session_ids[@]}"; then
    deny "$(eci_diagnostic_reason "ECI_SESSION_OWNERSHIP_DENIED" "PreToolUse" "patch-validation" "$path" "Refusing to edit ${path##*/}: file belongs to session $owner_session_id, allowed sessions are ${allowed_session_ids[*]}." "use the current session's canonical path")"
  fi
done <<<"$patch_paths"
trap - ERR
eci_install_fail_closed_trap validate-apply-patch

# Active ECI edit callbacks are deliberately marker-only and read-only.  Keep
# the legacy activity bookkeeping for ordinary edits, but do not take its
# lock/write path while a direct marker is active.
eci_direct_marker="$(eci_proof_root)/${session_id}/eci_active"
if [ ! -f "$eci_direct_marker" ] || [ -L "$eci_direct_marker" ]; then
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    eci_note_touched_repo "$session_id" "$cwd" "$path" || true
  done <<<"$patch_paths"
  if [ "$hook_subagent_status" = main ]; then
    eci_mark_activity "$session_id" "$cwd" edit || true
  fi
fi
