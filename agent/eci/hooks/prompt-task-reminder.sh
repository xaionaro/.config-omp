#!/usr/bin/env bash
# UserPromptSubmit hook: emit the status-report reminder and write the /side stop marker; never scan transcript history.

set -Eeuo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export LC_ALL=C
# A deny decision is written to stdout.  bash replaces a closed stdout with
# /dev/null, and a stdout whose target cannot be checked (no /proc) cannot be
# shown to reach the caller; the prompt is blocked with a stderr message.
deny_stdout_usable() {
  [ -e /proc/self/fd/1 ] && ! [ /proc/self/fd/1 -ef /dev/null ]
}
prompt_write_failed() {
  printf '%s\n' 'prompt-task-reminder: could not write the deny decision to stdout; the prompt is blocked' >&2 || true
  exit 2
}
# Verify the native hook helper before any reminder is emitted, so a deny never mixes with reminder output.
hook_deny() {
  deny_stdout_usable || prompt_write_failed
  local json
  if ! json="$(jq -cn --arg reason "$1" '{hookSpecificOutput:{hookEventName:"UserPromptSubmit",permissionDecision:"deny",permissionDecisionReason:$reason,additionalContext:$reason}}' 2>/dev/null)"; then
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","permissionDecision":"deny","permissionDecisionReason":"ECI prompt reminder denied: the reason could not be encoded because jq is unavailable or failed; provision jq and retry","additionalContext":"ECI prompt reminder denied: the reason could not be encoded because jq is unavailable or failed; provision jq and retry"}}' || prompt_write_failed
  elif [ -z "$json" ] || ! printf '%s' "$json" | jq -e . >/dev/null 2>&1; then
    prompt_write_failed
  else
    printf '%s\n' "$json" || prompt_write_failed
  fi
  exit 0
}
# A marker verdict may exit 0 only when the marker predicate is defined and
# reports the marker absent.  An undefined predicate, or an active or unknown
# marker, is a deny.
eci_prompt_marker_verified_absent() {
  declare -F eci_marker_active_or_unknown >/dev/null 2>&1 || return 1
  ! eci_marker_active_or_unknown
}

# Pre-helper denies: with no active or unknown marker the prompt is not under
# ECI control, so exit 0 as the source does; otherwise deny.
prompt_skip_or_deny() {
  if eci_prompt_marker_verified_absent; then
    exit 0
  fi
  hook_deny "$1"
}
if ! . "$HOOK_DIR/lib/eci-native-binary.sh" 2>/dev/null; then
  eci_prompt_marker_verified_absent && exit 0
  hook_deny "[ECI_HOOK_LIB_UNAVAILABLE] ECI native gate binary eci-hook-helper is unavailable: lib/eci-native-binary.sh failed to load; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
fi
if ! . "$HOOK_DIR/lib/eci-proof-state.sh" 2>/dev/null; then
  eci_prompt_marker_verified_absent && exit 0
  hook_deny "[ECI_HOOK_LIB_UNAVAILABLE] ECI prompt reminder denied: lib/eci-proof-state.sh failed to load; reinstall the ECI hooks and retry"
fi
# Every lib function this hook calls must be defined; a partial load would let
# a missing marker check fall through to a silent exit.
for fn in eci_runtime_open_verified eci_marker_active_or_unknown eci_proof_root eci_valid_session_id eci_ensure_cwd_state_dir; do
  declare -F "$fn" >/dev/null 2>&1 || { eci_prompt_marker_verified_absent && exit 0; deny_stdout_usable || prompt_write_failed; printf '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","permissionDecision":"deny","permissionDecisionReason":"[ECI_HOOK_LIB_UNAVAILABLE] ECI prompt reminder denied: library function %s is not defined; reinstall the ECI hooks and retry","additionalContext":"[ECI_HOOK_LIB_UNAVAILABLE] ECI prompt reminder denied: library function %s is not defined; reinstall the ECI hooks and retry"}}\n' "$fn" "$fn" || prompt_write_failed; exit 0; }
done
if ! command -v jq >/dev/null 2>&1; then
  prompt_skip_or_deny "[ECI_JQ_UNAVAILABLE] ECI prompt reminder denied: jq is not installed; install jq and retry"
fi
private_dir="$(mktemp -d -- "${TMPDIR:-${ECI_TMPDIR:-/tmp}}/eci-prompt-reminder.XXXXXX")" || prompt_skip_or_deny "[ECI_PROMPT_SCRATCH_UNAVAILABLE] ECI prompt reminder denied: the private scratch directory cannot be created; ensure TMPDIR is writable and retry"
trap 'eci_runtime_close_verified; rm -rf -- "$private_dir"' EXIT
if ! eci_runtime_open_verified eci-hook-helper; then
  prompt_skip_or_deny "[ECI_NATIVE_HELPER_UNVERIFIED] $ECI_RUNTIME_DENY_REASON"
