#!/usr/bin/env bash
set -euo pipefail
# Focused current-contract proof uses the real registered private launcher and
# a CLI compiled from current module source. Historical native grammar remains
# explicitly available through test-normal-git-admission.sh component targets.
SOURCE_ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
case "${1:-matrix}" in
  native) export NORMAL_GIT_ADMISSION_TARGET=worker-git-native ;;
  slice) export NORMAL_GIT_ADMISSION_TARGET=worker-git-cli-slice ;;
  edges) export NORMAL_GIT_ADMISSION_TARGET=worker-git-cli-edges ;;
  aliases) export NORMAL_GIT_ADMISSION_TARGET=worker-git-index-aliases ;;
  matrix|full) export NORMAL_GIT_ADMISSION_TARGET=worker-git-cli-matrix ;;
  *) printf 'usage: %s [native|slice|edges|aliases|matrix|full]\n' "$0" >&2; exit 64 ;;
esac
exec bash "$SOURCE_ROOT/hooks/tests/test-normal-git-admission.sh"
