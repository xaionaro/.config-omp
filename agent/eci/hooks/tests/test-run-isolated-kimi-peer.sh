#!/usr/bin/env bash
# Regression probe for run-isolated.sh. Run it through run-isolated.sh only: the
# runner exports ECI_ACCOUNT_KIMI_CODE_HOME (the account's Kimi peer). An isolated
# run must leave that peer unchanged. The probe writes through the isolated peer
# path, then checks that the write did not reach the account peer and that the
# account peer's top-level mtime and entry count did not move.
set -euo pipefail

account="${ECI_ACCOUNT_KIMI_CODE_HOME:?run-isolated.sh must export ECI_ACCOUNT_KIMI_CODE_HOME}"
probe="zz-isolation-probe.$$"
failed=0

note_failure() {
  printf 'FAIL: %s\n' "$1" >&2
  failed=1
}

snapshot() {
  printf 'mtime=%s entries=%s\n' "$(stat -c '%y' -- "$account")" \
    "$(find "$account" -mindepth 1 -maxdepth 1 | wc -l)"
}

cleanup() { rm -f -- "$HOME/.kimi-code/$probe" "$HOME/.kimi-code/bin/$probe"; }
trap cleanup EXIT

[ "$(realpath -e -- "$HOME/.kimi-code")" != "$(realpath -e -- "$account")" ] ||
  note_failure 'the isolated Kimi peer resolves to the account peer'

before="$(snapshot)"
printf 'probe\n' >"$HOME/.kimi-code/$probe"
printf 'probe\n' >"$HOME/.kimi-code/bin/$probe"
[ -f "$HOME/.kimi-code/$probe" ] && [ -f "$HOME/.kimi-code/bin/$probe" ] ||
  note_failure 'the probe write did not land in the isolated peer'
[ ! -e "$account/$probe" ] && [ ! -e "$account/bin/$probe" ] ||
  note_failure 'the probe write reached the account peer'
cleanup
after="$(snapshot)"
printf 'account peer before: %s\naccount peer after:  %s\n' "$before" "$after"
[ "$before" = "$after" ] || note_failure "account peer changed: before [$before] after [$after]"

[ "$failed" = 0 ] || exit 1
printf '%s\n' 'isolated Kimi peer regression probe: PASS'
