#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-lifecycle-source-authority.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

failures=0

fail() {
  printf 'FAIL %s\n' "$*" >&2
  failures=$((failures + 1))
}

fixture_home="$TMP_ROOT/home"
fixture_eci="$fixture_home/.omp/agent/eci"
fixture_kimi_home="$TMP_ROOT/kimi-home"
fixture_kimi="$fixture_kimi_home/.kimi-code"
foreign_root="$TMP_ROOT/foreign"
poisoned_eci="$TMP_ROOT/poisoned-eci"
proof_root="$TMP_ROOT/proof"
cwd="$TMP_ROOT/cwd"
ledger_input="$TMP_ROOT/ledger-input.json"

mkdir -p -- "$fixture_home" "$fixture_eci" "$fixture_kimi_home" "$fixture_kimi" "$foreign_root/hooks/lib" \
  "$poisoned_eci" "$proof_root" "$cwd"
proof_root="$(eci_realpath_existing "$proof_root")"
cp -a -- "$ROOT/hooks" "$fixture_eci/hooks"
cp -a -- "$ROOT/hooks" "$fixture_kimi/hooks"
cp -- "$ROOT/hooks/lib/eci-proof-state.sh" "$foreign_root/hooks/lib/eci-proof-state.sh"
cp -- "$ROOT/hooks/lib/eci-file-lock.sh" "$foreign_root/hooks/lib/eci-file-lock.sh"

make_claim() {
  local session_id="$1" claim
  claim="$proof_root/$session_id/.eci-accidental-mistake-override.claim"
  mkdir -p -- "$claim"
  printf '%s\n' "$claim"
}

eci_claim="$(make_claim eci-recovery)"
kimi_claim="$(make_claim kimi-recovery)"
expected_eci_command='"$HOME/.omp/agent/eci/bin/eci-active"'
expected_kimi_command='"${KIMI_CODE_HOME:-$HOME/.kimi-code}/bin/eci-active"'

eci_cleanup_command="$(
  HOME="$fixture_home" ECI_HOME="$poisoned_eci" KIMI_CODE_HOME="$fixture_kimi" \
    ECI_PROOF_ROOT="$proof_root" \
    bash -c '. "$1"; eci_accidental_override_cleanup_command "$2"' _ \
      "$fixture_eci/hooks/lib/eci-proof-state.sh" "$eci_claim"
)" || fail 'ECI recovery helper did not produce a canonical lifecycle command'
if [ "$eci_cleanup_command" != "$expected_eci_command accidental-override-cleanup --authorized-by-user $eci_claim" ]; then
  fail "ECI recovery command did not use literal HOME authority: $eci_cleanup_command"
fi

kimi_cleanup_command="$(
  HOME="$fixture_kimi_home" ECI_HOME="$poisoned_eci" KIMI_CODE_HOME="$fixture_kimi" \
    ECI_PROOF_ROOT="$proof_root" \
    bash -c '. "$1"; eci_accidental_override_cleanup_command "$2"' _ \
      "$fixture_kimi/hooks/lib/eci-proof-state.sh" "$kimi_claim"
)" || fail 'Kimi recovery helper did not produce a provider-specific lifecycle command'
if [ "$kimi_cleanup_command" != "$expected_kimi_command accidental-override-cleanup --authorized-by-user $kimi_claim" ]; then
  fail "Kimi recovery command did not use KIMI_CODE_HOME authority: $kimi_cleanup_command"
fi

if kimi_mismatched_cleanup_command="$(
  HOME="$fixture_kimi_home" ECI_HOME="$poisoned_eci" KIMI_CODE_HOME="$fixture_eci" \
    ECI_PROOF_ROOT="$proof_root" \
    bash -c '. "$1"; eci_accidental_override_cleanup_command "$2"' _ \
      "$fixture_kimi/hooks/lib/eci-proof-state.sh" "$kimi_claim"
)"; then
  fail "Kimi proof helper emitted a lifecycle command when KIMI_CODE_HOME selected another root: $kimi_mismatched_cleanup_command"
fi

if foreign_cleanup_command="$(
  HOME="$fixture_home" ECI_HOME="$poisoned_eci" KIMI_CODE_HOME="$fixture_kimi" \
    ECI_PROOF_ROOT="$proof_root" \
    bash -c '. "$1"; eci_accidental_override_cleanup_command "$2"' _ \
      "$foreign_root/hooks/lib/eci-proof-state.sh" "$eci_claim"
)"; then
  fail "foreign proof helper emitted a lifecycle command: $foreign_cleanup_command"
fi

ledger_path="$fixture_kimi_home/.agent/context/sessions/kimi-ledger/high_level_log.jsonl"
mkdir -p -- "${ledger_path%/*}"
printf '%s\n' '{"event":"decision","summary":"coordinator ledger"}' >"$ledger_path"
jq -cn --arg cwd "$cwd" --arg path "$ledger_path" \
  '{session_id:"kimi-ledger",cwd:$cwd,tool_name:"Write",tool_input:{file_path:$path,content:"forged"}}' \
  >"$ledger_input"
ledger_output="$TMP_ROOT/kimi-ledger-output.json"
HOME="$fixture_kimi_home" ECI_HOME="$poisoned_eci" KIMI_CODE_HOME="$fixture_kimi" \
  ECI_PROOF_ROOT="$proof_root" \
  bash "$fixture_kimi/hooks/eci-active-gate.sh" <"$ledger_input" >"$ledger_output"
if [ -s "$ledger_output" ]; then
  fail "Kimi current-session ledger edit was blocked: $(cat -- "$ledger_output")"
fi

if [ "$failures" -ne 0 ]; then
  exit 1
fi

printf '%s\n' 'lifecycle source authority assertions: PASS'
