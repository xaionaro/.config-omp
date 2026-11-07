#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${HOME:?HOME must be set}/tmp}"
mkdir -p -- "$TMP_PARENT"
TEST_ROOT="$(mktemp -d "$TMP_PARENT/eci-maintain-planner-local-first.XXXXXX")"
trap 'rm -rf -- "$TEST_ROOT"' EXIT HUP INT TERM

TEST_HOME="$TEST_ROOT/home"
ECI_HOME="$TEST_HOME/.omp/agent/eci"
PLANNER_DIR="$ECI_HOME/hooks/lib/eci-command-plan-go"
PLANNER_BINARY="$PLANNER_DIR/eci-command-plan"
PLANNER_RECEIPT="$PLANNER_DIR/.eci-command-plan.provenance"
OUTPUT="$TEST_ROOT/maintain-planner-output"

# There deliberately is no $TEST_HOME/.kimi-code. The local ECI planner is
# the maintenance operation; an optional peer cannot prevent its repair.
mkdir -p -- "$TEST_HOME/tmp" "$ECI_HOME"
cp -a -- "$ROOT/bin" "$ECI_HOME/"
cp -a -- "$ROOT/hooks" "$ECI_HOME/"
cp -- "$ROOT/hooks.json" "$ECI_HOME/hooks.json"

printf '%s\n' 'stale local planner' >"$PLANNER_BINARY"
chmod 600 -- "$PLANNER_BINARY"
rm -f -- "$PLANNER_RECEIPT"

if ! HOME="$TEST_HOME" "$ECI_HOME/bin/eci-active" maintain-planner >"$OUTPUT" 2>&1; then
  printf '%s\n' 'maintain-planner should repair the local ECI planner when Kimi is unavailable:' >&2
  cat -- "$OUTPUT" >&2
  exit 1
fi

[ -f "$PLANNER_BINARY" ] && [ ! -L "$PLANNER_BINARY" ]
[ "$(stat -c '%a' -- "$PLANNER_BINARY")" = 755 ]
[ "$(head -c 4 -- "$PLANNER_BINARY")" = $'\x7fELF' ]
[ -f "$PLANNER_RECEIPT" ] && [ ! -L "$PLANNER_RECEIPT" ]
grep -Fq 'PLANNER_LOCAL_SYNCED' "$OUTPUT"
grep -Fq 'ECI planner peer synchronization advisory:' "$OUTPUT"

printf '%s\n' 'eci-maintain-planner local-first: PASS'
