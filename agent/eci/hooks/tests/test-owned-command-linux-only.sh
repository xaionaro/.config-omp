#!/usr/bin/env bash
# owned-command is Linux-only by design (cgroup v2 and pidfd custody). On any other host it
# must refuse before building or launching anything. A uname shim simulates Darwin.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/owned-command-linux-only.XXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/shim"
printf '#!/usr/bin/env bash\necho Darwin\n' >"$tmp/shim/uname"
chmod 0755 "$tmp/shim/uname"
expected='owned-command: Linux-only (cgroup v2 and pidfd custody); Darwin is not supported'
rc=0
out="$(PATH="$tmp/shim:$PATH" "$root/bin/owned-command" -deadline 1s -- true 2>&1)" || rc=$?
if [ "$rc" -ne 1 ]; then
  printf 'owned-command Linux-only: expected exit 1, got %s\n' "$rc" >&2
  exit 1
fi
if [ "$out" != "$expected" ]; then
  printf 'owned-command Linux-only: unexpected message: %s\n' "$out" >&2
  exit 1
fi
printf '%s\n' 'owned-command Linux-only fail-fast: PASS'
