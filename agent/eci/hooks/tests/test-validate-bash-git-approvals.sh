#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

# Kept at its established test-entrypoint name for callers. Git approval
# artifacts are obsolete: the focused tests below verify normal commits and
# targeted repository actions directly, plus the legacy CLI's harmless
# compatibility response.
bash "$ROOT/hooks/tests/test-normal-git-admission.sh"
bash "$ROOT/hooks/tests/test-eci-active-normal-git.sh"

printf '%s\n' 'validate-bash normal Git tests: PASS'
