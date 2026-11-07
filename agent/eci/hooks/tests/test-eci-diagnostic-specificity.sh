#!/usr/bin/env bash
set -euo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$HOOK_DIR/lib/eci-diagnostic.sh"

assert_code() {
  local expected="$1" reason="$2" actual
  actual="$(eci_diagnostic_code_for_reason "$reason")"
  [ "$actual" = "$expected" ] || { printf 'expected %s, got %s\n' "$expected" "$actual" >&2; exit 1; }
  [[ "$actual" == ECI_* ]] || exit 1
}

assert_code ECI_UNSAFE_PATH_DENIED 'Refusing unsafe session-scoped edit path marker.'
assert_code ECI_GIT_PUSH_DENIED 'git push is blocked.'
assert_code ECI_REVIEW_MANIFEST_DENIED 'review gate denied malformed manifest.'

unknown="$(eci_diagnostic_code_for_reason 'mystery policy / input')"
[ "$unknown" = ECI_GATE_FAILURE_MYSTERY_POLICY_INPUT ] || exit 1
[[ "$unknown" != ECI_DIAGNOSTIC_DENIED ]] || exit 1
legacy="$(eci_diagnostic_code_for_reason '[ECI_COMMAND_IDENTITY_UNSUPPORTED] unsupported wrapper: literal command')"
[ "$legacy" = ECI_COMMAND_IDENTITY_INVALID ] || exit 1
for legacy_reason in '[ECI_GATE_DENIED]: malformed input' '[ECI_REVIEW_GATE_DENIED]' '[ECI_STOP_GATE_DENIED], marker invalid' '[ECI_DIAGNOSTIC_DENIED]'; do
  code="$(eci_diagnostic_code_for_reason "$legacy_reason")"
  [[ "$code" == ECI_* ]] || exit 1
  [[ "$code" != ECI_GATE_DENIED && "$code" != ECI_REVIEW_GATE_DENIED && "$code" != ECI_STOP_GATE_DENIED && "$code" != ECI_DIAGNOSTIC_DENIED ]] || exit 1
done
direct_bracket="$(eci_diagnostic_reason '[ECI_GATE_DENIED]' PreToolUse test-operation subject 'legacy detail' retry)"
[[ "$direct_bracket" != *'[[ECI_GATE_DENIED]]'* ]] || exit 1
direct_reason="$(eci_diagnostic_reason ECI_GATE_DENIED PreToolUse test-operation subject '[ECI_GATE_DENIED] legacy detail' retry)"
[[ "$direct_reason" != *'ECI_GATE_DENIED'* ]] || exit 1
for legacy_code in ECI_COMMAND_IDENTITY_UNSUPPORTED ECI_GATE_DENIED ECI_REVIEW_GATE_DENIED ECI_STOP_GATE_DENIED ECI_DIAGNOSTIC_DENIED ECI_EDIT_ROUTE_DENIED; do
  direct="$(eci_diagnostic_reason "$legacy_code" PreToolUse test-operation subject 'legacy detail' retry)"
  [[ "$direct" != *"[$legacy_code]"* ]] || exit 1
  for field in 'phase=PreToolUse' 'operation=test-operation' 'subject=subject' 'reason: legacy detail' 'remediation: retry'; do
    [[ "$direct" == *"$field"* ]] || exit 1
  done
done

diagnostic="$(eci_diagnostic_reason "$unknown" PreToolUse edit-validation subject 'mystery policy / input' retry)"
for field in 'phase=PreToolUse' 'operation=edit-validation' 'subject=subject' 'reason: mystery policy / input' 'remediation: retry'; do
  [[ "$diagnostic" == *"$field"* ]] || { printf 'missing diagnostic field: %s\n' "$field" >&2; exit 1; }
done

if rg -n 'ECI_(COMMAND_IDENTITY_UNSUPPORTED|REVIEW_GATE_DENIED|STOP_GATE_DENIED|DIAGNOSTIC_DENIED|EDIT_ROUTE_DENIED)' "$HOOK_DIR" --glob '!**/tests/**' --glob '!**/lib/eci-diagnostic.sh'; then
  printf 'legacy generic diagnostic code found\n' >&2
  exit 1
fi

printf 'ok\n'