fi
input_file="$private_dir/input"
helper_output="$private_dir/output"
head -c 65537 >"$input_file" || prompt_skip_or_deny "[ECI_PROMPT_INPUT_INVALID] ECI prompt reminder denied: the prompt input could not be read; retry"
if (( $(wc -c <"$input_file") > 65536 )); then prompt_skip_or_deny "[ECI_PROMPT_INPUT_INVALID] ECI prompt reminder denied: the prompt input exceeds 65536 bytes; shorten the prompt and retry"; fi
if (( $(tr -d '\000' <"$input_file" | wc -c) != $(wc -c <"$input_file") )); then prompt_skip_or_deny "[ECI_PROMPT_INPUT_INVALID] ECI prompt reminder denied: the prompt input contains NUL bytes; remove them and retry"; fi
iconv -f UTF-8 -t UTF-8 "$input_file" >/dev/null 2>&1 || prompt_skip_or_deny "[ECI_PROMPT_INPUT_INVALID] ECI prompt reminder denied: the prompt input is not valid UTF-8; re-enter it and retry"
jq -se 'length == 1 and (.[0] | type == "object")' "$input_file" >/dev/null 2>&1 || prompt_skip_or_deny "[ECI_PROMPT_INPUT_INVALID] ECI prompt reminder denied: the prompt input is not one JSON object; retry"
prompt=$(jq -r 'if (.prompt? | type) == "string" then .prompt else "" end' "$input_file" 2>/dev/null || true)
normalized_prompt="${prompt,,}"
explicit_report='(^|[^[:alpha:]])(give|show|provide|write|send|want|need|request|get|what[[:space:]]+is)([[:space:]]+(me|us|a|an|the|my|our|proper|current|full|detailed|quick|brief|please|latest|updated|comprehensive))*[[:space:]]+(status([[:space:]]+and[[:space:]]+progress)?|progress)[[:space:]]+(report|update)'
reminder=false
while IFS= read -r clause; do
  if [[ "$clause" =~ ^[[:space:]]*what[[:space:]]+is[[:space:]]+(a|an)[[:space:]]+(status([[:space:]]+and[[:space:]]+progress)?|progress)[[:space:]]+(report|update)([[:space:]]+and[[:space:]]+.*)?[[:space:]]*$ ]] ||
     [[ "$clause" =~ ^[[:space:]]*how[[:space:]]+(do|can)[[:space:]]+(i|we)[[:space:]]+get[[:space:]]+(a|an)[[:space:]]+(status([[:space:]]+and[[:space:]]+progress)?|progress)[[:space:]]+(report|update)([[:space:]]+and[[:space:]]+.*)?[[:space:]]*$ ]]; then
    continue
  fi
  if [[ "$clause" =~ $explicit_report ]]; then
    remainder="${clause#*"${BASH_REMATCH[0]}"}"
    # A following noun makes the report a modifier (for example, a template).
    if [[ "$remainder" =~ ^[[:space:]]*$ ]] ||
       [[ "$remainder" =~ ^[[:space:]]+(on|about|for|of|with|please|now|and|right[[:space:]]+now)([[:space:]]|$) ]]; then
      reminder=true
      break
    fi
    continue
  fi
  if [[ "$clause" =~ (fix|repair|change|modify|implement).*(status|progress)[[:space:]]+report ]] ||
     [[ "$clause" =~ how.*(status|progress)[[:space:]]+report.*(works|work|implemented) ]] ||
     [[ "$clause" =~ (status|progress)[[:space:]]+report[[:space:]]+(renderer|tool|generator) ]]; then
    continue
  fi
  if [[ "$clause" =~ ^[[:space:]]*(current[[:space:]]+)?(status|sitrep|progress|checkpoint)([[:space:]]+update)?([[:space:]]+please)?[[:space:]]*$ ]] ||
     [[ "$clause" =~ (give|show|provide|write|send|want|need|request|what.s|what[[:space:]]+is|how.s|how[[:space:]]+is).*(status|progress|sitrep|checkpoint)[[:space:]]*$ ]] ||
     [[ "$clause" =~ ^[[:space:]]*(a[[:space:]]+)?(current[[:space:]]+|updated[[:space:]]+|comprehensive[[:space:]]+)?(status|progress)[[:space:]]+report([[:space:]]+please)?[[:space:]]*$ ]]; then
    reminder=true
    break
  fi
