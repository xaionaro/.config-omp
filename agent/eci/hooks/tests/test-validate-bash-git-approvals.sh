#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

# Git approval artifacts are obsolete: the focused test below verifies
# normal commits and targeted repository actions directly.
bash "$ROOT/hooks/tests/test-normal-git-admission.sh"

printf '%s\n' 'validate-bash normal Git tests: PASS'
