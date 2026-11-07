#!/usr/bin/env bash
set -euo pipefail
test_bash="${BASH:?the running Bash shell must provide BASH}"
case "$test_bash" in
  /*) ;;
  *)
    printf 'running Bash path is not absolute: %s\n' "$test_bash" >&2
    exit 1
    ;;
esac
[ -x "$test_bash" ] || {
  printf 'running Bash path is not executable: %s\n' "$test_bash" >&2
  exit 1
}
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-fast.XXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT

proof_root="$tmp/proof"
# Keep a complete temporary ECI source under the test HOME.  Every hook and
# lifecycle invocation therefore uses the same literal HOME/.omp/agent/eci root while
# all test-only sessions and transcript paths stay isolated.
home="$tmp/home"
ECI_ROOT="$home/.omp/agent/eci"
mkdir -p "$proof_root/t00-session" "$ECI_ROOT"
cp -a -- "$ROOT/bin" "$ECI_ROOT/bin"
cp -a -- "$ROOT/hooks" "$ECI_ROOT/hooks"
export HOME="$home"
printf '%s\n' \
  'scope: fast-path probe' \
  "cwd: $ROOT" \
  'session_id: t00-session' \
  'created_utc: 2026-08-15T00:00:00Z' \
  >"$proof_root/t00-session/eci_active"
# A worker with a different session id must still route through its validated
# parent marker when unrelated proof-root entries exhaust the bounded scan.
# Parent metadata is resolved directly from the bounded transcript record.
overflow_edit_root="$tmp/overflow-edit-root"
mkdir -p "$overflow_edit_root"
for i in $(seq 1 65); do
  mkdir -p "$overflow_edit_root/t00-edit-unrelated-$i"
done
mkdir -p "$overflow_edit_root/parent-session"
printf 'scope: parent worker\ncwd: %s\nsession_id: parent-session\n' "$ROOT" \
  >"$overflow_edit_root/parent-session/eci_active"
mkdir -p "$home/.omp/agent/sessions"
overflow_transcript="$home/.omp/agent/sessions/overflow-worker.jsonl"
printf '%s\n' '{"type":"session_meta","payload":{"source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-session","depth":1,"agent_nickname":"Overflow","agent_role":"worker"}}}}}' \
  >"$overflow_transcript"
overflow_edit_input="$tmp/overflow-edit.json"
jq -n --arg cwd "$ROOT" --arg transcript "$overflow_transcript" \
  '{session_id:"child-session",cwd:$cwd,transcript_path:$transcript,tool_name:"apply_patch",tool_input:{command:"*** Begin Patch\\n*** Add File: worker-file.txt\\n+worker\\n*** End Patch\\n"}}' >"$overflow_edit_input"
env -u ECI_ROLE HOME="$home" ECI_PROOF_ROOT="$overflow_edit_root" \
  bash "$ECI_ROOT/hooks/eci-active-gate.sh" <"$overflow_edit_input" >"$tmp/overflow-edit.out"
[ ! -s "$tmp/overflow-edit.out" ]

# An inactive coordinator callback must ignore an overflow made entirely of
# unrelated proof-root entries.  The direct current-session lookup is the
# authority; an empty direct marker must not become MISSING_CURRENT merely
# because the bounded unrelated scan emitted its overflow sentinel.
inactive_overflow_edit_root="$tmp/inactive-overflow-edit-root"
mkdir -p "$inactive_overflow_edit_root"
for i in $(seq 1 65); do
  mkdir -p "$inactive_overflow_edit_root/t00-inactive-unrelated-$i"
  printf 'scope: unrelated overflow\ncwd: /other/cwd\nsession_id: t00-inactive-unrelated-%s\n' "$i" \
    >"$inactive_overflow_edit_root/t00-inactive-unrelated-$i/eci_active"
done
inactive_overflow_edit_input="$tmp/inactive-overflow-edit.json"
jq -n --arg cwd "$ROOT" \
  '{session_id:"t00-inactive-overflow",cwd:$cwd,tool_name:"apply_patch",tool_input:{command:"*** Begin Patch\n*** Add File: inactive-overflow-file.txt\n+inactive\n*** End Patch\n"}}' >"$inactive_overflow_edit_input"
env -u ECI_ROLE HOME="$home" ECI_PROOF_ROOT="$inactive_overflow_edit_root" \
  bash "$ECI_ROOT/hooks/eci-active-gate.sh" <"$inactive_overflow_edit_input" >"$tmp/inactive-overflow-edit.out"
[ ! -s "$tmp/inactive-overflow-edit.out" ]

# A malformed callback must likewise remain inactive when the bounded scan
# sees only unrelated entries.  The overflow sentinel is a scan limitation,
# not an active owner and must not fabricate an identity denial.
malformed_overflow_edit_input="$tmp/malformed-overflow-edit.json"
jq -n --arg cwd "$ROOT" \
  '{session_id:123,cwd:$cwd,tool_name:"Edit",tool_input:{file_path:"inactive-overflow-file.txt"}}' \
  >"$malformed_overflow_edit_input"
env -u ECI_ROLE HOME="$home" ECI_PROOF_ROOT="$inactive_overflow_edit_root" \
  bash "$ECI_ROOT/hooks/eci-active-gate.sh" <"$malformed_overflow_edit_input" >"$tmp/malformed-overflow-edit.out"
[ ! -s "$tmp/malformed-overflow-edit.out" ]
printf 'edit marker overflow assertions: PASS\n'
