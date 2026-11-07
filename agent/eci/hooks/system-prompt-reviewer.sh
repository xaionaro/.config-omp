#!/usr/bin/env bash
# TODO: Remove ECI marker-based VCS context suppression; native goals own lifecycle.
# Stop reviewer: fail-open external compliance check for main sessions.

set -uo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/lib/eci-proof-state.sh"
. "$HOOK_DIR/lib/eci-tmp.sh"
. "$HOOK_DIR/lib/reviewer-backend.sh"
. "$HOOK_DIR/lib/compose-reviewer-prompt.sh"
. "$HOOK_DIR/lib/reviewer-filter.sh"
. "$HOOK_DIR/lib/reviewer-call.sh"
. "$HOOK_DIR/lib/reviewer-redact.sh"
. "$HOOK_DIR/lib/eci-diagnostic.sh"
eci_init_tmp || true

input=$(cat)
session_id=$(printf '%s' "$input" | jq -r '.session_id // empty' 2>/dev/null || true)
cwd=$(printf '%s' "$input" | jq -r '.cwd // empty' 2>/dev/null || true)
[ -z "$cwd" ] && cwd="$PWD"

eci_valid_session_id "$session_id" || exit 0
if [ "$(eci_hook_subagent_status "$input")" = subagent ]; then
  exit 0
fi

if ! parse_reviewer_env ECI_STOP_REVIEWER; then
  exit 0
fi
[ -n "$REVIEWER_BACKEND" ] || exit 0

root="$(eci_proof_root)"
state_dir="$root/reviewer/$session_id"
mkdir -p "$state_dir" 2>/dev/null || exit 0
[ -f "$state_dir/bypass" ] && exit 0

find_transcript() {
  local path
  path=$(printf '%s' "$input" | jq -r '.transcript_path // empty' 2>/dev/null || true)
  if [ -n "$path" ] && [ -f "$path" ]; then
    printf '%s\n' "$path"
    return 0
  fi
  find "$HOME/.omp/agent/sessions" -name "*${session_id}*.jsonl" -type f 2>/dev/null | head -n1
}

