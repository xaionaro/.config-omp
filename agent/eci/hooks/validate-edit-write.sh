#!/usr/bin/env bash
# TODO: Replace the retired ECI marker activity check with ordinary callback bookkeeping.
# PreToolUse hook: validate direct file edits made through Edit, Write, MultiEdit, and NotebookEdit.

set -Eeuo pipefail

# A deny decision that cannot be written exits 2 with a stderr message: the tool
# call is blocked, and the hook never exits 1 with no output.
deny_write_failed() {
  printf '%s\n' "$1: could not write the deny decision to stdout; the tool call is blocked" >&2 || true
  exit 2
}
# A deny decision is written to stdout.  bash replaces a closed stdout with
# /dev/null, and a stdout whose target cannot be checked (no /proc) cannot be
# shown to reach the caller; the tool call is blocked with a stderr message.
deny_stdout_usable() {
  [ -e /proc/self/fd/1 ] && ! [ /proc/self/fd/1 -ef /dev/null ]
}

# The hook directory as `dirname` names it, without the extra process.
case "${BASH_SOURCE[0]}" in
  */*) HOOK_DIR="${BASH_SOURCE[0]%/*}"; HOOK_DIR="${HOOK_DIR:-/}" ;;
  *) HOOK_DIR=. ;;
esac
HOOK_DIR="$(cd "$HOOK_DIR" && pwd)"
if ! . "$HOOK_DIR/lib/eci-proof-state.sh" || ! . "$HOOK_DIR/lib/eci-tmp.sh" || ! . "$HOOK_DIR/lib/eci-diagnostic.sh"; then
  deny_stdout_usable || deny_write_failed validate-edit-write
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] hook validate-edit-write cannot load its ECI hook library; the native install is incomplete or damaged; reinstall the ECI hooks and retry"}}' || deny_write_failed validate-edit-write
  exit 0
fi
# Every lib function this hook calls must be defined; a partial load would let
# a missing marker check fall through to a silent exit.
for fn in eci_init_tmp eci_require_jq_or_deny eci_install_fail_closed_trap eci_diagnostic_value eci_diagnostic_reason eci_diagnostic_code_for_reason eci_marker_active_or_unknown eci_hook_subagent_status eci_hook_subagent_status_seed eci_edit_dispatch_fields_load eci_strip_trailing_newlines eci_proof_root_load eci_hook_roots_load eci_resolve_hook_path eci_lexical_hook_path eci_session_path_is_unsafe eci_path_is_eci_control_alias eci_path_is_session_ledger_file eci_path_is_eci_control_file eci_path_is_git_approval_file eci_path_owner_session_id eci_hook_allowed_session_ids eci_session_owner_allowed eci_path_is_under_proof_root eci_context_session_rest eci_proof_root eci_note_touched_repo eci_mark_activity _eci_fail_closed_constant _eci_lib_unavailable_constant; do
  declare -F "$fn" >/dev/null 2>&1 || { deny_stdout_usable || deny_write_failed validate-edit-write; printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] hook validate-edit-write cannot load ECI library function %s; reinstall the ECI hooks and retry"}}\n' "$fn" || deny_write_failed validate-edit-write; exit 0; }
done
eci_init_tmp || true
eci_require_jq_or_deny validate-edit-write
# Leave failure classification to the dispatcher, which knows the target.
edit_guard_error() {
  local line="$1" code="$2" cmd="$3"
  printf 'validate-edit-write failed at line %s (exit %s): %s\n' "$line" "$code" "$cmd" >&2 || true
  exit "$code"
}
trap 'edit_guard_error "$LINENO" "$?" "$BASH_COMMAND"' ERR

input=$(cat)
# pretooluse-edit-dispatch.sh parsed the fields below from this stdin with one jq
# call (eci_edit_dispatch_fields_load); they equal the per-field reads, which
# run instead when it did not, or could not, take its fast path.
parsed_input=false
if eci_edit_dispatch_fields_load "$input"; then
  parsed_input=true
  tool_name="${eci_edit_dispatch_fields[0]}"
  session_id="${eci_edit_dispatch_fields[1]}"
  cwd="${eci_edit_dispatch_fields[2]}"
  parsed_file_path="${eci_edit_dispatch_fields[3]}"
  parsed_transcript="${eci_edit_dispatch_fields[5]}"
  # As $(jq -r ...) would, drop trailing newlines.
  eci_strip_trailing_newlines tool_name
  eci_strip_trailing_newlines session_id
  eci_strip_trailing_newlines cwd
  eci_strip_trailing_newlines parsed_file_path
  eci_strip_trailing_newlines parsed_transcript
  eci_hook_subagent_status_seed "$input" "${eci_edit_dispatch_fields[4]}" "$parsed_transcript"
else
  tool_name=$(printf '%s' "$input" | jq -r '.tool_name // empty' 2>/dev/null || true)
  session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null || true)
  cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null || true)
fi

deny() {
  local reason="${1:-unspecified edit validation denial}"
  if [[ "$reason" != \[ECI_* ]]; then
    local subject
    subject="tool=${tool_name:-Edit},path=$(eci_diagnostic_value "${file_path:-<missing>}"),session=$(eci_diagnostic_value "${session_id:-<missing>}"),cwd=$(eci_diagnostic_value "${cwd:-<missing>}")"
    reason="$(eci_diagnostic_reason "$(eci_diagnostic_code_for_reason "$reason")" "PreToolUse" "edit-validation" "$subject" "$reason" "correct the reported edit target or route the change through the approved source/coordinator path, then retry")"
  fi
  local json
  json="$(jq -n --arg reason "$reason" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: $reason
    }
  }')" || deny_write_failed validate-edit-write
  if [ -z "$json" ] || ! printf '%s' "$json" | jq -e . >/dev/null 2>&1; then
    deny_write_failed validate-edit-write
  fi
  deny_stdout_usable || deny_write_failed validate-edit-write
  printf '%s\n' "$json" || deny_write_failed validate-edit-write
  exit 0
}

# A callback with no tool name cannot be classified.  While an active marker
# exists it is denied; with no marker the ordinary path exits 0.
case "$tool_name" in
  Edit|Write|MultiEdit|NotebookEdit) ;;
  "")
    if eci_marker_active_or_unknown; then
      deny "[ECI_EDIT_TOOL_NAME_EMPTY] tool_name is empty while an active marker exists; the edit callback cannot be classified, so it is denied"
    fi
    exit 0
    ;;
  *) exit 0 ;;
esac

if [ "$parsed_input" = true ]; then
  file_path="$parsed_file_path"
else
  file_path=$(printf '%s' "$input" | jq -r '.tool_input.file_path // .tool_input.notebook_path // .tool_input.path // .tool_input.target_file // empty' 2>/dev/null || true)
fi

if [ -z "$file_path" ]; then
  if eci_marker_active_or_unknown; then
    deny "[ECI_EDIT_TARGET_EMPTY] edit target path is empty while an active marker exists; the edit cannot be attributed to a session-scoped path, so it is denied"
  fi
  exit 0
fi

# Thread ownership is resolved once.  "unknown" means the transcript could not
# be verified; subagent-guarded control and ledger edits fail closed on it.
eci_hook_subagent_status "$input"
hook_subagent_status="$ECI_HOOK_SUBAGENT_STATUS"

# Resolve the proof, default-proof and context roots once in this process, with
# the target's resolved form in the same realpath call; the path predicates
# below and their subshells reuse them.
eci_hook_roots_load "${cwd:-$PWD}" "$file_path"
if [ "$eci_hook_roots_target_ready" = 1 ]; then
  resolved_file_path="$eci_hook_roots_target_resolved"
else
  resolved_file_path="$(eci_resolve_hook_path "${cwd:-$PWD}" "$file_path" 2>/dev/null || true)"
fi
lexical_file_path="$(eci_lexical_hook_path "${cwd:-$PWD}" "$file_path" 2>/dev/null || true)"
# A relative ECI_PROOF_ROOT cannot be verified as a proof root, so the marker
# state is unknown.  An edit under that root (lexically or resolved), or under
# the context root, is denied while the marker check reports active or unknown;
# ordinary targets are not affected.
under_relative_proof_root() {
  local rel_root
  for rel_root in "$(eci_lexical_hook_path "${cwd:-$PWD}" "$ECI_PROOF_ROOT" 2>/dev/null || true)" "$(eci_resolve_hook_path "${cwd:-$PWD}" "$ECI_PROOF_ROOT" 2>/dev/null || true)"; do
    [ -n "$rel_root" ] || continue
    case "$1" in "$rel_root"|"$rel_root"/*) return 0 ;; esac
    case "$2" in "$rel_root"|"$rel_root"/*) return 0 ;; esac
  done
  return 1
}
case "${ECI_PROOF_ROOT:-}" in
  ""|/*) ;;
  *)
    if { under_relative_proof_root "$lexical_file_path" "$resolved_file_path" ||
         eci_context_session_rest "$lexical_file_path" >/dev/null 2>&1 ||
         eci_context_session_rest "$resolved_file_path" >/dev/null 2>&1; } &&
       eci_marker_active_or_unknown; then
      deny "[ECI_PROOF_ROOT_RELATIVE] ECI_PROOF_ROOT is not an absolute path, so the proof root cannot be verified and the marker state is unknown; this edit targets the proof root or a context root and is denied; set ECI_PROOF_ROOT to an absolute path and retry"
    fi
    ;;
esac

if eci_session_path_is_unsafe "$lexical_file_path"; then
  deny "$(eci_diagnostic_reason "ECI_PROOF_PATH_ESCAPE_DENIED" "PreToolUse" "edit-validation" "$file_path" "Refusing unsafe session-scoped edit path ${file_path##*/}: proof-root and context-root ancestors and final components must not be symlinks." "use the current session's canonical path")"
