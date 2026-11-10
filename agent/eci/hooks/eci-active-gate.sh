#!/usr/bin/env bash
# TODO: Retire ECI marker control-path handling while preserving concrete session ownership checks.
# PreToolUse hook: protect active ECI control and ledger targets during edits.

set -Eeuo pipefail

# The hook directory as `dirname` names it, without the extra process.
case "${BASH_SOURCE[0]}" in
  */*) HOOK_DIR="${BASH_SOURCE[0]%/*}"; HOOK_DIR="${HOOK_DIR:-/}" ;;
  *) HOOK_DIR=. ;;
esac
HOOK_DIR="$(cd "$HOOK_DIR" && pwd)"
if ! . "$HOOK_DIR/lib/eci-proof-state.sh" || ! . "$HOOK_DIR/lib/eci-diagnostic.sh"; then
  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] hook eci-active-gate cannot load its ECI hook library; the native install is incomplete or damaged; reinstall the ECI hooks and retry"}}'
  exit 0
fi
# Every lib function this gate calls must be defined; a partial load would let
# a missing marker check fall through to a silent exit.
for fn in eci_marker_active_or_unknown eci_diagnostic_reason eci_diagnostic_value eci_lexical_hook_path eci_resolve_hook_path eci_path_is_eci_control_alias eci_path_is_eci_control_file eci_path_owner_session_id eci_hook_roots_load eci_edit_dispatch_fields_load; do
  declare -F "$fn" >/dev/null 2>&1 || { printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] hook eci-active-gate cannot load ECI library function %s; reinstall the ECI hooks and retry"}}\n' "$fn"; exit 0; }
done

input=$(cat)

# Constant deny used when jq is unavailable or failed.  It names this hook and
# the code only; it carries no repair instruction for another hook.
gate_fallback_deny() {
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"[%s]: hook eci-active-gate denied the tool call while an active marker exists because jq is unavailable or failed; restore jq and inspect the hook log, then retry"}}\n' "$1"
}

# A malformed typed callback is a repair task only while an ECI marker is
# active; with no marker the ordinary tool call keeps its normal validation.
malformed_identity_exit() {
  local detail="${1:-malformed hook identity: tool_name, session_id, and cwd must be typed strings while an active marker exists}"
  local reason
  if eci_marker_active_or_unknown; then
    reason="$(eci_diagnostic_reason "ECI_HOOK_IDENTITY_MALFORMED" "PreToolUse" "edit-routing" "session=${session_id:-<missing>},cwd=${cwd:-<missing>}" "$detail" "provide typed tool_name, session_id, and cwd fields, then retry")" ||
      reason="[ECI_HOOK_IDENTITY_MALFORMED]: $detail; provide typed tool_name, session_id, and cwd fields, then retry"
    if ! jq -n --arg reason "$reason" '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: $reason
      }
    }'; then
      gate_fallback_deny ECI_HOOK_IDENTITY_MALFORMED
    fi
  fi
  exit 0
}

# Fail-closed ERR handler for this gate.  While an active marker exists (or its
# scan is unknown) an unexpected failure denies the tool call; with no marker
# the hook exits 0, as the source gate does.  With set -E the handler is also
# inherited by subshells: a subshell failure exits with its own status so no
# deny JSON is captured into a value.  The busy flag stops a failure inside the
# handler from re-entering it, which would otherwise loop.
_gate_error_busy=""
_eci_gate_error_trap() {
  local line="$1" code="$2" cmd="$3" reason
  if [ "$BASHPID" != "$$" ]; then
    exit "$code"
  fi
  if [ -n "$_gate_error_busy" ]; then
    if eci_marker_active_or_unknown; then
      gate_fallback_deny ECI_HOOK_GATE_ERROR
    fi
    exit 0
  fi
  _gate_error_busy=1
  if eci_marker_active_or_unknown; then
    reason="$(eci_diagnostic_reason "ECI_HOOK_GATE_ERROR" "PreToolUse" "edit-routing" "hook=eci-active-gate,line=$line,exit=$code,command=$cmd" "gate hook failed while an active marker exists; denying the tool call" "inspect the hook log and available temporary-directory/disk state, then retry")" ||
      reason="[ECI_HOOK_GATE_ERROR]: gate hook eci-active-gate failed while an active marker exists; inspect the hook log and available temporary-directory/disk state, then retry"
    if ! jq -n --arg reason "$reason" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}'; then
      gate_fallback_deny ECI_HOOK_GATE_ERROR
    fi
  fi
  exit 0
}
trap '_eci_gate_error_trap "$LINENO" "$?" "$BASH_COMMAND"' ERR

# One jq call validates the identity and reads every stdin field this gate
# uses.  Its NUL-separated fast path covers a single JSON object that passes
# the identity check below and whose target field is a string (or absent, null
# or false) without NUL bytes; any other input takes the per-field reads, so
# every value, and every decision built from it, is the same either way.
parsed_fields=()
# pretooluse-edit-dispatch.sh may already have read these fields with the same
# identity check (eci_edit_dispatch_fields_load, field 6); otherwise this gate
# reads them itself.
if eci_edit_dispatch_fields_load "$input" && [ "${eci_edit_dispatch_fields[6]}" = yes ]; then
  parsed_fields=("${eci_edit_dispatch_fields[@]:0:4}" ok)
else
  mapfile -d '' -t parsed_fields < <(printf '%s' "$input" | jq -nj '[inputs]
    | if length != 1 then empty else .[0]
      | if type == "object" and (.tool_name | type) == "string" and
          (.tool_name | length > 0) and
          (.session_id | type) == "string" and ((.cwd | type) == "string" or (.cwd | type) == "null")
        then [.tool_name, .session_id, (.cwd // ""),
              (if .tool_name == "apply_patch" then (.tool_input.command // .tool_input.patch // .tool_input.input // "")
               elif .tool_name == "Edit" or .tool_name == "Write" or .tool_name == "MultiEdit" or .tool_name == "NotebookEdit"
               then (.tool_input.file_path // .tool_input.notebook_path // .tool_input.path // .tool_input.target_file // "")
               else "" end)]
          | if all(.[]; type == "string" and (test("\u0000") | not)) and all(.[0:3][]; endswith("\n") | not)
            then (.[] + "\u0000"), "ok\u0000" else empty end
        else empty end
      end' 2>/dev/null || true)
fi
if [ "${#parsed_fields[@]}" -eq 5 ] && [ "${parsed_fields[4]}" = ok ]; then
  tool_name="${parsed_fields[0]}"
  session_id="${parsed_fields[1]}"
  cwd="${parsed_fields[2]}"
  tool_path_source="${parsed_fields[3]}"
  # The fast path admits no identity field ending in a newline, so these equal
  # the $(jq -r ...) values, which drop trailing newlines.
  parsed_input=true
else
  parsed_input=false
  if ! printf '%s' "$input" | jq -e '
    type == "object" and (.tool_name | type) == "string" and
    (.tool_name | length > 0) and
    (.session_id | type) == "string" and ((.cwd | type) == "string" or (.cwd | type) == "null")
  ' >/dev/null 2>&1; then
    # Typed tool_name and session_id must be strings while an active marker
    # exists; a malformed callback is then a repair task, not a silent allow.
    # An absent cwd is denied while an active marker exists; with no marker the ordinary path keeps.
    malformed_identity_exit
  fi
  tool_name=$(printf '%s' "$input" | jq -r '.tool_name' 2>/dev/null || true)

  session_id=$(printf '%s' "$input" | jq -r '.session_id' 2>/dev/null || true)
  cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null || true)
fi
case "$session_id" in
  ""|*[!A-Za-z0-9_-]*)
    malformed_identity_exit "hook identity has an empty or invalid session_id while an active marker exists; session_id must be made of letters, digits, underscore, and hyphen"
    ;;
esac
[ -n "$cwd" ] || malformed_identity_exit "hook identity has no cwd while an active marker exists; cwd must be the absolute working directory string"
case "$cwd" in
  *[![:print:]]*)
    malformed_identity_exit
    ;;
esac

case "$tool_name" in
  apply_patch|Edit|Write|MultiEdit|NotebookEdit) ;;
  *) exit 0 ;;
esac

# The tool's target text as `jq -r '<target expression> // empty'` prints it;
# an empty string and an absent target both yield no path below.
tool_path_text() {
  if [ "$parsed_input" = true ]; then
    [ -z "$tool_path_source" ] || printf '%s\n' "$tool_path_source"
  elif [ "$1" = apply_patch ]; then
    printf '%s' "$input" |
      jq -r '.tool_input.command // .tool_input.patch // .tool_input.input // empty' 2>/dev/null
  else
    printf '%s' "$input" |
      jq -r '.tool_input.file_path // .tool_input.notebook_path // .tool_input.path // .tool_input.target_file // empty' 2>/dev/null
  fi
}

tool_paths() {
  case "$tool_name" in
    apply_patch)
      tool_path_text apply_patch |
        awk '
          /^\*\*\* (Add|Update|Delete) File: / {
            sub(/^\*\*\* (Add|Update|Delete) File: /, "")
            print
          }
          /^\*\*\* Move to: / {
            sub(/^\*\*\* Move to: /, "")
            print
          }
        '
      ;;
    Edit|Write|MultiEdit|NotebookEdit)
      tool_path_text file
      ;;
  esac
}

markdown_only_edit() {
  local path
  local seen=false

  while IFS= read -r path; do
    [ -n "$path" ] || continue
    seen=true
    case "$path" in
      *.[mM][dD]|*.[mM][aA][rR][kK][dD][oO][wW][nN]) ;;
      *) return 1 ;;
    esac
  done

  [ "$seen" = "true" ]
}

# These are current-session coordination documents, not cross-scope lifecycle
# controls.  Keep the exception scoped to the callback's own proof session so
# a foreign proof record still reaches the concrete control boundary below.
#
# The high-level anchor is derived local coordination state.  PreToolUse sees
# a requested write, not its result, so requiring a record-shaped payload or a
# hash here would make metadata a prerequisite for ordinary coordinator work.
# A later PostToolUse reconciler could regenerate the anchor from the actual
# log bytes; until one exists, a coordinator's own bounded update remains
# nonblocking rather than becoming a lifecycle-artifact denial.
current_session_working_document() {
  local path="${1:-}" owner_session

  case "${path##*/}" in
    project-understanding.yaml|latest-status-report.md|latest-status-report.yaml|handoff.md|high_level_log.jsonl|high_level_log.anchor) ;;
    *) return 1 ;;
  esac
  owner_session="$(eci_path_owner_session_id "$path" 2>/dev/null || true)"
  [ "$owner_session" = "$session_id" ]
}

