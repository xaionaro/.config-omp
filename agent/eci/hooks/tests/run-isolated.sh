#!/usr/bin/env bash
# Run one hooks test (or run.sh) with a per-test temporary HOME.
#
# Usage: run-isolated.sh <test-file-in-this-dir> [args...]
#
# The per-test work area lives under $XDG_RUNTIME_DIR (mode 0700). Its ancestors
# must be private because the planner refuses a bin directory with a
# group/other-writable ancestor, and ~/tmp is group-writable.
# HOME is set to <work>/home. The runtime is copied to <work>/home/.omp/agent/eci
# and the test runs from that copy, so ROOT equals the HOME-derived runtime path.
# The published Go helpers ($HOME/tmp/eci-runtime) are copied too. ~/.kimi-code is
# a symlink to the real peer tree (read by tests). Context and proof roots
# (~/.agent, ~/tmp/agent) resolve under <work> and are removed on exit. Go caches
# stay on account paths.
set -euo pipefail
umask 077

account_home="$(realpath -e -- "${HOME:?HOME must be set}")"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
runtime_root="$(cd "$here/../.." && pwd -P)"
test_name="${1:?usage: run-isolated.sh <test-file-in-this-dir> [args...]}"
shift

export GOPATH="${GOPATH:-$account_home/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
export GOCACHE="${GOCACHE:-$account_home/.cache/go-build}"

base="${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR must be set; it is the private (0700) work area}/eci-hooktest"
mkdir -p -m 700 "$base"
work="$(realpath -e -- "$(mktemp -d "$base/eci-hooktest.XXXXXX")")"
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

mkdir -p "$work/home/.omp/agent" "$work/home/tmp/mtlog"
cp -a "$runtime_root" "$work/home/.omp/agent/eci"
if [ -d "$account_home/tmp/eci-runtime" ]; then
  cp -a "$account_home/tmp/eci-runtime" "$work/home/tmp/eci-runtime"
fi
ln -s "$account_home/.kimi-code" "$work/home/.kimi-code"

export HOME="$work/home"
export TMPDIR="$HOME/tmp/mtlog"
export ECI_PROOF_ROOT="$HOME/tmp/agent/proof"

rc=0
cd "$HOME/.omp/agent/eci"
bash "$HOME/.omp/agent/eci/hooks/tests/$test_name" "$@" || rc=$?
exit "$rc"
