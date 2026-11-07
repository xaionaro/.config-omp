#!/usr/bin/env bash

# Focused R11 regression coverage for Stop behavior.  The fixture owns a
# private ECI home so the Stop hook's source-home transfer is exercised
# without touching the live runtime.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-stop-r11.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

FIXTURE_HOME="$TMP_ROOT/home"
FIXTURE_ECI="$FIXTURE_HOME/.omp/agent/eci"
PROOF_ROOT="$TMP_ROOT/proof"
WORKER_PROOF_ROOT="$TMP_ROOT/worker-proof"
REPO="$TMP_ROOT/repo"

mkdir -p -- "$FIXTURE_ECI" "$FIXTURE_HOME/tmp" "$PROOF_ROOT" "$WORKER_PROOF_ROOT" "$REPO"
cp -a -- "$ROOT/hooks" "$FIXTURE_ECI/hooks"

run_stop() {
  local input="$1" output="$2" proof_root="${3:-$PROOF_ROOT}"

  env -u ECI_HOME -u ECI_ROLE \
    HOME="$FIXTURE_HOME" ECI_PROOF_ROOT="$proof_root" \
    bash "$FIXTURE_ECI/hooks/stop-gate.sh" <"$input" >"$output"
}

write_marker() {
  local session="$1" marker_cwd="$2"
  local marker="$PROOF_ROOT/$session/eci_active"

  mkdir -p -- "${marker%/*}"
  printf 'scope: stop-r11 fixture\ncwd: %s\nsession_id: %s\ncreated_utc: 2026-08-28T00:00:00Z\n' \
    "$marker_cwd" "$session" >"$marker"
}

# A valid current marker is the only stop authority for this callback.  A
# duplicate and a malformed sibling are maintenance observations, not a reason
# to replace the current callback with an ambiguity/scan denial.
direct_session=direct-session
duplicate_session=duplicate-session
malformed_session=malformed-session
unrelated_session=unrelated-session
unrelated_cwd="$TMP_ROOT/unrelated-cwd"
mkdir -p -- "$unrelated_cwd"
write_marker "$direct_session" "$ROOT"
write_marker "$duplicate_session" "$ROOT"
write_marker "$unrelated_session" "$unrelated_cwd"
mkdir -p -- "$PROOF_ROOT/$malformed_session"
printf 'malformed sibling marker\n' >"$PROOF_ROOT/$malformed_session/eci_active"

direct_marker="$PROOF_ROOT/$direct_session/eci_active"
duplicate_marker="$PROOF_ROOT/$duplicate_session/eci_active"
malformed_marker="$PROOF_ROOT/$malformed_session/eci_active"
unrelated_marker="$PROOF_ROOT/$unrelated_session/eci_active"
cp -- "$direct_marker" "$TMP_ROOT/direct-marker.before"
cp -- "$duplicate_marker" "$TMP_ROOT/duplicate-marker.before"
cp -- "$malformed_marker" "$TMP_ROOT/malformed-marker.before"
cp -- "$unrelated_marker" "$TMP_ROOT/unrelated-marker.before"

direct_input="$TMP_ROOT/direct-input.json"
direct_first="$TMP_ROOT/direct-first.json"
direct_second="$TMP_ROOT/direct-second.json"
direct_third="$TMP_ROOT/direct-third.json"
jq -cn --arg session_id "$direct_session" --arg cwd "$ROOT" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"",stop_hook_active:false}' >"$direct_input"