filter_reviewer_processes() {
  awk '
    {
      lower = tolower($0)
      if (lower ~ /\/\.omp\/agent\/eci\/hooks\//) next
      if (lower ~ /(system-prompt-reviewer|reviewer-call|reviewer-backend|compose-reviewer-prompt|stop-gate\.sh)/) next
      print
    }
  '
}

render_transcript() {
  local transcript="$1"
  [ -f "$transcript" ] || return 0
  jq -rs \
    --arg synth_re "$SYNTHETIC_USER_TAG_RE" \
    --argjson tr_cap 1000 \
    --argjson asst_cap 1500 \
    --argjson input_cap 5000 \
    '
    def response_item_type($e):
      $e.payload.type // $e.payload.item.type // "";
    def content_of($e):
      $e.message.content // $e.payload.message.content // $e.payload.item.content // $e.payload.content // "";
    def event_role($e):
      if $e.type == "user" then "user"
      elif $e.type == "assistant" then "assistant"
      elif $e.type == "response_item" then
        if response_item_type($e) == "function_call" then "assistant"
        elif response_item_type($e) == "function_call_output" then "tool_result"
        else ($e.payload.role // $e.payload.item.role // "") end
      elif $e.type == "message" then ($e.role // "")
      else "" end;
    def ends_trim($cap):
      if (. | length) <= $cap then .
      else (($cap / 2) | floor) as $half
        | .[:$half] + "...[truncated " + ((. | length) - $cap | tostring) + " chars]..." + .[-$half:]
      end;
    def text_parts:
      if type == "string" then .
      elif type == "array" then
        [.[] |
          if type == "string" then .
          elif .type == "text" or .type == "input_text" or .type == "output_text" then (.text // "")
          elif .type == "tool_result" or .type == "function_call_output" then
            (.content | if type == "string" then . elif type == "array" then ([.[] | .text? // .content? // empty] | join(" ")) else "" end)
          else "" end] | join(" ")
      else "" end;
    def is_real_user($e):
      event_role($e) == "user"
      and ((content_of($e) | type) == "string")
      and (($e.isMeta // $e.message.isMeta // false) | not)
      and (($e.origin.kind // $e.message.origin.kind // "") == "")
      and ((content_of($e) | tostring | test($synth_re; "i")) | not);
    def render_user_text($e):
      (content_of($e) | text_parts | ends_trim(1000)) as $text
      | if ($text | length) > 0 then "USER: " + $text else null end;
    def tool_output_text:
      if type == "string" then ends_trim($tr_cap)
      elif type == "array" then ([.[] | .text? // .content? // .output? // empty] | join(" ") | ends_trim($tr_cap))
      elif type == "object" then (tostring | ends_trim($tr_cap))
      else "" end;
    def render_tool_result($e):
      if $e.type == "response_item" and response_item_type($e) == "function_call_output" then
        (($e.payload.output // $e.payload.content // $e.payload.item.output // $e.payload.item.content // "") | tool_output_text) as $text
        | if ($text | length) > 0 then "TOOL_RESULT: [" + $text + "]" else null end
      else
        (content_of($e) as $c
        | if ($c | type) == "array" then
            [$c[] | select(.type == "tool_result" or .type == "function_call_output")
              | ((.content // .output // "") | tool_output_text)]
          else [] end as $items
        | if ($items | length) > 0 then "TOOL_RESULT: " + ($items | map("[" + . + "]") | join(" ")) else null end)
      end;
    def render_assistant($e):
      if $e.type == "response_item" and response_item_type($e) == "function_call" then
        ("[tool_use=" + ($e.payload.name // $e.payload.item.name // "") +
          " input=" + (($e.payload.arguments // $e.payload.input // $e.payload.item.arguments // $e.payload.item.input // "") | tostring | ends_trim($input_cap)) + "]") as $body
        | if ($body | length) > 0 then "ASSISTANT: " + $body else null end
      else
        (content_of($e) as $c
        | (
          if ($c | type) == "array" then
            [$c[] |
              if .type == "text" or .type == "output_text" then (.text // "" | ends_trim($asst_cap))
              elif .type == "tool_use" then
                "[tool_use=" + (.name // "") + " input=" + ((.input // {}) | tostring | ends_trim($input_cap)) + "]"
              elif .type == "function_call" then
                "[tool_use=" + (.name // "") + " input=" + ((.arguments // "") | tostring | ends_trim($input_cap)) + "]"
              else "" end] | join(" ")
          elif ($c | type) == "string" then ($c | ends_trim($asst_cap))
          else "" end
        ) as $body
        | if ($body | length) > 0 then "ASSISTANT: " + $body else null end)
      end;
    def wrap_entries:
      map("<entry>" + (split("</entry>") | join("</_entry>") | split("<entry>") | join("<_entry>")) + "</entry>") | join("\n");
    . as $all
    | ([ $all | to_entries[] | select(is_real_user(.value)) | .key ] | last // 0) as $lts
    | ($all | to_entries
        | map(select(.key < $lts))
        | map(if is_real_user(.value) then render_user_text(.value) else null end)
        | map(select(. != null))
        | wrap_entries) as $history
    | ($all | to_entries
        | map(select(.key >= $lts))
        | map(if is_real_user(.value) then render_user_text(.value)
              elif event_role(.value) == "user" then render_tool_result(.value)
              elif event_role(.value) == "tool_result" then render_tool_result(.value)
              elif event_role(.value) == "assistant" then render_assistant(.value)
              else null end)
        | map(select(. != null))
        | wrap_entries) as $current
    | "## USER_HISTORY\n\n" + $history + "\n\n## CURRENT_TURN\n\n" + $current
    ' "$transcript" 2>/dev/null | tail -c 120000
}

append_repo_context() {
  local repo="$1"
  printf '\n\n## VCS_STATUS\n\n'
  local eci_marker="$root/$session_id/eci_active"
  if [ -e "$eci_marker" ]; then
    printf '(skipped: ECI active; working-tree state is transient until delegated/subagent work lands.)\n'
    printf '\n\n## DIFF\n\n'
    printf '(skipped: same reason as VCS_STATUS.)\n'
  elif git -C "$repo" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    local porcelain
    porcelain=$(git -C "$repo" status --porcelain 2>/dev/null || true)
    if [ -z "$porcelain" ]; then
      printf '### %s (git)\nclean - all changes committed\n' "$repo"
    else
      printf '### %s (git)\nDIRTY\n%s\n' "$repo" "$porcelain" | head -c 4096
    fi
    printf '\n\n## DIFF\n\n'
    git -C "$repo" log --pretty=format:'%H %s' -5 2>/dev/null || true
    printf '\n'
    local base diff_raw diff_bytes
    base="HEAD~1"
    if [ -s "$state_dir/prompt_head" ]; then
      local prompt_head
      prompt_head=$(cat "$state_dir/prompt_head" 2>/dev/null || true)
      if [ -n "$prompt_head" ] && git -C "$repo" cat-file -e "${prompt_head}^{commit}" 2>/dev/null; then
        base="$prompt_head"
      fi
    fi
    diff_raw=$(git -C "$repo" diff "$base..HEAD" 2>/dev/null || true)
    diff_bytes=$(printf '%s' "$diff_raw" | wc -c | awk '{print $1}')
    if [ "${diff_bytes:-0}" -gt 4096 ]; then
      printf '(diff body omitted: %s bytes raw, exceeds 4096-byte budget)\n' "$diff_bytes"
    else
      printf '%s\n' "$diff_raw"
    fi
  else
    printf '(no git repository detected for %s)\n' "$repo"
  fi

  printf '\n## BACKGROUND_PROCESSES\n\n'
  ps -eo pid,ppid,etimes,stat,cmd -u "$USER" --no-headers 2>/dev/null |
    awk -v me="$$" -v me_pp="$PPID" '$3 <= 3600 && $1 != me && $2 != me && $1 != me_pp && $2 != me_pp' |
    filter_reviewer_processes |
    head -30 |
    redact_sensitive_text
}

rules_file=$(mktemp "$TMPDIR/eci-system-rules.XXXXXX")
body_file=$(mktemp "$TMPDIR/eci-system-body.XXXXXX")
trap 'rm -f "$rules_file" "$body_file"' EXIT

compose_reviewer_prompt "$HOOK_DIR/reviewer-rules.md" >"$rules_file" || exit 0

transcript=$(find_transcript || true)
{
  if [ -n "$transcript" ]; then
    render_transcript "$transcript"
  else
    printf '## USER_HISTORY\n\n(no transcript found)\n\n## CURRENT_TURN\n\n(no transcript found)\n'
  fi
  append_repo_context "$cwd"
} >"$body_file"

redacted_body=$(mktemp "$TMPDIR/eci-system-redacted.XXXXXX")
if redact_sensitive_text <"$body_file" >"$redacted_body" 2>/dev/null; then
  mv "$redacted_body" "$body_file"
else
  rm -f "$redacted_body"
fi

if [ -n "${ECI_REVIEWER_DEBUG_BODY_PATH:-}" ]; then
  cp "$body_file" "$ECI_REVIEWER_DEBUG_BODY_PATH" 2>/dev/null || true
fi

model="$REVIEWER_BACKEND"
start_call=$(date +%s)
if [ -n "${ECI_REVIEWER_FAKE_RESULT:-}" ]; then
  result=$(printf '%s' "$ECI_REVIEWER_FAKE_RESULT" | reviewer_strip_fences)
  model="fake"
else
  result=$(reviewer_call_chat "stop_reviewer" "$rules_file" "$body_file" "$HOOK_DIR/lib/reviewer-schema.json" "$ECI_STOP_REVIEWER_TIMEOUT" 2>/dev/null) || {
    printf '%s\n' "$(eci_diagnostic_reason "ECI_REVIEW_BACKEND_CALL_FAILED" "Stop" "external-review" "reviewer=$(eci_diagnostic_value "$REVIEWER_BACKEND"),session=$(eci_diagnostic_value "$session_id"),cwd=$(eci_diagnostic_value "$cwd")" "reviewer call failed; review skipped" "retry the reviewer when the backend is available; Stop admission remains unchanged")" >&2
    exit 0
  }
fi
elapsed=$(( $(date +%s) - start_call ))

verdict=$(printf '%s' "$result" | jq -r '.verdict // empty' 2>/dev/null || true)
if [ "$verdict" = "fail" ]; then
  filtered=$(filter_violations "$result" "$body_file")
  [ -n "$filtered" ] && result="$filtered"
  verdict=$(printf '%s' "$result" | jq -r '.verdict // empty' 2>/dev/null || true)
fi

if [ -n "$result" ]; then
  deduped=$(printf '%s' "$result" | jq '
    def norm: ascii_downcase | gsub("[[:punct:]]"; "") | gsub("\\s+"; " ") | sub("^ "; "") | sub(" $"; "");
    .violations |= ((. // []) | reduce .[] as $v ([]; ($v.rule | norm) as $k | if any(.[]; (.rule | norm) == $k) then . else . + [$v] end))
  ' 2>/dev/null || true)
  [ -n "$deduped" ] && result="$deduped"
fi

case "$verdict" in
  pass)
    exit 0
    ;;
  fail)
    violations=$(printf '%s' "$result" | jq -r '.violations[]? | "- \(.rule)\n  evidence: \(.evidence)"' 2>/dev/null || true)
    [ -n "$violations" ] || violations='(reviewer returned fail without enumerating violations)'
    {
      printf '\n---\n\n## External-reviewer result\n\n'
      printf -- '- Elapsed: %ss\n- Backend: %s\n- Model: %s\n- Verdict: fail\n\n%s\n' "$elapsed" "$REVIEWER_BACKEND" "$model" "$violations"
    } >"$state_dir/last-result.md" 2>/dev/null || true
    detail=$(printf 'External compliance reviewer (%s via %s) flagged violations in your last turn.\n\nViolations:\n%s\n\nPRIMARY ACTION: fix the violations this turn.' "$model" "$REVIEWER_BACKEND" "$violations")
    reason="$(eci_diagnostic_reason "$(eci_diagnostic_code_for_reason "$detail")" "Stop" "external-review" "session=$(eci_diagnostic_value "$session_id"),cwd=$(eci_diagnostic_value "$cwd"),state=$(eci_diagnostic_value "$state_dir/last-result.md")" "$detail" "fix every listed violation in this turn, then invoke Stop again")"
    jq -n --arg reason "$reason" '{decision:"block", reason:$reason}'
    exit 0
    ;;
  *)
    printf '%s\n' "$(eci_diagnostic_reason "ECI_REVIEW_VERDICT_MALFORMED" "Stop" "external-review" "reviewer=$(eci_diagnostic_value "$REVIEWER_BACKEND"),session=$(eci_diagnostic_value "$session_id"),cwd=$(eci_diagnostic_value "$cwd")" "malformed reviewer verdict; review skipped" "return a schema-valid pass/fail verdict and retry the reviewer")" >&2
    exit 0
    ;;
esac
