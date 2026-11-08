#!/usr/bin/env bash
# Test for hooks/lib/eci-native-provision.sh: on-demand provisioning of the native ECI tools.
# Hermetic: a fake go on PATH answers `go env GOHOSTOS GOHOSTARCH`, copies a host-native fixture for
# `go build`, and logs one line per built name, so the real hooks/build-native-binaries.sh runs unchanged.
# Covers cold, warm, concurrent, tampered, partially deleted, host-incompatible, symlinked and directory
# entries, and a failing builder.
# Under bash older than 4.1 on Linux the loader's descriptor path cannot run (named descriptors), so the
# assertions that need a loader verification print SKIP; classification, locking and build counts still run.
# Usage: test-eci-native-provision.sh
set -euo pipefail
umask 077

tests_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
. "$tests_dir/portable-test.sh"
src_hooks="$(cd -P -- "$tests_dir/.." && pwd -P)"
host_os="$(uname -s)"
case "$host_os" in
  Linux | Darwin) ;;
  *) printf 'FAIL: unsupported host OS %s\n' "$host_os" >&2; exit 1 ;;
esac

checks=0
pass() { checks=$((checks + 1)); printf 'PASS: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
skip() { printf 'SKIP: %s\n' "$*"; }

if ! "$BASH" -n "$src_hooks/lib/eci-native-provision.sh"; then
  fail "lib/eci-native-provision.sh does not parse under bash $BASH_VERSION"
fi
pass "lib/eci-native-provision.sh parses under bash $BASH_VERSION"

# Resolve the real Go caches and toolchain before HOME moves into the fixture.
GOCACHE="$(go env GOCACHE)"; export GOCACHE
GOMODCACHE="$(go env GOMODCACHE)"; export GOMODCACHE
host_goarch="$(go env GOARCH)"
real_go="$(command -v go)"

if [ "$host_os" = Linux ]; then
  base_parent="${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR must be set: the fixture base must be private}"
  fake_goos=linux
else
  base_parent="${TMPDIR:-/tmp}"
  fake_goos=darwin
fi
base="$(mktemp -d -- "${base_parent%/}/eci-native-provision.XXXXXX")"
base="$(cd -P -- "$base" && pwd -P)"
trap '[ ! -s "$base/linger.pid" ] || kill "$(cat -- "$base/linger.pid")" 2>/dev/null || true; chmod -R -- u+rwx "$base" 2>/dev/null || true; rm -rf -- "$base"' EXIT

home="$base/home"
eci="$home/.omp/agent/eci"
bin="$eci/bin"
lib="$eci/hooks/lib/eci-native-provision.sh"
mkdir -p -- "$eci/hooks/lib" "$base/src" "$base/fakebin" "$base/tmp" "$base/out"
cp -- "$src_hooks/build-native-binaries.sh" "$eci/hooks/"
cp -- "$src_hooks/lib/eci-native-binary.sh" "$src_hooks/lib/eci-file-lock.sh" "$src_hooks/lib/eci-native-provision.sh" "$eci/hooks/lib/"
for name in eci-command-plan eci-safe-import eci-hook-helper eci-worker-git eci-command-gate-mode eci-verexec; do
  mkdir -p -- "$eci/hooks/lib/$name-go"
  printf 'module fixture\n\ngo 1.21\n' >"$eci/hooks/lib/$name-go/go.mod"
done

# One host-native fixture executable; every fake build installs a copy of it.
printf 'module ecinativeprovisionfixture\n\ngo 1.21\n' >"$base/src/go.mod"
printf 'package main\n\nfunc main() {}\n' >"$base/src/main.go"
(cd -- "$base/src" && env CGO_ENABLED=0 GOTOOLCHAIN=local "$real_go" build -buildvcs=false -o "$base/fixture" .)
# On Linux the gate binaries are verified by eci-verexec, so a fake build of it installs the real verifier.
if [ "$host_os" = Linux ]; then
  (cd -- "$src_hooks/lib/eci-verexec-go" && env CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off GOFLAGS=' ' "$real_go" build -mod=readonly -trimpath -buildvcs=false -o "$base/verexec" .)
else
  cp -- "$base/fixture" "$base/verexec"
fi
chmod -- 0755 "$base/verexec"
chmod -- 0755 "$base/fixture"

build_log="$base/build.log"
cat >"$base/fakebin/go" <<'EOF'
#!/bin/sh
case "$1" in
  env) printf '%s\n%s\n' "$FAKE_GOOS" "$FAKE_GOARCH" ;;
  build)
    if [ -n "${FAKE_GO_FAIL:-}" ]; then
      # A lingering child inherits the caller's descriptors, as a stray process from a real build could.
      # It keeps the provisioning lock's descriptor open after the provisioner returns.
      if [ -n "${FAKE_GO_LINGER:-}" ]; then
        sleep 60 </dev/null >/dev/null 2>&1 &
        printf '%s\n' "$!" >"$FAKE_GO_LINGER"
      fi
      echo 'fake go: build failed' >&2
      exit 1
    fi
    out=''
    while [ "$#" -gt 0 ]; do
      [ "$1" != -o ] || out="$2"
      shift
    done
    sleep "${FAKE_GO_SLEEP:-0}"
    case "${out##*/}" in
      eci-verexec) cp -- "$FAKE_GO_VEREXEC" "$out" ;;
      *) cp -- "$FAKE_GO_FIXTURE" "$out" ;;
    esac
    printf '%s\n' "${out##*/}" >>"$FAKE_GO_LOG"
    ;;
  *) exit 2 ;;
