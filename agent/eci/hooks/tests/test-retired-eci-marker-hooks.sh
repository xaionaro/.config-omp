#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
[ ! -e "$ROOT/hooks/stop-gate.sh" ]
jq -e '.hooks | has("Stop") | not' "$ROOT/hooks.json" >/dev/null
for hook in session-snapshot eci-post-compact-refresh system-prompt-reviewer; do
  sed -n '2p' "$ROOT/hooks/$hook.sh" | grep -q '^# TODO: .*ECI marker'
done
printf '%s\n' 'Stop deletion and deferred hook migration: PASS'
