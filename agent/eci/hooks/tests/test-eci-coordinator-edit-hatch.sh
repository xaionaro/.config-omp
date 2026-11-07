#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-coordinator-edit-hatch.XXXXXX")"
TMP_ROOT="$(realpath -e -- "$TMP_ROOT")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fixture_home="$TMP_ROOT/home"
fixture_eci="$fixture_home/.omp/agent/eci"
fixture_eci="$fixture_eci/bin/eci-active"
proof_root="$TMP_ROOT/proof"
session_id=t00-coordinator-edit
other_session=t00-newest-session
cwd="$TMP_ROOT/repository"
other_cwd="$TMP_ROOT/other-cwd"
session_dir="$proof_root/$session_id"
marker="$session_dir/eci_active"
record="$session_dir/eci-coordinator-edit"
output="$TMP_ROOT/output"
error="$TMP_ROOT/error"

mkdir -p -- "$fixture_eci/bin" "$fixture_eci/hooks/lib" "$session_dir" \
  "$proof_root/$other_session" "$cwd" "$other_cwd"
cp -- "$ROOT/bin/eci-active" "$fixture_eci"
cp -- "$ROOT/hooks/lib/eci-proof-state.sh" "$fixture_eci/hooks/lib/eci-proof-state.sh"
cp -- "$ROOT/hooks/lib/eci-file-lock.sh" "$fixture_eci/hooks/lib/eci-file-lock.sh"
chmod 755 -- "$fixture_eci"

# Preserve the live hooks regardless of whether a line-2 bypass is present.
cp -- "$ROOT/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.before"
cp -- "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$TMP_ROOT/dispatcher.before"

write_marker() {
  local target_session="$1" target_cwd="$2"
  local target_marker="$proof_root/$target_session/eci_active"

  mkdir -p -- "$proof_root/$target_session"
  printf '%s\n' \
    'scope: coordinator edit hatch test' \
    "cwd: $(realpath -e -- "$target_cwd")" \
    "session_id: $target_session" \
    'created_utc: 2026-08-28T00:00:00Z' \
    >"$target_marker"
}

run_active() {
  local target_cwd="$1"
  shift
  (
    cd -- "$target_cwd"
    HOME="$fixture_home" ECI_HOME="$fixture_eci" ECI_PROOF_ROOT="$proof_root" \
      ECI_SESSION_ID="$session_id" ECI_ROLE=coordinator \
      "$fixture_eci" "$@"
  ) >"$output" 2>"$error"
}

run_thread_active() {
  local target_cwd="$1"
  shift
  (
    cd -- "$target_cwd"
    env -u ECI_SESSION_ID HOME="$fixture_home" ECI_HOME="$fixture_eci" \
      ECI_PROOF_ROOT="$proof_root" ECI_THREAD_ID="$session_id" ECI_ROLE=coordinator \
      "$fixture_eci" "$@"
  ) >"$output" 2>"$error"
}

run_without_session() {
  (
    cd -- "$cwd"
    env -u ECI_SESSION_ID -u ECI_THREAD_ID HOME="$fixture_home" \
      ECI_HOME="$fixture_eci" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=coordinator \
      "$fixture_eci" "$@"
  ) >"$output" 2>"$error"
}

run_worker() {
  (
    cd -- "$cwd"
    HOME="$fixture_home" ECI_HOME="$fixture_eci" ECI_PROOF_ROOT="$proof_root" \
      ECI_SESSION_ID="$session_id" ECI_ROLE=worker ECI_HOOK_IS_SUBAGENT=true \
      "$fixture_eci" coordinator-edit-on
  ) >"$output" 2>"$error"
}

expect_failure() {
  if "$@"; then
    printf 'expected command to fail: %q\n' "$*" >&2
    exit 1
  fi
}

expect_record_absent() {
  [ ! -e "$record" ] && [ ! -L "$record" ] || {
    printf 'coordinator edit state was unexpectedly created: %s\n' "$record" >&2
    exit 1
  }
}

assert_active_record() {
  local issued expires
  [ -f "$record" ] && [ ! -L "$record" ]
  [ "$(stat -c '%a' -- "$record")" = 600 ]
  mapfile -t record_lines <"$record"
  [ "${#record_lines[@]}" -eq 6 ]
  [ "${record_lines[0]}" = 'schema: eci-coordinator-edit/v1' ]
  [ "${record_lines[1]}" = "session_id: $session_id" ]
  [ "${record_lines[2]}" = "cwd: $(realpath -e -- "$cwd")" ]
  issued="${record_lines[3]#issued_at_epoch: }"
  expires="${record_lines[4]#expires_at_epoch: }"
  [[ "$issued" =~ ^[0-9]+$ && "$expires" =~ ^[0-9]+$ ]]
  [ $((expires - issued)) -eq 600 ]
  [ "${record_lines[5]}" = 'state: active' ]
  ! grep -Eqi 'authorized|fingerprint|sha|receipt|planner|sync' "$record"
}

# A marker exists in another session, but the new command must never fall
# back to the newest session. With no explicit session, it does nothing.
write_marker "$other_session" "$cwd"
expect_failure run_without_session coordinator-edit-on
expect_failure run_without_session coordinator-edit-status
expect_record_absent
[ ! -e "$proof_root/$other_session/eci-coordinator-edit" ]

