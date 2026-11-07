#!/usr/bin/env bash
# Detect drift between the stop proof and checklist audit grammar.

set -uo pipefail

HOOK_DIR="$(cd "$(dirname "$0")" && pwd)"
VER="$HOOK_DIR/stop-verification.md"
CHK="$HOOK_DIR/stop-checklist.md"

if [ ! -f "$VER" ]; then
  echo "check-audit-sync: missing required file $VER" >&2
  exit 1
fi
if [ ! -f "$CHK" ]; then
  echo "check-audit-sync: missing required file $CHK" >&2
  exit 1
fi

FAIL=0

check_phrase() {
  local phrase="$1"
  if ! grep -qF "$phrase" "$VER"; then
    echo "DRIFT: '$phrase' missing from stop-verification.md"
    FAIL=1
  fi
  if ! grep -qF "$phrase" "$CHK"; then
    echo "DRIFT: '$phrase' missing from stop-checklist.md"
    FAIL=1
  fi
}

check_phrase "Rule-Compliance Self-Audit"
check_phrase "The audit subject is the written rule"
check_phrase "the last turn only"
check_phrase "clean-scan: AGENTS.md"
check_phrase "Violation:"
check_phrase "correction marker"
check_phrase "rescanned:"

if ! grep -q "Keep in sync" "$VER"; then
  echo "DRIFT: stop-verification.md is missing its Keep in sync marker"
  FAIL=1
fi
if ! grep -q "Keep in sync" "$CHK"; then
  echo "DRIFT: stop-checklist.md is missing its Keep in sync marker"
  FAIL=1
fi

if [ "$FAIL" -eq 0 ]; then
  echo "check-audit-sync: OK (7 phrases verified in both files)"
fi
exit "$FAIL"
