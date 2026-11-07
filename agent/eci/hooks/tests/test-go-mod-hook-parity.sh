#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for relative in \
  hooks/install-pre-commit-go-mod.sh \
  hooks/tests/test-pre-commit-go-mod.sh; do
  local_file=$root/$relative
  [[ -f $local_file ]] || fail "missing local file: $local_file"
  mode=$(stat -c '%a' "$local_file")
  [[ $mode == 755 ]] || fail "expected mode 755 for $relative, got $mode"
done

checker=$root/hooks/pre-commit-go-mod.sh
[[ -f $checker ]] || fail "missing local file: $checker"
[[ -x $checker ]] || fail "expected executable checker: $checker" # install-pre-commit-go-mod.sh:29
grep -Fq 'exec "$repo_root/hooks/pre-commit-go-mod.sh"' "$root/hooks/install-pre-commit-go-mod.sh" || \
  fail 'installer does not exec the canonical checker' # install-pre-commit-go-mod.sh:142-148

printf 'PASS: checker, installer, test parity and executable-mode expectations\n'