esac
EOF
chmod -- 0755 "$base/fakebin/go"

export HOME="$home" TMPDIR="$base/tmp" PATH="$base/fakebin:/usr/bin:/bin"
export FAKE_GOOS="$fake_goos" FAKE_GOARCH="$host_goarch" FAKE_GO_FIXTURE="$base/fixture" FAKE_GO_VEREXEC="$base/verexec" FAKE_GO_LOG="$build_log"
: >"$build_log"

# run_provision <label>: one provisioning call in a fresh shell; stdout, stderr and rc land in $base/out/<label>.*
run_provision() {
  local rc=0
  "$BASH" -c 'source "$1" && eci_native_provision' provision "$lib" >"$base/out/$1.out" 2>"$base/out/$1.err" || rc=$?
  printf '%s\n' "$rc" >"$base/out/$1.rc"
}
rc_of() { cat -- "$base/out/$1.rc"; }
builds() { wc -l <"$build_log" | tr -d ' '; }
built_names() { LC_ALL=C sort "$build_log" | tr '\n' ' ' | sed 's/ $//'; }
expect_stdout_empty() { [ ! -s "$base/out/$1.out" ] || fail "$1: provisioner wrote to stdout: $(cat -- "$base/out/$1.out")"; }
expect_err() { grep -F -q -- "$2" "$base/out/$1.err" || fail "$1: stderr lacks '$2': $(cat -- "$base/out/$1.err")"; }
reset_bin() { rm -rf -- "$bin"; : >"$build_log"; }
digest_of() { eci_sha256_hex "$1"; }
patch_byte() { printf "$3" | dd of="$1" bs=1 seek="$2" conv=notrunc 2>/dev/null; }
# Linux also provisions eci-verexec, the native verifier the gate binaries run through.
all='eci-command-plan eci-hook-helper eci-safe-import eci-worker-git'
cold_list="$all"
if [ "$host_os" = Linux ]; then
  all='eci-command-plan eci-hook-helper eci-safe-import eci-verexec eci-worker-git'
  cold_list='eci-command-plan eci-hook-helper eci-safe-import eci-worker-git eci-verexec'
fi

# 1. Cold: one build of every name; messages on stderr only.
reset_bin
run_provision cold
[ "$(built_names)" = "$all" ] || fail "cold: built '$(built_names)', want '$all'"
expect_stdout_empty cold
expect_err cold "eci-active: building native ECI tools ($cold_list); this takes about 1-2 minutes"
expect_err cold 'eci-active: built native ECI tools in '
pass "cold: one build of $all, stdout empty"
[ "$(rc_of cold)" = 0 ] || fail "cold: rc $(rc_of cold): $(cat -- "$base/out/cold.err")"
pass "cold: rc 0 after the loader verified the built gate binaries"
(cd -- "$bin" && for name in $all; do
  [ "$(cut -d ' ' -f 1 "$name.sha256")" = "$(digest_of "$name")" ] || exit 1
done) || fail "cold: a receipt does not match its binary"
pass "cold: every receipt matches its binary"

