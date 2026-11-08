#!/usr/bin/env bash
# Regression probe for run-isolated.sh. Run it through run-isolated.sh only: the runner
# exports ECI_ACCOUNT_KIMI_CODE_HOME (the account's Kimi peer) and KIMI_CODE_HOME (the
# scratch peer). The probe checks, in order:
#   1. no test in this tree names the account peer by path. A failure stops the probe
#      before any test that names it can run;
#   2. writes through the isolated peer path land in the scratch copy, not the account;
#   3. each peer-touching test, run through run-isolated.sh with HOME set to the account,
#      leaves the account peer's top level, hooks/ and bin/ unchanged (mtime and entry
#      count). The test's own exit code is printed, not asserted: this probe asserts
#      isolation, and a test's functional result is reported separately.
set -euo pipefail

account="${ECI_ACCOUNT_KIMI_CODE_HOME:?run-isolated.sh must export ECI_ACCOUNT_KIMI_CODE_HOME}"
account_home="${account%/.kimi-code}"
tests_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
. "$tests_dir/portable-test.sh"
logical_home="$(eci_user_home)"
probe="zz-isolation-probe.$$"
# Tests that run the Kimi peer's hooks or bin; step 3 runs each one.
peer_touching_tests=(test-eci-malformed-input-parity.sh)
failed=0

note_failure() {
  printf 'FAIL: %s\n' "$1" >&2
  failed=1
}

# Mtime and entry count of the account peer's top level, hooks/ and bin/.
account_state() {
  local dir
  for dir in "$account" "$account/hooks" "$account/bin"; do
    printf '%s mtime=%s entries=%s\n' "${dir#"$account_home"/}" "$(eci_mtime "$dir")" \
      "$(find "$dir" -mindepth 1 -maxdepth 1 | wc -l)"
  done
}

cleanup() { rm -f -- "$HOME/.kimi-code/$probe" "$HOME/.kimi-code/bin/$probe"; }
trap cleanup EXIT

# 1. Static guard. It runs first, so nothing that names the account peer runs after a failure.
[ -n "$logical_home" ] || { printf '%s\n' 'FAIL: cannot resolve the account home' >&2; exit 1; }
offenders="$(grep -l -F -e "$account_home/.kimi-code" -e "$logical_home/.kimi-code" -- "$tests_dir"/*.sh |
  grep -v '/test-run-isolated-kimi-peer\.sh$' || true)"
if [ -n "$offenders" ]; then
  printf 'FAIL: test names the account Kimi peer by path (use KIMI_CODE_HOME):\n%s\n' "$offenders" >&2
  exit 1
fi

# 2. Writes through the isolated peer path.
[ "$(eci_realpath_existing "$HOME/.kimi-code")" != "$(eci_realpath_existing "$account")" ] ||
  note_failure 'the isolated Kimi peer resolves to the account peer'
before="$(account_state)"
printf 'probe\n' >"$HOME/.kimi-code/$probe"
printf 'probe\n' >"$HOME/.kimi-code/bin/$probe"
[ -f "$HOME/.kimi-code/$probe" ] && [ -f "$HOME/.kimi-code/bin/$probe" ] ||
  note_failure 'the probe write did not land in the isolated peer'
[ ! -e "$account/$probe" ] && [ ! -e "$account/bin/$probe" ] ||
  note_failure 'the probe write reached the account peer'
cleanup
after="$(account_state)"
if [ "$before" != "$after" ]; then
  note_failure "account peer changed under probe writes: before [$before] after [$after]"
fi

# 3. Peer-touching tests. Each runs through run-isolated.sh with HOME set to the account, so
# the run binds the account peer as an unisolated run would. The account state is bracketed.
for test_name in "${peer_touching_tests[@]}"; do
  before="$(account_state)"
  log="$(mktemp "${TMPDIR:-/tmp}/probe-$test_name.XXXXXX")"
  rc=0
  HOME="$account_home" bash "$tests_dir/run-isolated.sh" "$test_name" >"$log" 2>&1 || rc=$?
  after="$(account_state)"
  printf '%s: run-isolated EXIT=%s (reported, not asserted)\n' "$test_name" "$rc"
  if [ "$rc" != 0 ]; then
    tail -n 5 "$log" >&2
  fi
  if [ "$before" != "$after" ]; then
    note_failure "account peer changed during $test_name: before [$before] after [$after]"
  fi
  rm -f -- "$log"
done

[ "$failed" = 0 ] || exit 1
printf '%s\n' 'isolated Kimi peer regression probe: PASS'
