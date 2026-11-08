#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/stop-ordinary-continuation.XXXXXX")"
FIXTURE_HOME="$TMP_ROOT/home"
FIXTURE_ECI="$FIXTURE_HOME/.omp/agent/eci"
REPO="$TMP_ROOT/repo"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
# Smoke-test the copied PreToolUse hook bodies, removing a line-2 bypass if
# present, with any callback state confined to the private fixture.
mkdir -p -- "$FIXTURE_ECI" "$FIXTURE_HOME/tmp" "$REPO"
cp -a -- "$ROOT/hooks" "$FIXTURE_ECI/hooks"
cp -a -- "$ROOT/bin" "$FIXTURE_ECI/bin"
eci_sed_inplace '2{/^exit 0$/d;}' "$FIXTURE_ECI/hooks/validate-bash.sh"
eci_sed_inplace '2{/^exit 0$/d;}' "$FIXTURE_ECI/hooks/pretooluse-edit-dispatch.sh"
(
  export HOME="$FIXTURE_HOME" ECI_HOME="$FIXTURE_ECI" ECI_PROOF_ROOT="$TMP_ROOT/pretooluse-proof"
  export XDG_CONFIG_HOME="$TMP_ROOT/config" XDG_STATE_HOME="$TMP_ROOT/state" XDG_CACHE_HOME="$TMP_ROOT/cache"
  printf '%s\n' '{"tool_name":"Bash"}' | bash "$FIXTURE_ECI/hooks/validate-bash.sh" >/dev/null
  printf '%s\n' '{"tool_name":"Edit"}' | bash "$FIXTURE_ECI/hooks/pretooluse-edit-dispatch.sh" >/dev/null
)
printf 'PreToolUse copied-hook smoke: PASS\n'
