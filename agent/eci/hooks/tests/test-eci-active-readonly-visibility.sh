#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="${ECI_TEST_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)}"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-active-readonly-visibility.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

empty_home="$TMP_ROOT/empty-home"
copy_root="$TMP_ROOT/copied-runtime"
copy_eci="$copy_root/bin/eci-active"
path_root="$TMP_ROOT/path-bin"
alias_eci="$TMP_ROOT/eci-active-alias"
proof_root="$TMP_ROOT/proof"
mkdir -p -- "$empty_home" "$copy_root/bin" "$path_root" "$proof_root"
mkdir -p -- "$empty_home/.omp/agent"
ln -s -- "$ROOT" "$empty_home/.omp/agent/eci"
cp -- "$ROOT/bin/eci-active" "$copy_eci"
ln -s -- "$copy_eci" "$path_root/eci-active"
ln -s -- "$copy_eci" "$alias_eci"
chmod -- 755 "$copy_eci"
proof_root="$(realpath -e -- "$proof_root")"

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

# The first run of each wrapper location builds its tool and reports the build on stderr. Build every
# location here, so the asserted runs check only help output and their stderr.
HOME="$empty_home" "$ROOT/bin/eci-active" --help >/dev/null 2>&1 || true
HOME="$empty_home" "$copy_eci" --help >/dev/null 2>&1 || true
HOME="$empty_home" env PATH="$path_root:/usr/bin:/bin" eci-active --help >/dev/null 2>&1 || true

# These commands run with a HOME that supplies only the canonical runtime link: no runtime receipt, planner,
# peer provider, or other configured HOME authority, and the copied binary has no sibling hook tree.
# Read-only discovery must still inspect the resolved executable and return normal help.
assert_help canonical-runtime-home "$ROOT/bin/eci-active" --help
assert_help copied "$copy_eci" --help
assert_help equivalent-symlink "$alias_eci" -h
assert_help path \
  env PATH="$path_root:/usr/bin:/bin" eci-active --help

[ "$failures" -eq 0 ] || exit 1
printf '%s\n' 'eci-active read-only visibility: PASS'
