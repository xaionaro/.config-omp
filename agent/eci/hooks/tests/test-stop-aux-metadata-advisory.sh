#!/usr/bin/env bash

# Stop metadata is diagnostic only. A copied hook must not turn a missing
# canonical source, peer marker debris, or an unusable loop-state file into a
# repeated Stop denial. Only the direct current-session marker owns the one
# useful reminder.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-stop-aux-metadata.XXXXXX")"
trap 'chmod -R u+rwx -- "$TMP_ROOT" 2>/dev/null || true; rm -rf -- "$TMP_ROOT"' EXIT

FIXTURE_HOME="$TMP_ROOT/home"
FIXTURE_ECI="$FIXTURE_HOME/.omp/agent/eci"
PROOF_ROOT="$TMP_ROOT/proof"
REPO="$TMP_ROOT/repo"

mkdir -p -- "$FIXTURE_ECI" "$FIXTURE_HOME/tmp" "$PROOF_ROOT" "$REPO"
cp -a -- "$ROOT/hooks" "$FIXTURE_ECI/hooks"

run_stop() {
  local input="$1" output="$2" proof_root="$3" home="$4"

  env -u ECI_HOME -u ECI_ROLE \
    HOME="$home" ECI_PROOF_ROOT="$proof_root" \
    bash "$FIXTURE_ECI/hooks/stop-gate.sh" <"$input" >"$output"
}

assert_continue() {
  local output="$1"

  jq -e '.continue == true and (has("decision") | not)' "$output" >/dev/null || {
    cat "$output" >&2
    exit 1
  }
}

write_marker() {
  local proof_root="$1" session="$2" marker_cwd="$3"
  local marker="$proof_root/$session/eci_active"

  mkdir -p -- "${marker%/*}"
  printf 'scope: stop-aux-metadata fixture\ncwd: %s\nsession_id: %s\ncreated_utc: 2026-08-28T00:00:00Z\n' \
    "$marker_cwd" "$session" >"$marker"
  printf '%s\n' "$marker"
}

git -C "$REPO" init -q
git -C "$REPO" config user.email 'eci-stop-aux@example.invalid'
git -C "$REPO" config user.name 'ECI stop auxiliary metadata test'
printf 'base\n' >"$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -qm 'initial fixture'

# The source already executing the callback is sufficient. A missing or
# differently spelled $HOME/.omp/agent/eci source is provenance metadata, not a
# reason to block an otherwise inactive Stop.
source_input="$TMP_ROOT/source-input.json"
source_output="$TMP_ROOT/source-output.json"
source_proof="$TMP_ROOT/source-proof"
missing_home="$TMP_ROOT/missing-home"
mkdir -p -- "$source_proof" "$missing_home"
jq -cn --arg session_id source-fallback --arg cwd "$REPO" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"",stop_hook_active:false}' >"$source_input"
run_stop "$source_input" "$source_output" "$source_proof" "$missing_home"
assert_continue "$source_output"

# A stale/non-runnable canonical copy likewise cannot displace the source
# already running this callback or create a provenance-only Stop denial.
mismatch_home="$TMP_ROOT/mismatch-home"
mismatch_output="$TMP_ROOT/mismatch-output.json"
mkdir -p -- "$mismatch_home/.omp/agent/eci/hooks"
printf '%s\n' '# stale copy' >"$mismatch_home/.omp/agent/eci/hooks/stop-gate.sh"
chmod 600 -- "$mismatch_home/.omp/agent/eci/hooks/stop-gate.sh"
run_stop "$source_input" "$mismatch_output" "$source_proof" "$mismatch_home"
assert_continue "$mismatch_output"

# A callback without its own marker must not be captured by a valid peer,
# malformed sibling, or scan ambiguity. They are somebody else's maintenance
# state, not a concrete current-session target.
peer_proof="$TMP_ROOT/peer-proof"
mkdir -p -- "$peer_proof"
write_marker "$peer_proof" peer-session "$REPO" >/dev/null
peer_input="$TMP_ROOT/peer-input.json"
peer_output="$TMP_ROOT/peer-output.json"
jq -cn --arg session_id callback-session --arg cwd "$REPO" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"",stop_hook_active:false}' >"$peer_input"
run_stop "$peer_input" "$peer_output" "$peer_proof" "$FIXTURE_HOME"
assert_continue "$peer_output"

malformed_proof="$TMP_ROOT/malformed-proof"
mkdir -p -- "$malformed_proof/stale-session"
printf 'not a marker\n' >"$malformed_proof/stale-session/eci_active"
malformed_output="$TMP_ROOT/malformed-output.json"
run_stop "$peer_input" "$malformed_output" "$malformed_proof" "$FIXTURE_HOME"
assert_continue "$malformed_output"

scan_proof="$TMP_ROOT/scan-proof"
mkdir -p -- "$scan_proof"
ln -s -- "$TMP_ROOT/not-a-session" "$scan_proof/stale-link"
scan_output="$TMP_ROOT/scan-output.json"
run_stop "$peer_input" "$scan_output" "$scan_proof" "$FIXTURE_HOME"
assert_continue "$scan_output"

