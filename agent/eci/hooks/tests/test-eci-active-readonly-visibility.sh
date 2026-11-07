#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="${ECI_TEST_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)}"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-active-readonly-visibility.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

empty_home="$TMP_ROOT/empty-home"
copy_root="$TMP_ROOT/copied-runtime"
copy_eci="$copy_root/bin/eci-active"
path_root="$TMP_ROOT/path-bin"
provider_root="$TMP_ROOT/provider/.kimi-code"
provider_eci="$provider_root/bin/eci-active"
alias_eci="$TMP_ROOT/eci-active-alias"
proof_root="$TMP_ROOT/proof"
session_id=t00-readonly-visibility
session_dir="$proof_root/$session_id"
mkdir -p -- "$empty_home" "$copy_root/bin" "$path_root" "$provider_root/bin" "$session_dir"
cp -- "$ROOT/bin/eci-active" "$copy_eci"
cp -- "$ROOT/bin/eci-active" "$provider_eci"
ln -s -- "$copy_eci" "$path_root/eci-active"
ln -s -- "$copy_eci" "$alias_eci"
chmod 755 -- "$copy_eci" "$provider_eci"
proof_root="$(realpath -e -- "$proof_root")"

printf '%s\n' \
  'scope: read-only visibility fixture' \
  "cwd: $ROOT" \
  "session_id: $session_id" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$session_dir/eci_active"

failures=0

fail() {
  printf 'FAIL %s\n' "$*" >&2
  failures=$((failures + 1))
}

assert_help() {
  local label="$1"
  shift
  local output="$TMP_ROOT/$label.out" error="$TMP_ROOT/$label.err"

  if ! HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=worker \
    "$@" >"$output" 2>"$error"; then
    fail "$label help failed: $(cat -- "$error")"
    return
  fi
  grep -Fqx 'Usage:' "$output" || fail "$label help did not print Usage"
  [ ! -s "$error" ] || fail "$label help wrote stderr: $(cat -- "$error")"
}

assert_status() {
  local label="$1" expected="$2"
  shift 2
  local output="$TMP_ROOT/$label.out" error="$TMP_ROOT/$label.err"

  if ! HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=worker \
    ECI_SESSION_ID="$session_id" "$@" >"$output" 2>"$error"; then
    fail "$label status failed: $(cat -- "$error")"
    return
  fi
  grep -Fqx "$expected" "$output" || fail "$label status did not show '$expected': $(cat -- "$output")"
  [ ! -s "$error" ] || fail "$label status wrote stderr: $(cat -- "$error")"
}

# These commands intentionally have no sibling hook tree, runtime receipt,
# planner, peer provider, or configured HOME authority. Read-only discovery
# must still inspect the resolved executable and return normal help/status.
assert_help canonical-empty-home "$ROOT/bin/eci-active" --help
assert_help copied "$copy_eci" --help
assert_help equivalent-symlink "$alias_eci" -h
assert_help path \
  env PATH="$path_root:/usr/bin:/bin" eci-active --help
assert_status copied-status "session_id: $session_id" "$copy_eci" status
assert_status provider-status "session_id: $session_id" "$provider_eci" status

# Status and mutation paths share the proof-root resolver.  XDG cache is
# unrelated unless the caller explicitly selected it as ECI_PROOF_ROOT.
split_home="$TMP_ROOT/split-home"
split_xdg_cache="$TMP_ROOT/split-xdg-cache"
split_session=t00-readonly-shared-root
split_root="$split_home/tmp/agent/proof"
mkdir -p -- "$split_root/$split_session" "$split_xdg_cache"
printf '%s\n' \
  'scope: shared proof root status fixture' \
  "cwd: $ROOT" \
  "session_id: $split_session" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$split_root/$split_session/eci_active"
split_marker="$(realpath -e -- "$split_root/$split_session/eci_active")"
split_status_output="$TMP_ROOT/shared-root-status.out"
split_status_error="$TMP_ROOT/shared-root-status.err"
if ! HOME="$split_home" XDG_CACHE_HOME="$split_xdg_cache" ECI_ROLE=worker \
  ECI_SESSION_ID="$split_session" "$copy_eci" status >"$split_status_output" 2>"$split_status_error"; then
  fail "shared-proof-root status failed: $(cat -- "$split_status_error")"
else
  grep -Fqx "ECI active: $split_marker" "$split_status_output" ||
    fail "shared-proof-root status did not use the mutation proof root: $(cat -- "$split_status_output")"
  [ ! -s "$split_status_error" ] ||
    fail "shared-proof-root status wrote stderr: $(cat -- "$split_status_error")"
fi

# Ordinary status remains visible when a current direct marker carries a long
# benign historical field, but it must summarize the marker rather than dump
# that unbounded record.
oversized_legacy_note="$(printf '%*s' 5000 '' | tr ' ' x)"
[ "${#oversized_legacy_note}" -gt 4096 ] || fail 'oversized fixture was not oversized'
printf 'legacy_note: %s\n' "$oversized_legacy_note" >>"$session_dir/eci_active"
oversized_output="$TMP_ROOT/oversized-status.out"
oversized_error="$TMP_ROOT/oversized-status.err"
if ! HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=worker \
  ECI_SESSION_ID="$session_id" "$copy_eci" status >"$oversized_output" 2>"$oversized_error"; then
  fail "oversized-marker status failed: $(cat -- "$oversized_error")"
