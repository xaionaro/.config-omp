#!/usr/bin/env bash
set -euo pipefail

# Verify the environment-command recognizer alone so the contract remains
# testable while the live PreToolUse dispatcher is temporarily bypassed.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

. "$ROOT/hooks/lib/eci-environment-command.sh"

assert_detail() {
  local command="$1" expected="$2" actual
  actual="$(environment_command_detail "$command")"
  [ "$actual" = "$expected" ] || {
    printf 'environment detail mismatch: command=%q expected=%q actual=%q\n' \
      "$command" "$expected" "$actual" >&2
    return 1
  }
}

# A bounded identifier query is read-only. This helper does not handle output,
# so local variable-name registration cannot be an admission condition.
assert_detail 'printenv FIRMWARE_CAPTURE_PORT' \
  $'ALLOW\tprintenv\t1\t0\tprintenv\tdirect-registered-query'
assert_detail "printenv 'FIRMWARE_CAPTURE_PORT'" \
  $'ALLOW\tprintenv\t1\t0\tprintenv\tdirect-registered-query'

# Concrete query-shape errors remain diagnosable without converting a name into
# a registry-based access boundary.
assert_detail 'printenv PATH PATH' \
  $'DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t1\t2\tPATH\tduplicate-name'
assert_detail 'printenv PATH | env' \
  $'DENY\tECI_ENVIRONMENT_ENUMERATION_DENIED\t2\t0\tenv\tno-child'

printf 'eci environment command: PASS\n'
