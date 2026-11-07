#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${HOME:?HOME must be set}/tmp}"
mkdir -p -- "$TMP_PARENT"
TEST_ROOT="$(mktemp -d "$TMP_PARENT/eci-maintain-planner-local-first.XXXXXX")"
trap 'rm -rf -- "$TEST_ROOT"' EXIT HUP INT TERM

TEST_HOME="$TEST_ROOT/home"
ECI_HOME="$TEST_HOME/.omp/agent/eci"
BIN="$ECI_HOME/bin"
MODULE_PLANNER="$ECI_HOME/hooks/lib/eci-command-plan-go/eci-command-plan"
OUTPUT="$TEST_ROOT/maintain-planner-output"

# There deliberately is no Kimi home under TEST_HOME. The local planner pair in
# bin/ is the maintenance operation; an optional peer cannot prevent its repair.
mkdir -p -- "$TEST_HOME/tmp" "$ECI_HOME"
cp -a -- "$ROOT/bin" "$ROOT/hooks" "$ROOT/tools" "$ECI_HOME/"
cp -- "$ROOT/hooks.json" "$ECI_HOME/hooks.json"
chmod -R go-w -- "$TEST_HOME"

# A stale pair the native gate refuses: not an executable, receipts that name no digest.
for name in eci-command-plan eci-safe-import eci-hook-helper; do
  printf 'stale %s\n' "$name" >"$BIN/$name"
  chmod 600 -- "$BIN/$name"
  printf '%064d  %s\n' 0 "$name" >"$BIN/$name.sha256"
done
printf '%s\n' 'stale module planner' >"$MODULE_PLANNER"

if ! HOME="$TEST_HOME" KIMI_CODE_HOME="$TEST_HOME/.kimi-code" \
  "$BIN/eci-active" maintain-planner >"$OUTPUT" 2>&1; then
  printf '%s\n' 'maintain-planner should repair the local ECI planner pair when Kimi is unavailable:' >&2
  cat -- "$OUTPUT" >&2
  exit 1
fi

for name in eci-command-plan eci-safe-import eci-hook-helper; do
  [ -f "$BIN/$name" ] && [ ! -L "$BIN/$name" ]
  [ "$(stat -c '%a' -- "$BIN/$name")" = 755 ]
  [ "$(cat -- "$BIN/$name.sha256")" = "$(sha256sum -- "$BIN/$name" | cut -d ' ' -f 1)  $name" ]
done
cmp -- "$BIN/eci-command-plan" "$MODULE_PLANNER"
(
  HOME="$TEST_HOME"
  source "$ECI_HOME/hooks/lib/eci-native-binary.sh"
  eci_runtime_open_verified eci-command-plan || { printf '%s\n' "$ECI_RUNTIME_DENY_REASON" >&2; exit 1; }
  eci_runtime_close_verified
)
[ -z "$(find "$BIN" -maxdepth 1 -name '.planner-importer.txn.*')" ]
grep -Fq "PLANNER_LOCAL_SYNCED $BIN" "$OUTPUT"
grep -Fq 'ECI pair complete: local=true peer=false requested=eci' "$OUTPUT"

printf '%s\n' 'eci-maintain-planner local-first: PASS'
