#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
command="$(jq -er '.hooks.UserPromptSubmit[0].hooks[0].command' "$root/hooks.json")"

check_reminder() {
  local prompt="$1" output
  output="$(jq -cn --arg prompt "$prompt" '{session_id:"bad/session",prompt:$prompt}' | bash -c "$command")"
  jq -e '.hookSpecificOutput.hookEventName == "UserPromptSubmit" and (.hookSpecificOutput.additionalContext | contains("status-report render"))' <<<"$output" >/dev/null
}
check_reminder 'status'
check_reminder 'Give me a proper status report'
check_reminder 'Can you give me a progress report?'
check_reminder 'Status report, please.'
check_reminder 'Status update, please.'
check_reminder 'Progress update?'
check_reminder 'Current status?'
check_reminder 'Current progress?'
check_reminder 'Give me a status report right now'
check_reminder 'Please provide an updated status report'
check_reminder 'Please provide a comprehensive progress report'
check_reminder 'Comprehensive progress report'
check_reminder 'Status update please'
check_reminder 'Can I get a status update?'
check_reminder 'What is the current progress update?'
check_reminder 'Can we get a progress report?'
check_reminder 'Fix the status report renderer and show me current status'
check_reminder 'What is a status report and show me current status'
check_reminder 'What is a status report and can you show me current status?'
check_reminder 'How do I get a status report and give me current status?'
check_reminder 'Show me status and progress update'
check_reminder "What's the status?"
check_reminder 'Fix the renderer and give me a status report'
check_reminder 'Give me a status report on the status report renderer'
check_reminder 'Fix the status report renderer, then show me the current progress'
check_reminder 'Provide a progress update for the renderer'
check_reminder 'Show me a status report, then fix the renderer'
check_reminder 'Give me a status report now'
check_reminder 'Give me a status report and explain the blockers'
check_reminder 'What is the status?'
printf '%s\n' 'python3() { return 1; }' 'export -f python3' >"$tmp/fail-capture"
output="$(jq -cn '{session_id:"valid-session",turn_id:"turn",prompt:"status"}' | BASH_ENV="$tmp/fail-capture" bash -c "$command")"
jq -e '.hookSpecificOutput.additionalContext | contains("status-report render")' <<<"$output" >/dev/null
printf '%s\n' 'python3() { /usr/bin/python3 "$@"; }' 'export -f python3' >"$tmp/use-python"
output="$(jq -cn '{session_id:"valid-session",turn_id:"turn",prompt:"status"}' | ECI_PROOF_ROOT="$tmp/proof" BASH_ENV="$tmp/use-python" bash -c "$command")"
jq -e '.hookSpecificOutput.additionalContext | contains("status-report render")' <<<"$output" >/dev/null
capture="$(find "$tmp/proof/pre-reviewer/valid-session" -name 'capture-turn-*.json' -type f -print -quit)"
[[ -n "$capture" ]]
jq -e '.turn_id == "turn" and .prompt == "status"' "$capture" >/dev/null
for prompt in 'Fix the status report renderer' 'provide a fix for the status report renderer' 'show me how status report renderer works' 'Show me a status report renderer implementation' 'Provide a status report renderer fix' 'Show me a status report template' 'Write a status report regression test' 'Provide a progress report schema' 'Fix the status update renderer' 'Current status code is broken' 'Progress update schema' 'Please update the status field' 'Can I get a status update template?' 'What is the current progress update schema?' 'What is a status report?' 'What is a progress update?' 'How do I get a status report?' 'How can I get a progress update?' 'What is a status report and how does it work?' 'How do I get a status report and where is it saved?' 'Write status and progress report regression tests' 'Show me a status and progress update template' 'Provide status and progress report schemas' 'How do I get a status report and interpret it?' 'What is a status and progress report?' 'How do I get a status and progress update?'; do
  output="$(jq -cn --arg prompt "$prompt" '{session_id:"bad/session",prompt:$prompt}' | bash -c "$command")"
  [[ -z "$output" ]]
done
for invalid in nul utf8 multiple; do
  case "$invalid" in
    nul) printf '{"prompt":"status"}\0' >"$tmp/input" ;;
    utf8) printf '{"prompt":"status"}\377' >"$tmp/input" ;;
    multiple) printf '%s\n' '{"prompt":"status"}' '{}' >"$tmp/input" ;;
  esac
  output="$(bash -c "$command" <"$tmp/input")"
  [[ -z "$output" ]]
done
printf '%s\n' 'Status-report reminder tests: PASS'