# The same scan debris alongside a valid direct marker is advisory: preserve
# the direct one-reminder behavior rather than reclassifying it as scan state.
direct_scan_proof="$TMP_ROOT/direct-scan-proof"
direct_scan_session=direct-scan-session
direct_scan_marker="$(write_marker "$direct_scan_proof" "$direct_scan_session" "$REPO")"
ln -s -- "$TMP_ROOT/nonexistent-direct-peer" "$direct_scan_proof/stale-link"
direct_scan_input="$TMP_ROOT/direct-scan-input.json"
direct_scan_first="$TMP_ROOT/direct-scan-first.json"
direct_scan_second="$TMP_ROOT/direct-scan-second.json"
jq -cn --arg session_id "$direct_scan_session" --arg cwd "$REPO" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"",stop_hook_active:false}' >"$direct_scan_input"
run_stop "$direct_scan_input" "$direct_scan_first" "$direct_scan_proof" "$FIXTURE_HOME"
jq -e --arg marker "$direct_scan_marker" '
  .decision == "block" and
  ((.reason // "") | contains("[ECI_STOP_ACTIVE_ECI]")) and
  ((.reason // "") | contains($marker)) and
  ((.reason // "") | contains("ECI_STOP_MARKER_SCAN_UNSAFE") | not)
' "$direct_scan_first" >/dev/null || {
  cat "$direct_scan_first" >&2
  exit 1
}
run_stop "$direct_scan_input" "$direct_scan_second" "$direct_scan_proof" "$FIXTURE_HOME"
assert_continue "$direct_scan_second"

# A malformed ordinary loop record is replaced locally, then the valid direct
# marker gets its one reminder and an unchanged callback continues.
loop_proof="$TMP_ROOT/loop-proof"
loop_session=loop-session
loop_marker="$(write_marker "$loop_proof" "$loop_session" "$REPO")"
printf 'broken loop state\n' >"$loop_proof/$loop_session/stop_loop_state"
loop_input="$TMP_ROOT/loop-input.json"
loop_first="$TMP_ROOT/loop-first.json"
loop_second="$TMP_ROOT/loop-second.json"
jq -cn --arg session_id "$loop_session" --arg cwd "$REPO" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"",stop_hook_active:false}' >"$loop_input"
run_stop "$loop_input" "$loop_first" "$loop_proof" "$FIXTURE_HOME"
jq -e --arg marker "$loop_marker" '
  .decision == "block" and
  ((.reason // "") | contains("[ECI_STOP_ACTIVE_ECI]")) and
  ((.reason // "") | contains($marker)) and
  ((.reason // "") | contains("ECI_STOP_LOOP_STATE_UNSAFE") | not)
' "$loop_first" >/dev/null || {
  cat "$loop_first" >&2
  exit 1
}
grep -Fx 'version: 2' "$loop_proof/$loop_session/stop_loop_state" >/dev/null
run_stop "$loop_input" "$loop_second" "$loop_proof" "$FIXTURE_HOME"
assert_continue "$loop_second"

# A direct malformed control target still gets a focused first reminder. The
# auxiliary loop record then suppresses an unchanged retry instead of hiding
# the concrete marker problem behind loop bookkeeping.
mismatch_proof="$TMP_ROOT/mismatch-proof"
mismatch_session=mismatch-session
other_cwd="$TMP_ROOT/other-cwd"
mkdir -p -- "$other_cwd"
write_marker "$mismatch_proof" "$mismatch_session" "$other_cwd" >/dev/null
# This peer scan artifact must not replace the focused direct-marker result.
ln -s -- "$TMP_ROOT/nonexistent-peer" "$mismatch_proof/stale-link"
mismatch_input="$TMP_ROOT/mismatch-input.json"
mismatch_first="$TMP_ROOT/mismatch-first.json"
mismatch_second="$TMP_ROOT/mismatch-second.json"
jq -cn --arg session_id "$mismatch_session" --arg cwd "$REPO" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"",stop_hook_active:false}' >"$mismatch_input"
run_stop "$mismatch_input" "$mismatch_first" "$mismatch_proof" "$FIXTURE_HOME"
jq -e '
  .decision == "block" and
  ((.reason // "") | contains("[ECI_MARKER_SCOPE_MISMATCH]")) and
  ((.reason // "") | contains("ECI_STOP_LOOP_STATE_UNSAFE") | not)
' "$mismatch_first" >/dev/null || {
  cat "$mismatch_first" >&2
  exit 1
}
run_stop "$mismatch_input" "$mismatch_second" "$mismatch_proof" "$FIXTURE_HOME"
assert_continue "$mismatch_second"

# If the auxiliary loop record cannot be published at all, do not create a
# permanent new denial loop around an already known direct marker.
unwritable_proof="$TMP_ROOT/unwritable-proof"
unwritable_session=unwritable-session
write_marker "$unwritable_proof" "$unwritable_session" "$REPO" >/dev/null
unwritable_input="$TMP_ROOT/unwritable-input.json"
unwritable_output="$TMP_ROOT/unwritable-output.json"
jq -cn --arg session_id "$unwritable_session" --arg cwd "$REPO" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"",stop_hook_active:false}' >"$unwritable_input"
chmod 500 -- "$unwritable_proof/$unwritable_session"
run_stop "$unwritable_input" "$unwritable_output" "$unwritable_proof" "$FIXTURE_HOME"
chmod 700 -- "$unwritable_proof/$unwritable_session"
assert_continue "$unwritable_output"

printf '%s\n' 'stop auxiliary metadata advisory assertions: PASS'
