#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${HOME:?HOME must be set}/tmp}"
mkdir -p -- "$TMP_PARENT"
TEST_ROOT="$(mktemp -d "$TMP_PARENT/eci-active-sync-runtime-local-first.XXXXXX")"
trap 'rm -rf -- "$TEST_ROOT"' EXIT HUP INT TERM

TEST_HOME="$TEST_ROOT/home"
SOURCE_ECI="$TEST_HOME/.omp/agent/eci"
TARGET_ECI="$TEST_ROOT/deployed/.omp/agent/eci"
SOURCE_ECI="$SOURCE_ECI/bin/eci-active"
OUTPUT="$TEST_ROOT/sync-runtime-output"

mkdir -p -- "$TEST_HOME/tmp" "$SOURCE_ECI" "$TEST_ROOT/deployed"
cp -a -- "$ROOT/bin" "$SOURCE_ECI/"
cp -a -- "$ROOT/hooks" "$SOURCE_ECI/"
cp -- "$ROOT/hooks.json" "$SOURCE_ECI/hooks.json"
cp -a -- "$SOURCE_ECI" "$TARGET_ECI"

# Prove that a real local runtime publish is required, rather than merely a
# source receipt update. Kimi is deliberately absent from this fixture.
printf '%s\n' 'stale local runtime target' >"$TARGET_ECI/hooks/validate-bash.sh"
rm -f -- "$SOURCE_ECI/.eci-runtime-sync-manifest" \
  "$TARGET_ECI/.eci-runtime-sync-manifest"

if ! env -u KIMI_CODE_HOME \
  HOME="$TEST_HOME" ECI_RUNTIME_ROOTS="$TARGET_ECI" \
  "$SOURCE_ECI" sync-runtime >"$OUTPUT" 2>&1; then
  printf 'sync-runtime should succeed when the Kimi peer is unavailable:\n' >&2
  cat -- "$OUTPUT" >&2
  exit 1
fi

cmp -- "$SOURCE_ECI/hooks/validate-bash.sh" "$TARGET_ECI/hooks/validate-bash.sh"
[ -f "$SOURCE_ECI/.eci-runtime-sync-manifest" ] && [ ! -L "$SOURCE_ECI/.eci-runtime-sync-manifest" ]
[ -f "$TARGET_ECI/.eci-runtime-sync-manifest" ] && [ ! -L "$TARGET_ECI/.eci-runtime-sync-manifest" ]
grep -Fq 'ECI runtime synchronization complete: provider=eci' "$OUTPUT"
grep -Fq 'ECI runtime peer synchronization advisory:' "$OUTPUT"

printf '%s\n' 'eci-active sync-runtime local-first: PASS'