# 2. Warm: nothing to build, no lock, no loader.
: >"$build_log"
start=$SECONDS
run_provision warm
[ "$(rc_of warm)" = 0 ] || fail "warm: rc $(rc_of warm): $(cat -- "$base/out/warm.err")"
[ "$(builds)" = 0 ] || fail "warm: rebuilt $(built_names)"
[ ! -s "$base/out/warm.err" ] || fail "warm: unexpected stderr: $(cat -- "$base/out/warm.err")"
expect_stdout_empty warm
pass "warm: rc 0, no build, no output ($((SECONDS - start))s)"

# 3. Two concurrent cold callers: exactly one build; one builds, the other waits.
reset_bin
export FAKE_GO_SLEEP=1
run_provision concurrent-a &
first=$!
run_provision concurrent-b &
second=$!
wait "$first" "$second"
unset FAKE_GO_SLEEP
[ "$(built_names)" = "$all" ] || fail "concurrent: built '$(built_names)', want one build of '$all'"
building=$(cat -- "$base/out/concurrent-a.err" "$base/out/concurrent-b.err" | grep -c 'eci-active: building native ECI tools' || true)
waiting=$(cat -- "$base/out/concurrent-a.err" "$base/out/concurrent-b.err" | grep -c 'eci-active: waiting for a concurrent native build' || true)
[ "$building" = 1 ] && [ "$waiting" = 1 ] || fail "concurrent: building=$building waiting=$waiting"
pass "concurrent: one build, one building line, one waiting line"
[ "$(rc_of concurrent-a)" = 0 ] && [ "$(rc_of concurrent-b)" = 0 ] || fail "concurrent: rc $(rc_of concurrent-a)/$(rc_of concurrent-b)"
pass "concurrent: both callers rc 0"

# 4. Tamper: a flipped non-header byte is not rebuilt; the cold path stops on the loader's denial.
size=$(wc -c <"$bin/eci-hook-helper" | tr -d ' ')
patch_byte "$bin/eci-hook-helper" "$((size - 1))" '\001'
tampered=$(digest_of "$bin/eci-hook-helper")
: >"$build_log"
run_provision tamper-warm
[ "$(rc_of tamper-warm)" = 0 ] && [ "$(builds)" = 0 ] || fail "tamper warm: rc $(rc_of tamper-warm), builds $(builds)"
pass "tamper: warm path rc 0 and no build (the hook loader denies the binary on use)"
rm -f -- "$bin/eci-safe-import"
run_provision tamper-cold
[ "$(builds)" = 0 ] || fail "tamper cold: built $(built_names)"
[ "$(rc_of tamper-cold)" != 0 ] || fail "tamper cold: rc 0"
[ "$(digest_of "$bin/eci-hook-helper")" = "$tampered" ] || fail "tamper cold: eci-hook-helper was replaced"
[ ! -e "$bin/eci-safe-import" ] || fail "tamper cold: eci-safe-import was built"
pass "tamper: cold path rc nonzero, nothing built, tampered binary left in place"
expect_err tamper-cold 'ECI native gate binary eci-hook-helper is unavailable or unverified (sha256 does not match its receipt)'
pass "tamper: cold path prints the loader's sha256 denial"