fi
if [ "$resolved_file_path" != "$lexical_file_path" ] && eci_session_path_is_unsafe "$resolved_file_path"; then
  deny "$(eci_diagnostic_reason "ECI_PROOF_PATH_ESCAPE_DENIED" "PreToolUse" "edit-validation" "$file_path" "Refusing unsafe resolved session-scoped edit path ${file_path##*/}: resolved proof-root and context-root ancestors and final components must not be symlinks." "use the current session's canonical path")"
fi
if [ "$hook_subagent_status" != main ] &&
   { control_alias=false
     if [ -n "$lexical_file_path" ] && eci_path_is_eci_control_alias "$lexical_file_path"; then
       control_alias=true
     elif [ "$resolved_file_path" != "$lexical_file_path" ] &&
          eci_path_is_eci_control_alias "$resolved_file_path"; then
       control_alias=true
     fi
     eci_path_is_session_ledger_file "$resolved_file_path" ||
     eci_path_is_eci_control_file "$lexical_file_path" ||
     eci_path_is_eci_control_file "$resolved_file_path" ||
     [ "$control_alias" = true ] ||
     eci_path_is_git_approval_file "$lexical_file_path" ||
     eci_path_is_git_approval_file "$resolved_file_path"; }; then
  deny "$(eci_diagnostic_reason "ECI_CONTROL_OWNER_REQUIRED" "PreToolUse" "edit-validation" "$file_path" "Only the main thread may modify coordinator-owned ECI or authorization state." "route coordinator-owned ECI or authorization changes through the main/orchestrator")"
fi

# Session-scoped targets fail closed.  An owner that resolves must match an
# allowed session, and a session-scoped target with no resolvable session id
# is denied rather than allowed through.
ownership_failure_deny() {
  local code="${1:-1}"
  # Under set -E a subshell failure exits with its own status, so no deny JSON
  # is printed into a captured value.
  if [ "$BASHPID" != "$$" ]; then
    exit "$code"
  fi
  # Unconditional deny, as source 575bb81:hooks/validate-edit-write.sh:85-88.
  # deny() exits 0 after writing its decision, or exits 2 if that write fails,
  # so it never returns here.
  deny "ownership check failed; failing closed for session-scoped path safety (file=${BASH_SOURCE[0]},line=${BASH_LINENO[0]:-unknown},command=${BASH_COMMAND})"
}
trap 'ownership_failure_deny "$?"' ERR
owner_probe="${lexical_file_path:-$file_path}"
owner_session_id="$(eci_path_owner_session_id "$owner_probe" 2>/dev/null || true)"
[ -n "$owner_session_id" ] || owner_session_id="$(eci_path_owner_session_id "${resolved_file_path:-$file_path}" 2>/dev/null || true)"
if [ -n "$owner_session_id" ]; then
  eci_hook_allowed_session_ids "$input" "$hook_subagent_status"
  allowed_session_ids=("${ECI_HOOK_ALLOWED_SESSION_IDS[@]}")
  if [ "${#allowed_session_ids[@]}" -eq 0 ]; then
    deny "$(eci_diagnostic_reason "ECI_SESSION_REQUIRED" "PreToolUse" "edit-validation" "$file_path" "Session-scoped file ${file_path##*/} requires a current session id; none resolved. Refusing fail-open on a session-scoped path." "use the current session's canonical path")"
  fi
  if ! eci_session_owner_allowed "$owner_session_id" "${allowed_session_ids[@]}"; then
    deny "$(eci_diagnostic_reason "ECI_SESSION_OWNERSHIP_DENIED" "PreToolUse" "edit-validation" "$file_path" "Refusing to edit ${file_path##*/}: file belongs to session $owner_session_id, allowed sessions are ${allowed_session_ids[*]}." "use the current session's canonical path")"
  fi
elif { eci_path_is_under_proof_root "$lexical_file_path" ||
       eci_path_is_under_proof_root "$resolved_file_path" ||
       eci_context_session_rest "$lexical_file_path" >/dev/null ||
       eci_context_session_rest "$resolved_file_path" >/dev/null; } &&
     eci_marker_active_or_unknown; then
  deny "$(eci_diagnostic_reason "ECI_SESSION_OWNER_UNRESOLVED" "PreToolUse" "edit-validation" "$file_path" "Session-scoped file ${file_path##*/} has no resolvable session owner; refusing fail-open on a session-scoped path." "use the current session's canonical path")"
fi
trap - ERR
trap 'edit_guard_error "$LINENO" "$?" "$BASH_COMMAND"' ERR

eci_proof_root_load
eci_direct_marker="$eci_proof_root_value/${session_id}/eci_active"
if [ ! -f "$eci_direct_marker" ] || [ -L "$eci_direct_marker" ]; then
  eci_note_touched_repo "$session_id" "$cwd" "$file_path" || true
  if [ "$hook_subagent_status" = main ]; then
    eci_mark_activity "$session_id" "$cwd" edit || true
  fi
fi