run_stop "$direct_input" "$direct_first"
jq -e --arg marker "$direct_marker" '
  .decision == "block" and
  ((.reason // "") | contains("[ECI_STOP_ACTIVE_ECI]")) and
  ((.reason // "") | contains($marker)) and
  ((.reason // "") | contains("sibling marker state is advisory")) and
  ((.reason // "") | contains("ECI_STOP_MARKER_AMBIGUOUS") | not) and
  ((.reason // "") | contains("ECI_STOP_MARKER_SCAN_UNSAFE") | not)
' "$direct_first" >/dev/null || {
  cat "$direct_first" >&2
  exit 1
}
cmp -s "$TMP_ROOT/direct-marker.before" "$direct_marker"
cmp -s "$TMP_ROOT/duplicate-marker.before" "$duplicate_marker"
cmp -s "$TMP_ROOT/malformed-marker.before" "$malformed_marker"
cmp -s "$TMP_ROOT/unrelated-marker.before" "$unrelated_marker"

# The first callback is the sole reminder.  A host retry with no state change
# is allowed to finish rather than producing an escalating stream of Stop
# denials.
run_stop "$direct_input" "$direct_second"
jq -e '.continue == true and (has("decision") | not)' "$direct_second" >/dev/null || {
  cat "$direct_second" >&2
  exit 1
}
run_stop "$direct_input" "$direct_third"
cmp -s "$direct_second" "$direct_third" || {
  diff -u "$direct_second" "$direct_third" >&2 || true
  exit 1
}
cmp -s "$TMP_ROOT/direct-marker.before" "$direct_marker"
cmp -s "$TMP_ROOT/duplicate-marker.before" "$duplicate_marker"
cmp -s "$TMP_ROOT/malformed-marker.before" "$malformed_marker"
cmp -s "$TMP_ROOT/unrelated-marker.before" "$unrelated_marker"

# A worker's dirty owned path is a handoff, not a demand to commit, run BRP,
# create a bypass record, or ask the user.  Stop publishes the status to the
# parent coordinator and lets the worker result return normally.
git -C "$REPO" init -q
git -C "$REPO" config user.email 'eci-stop-r11@example.invalid'
git -C "$REPO" config user.name 'ECI stop R11 test'
printf 'base\n' >"$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -qm 'initial'

worker_session=worker-session
parent_session=parent-session
worker_transcript="$FIXTURE_ECI/sessions/worker.jsonl"
mkdir -p -- "$(dirname -- "$worker_transcript")"
printf '%s\n' '{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1}}}}}' \
  >"$worker_transcript"

mkdir -p -- "$WORKER_PROOF_ROOT/touched-repos/sessions/$worker_session"
printf 'repo: %s\npath: file.txt\n' "$REPO" \
  >"$WORKER_PROOF_ROOT/touched-repos/sessions/$worker_session/owned-path"
printf 'worker dirty change\n' >>"$REPO/file.txt"

worker_input="$TMP_ROOT/worker-input.json"
worker_output="$TMP_ROOT/worker-output.json"
worker_handoff="$FIXTURE_HOME/.agent/context/sessions/$worker_session/latest-status-report.md"
jq -cn --arg session_id "$worker_session" --arg cwd "$REPO" --arg transcript "$worker_transcript" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:$transcript,stop_hook_active:false}' >"$worker_input"

run_stop "$worker_input" "$worker_output" "$WORKER_PROOF_ROOT"
jq -e '.continue == true and (has("decision") | not)' "$worker_output" >/dev/null || {
  cat "$worker_output" >&2
  exit 1
}
[ -f "$worker_handoff" ] && [ ! -L "$worker_handoff" ] || {
  printf 'worker dirty handoff was not published: %s\n' "$worker_handoff" >&2
  exit 1
}
grep -Fx 'state: dirty-worktree-handoff' "$worker_handoff" >/dev/null
grep -Fx "worker_session_id: $worker_session" "$worker_handoff" >/dev/null
grep -Fx "coordinator_session_id: $parent_session" "$worker_handoff" >/dev/null
grep -F 'file.txt' "$worker_handoff" >/dev/null
! grep -Eqi 'commit|blocker-resolution|skip-stop|user action' "$worker_handoff"
[ ! -e "$WORKER_PROOF_ROOT/$worker_session/subagent-commit-reminder.md" ]

printf '%s\n' 'stop R11 recovery assertions: PASS'
