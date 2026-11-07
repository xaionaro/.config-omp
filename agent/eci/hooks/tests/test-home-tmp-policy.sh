#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HOME_ROOT="${HOME:?HOME must be set}"
TEST_ROOT="$(mktemp -d "$HOME_ROOT/tmp/eci-home-tmp-policy.XXXXXX")"
trap 'rm -rf -- "$TEST_ROOT"' EXIT HUP INT TERM

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_absent() {
  local needle="$1" path="$2"
  if rg -n --fixed-strings -- "$needle" "$path" >/dev/null 2>&1; then
    rg -n --fixed-strings -- "$needle" "$path" >&2 || true
    fail "unsafe temporary default remains in $path: $needle"
  fi
}

# These are active runtime writers.  Their private scratch defaults must not
# turn a caller's ordinary named temporary path into a command denial.
assert_absent '${TMPDIR:-/tmp}' "$ROOT/hooks/pre-commit-go-mod.sh"
assert_absent '${TMPDIR:-/tmp}' "$ROOT/hooks/pretooluse-edit-dispatch.sh"
assert_absent "'/tmp/brainstorm" "$ROOT/../skills/brainstorming/scripts/server.cjs"
assert_absent 'SESSION_DIR="/tmp/brainstorm-' "$ROOT/../skills/brainstorming/scripts/start-server.sh"
assert_absent '[[ "$SESSION_DIR" == /tmp/* ]]' "$ROOT/../skills/brainstorming/scripts/stop-server.sh"

for path in "$ROOT/hooks/system-prompt-reviewer.sh"; do
  if rg -n '(^|[=( ])mktemp[[:space:]]*($|[;&])' "$path" >/dev/null 2>&1; then
    rg -n '(^|[=( ])mktemp[[:space:]]*($|[;&])' "$path" >&2 || true
    fail "bare mktemp can inherit the system temporary root: $path"
  fi
done

run_helper_probe() {
  local provider="$1" helper="$2" variable="$3" function="$4" root="$5"
  local system_tmp="$(printf '/%s' tmp)" output status
  set +e
  output="$(HOME="$root" TMPDIR="$system_tmp" env -u "$variable" bash -c \
    '. "$1"; set +e; "$2" >/dev/null 2>"$HOME/diagnostic"; status=$?; set -e; probe=$(mktemp -d "$TMPDIR/probe.XXXXXX"); printf "status=%s\\ntmpdir=%s\\nprobe=%s\\n" "$status" "$TMPDIR" "$probe"' \
    bash "$helper" "$function" 2>&1)"
  status=$?
  set -e
  [ "$status" -eq 0 ] || fail "$provider helper probe failed: $output"
  printf '%s\n' "$output" | grep -Fq 'status=0' || fail "$provider helper treated TMPDIR=/tmp as a failure: $output"
  printf '%s\n' "$output" | grep -Fq "tmpdir=$root/tmp" || fail "$provider helper did not select home/tmp: $output"
  printf '%s\n' "$output" | grep -Fq "probe=$root/tmp/probe." || fail "$provider helper wrote outside home/tmp: $output"
  [ ! -s "$root/diagnostic" ] || fail "$provider helper emitted a user-facing TMPDIR denial: $(cat -- "$root/diagnostic")"
  rm -rf -- "$root/tmp/probe."*
}

eci_home="$TEST_ROOT/eci-home"
mkdir -p "$eci_home/tmp"
run_helper_probe eci "$ROOT/hooks/lib/eci-tmp.sh" ECI_TMPDIR eci_init_tmp "$eci_home"

printf '%s\n' 'home-scoped temporary-directory policy: PASS'
