#!/usr/bin/env bash
# Run one hooks test (or run.sh) with a per-test temporary HOME.
#
# Usage: run-isolated.sh <test-file-in-this-dir> [args...]
#
# A copy of the runtime is placed at <tmp>/home/.omp/agent/eci and the test
# runs from that copy, so ROOT equals the HOME-derived runtime path the tests
# check. HOME=<tmp>/home, so the context root (~/.agent/context) and the proof
# root (~/tmp/agent/proof, also ECI_PROOF_ROOT) resolve under <tmp> and are
# removed on exit. ~/.kimi-code is a symlink to the real peer tree (read by
# tests, not copied). Go caches stay on account paths so child go processes
# never fetch under the temp HOME.
set -euo pipefail

real_home="${HOME:?HOME must be set}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
runtime_root="$(cd "$here/../.." && pwd)"
test_name="${1:?usage: run-isolated.sh <test-file-in-this-dir> [args...]}"
shift

export GOPATH="${GOPATH:-$real_home/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
export GOCACHE="${GOCACHE:-$real_home/.cache/go-build}"

mkdir -p "$real_home/tmp/mtlog"
work="$(mktemp -d "$real_home/tmp/mtlog/eci-hooktest.XXXXXX")"
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

mkdir -p "$work/home/.omp/agent" "$work/home/tmp/mtlog"
cp -a "$runtime_root" "$work/home/.omp/agent/eci"
ln -s "$real_home/.kimi-code" "$work/home/.kimi-code"

export HOME="$work/home"
export TMPDIR="$HOME/tmp/mtlog"
export ECI_PROOF_ROOT="$HOME/tmp/agent/proof"

rc=0
cd "$HOME/.omp/agent/eci"
bash "$HOME/.omp/agent/eci/hooks/tests/$test_name" "$@" || rc=$?
exit "$rc"