# 5-6 and 8 build next to present gate binaries, which the cold path first verifies through the loader.
# 5. A deleted binary or receipt: only that name is rebuilt.
reset_bin
run_provision refill
rm -f -- "$bin/eci-safe-import"
: >"$build_log"
run_provision deleted-binary
[ "$(built_names)" = eci-safe-import ] || fail "deleted binary: built '$(built_names)'"
[ "$(rc_of deleted-binary)" = 0 ] || fail "deleted binary: rc $(rc_of deleted-binary)"
pass "deleted eci-safe-import binary: only eci-safe-import rebuilt, rc 0"
rm -f -- "$bin/eci-worker-git.sha256"
: >"$build_log"
run_provision deleted-receipt
[ "$(built_names)" = eci-worker-git ] || fail "deleted receipt: built '$(built_names)'"
[ -f "$bin/eci-worker-git.sha256" ] || fail "deleted receipt: eci-worker-git.sha256 not rewritten"
[ "$(rc_of deleted-receipt)" = 0 ] || fail "deleted receipt: rc $(rc_of deleted-receipt)"
pass "deleted eci-worker-git receipt: only eci-worker-git rebuilt, rc 0"
# 6. Host-incompatible regular binaries with matching receipts are rebuilt.
if [ "$host_os" = Linux ]; then format_offset=18; else format_offset=4; fi
for name in eci-hook-helper eci-worker-git; do
  patch_byte "$bin/$name" "$format_offset" '\377'
  printf '%s  %s\n' "$(digest_of "$bin/$name")" "$name" >"$bin/$name.sha256"
done
: >"$build_log"
run_provision foreign
[ "$(built_names)" = 'eci-hook-helper eci-worker-git' ] || fail "foreign: built '$(built_names)'"
for name in eci-hook-helper eci-worker-git; do
  [ "$(digest_of "$bin/$name")" = "$(digest_of "$base/fixture")" ] || fail "foreign: $name not replaced"
done
pass "host-incompatible eci-hook-helper and eci-worker-git: rebuilt"

# 7. A symlink at a gate binary is stuck: nothing is built, even an absent name.
ln -s -- "$base/fixture" "$base/link-target"
rm -f -- "$bin/eci-hook-helper" "$bin/eci-safe-import"
ln -s -- "$base/link-target" "$bin/eci-hook-helper"
: >"$build_log"
run_provision gate-symlink
[ "$(rc_of gate-symlink)" != 0 ] && [ "$(builds)" = 0 ] && [ -L "$bin/eci-hook-helper" ] && [ ! -e "$bin/eci-safe-import" ] ||
  fail "gate symlink: rc $(rc_of gate-symlink), builds $(builds)"
pass "symlink at eci-hook-helper: rc nonzero, nothing built, symlink kept"
expect_err gate-symlink 'ECI native gate binary eci-hook-helper is unavailable or unverified (is missing or a symlink)'
pass "symlink at eci-hook-helper: the loader's reason is printed"

# 8. A symlink at eci-worker-git gets one advisory and does not block the gate set.
rm -f -- "$bin/eci-hook-helper" "$bin/eci-worker-git"
ln -s -- "$base/link-target" "$bin/eci-worker-git"
: >"$build_log"
run_provision worker-symlink
[ "$(built_names)" = 'eci-hook-helper eci-safe-import' ] || fail "worker symlink: built '$(built_names)'"
[ -L "$bin/eci-worker-git" ] || fail "worker symlink: the symlink was replaced"
[ "$(grep -c 'eci-worker-git or its receipt is a symlink or not a regular file; not rebuilt' "$base/out/worker-symlink.err")" -ge 1 ] ||
  fail "worker symlink: no advisory: $(cat -- "$base/out/worker-symlink.err")"
[ "$(rc_of worker-symlink)" = 0 ] || fail "worker symlink: rc $(rc_of worker-symlink): $(cat -- "$base/out/worker-symlink.err")"
pass "symlink at eci-worker-git: advisory, the absent gate binaries still built, symlink kept, rc 0"

