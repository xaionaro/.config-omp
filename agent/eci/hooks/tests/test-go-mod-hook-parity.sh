#!/usr/bin/env bash

set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
if [[ -n ${GO_MOD_HOOK_PEER_ROOT:-} ]]; then
  peer_root=$(cd "$GO_MOD_HOOK_PEER_ROOT" && pwd -P)
elif [[ $(basename "$root") == eci ]]; then
  peer_root=$HOME/.kimi-code
else
  peer_root=$HOME/.omp/agent/eci
fi

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for relative in \
  hooks/pre-commit-go-mod.sh \
  hooks/install-pre-commit-go-mod.sh \
  hooks/tests/test-pre-commit-go-mod.sh; do
  local_file=$root/$relative
  peer_file=$peer_root/$relative
  [[ -f $local_file ]] || fail "missing local file: $local_file"
  [[ -f $peer_file ]] || fail "missing peer file: $peer_file"
  cmp -s "$local_file" "$peer_file" || fail "bytes differ: $relative"
  mode=$(stat -c '%a' "$local_file")
  [[ $mode == 755 ]] || fail "expected mode 755 for $relative, got $mode"
done

[[ "$root/hooks/pre-commit-go-mod.sh" -ef "$peer_root/hooks/pre-commit-go-mod.sh" ]] || \
  fail 'checker files are not one hard-linked inode'

printf 'PASS: checker, installer, test parity and executable-mode expectations\n'