done < <(printf '%s\n' "$normalized_prompt" | sed -E 's/[,;.!?]/\n/g; s/[[:space:]]+then[[:space:]]+/\n/g; s/[[:space:]]+and[[:space:]]+(show|give|provide|write|send|what|how|where|can[[:space:]]+you)[[:space:]]+/\n\1 /g')
# Run the verified helper before any reminder is emitted; a helper runtime failure is an explicit deny, not a silent exit.
helper_rc=0
if [ "$helper_rc" -eq 0 ]; then
  "$ECI_RUNTIME_BINARY_EXEC" stdin <"$input_file" >"$helper_output" || helper_rc=$?
fi
eci_runtime_close_verified
if [ "$helper_rc" -ne 0 ]; then
  prompt_skip_or_deny "[ECI_NATIVE_HELPER_FAILED] ECI native gate binary eci-hook-helper failed to run; provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry"
fi
input="$(cat -- "$helper_output")" || { rm -rf -- "$private_dir"; prompt_skip_or_deny "[ECI_NATIVE_HELPER_OUTPUT_UNREADABLE] ECI prompt reminder denied: the native helper output was unreadable; reinstall the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry"; }
rm -rf -- "$private_dir"

# Fail closed once the helper gate has decided: an internal failure after that
# point denies the prompt rather than exiting 0 with a partial result.  The busy
# flag stops a failure inside the handler from re-entering it.
prompt_busy=""
prompt_fail_closed() {
  local code="${1:-1}"
  if [ "$BASHPID" != "$$" ]; then
    exit "$code"
  fi
  if [ -n "$prompt_busy" ]; then
    deny_stdout_usable || prompt_write_failed
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","permissionDecision":"deny","permissionDecisionReason":"[ECI_PROMPT_INTERNAL_FAILURE] ECI prompt reminder denied: an internal step failed after the native helper gate; provision jq and the native binaries with $HOME/.omp/agent/eci/hooks/build-native-binaries.sh, and retry","additionalContext":"[ECI_PROMPT_INTERNAL_FAILURE] ECI prompt reminder denied: an internal step failed after the native helper gate; provision jq and the native binaries with $HOME/.omp/agent/eci/hooks/build-native-binaries.sh, and retry"}}' || prompt_write_failed
    exit 0
  fi
  prompt_busy=1
  # With no active marker this prompt is not under ECI control, so exit 0 as
  # the source does.  An unknown marker scan, or a proof-state library that did
  # not load, counts as active and denies below.
  if eci_prompt_marker_verified_absent; then
    exit 0
  fi
  hook_deny "[ECI_PROMPT_INTERNAL_FAILURE] ECI prompt reminder denied: an internal step failed after the native helper gate; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
}
trap 'prompt_fail_closed "$?"' ERR
rm -f "$input_file"
trap - EXIT

session_id=$(printf '%s' "$input" | jq -r 'if (.session_id? | type) == "string" then .session_id else "" end' 2>/dev/null || true)
cwd=$(printf '%s' "$input" | jq -r 'if (.cwd? | type) == "string" then .cwd else "" end' 2>/dev/null || true)
root="$(eci_proof_root)"

write_side_stop_marker() {
  local dir="$1"
  mkdir -p "$dir"
  {
    printf 'command: /side\n'
    printf 'parent_session_id: %s\n' "$session_id"
    [ -n "$cwd" ] && printf 'cwd: %s\n' "$cwd"
    date -u '+created_utc: %Y-%m-%dT%H:%M:%SZ'
  } >"$dir/side_stop"
}

if eci_valid_session_id "$session_id"; then
  reviewer_dir="$root/reviewer/$session_id"
  mkdir -p "$reviewer_dir" || prompt_fail_closed 1
  head=$(git -C "$HOME/.omp/agent/eci" rev-parse HEAD 2>/dev/null || true)
  if [ -n "$head" ]; then
    printf '%s\n' "$head" >"$reviewer_dir/prompt_head"
  fi
  rm -f "$reviewer_dir/bypass"

  if printf '%s\n' "$prompt" | grep -Eq '^[[:space:]]*/side([[:space:]]|$)'; then
    write_side_stop_marker "$root/side-stop/sessions/$session_id"
    if [ -n "$cwd" ]; then
      side_dir="$(eci_ensure_cwd_state_dir side-stop "$cwd" 2>/dev/null || true)"
      [ -n "$side_dir" ] && write_side_stop_marker "$side_dir"
    fi
  fi

fi

# The reminder is the last output so a later failure cannot leave two JSON objects.
if [[ "$reminder" == true ]]; then
  jq -n '{hookSpecificOutput:{hookEventName:"UserPromptSubmit",additionalContext:"For the requested status report, load writing-status-reports; reconcile YAML, validate, render with status-report render, and publish the full rendered result. Correct known missing sections in YAML and rerender; use the documented incomplete fallback only if structural failure prevents rendering."}}'
fi