# 9. A directory at either path is never built into.
reset_bin
run_provision refill-9
rm -f -- "$bin/eci-worker-git"
mkdir -- "$bin/eci-worker-git"
: >"$build_log"
run_provision worker-dir
[ "$(builds)" = 0 ] && [ -z "$(ls -A -- "$bin/eci-worker-git")" ] || fail "worker dir: builds $(builds), contents $(ls -A -- "$bin/eci-worker-git")"
[ "$(rc_of worker-dir)" = 0 ] || fail "worker dir: rc $(rc_of worker-dir)"
expect_err worker-dir 'eci-worker-git or its receipt is a symlink or not a regular file; not rebuilt'
pass "directory at eci-worker-git: advisory, nothing built into it, rc 0"
rmdir -- "$bin/eci-worker-git"
rm -f -- "$bin/eci-hook-helper"
mkdir -- "$bin/eci-hook-helper"
: >"$build_log"
run_provision gate-dir
[ "$(rc_of gate-dir)" != 0 ] && [ "$(builds)" = 0 ] && [ -z "$(ls -A -- "$bin/eci-hook-helper")" ] ||
  fail "gate dir: rc $(rc_of gate-dir), builds $(builds)"
pass "directory at eci-hook-helper: rc nonzero, nothing built, nothing moved into it"
rmdir -- "$bin/eci-hook-helper"

# 10. A failing builder: rc nonzero, nothing installed, and the lock is free afterwards. The failing fake go leaves
# a child that inherited descriptor 7, so the lock is free only if the provisioner unlocked it explicitly
# (eci_file_lock_release_fd 7); closing its own copy of the descriptor is not enough while the child holds one.
reset_bin
linger_pid_file="$base/linger.pid"
export FAKE_GO_FAIL=1 FAKE_GO_LINGER="$linger_pid_file"
run_provision builder-fails
unset FAKE_GO_FAIL FAKE_GO_LINGER
linger_pid=$(cat -- "$linger_pid_file" 2>/dev/null || true)
[ -n "$linger_pid" ] && kill -0 "$linger_pid" 2>/dev/null || fail "builder fails: the lingering child is not running"
[ "$(rc_of builder-fails)" != 0 ] || fail "builder fails: rc 0"
[ ! -e "$bin/eci-command-plan" ] || fail "builder fails: a binary was installed"
expect_stdout_empty builder-fails
expect_err builder-fails 'build-native-binaries: eci-command-plan build failed'
lock_free=0
(
  exec 7>>"$bin/.native-provision.lock"
  source "$eci/hooks/lib/eci-file-lock.sh"
  eci_file_lock_acquire_fd 7 nonblocking
) && lock_free=1
kill "$linger_pid" 2>/dev/null || true
[ "$lock_free" = 1 ] || fail "builder fails: the provisioning lock is still held while a child keeps its descriptor"
pass "failing builder: rc nonzero, nothing installed, builder output on stderr, lock released explicitly"

# 11. Linux verifier bootstrap: with the gate binaries present but eci-verexec absent, nothing can verify them
# before the build, so one run builds the verifier and then verifies every gate binary through it.
if [ "$host_os" = Linux ]; then
  reset_bin
  run_provision bootstrap-fill
  rm -f -- "$bin/eci-verexec" "$bin/eci-verexec.sha256"
  : >"$build_log"
  run_provision bootstrap
  [ "$(built_names)" = eci-verexec ] || fail "bootstrap: built '$(built_names)'"
  [ "$(rc_of bootstrap)" = 0 ] || fail "bootstrap: rc $(rc_of bootstrap): $(cat -- "$base/out/bootstrap.err")"
  pass "verifier bootstrap: only eci-verexec built, then the present gate binaries verified, rc 0"
  rm -f -- "$bin/eci-verexec"
  size=$(wc -c <"$bin/eci-hook-helper" | tr -d ' ')
  patch_byte "$bin/eci-hook-helper" "$((size - 1))" '\002'
  : >"$build_log"
  run_provision bootstrap-tamper
  [ "$(built_names)" = eci-verexec ] || fail "bootstrap tamper: built '$(built_names)'"
  [ "$(rc_of bootstrap-tamper)" != 0 ] || fail "bootstrap tamper: rc 0"
  expect_err bootstrap-tamper 'ECI native gate binary eci-hook-helper is unavailable or unverified (sha256 does not match its receipt)'
  pass "verifier bootstrap with a tampered gate binary: the verifier is built, the gate binary is denied, rc nonzero"
fi

printf 'eci-native-provision: %s checks passed (host %s, bash %s)\n' "$checks" "$host_os" "$BASH_VERSION"