tool_path_list="$(tool_paths)"

# Resolve the proof, default-proof and context roots once in this process;
# the control-path predicates below and their subshells reuse them.  A single
# Edit-family target is resolved in the same realpath call.
gate_single_target=""
case "$tool_name" in
  Edit|Write|MultiEdit|NotebookEdit)
    case "$tool_path_list" in *$'\n'*) ;; *) gate_single_target="$tool_path_list" ;; esac
    ;;
esac
if [ -n "$gate_single_target" ]; then
  eci_hook_roots_load "$cwd" "$gate_single_target"
else
  eci_hook_roots_load
fi

# A coordinator's ordinary repository edit must not become a control edit
# solely because it is made from the main thread.  Preserve the real target
# boundary, including lexical aliases and resolved proof-root controls, before
# the Markdown fast path below permits ordinary documentation edits.
while IFS= read -r control_path; do
  [ -n "$control_path" ] || continue
  lexical_control_path="$(eci_lexical_hook_path "$cwd" "$control_path" 2>/dev/null || true)"
  if [ "$eci_hook_roots_target_ready" = 1 ] && [ "$control_path" = "$gate_single_target" ]; then
    resolved_control_path="$eci_hook_roots_target_resolved"
  else
    resolved_control_path="$(eci_resolve_hook_path "$cwd" "$control_path" 2>/dev/null || true)"
  fi
  control_alias=false
  if [ -n "$lexical_control_path" ] && eci_path_is_eci_control_alias "$lexical_control_path"; then
    control_alias=true
  elif [ -n "$resolved_control_path" ] && [ "$resolved_control_path" != "$lexical_control_path" ] &&
    eci_path_is_eci_control_alias "$resolved_control_path"; then
    control_alias=true
  fi
  if { eci_path_is_eci_control_file "$lexical_control_path" ||
       { [ "$resolved_control_path" != "$lexical_control_path" ] &&
         eci_path_is_eci_control_file "$resolved_control_path"; } ||
       [ "$control_alias" = true ]; } &&
     ! current_session_working_document "${resolved_control_path:-$lexical_control_path}"; then
    target_value="$(eci_diagnostic_value "${resolved_control_path:-$control_path}")"
    reason="$(eci_diagnostic_reason "ECI_CONTROL_OWNER_REQUIRED" "PreToolUse" "edit-routing" "tool=$tool_name,target=$target_value" "ECI denies direct edits to coordinator-owned active control state" "route this exact control-state operation through its coordinator lifecycle owner")"
    jq -n --arg reason "$reason" '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: $reason
      }
    }'
    exit 0
  fi
done <<< "$tool_path_list"

if printf '%s\n' "$tool_path_list" | markdown_only_edit; then
  exit 0
fi

# Marker discovery and callback-parent state are not edit targets.  They may
# be stale, incomplete, or disagree while this callback still names an
# ordinary repository file.  The concrete path checks above have already
# stopped direct marker, ledger, active-control, and cross-session control
# targets; leave every other edit to normal routing/validation.
exit 0