# A requested session without an active marker cannot create routing state.
expect_failure run_active "$cwd" coordinator-edit-on
expect_record_absent

write_marker "$session_id" "$cwd"

# The marker binds this state to the canonical CWD rather than merely the
# session name.
expect_failure run_active "$other_cwd" coordinator-edit-on
expect_record_absent

# Historic direct-marker fields larger than the Stop parser limit and an
# equivalent CWD spelling are not an activation boundary. The current
# session/CWD mapping remains decisive.
oversized_legacy_note="$(printf '%*s' 5000 '' | tr ' ' x)"
[ "${#oversized_legacy_note}" -gt 4096 ]
printf '%s\n' \
  "legacy_note: $oversized_legacy_note" \
  "session_id: $session_id" \
  "cwd: $cwd/." \
  >>"$marker"

# Role labels are routing context, not authority. A worker process with the
# exact active session and marker-bound CWD can activate this short-lived
# routing hatch; the record remains confined to that current session.
run_worker
grep -Fqx "ECI coordinator edit state active: $record" "$output"
[ ! -s "$error" ]
assert_active_record
rm -f -- "$record"

# The dedicated, zero-argument hatch becomes active without any authorization
# artifact, receipt, planner, sync, or external helper.
run_active "$cwd" coordinator-edit-on
grep -Fqx "ECI coordinator edit state active: $record" "$output"
[ ! -s "$error" ]
assert_active_record

(
  HOME="$fixture_home"
  ECI_PROOF_ROOT="$proof_root"
  . "$fixture_eci/hooks/lib/eci-proof-state.sh"
  eci_control_basename eci-coordinator-edit
  eci_control_basename eci-coordinator-edit.tmp.123
  eci_path_is_eci_control_file "$record"
)

# Status is informational and does not rewrite live state.
cp -- "$record" "$TMP_ROOT/record.before-status"
run_active "$cwd" coordinator-edit-status
grep -Fqx 'ECI coordinator edit state active' "$output"
[ ! -s "$error" ]
cmp -s "$TMP_ROOT/record.before-status" "$record"

# An explicit thread id is equally valid; neither command infers an owner.
rm -f -- "$record"
run_thread_active "$cwd" coordinator-edit-on
assert_active_record

# An expired but otherwise valid record is reported inactive and left byte-for-
# byte untouched by status. It is not a reason to reject normal coordinator
# work elsewhere.
now="$(date -u '+%s')"
issued=$((now - 601))
expires=$((now - 1))
printf '%s\n' \
  'schema: eci-coordinator-edit/v1' \
  "session_id: $session_id" \
  "cwd: $(realpath -e -- "$cwd")" \
  "issued_at_epoch: $issued" \
  "expires_at_epoch: $expires" \
  'state: active' \
  >"$record"
chmod 600 -- "$record"
cp -- "$record" "$TMP_ROOT/record.before-expired-status"
run_active "$cwd" coordinator-edit-status
grep -Fqx 'ECI coordinator edit state inactive' "$output"
[ ! -s "$error" ]
cmp -s "$TMP_ROOT/record.before-expired-status" "$record"

# Safe malformed ordinary files are repaired by a fresh activation. The
# atomic replacement also leaves no stack of prior state records behind.
printf '%s\n' 'malformed-but-regular' >"$record"
chmod 644 -- "$record"
run_active "$cwd" coordinator-edit-on
assert_active_record
before_inode="$(stat -c '%i' -- "$record")"
run_active "$cwd" coordinator-edit-on
after_inode="$(stat -c '%i' -- "$record")"
[ "$before_inode" != "$after_inode" ]
assert_active_record
[ "$(find "$session_dir" -maxdepth 1 -name 'eci-coordinator-edit*' -printf '%f\n' | sort | wc -l)" -eq 1 ]

# Unsafe targets are not repaired or followed. Keep the symlink and target
# bytes unchanged so a mistaken record path cannot redirect coordinator state.
rm -f -- "$record"
printf '%s\n' sentinel >"$TMP_ROOT/symlink-target"
ln -s -- "$TMP_ROOT/symlink-target" "$record"
cp -- "$TMP_ROOT/symlink-target" "$TMP_ROOT/symlink-target.before"
expect_failure run_active "$cwd" coordinator-edit-on
[ -L "$record" ]
cmp -s "$TMP_ROOT/symlink-target.before" "$TMP_ROOT/symlink-target"
expect_failure run_active "$cwd" coordinator-edit-status
[ -L "$record" ]

rm -f -- "$record"
mkdir -- "$record"
expect_failure run_active "$cwd" coordinator-edit-on
[ -d "$record" ] && [ ! -L "$record" ]
expect_failure run_active "$cwd" coordinator-edit-status
[ -d "$record" ] && [ ! -L "$record" ]

cmp -- "$TMP_ROOT/validate-bash.before" "$ROOT/hooks/validate-bash.sh"
cmp -- "$TMP_ROOT/dispatcher.before" "$ROOT/hooks/pretooluse-edit-dispatch.sh"
printf '%s\n' 'eci coordinator edit hatch assertions: PASS'