else
  grep -Fqx "ECI active: $proof_root/$session_id/eci_active" "$oversized_output" ||
    fail "oversized-marker status did not report its direct marker summary: $(cat -- "$oversized_output")"
  grep -Fqx "session_id: $session_id" "$oversized_output" ||
    fail "oversized-marker status did not report its direct session: $(cat -- "$oversized_output")"
  if grep -Fq 'legacy_note:' "$oversized_output"; then
    fail 'oversized-marker status printed raw marker metadata'
  fi
  if [ "$(wc -c <"$oversized_output")" -ge 1024 ]; then
    fail 'oversized-marker status output was not a bounded summary'
  fi
  [ ! -s "$oversized_error" ] || fail "oversized-marker status wrote stderr: $(cat -- "$oversized_error")"
fi

# Coordinator and worker roles are mutation-routing context, not a condition
# for help/status. A copied provider spelling must remain discoverable under
# either role.
coordinator_output="$TMP_ROOT/coordinator-status.out"
coordinator_error="$TMP_ROOT/coordinator-status.err"
if ! HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=coordinator \
  ECI_SESSION_ID="$session_id" "$provider_eci" status >"$coordinator_output" 2>"$coordinator_error"; then
  fail "coordinator-role status failed: $(cat -- "$coordinator_error")"
else
  grep -Fqx "session_id: $session_id" "$coordinator_output" || fail "coordinator-role status did not show the marker"
  [ ! -s "$coordinator_error" ] || fail "coordinator-role status wrote stderr: $(cat -- "$coordinator_error")"
fi

# No marker is a normal discovery result, not an error or a request to deploy
# a runtime receipt first.
missing_sid=t00-readonly-missing
missing_output="$TMP_ROOT/missing-status.out"
missing_error="$TMP_ROOT/missing-status.err"
if ! HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=worker \
  ECI_SESSION_ID="$missing_sid" "$copy_eci" status >"$missing_output" 2>"$missing_error"; then
  fail "missing-marker status failed: $(cat -- "$missing_error")"
else
  grep -Fqx 'ECI inactive' "$missing_output" || fail "missing-marker status was not inactive: $(cat -- "$missing_output")"
  [ ! -s "$missing_error" ] || fail "missing-marker status wrote stderr: $(cat -- "$missing_error")"
fi

no_session_output="$TMP_ROOT/no-session-status.out"
no_session_error="$TMP_ROOT/no-session-status.err"
if ! HOME="$empty_home" ECI_PROOF_ROOT="$TMP_ROOT/empty-proof" ECI_ROLE=worker \
  "$copy_eci" status >"$no_session_output" 2>"$no_session_error"; then
  fail "no-session status failed: $(cat -- "$no_session_error")"
else
  grep -Fqx 'ECI inactive' "$no_session_output" || fail "no-session status was not inactive: $(cat -- "$no_session_output")"
  [ ! -s "$no_session_error" ] || fail "no-session status wrote stderr: $(cat -- "$no_session_error")"
fi

# Status is discovery, not a lifecycle gate. A regular marker that cannot be
# read must produce an ordinary unavailable result without following, fixing,
# or failing on the control-file mode.
unreadable_sid=t00-readonly-unreadable
unreadable_dir="$proof_root/$unreadable_sid"
mkdir -p -- "$unreadable_dir"
printf '%s\n' \
  'scope: unreadable visibility fixture' \
  "cwd: $ROOT" \
  "session_id: $unreadable_sid" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$unreadable_dir/eci_active"
chmod 000 -- "$unreadable_dir/eci_active"
unreadable_output="$TMP_ROOT/unreadable-status.out"
unreadable_error="$TMP_ROOT/unreadable-status.err"
if ! HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=worker \
  ECI_SESSION_ID="$unreadable_sid" "$copy_eci" status >"$unreadable_output" 2>"$unreadable_error"; then
  fail "unreadable-marker status failed: $(cat -- "$unreadable_error")"
else
  grep -Fqx 'ECI unavailable' "$unreadable_output" || fail "unreadable-marker status was not unavailable: $(cat -- "$unreadable_output")"
  [ ! -s "$unreadable_error" ] || fail "unreadable-marker status wrote stderr: $(cat -- "$unreadable_error")"
fi
chmod 600 -- "$unreadable_dir/eci_active"

# Oversized historical metadata is advisory, but a direct symlink or
# nonregular marker remains an unsafe physical target and is rejected.
symlink_sid=t00-readonly-symlink
symlink_dir="$proof_root/$symlink_sid"
mkdir -p -- "$symlink_dir"
ln -s -- "$session_dir/eci_active" "$symlink_dir/eci_active"
symlink_output="$TMP_ROOT/symlink-status.out"
symlink_error="$TMP_ROOT/symlink-status.err"
if HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=worker \
  ECI_SESSION_ID="$symlink_sid" "$copy_eci" status >"$symlink_output" 2>"$symlink_error"; then
  fail 'symlink-marker status followed an unsafe direct marker'
else
  grep -Fq 'ECI marker is a symlink; refusing to read it:' "$symlink_error" ||
    fail "symlink-marker status did not explain its physical-target rejection: $(cat -- "$symlink_error")"
fi

nonregular_sid=t00-readonly-nonregular
nonregular_dir="$proof_root/$nonregular_sid"
mkdir -p -- "$nonregular_dir/eci_active"
nonregular_output="$TMP_ROOT/nonregular-status.out"
nonregular_error="$TMP_ROOT/nonregular-status.err"
if HOME="$empty_home" ECI_PROOF_ROOT="$proof_root" ECI_ROLE=worker \
  ECI_SESSION_ID="$nonregular_sid" "$copy_eci" status >"$nonregular_output" 2>"$nonregular_error"; then
  fail 'nonregular-marker status accepted an unsafe direct marker'
else
  grep -Fq 'ECI marker is not a regular file; refusing to read it:' "$nonregular_error" ||
    fail "nonregular-marker status did not explain its physical-target rejection: $(cat -- "$nonregular_error")"
fi

[ "$failures" -eq 0 ] || exit 1
printf '%s\n' 'eci-active read-only visibility: PASS'
