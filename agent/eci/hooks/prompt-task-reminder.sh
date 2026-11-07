#!/usr/bin/env bash
# UserPromptSubmit hook: emit the status-report reminder and write the /side stop marker; never scan transcript history.

set -euo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export LC_ALL=C
input_file="$(mktemp)" || exit 0
trap 'rm -f "$input_file"' EXIT
head -c 65537 >"$input_file" || exit 0
if (( $(wc -c <"$input_file") > 65536 )); then exit 0; fi
if (( $(tr -d '\000' <"$input_file" | wc -c) != $(wc -c <"$input_file") )); then exit 0; fi
iconv -f UTF-8 -t UTF-8 "$input_file" >/dev/null 2>&1 || exit 0
jq -se 'length == 1 and (.[0] | type == "object")' "$input_file" >/dev/null 2>&1 || exit 0
# Verify the native hook helper before any reminder is emitted, so a deny never mixes with reminder output.
hook_deny() {
  jq -cn --arg reason "$1" '{hookSpecificOutput:{hookEventName:"UserPromptSubmit",permissionDecision:"deny",permissionDecisionReason:$reason}}'
  exit 0
}
if ! . "$HOOK_DIR/lib/eci-native-binary.sh" 2>/dev/null; then
  hook_deny "ECI native gate binary eci-hook-helper is unavailable: lib/eci-native-binary.sh failed to load; reinstall the ECI hooks, provision the native binaries with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\", and retry"
fi
if ! eci_runtime_open_verified eci-hook-helper; then
  hook_deny "$ECI_RUNTIME_DENY_REASON"
fi
helper_fd="$ECI_RUNTIME_BINARY_FD"
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
if [[ "$reminder" == true ]]; then
  jq -n '{hookSpecificOutput:{hookEventName:"UserPromptSubmit",additionalContext:"For the requested status report, load writing-status-reports; reconcile YAML, validate, render with status-report render, and publish the full rendered result. Correct known missing sections in YAML and rerender; use the documented incomplete fallback only if structural failure prevents rendering."}}'
fi

# Keep the independent, nonblocking reminder even if the steps below fail.
trap 'exit 0' ERR
input="$("/proc/self/fd/$helper_fd" stdin <"$input_file")" || exit 0
rm -f "$input_file"
trap - EXIT
. "$HOOK_DIR/lib/eci-proof-state.sh"

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
  mkdir -p "$reviewer_dir"
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
