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

# Explicit repair must not loosen bin or retain an identical but unsafe binary.
reset_bin
run_provision repair-fill
chmod 0700 "$bin"
chmod 0775 "$bin/eci-command-plan"
"$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/repair.out" 2>"$base/out/repair.err" || fail "repair failed"
[ -n "$(find "$bin" -prune -perm 700 -print)" ] || fail "repair changed existing bin mode"
[ -n "$(find "$bin/eci-command-plan" -prune -type f -links 1 -perm 755 -print)" ] || fail "repair retained invalid binary metadata"
pass "repair preserves bin mode and replaces an identical binary with invalid metadata"

ln "$bin/eci-command-plan" "$base/linked-planner"
"$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/hardlink.out" 2>"$base/out/hardlink.err" || fail "hardlink repair failed"
[ -n "$(find "$bin/eci-command-plan" -prune -type f -links 1 -print)" ] || fail "repair retained hard-linked binary"
[ -f "$base/linked-planner" ] || fail "repair removed the other hard link"
pass "repair replaces hard-linked binary without removing the other link"

rm "$bin/eci-command-plan.sha256"
run_provision partial-publish
[ "$(rc_of partial-publish)" = 0 ] || fail "partial publication did not recover"
[ -f "$bin/eci-command-plan.sha256" ] || fail "partial publication left receipt missing"
[ -n "$(find "$bin" -prune -perm 700 -print)" ] || fail "provisioning changed existing bin mode"
pass "provisioning recovers receipt-first interrupted publication and preserves bin mode"

rm "$bin/eci-command-plan"
ln -s "$base/fixture" "$bin/eci-command-plan"
if "$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/refuse.out" 2>"$base/out/refuse.err"; then
  fail "repair accepted symlink target"
fi
[ -L "$bin/eci-command-plan" ] || fail "repair replaced symlink"
pass "repair refuses a symlinked managed leaf"

reset_bin
run_provision interrupted-fill
printf x >>"$bin/eci-command-plan"
cat >"$base/fakebin/mv" <<'EOF'
#!/bin/sh
case "$*" in *eci-command-plan.sha256*) exec /bin/mv "$@" ;; *eci-command-plan*) exit 1 ;; esac
exec /bin/mv "$@"
EOF
chmod 0755 "$base/fakebin/mv"
if "$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/interrupted.out" 2>"$base/out/interrupted.err"; then
  fail "interrupted publication unexpectedly succeeded"
fi
[ ! -e "$bin/eci-command-plan.sha256" ] || fail "failed publication retained an old receipt"
rm "$base/fakebin/mv"
run_provision interrupted-recovery
[ "$(rc_of interrupted-recovery)" = 0 ] || fail "failed publication was not recovered"
pass "failed binary rename removes the receipt and next provisioning repairs the pair"
# A foreign-owned but readable receipt must not silently stop a cold run.
if [ "$(id -u)" = 0 ]; then
  chown 1 "$bin/eci-command-plan.sha256"
  before_receipt=$(digest_of "$bin/eci-command-plan.sha256")
  rm "$bin/eci-safe-import"
  run_provision foreign-receipt
  [ "$(rc_of foreign-receipt)" != 0 ] || fail "foreign receipt accepted"
  expect_err foreign-receipt "$bin/eci-command-plan.sha256"
  expect_err foreign-receipt 'owned by another user'
  [ "$(digest_of "$bin/eci-command-plan.sha256")" = "$before_receipt" ] || fail "foreign receipt changed"
  chown 0 "$bin/eci-command-plan.sha256"
  run_provision foreign-recovery
  [ "$(rc_of foreign-recovery)" = 0 ] || fail "foreign receipt recovery failed"
  pass "foreign receipt is retained and its owner conflict is reported"
else
  skip "foreign receipt ownership test requires chown capability"
fi

printf z >>"$bin/eci-command-plan"
cat >"$base/fakebin/mv" <<'EOF'
#!/bin/sh
case "$*" in *eci-command-plan.sha256*) kill -KILL "$PPID"; exit 1 ;; esac
exec /bin/mv "$@"
EOF
chmod 0755 "$base/fakebin/mv"
if "$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/killed.out" 2>"$base/out/killed.err"; then
  fail "killed publisher unexpectedly succeeded"
fi
[ ! -e "$bin/eci-command-plan.sha256" ] || fail "killed publisher left a receipt"
rm "$base/fakebin/mv"
run_provision killed-recovery
[ "$(rc_of killed-recovery)" = 0 ] || fail "killed publisher was not recovered"
pass "SIGKILL between binary and receipt publication remains recoverable"


printf y >>"$bin/eci-command-plan"
before_lock=$(digest_of "$bin/eci-command-plan")
mkfifo "$base/publish-wait"
exec 8>>"$bin/.planner-importer.publish.lock"
source "$eci/hooks/lib/eci-file-lock.sh"
eci_file_lock_acquire_fd 8 blocking || fail "cannot hold publication lock"
"$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/locked.out" 2>"$base/publish-wait" &
locked_builder=$!
IFS= read -r waiting_line <"$base/publish-wait" || fail "builder exited before waiting"
[ "$waiting_line" = 'build-native-binaries: waiting for publication lock' ] || fail "unexpected builder diagnostic: $waiting_line"
[ "$(digest_of "$bin/eci-command-plan")" = "$before_lock" ] || fail "builder published while lock held"
eci_file_lock_release_fd 8 || fail "cannot release publication lock"
wait "$locked_builder" || fail "builder failed after publication lock released"
[ "$(digest_of "$bin/eci-command-plan")" != "$before_lock" ] || fail "builder did not publish after lock release"
pass "builder waits for another publisher before changing the managed pair"

# A retained foreign conflict blocks both normal provisioning and an already-waiting builder.
printf f >>"$bin/eci-command-plan"
rm "$bin/eci-command-plan.sha256"
foreign_digest=$(digest_of "$bin/eci-command-plan")
mkdir "$bin/.planner-importer.txn.foreign"
mkfifo "$base/conflict-wait"
exec 8>>"$bin/.planner-importer.publish.lock"
eci_file_lock_acquire_fd 8 blocking || fail "cannot hold publication lock for conflict"
"$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/conflict-builder.out" 2>"$base/conflict-wait" &
locked_builder=$!
IFS= read -r waiting_line <"$base/conflict-wait" || fail "conflict builder exited before waiting"
[ "$waiting_line" = 'build-native-binaries: waiting for publication lock' ] || fail "unexpected conflict builder diagnostic: $waiting_line"
: >"$bin/.planner-importer.txn.foreign/eci-command-plan.conflict"
eci_file_lock_release_fd 8 || fail "cannot release publication lock for conflict"
if wait "$locked_builder"; then fail "waiting builder overwrote a recorded foreign pair"; fi
[ "$(digest_of "$bin/eci-command-plan")" = "$foreign_digest" ] || fail "waiting builder changed the foreign binary"
[ ! -e "$bin/eci-command-plan.sha256" ] || fail "waiting builder created a receipt over the foreign binary"
run_provision conflict
[ "$(rc_of conflict)" != 0 ] || fail "provisioning accepted a recorded foreign pair"
expect_err conflict 'retained planner conflict for eci-command-plan'
[ "$(digest_of "$bin/eci-command-plan")" = "$foreign_digest" ] || fail "provisioning changed the foreign binary"
[ ! -e "$bin/eci-command-plan.sha256" ] || fail "provisioning created a receipt over the foreign binary"
pass "recorded foreign pair blocks provisioning and an already-waiting builder"

# Explicit repair retains both leaves and all conflict markers without touching old backups.
printf 'receipt before repair\n' >"$bin/eci-command-plan.sha256"
chmod 0640 "$bin/eci-command-plan" "$bin/eci-command-plan.sha256"
cp -p "$bin/eci-command-plan" "$base/expected-planner"
cp -p "$bin/eci-command-plan.sha256" "$base/expected-receipt"
printf 'original backup\n' >"$bin/.planner-importer.txn.foreign/original"
mkdir "$bin/.planner-importer.txn.second"
: >"$bin/.planner-importer.txn.foreign/eci-command-plan.conflict"
: >"$bin/.planner-importer.txn.second/eci-command-plan.conflict"
"$BASH" "$eci/hooks/build-native-binaries.sh" --preserve-conflict eci-command-plan >"$base/out/preserve.out" 2>"$base/out/preserve.err" || fail "conflict repair failed"
retained=$(find "$bin" -name eci-command-plan -path '*/.planner-importer.txn.*/*' -print)
[ -n "$retained" ] || fail "repair did not retain planner"
retained_dir=${retained%/*}
cmp -s "$retained" "$base/expected-planner" || fail "retained planner changed"
cmp -s "$retained_dir/eci-command-plan.sha256" "$base/expected-receipt" || fail "retained receipt changed"
[ -n "$(find "$retained" -prune -perm 640 -print)" ] && [ -n "$(find "$retained_dir/eci-command-plan.sha256" -prune -perm 640 -print)" ] || fail "retained modes changed"
[ "$(cat "$bin/.planner-importer.txn.foreign/original")" = 'original backup' ] || fail "old transaction changed"
[ ! -e "$bin/.planner-importer.txn.foreign/eci-command-plan.conflict" ] && [ ! -e "$bin/.planner-importer.txn.second/eci-command-plan.conflict" ] || fail "markers remain"
[ -f "$retained_dir/.planner-importer.txn.foreign.retired" ] && [ -f "$retained_dir/.planner-importer.txn.second.retired" ] || fail "marker provenance not retained"
[ ! -s "$retained_dir/.planner-importer.txn.foreign.retired" ] && [ ! -s "$retained_dir/.planner-importer.txn.second.retired" ] || fail "retired markers changed"
grep -Fq "$retained_dir" "$base/out/preserve.err" || fail "retained location not reported"
source "$lib"
! eci_native_conflict_record eci-command-plan || fail "repair left an active conflict"
eci_runtime_verify eci-command-plan || fail "installed planner failed native verification: $ECI_RUNTIME_DENY_REASON"
pass "conflict repair preserves pair, modes, original backups and multiple markers"

# A partial retention leaves the marker and both retained and remaining leaves recoverable.
printf 'partial binary' >"$bin/eci-command-plan"
printf 'partial receipt' >"$bin/eci-command-plan.sha256"
: >"$bin/.planner-importer.txn.foreign/eci-command-plan.conflict"
cat >"$base/fakebin/mv" <<'EOF'
#!/bin/sh
case "$*" in *eci-command-plan.sha256*".planner-importer.txn."*) exit 1 ;; esac
exec /bin/mv "$@"
EOF
chmod 0755 "$base/fakebin/mv"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --preserve-conflict eci-command-plan >"$base/out/partial-retain.out" 2>"$base/out/partial-retain.err"; then
  fail "interrupted retention succeeded"
fi
[ -f "$bin/.planner-importer.txn.foreign/eci-command-plan.conflict" ] || fail "interrupted retention retired marker"
[ "$(cat "$bin/eci-command-plan.sha256")" = 'partial receipt' ] || fail "interrupted retention lost receipt"
grep -Fq 'partial binary' "$bin"/.planner-importer.txn.*/eci-command-plan || fail "interrupted retention lost binary"
rm "$base/fakebin/mv"
"$BASH" "$eci/hooks/build-native-binaries.sh" --preserve-conflict eci-command-plan >"$base/out/retry-retain.out" 2>"$base/out/retry-retain.err" || fail "partial retention retry failed"
grep -Fq 'partial receipt' "$bin"/.planner-importer.txn.*/eci-command-plan.sha256 || fail "retry lost receipt"
pass "interrupted retention retries with an absent binary"

# A failure retiring a later marker keeps it active for a subsequent repair.
printf 'marker retry binary' >"$bin/eci-command-plan"
printf 'marker retry receipt' >"$bin/eci-command-plan.sha256"
: >"$bin/.planner-importer.txn.foreign/eci-command-plan.conflict"
: >"$bin/.planner-importer.txn.second/eci-command-plan.conflict"
cat >"$base/fakebin/mv" <<'EOF'
#!/bin/sh
case "$*" in *".planner-importer.txn.second/eci-command-plan.conflict"*) exit 1 ;; esac
exec /bin/mv "$@"
EOF
chmod 0755 "$base/fakebin/mv"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --preserve-conflict eci-command-plan >"$base/out/partial-marker.out" 2>"$base/out/partial-marker.err"; then
  fail "interrupted marker retirement succeeded"
fi
[ -f "$bin/.planner-importer.txn.second/eci-command-plan.conflict" ] || fail "remaining marker was retired"
grep -Fq 'marker retry binary' "$bin"/.planner-importer.txn.*/eci-command-plan || fail "marker interruption lost binary"
grep -Fq 'marker retry receipt' "$bin"/.planner-importer.txn.*/eci-command-plan.sha256 || fail "marker interruption lost receipt"
rm "$base/fakebin/mv"
"$BASH" "$eci/hooks/build-native-binaries.sh" --preserve-conflict eci-command-plan >"$base/out/retry-marker.out" 2>"$base/out/retry-marker.err" || fail "marker retirement retry failed"
pass "partial marker retirement retains leaves and retries remaining marker"

# An unresolved leaf prevents moving either leaf or retiring its marker.
rm "$bin/eci-command-plan"
ln -s "$base/fixture" "$bin/eci-command-plan"
printf 'keep receipt' >"$bin/eci-command-plan.sha256"
: >"$bin/.planner-importer.txn.foreign/eci-command-plan.conflict"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --preserve-conflict eci-command-plan >"$base/out/conflict-link.out" 2>"$base/out/conflict-link.err"; then
  fail "conflict repair accepted symlink"
fi
[ -L "$bin/eci-command-plan" ] && [ "$(cat "$bin/eci-command-plan.sha256")" = 'keep receipt' ] && [ -f "$bin/.planner-importer.txn.foreign/eci-command-plan.conflict" ] || fail "refusal changed conflict state"
rm "$bin/eci-command-plan" "$bin/eci-command-plan.sha256"
"$BASH" "$eci/hooks/build-native-binaries.sh" --preserve-conflict eci-command-plan >"$base/out/absent-retain.out" 2>"$base/out/absent-retain.err" || fail "absent pair repair failed"
pass "conflict repair refuses symlink and accepts absent leaves"

# A retained crash transaction without an observed conflict remains recoverable.
rm "$bin/eci-command-plan.sha256"
[ -f "$bin/.planner-importer.txn.foreign/original" ] || fail "old transaction backup missing"
! eci_native_conflict_record eci-command-plan || fail "crash fixture has an active conflict"
: >"$build_log"
run_provision retained-crash
[ "$(rc_of retained-crash)" = 0 ] || fail "nonconflicting retained transaction prevented crash recovery"
[ "$(built_names | tr ' ' '\n' | grep -c '^eci-command-plan$')" -ge 1 ] || fail "crash recovery did not rebuild planner"
[ -f "$bin/eci-command-plan.sha256" ] || fail "crash recovery did not recreate receipt"
[ "$(cat "$bin/.planner-importer.txn.foreign/original")" = 'original backup' ] || fail "crash recovery changed retained backup"
pass "retained transaction without a conflict record permits ordinary crash recovery"

# Fixed gate repair changes only damaged managed pairs and retains their contents.
: >"$build_log"
"$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-healthy.out" 2>"$base/out/gate-healthy.err" || fail "healthy gate repair failed"
[ "$(builds)" = 0 ] || fail "healthy gate repair built binaries"
printf 'damaged planner' >>"$bin/eci-command-plan"
cp -p "$bin/eci-command-plan" "$base/damaged-planner"
cp -p "$bin/eci-command-plan.sha256" "$base/damaged-receipt"
: >"$build_log"
"$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-damaged.out" 2>"$base/out/gate-damaged.err" || fail "damaged planner repair failed"
[ "$(built_names)" = eci-command-plan ] || fail "gate repair rebuilt unrelated binaries"
eci_runtime_verify eci-command-plan || fail "gate repair did not verify"
found=0
for retained in "$bin"/.planner-importer.txn.*/eci-command-plan; do
  if cmp -s "$retained" "$base/damaged-planner"; then
    cmp -s "${retained%/*}/eci-command-plan.sha256" "$base/damaged-receipt" || fail "gate repair lost damaged receipt"
    found=1
  fi
done
[ "$found" = 1 ] || fail "gate repair did not retain damaged planner"
: >"$bin/eci-command-plan.sha256"
chmod 0664 "$bin/eci-command-plan.sha256"
before_receipt=$(digest_of "$bin/eci-command-plan.sha256")
: >"$build_log"
if PATH=/usr/bin:/bin "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-writable-receipt.out" 2>"$base/out/gate-writable-receipt.err"; then fail "group-writable receipt accepted"; fi
grep -Fq 'unsafe or unsupported gate failure' "$base/out/gate-writable-receipt.err" || fail "unsafe failure masked by Go prerequisite"
[ "$(builds)" = 0 ] && [ "$(digest_of "$bin/eci-command-plan.sha256")" = "$before_receipt" ] && [ -n "$(find "$bin/eci-command-plan.sha256" -prune -perm 664 -print)" ] || fail "unsafe receipt changed"
chmod 0644 "$bin/eci-command-plan.sha256"
export FAKE_GO_FAIL=1
if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-go-fail.out" 2>"$base/out/gate-go-fail.err"; then fail "failed Go build succeeded"; fi
unset FAKE_GO_FAIL
grep -Fq 'build-native-binaries.sh" --gate-repair' "$base/out/gate-go-fail.err" || fail "build failure omitted fixed retry mode"
: >"$bin/eci-command-plan.sha256"
: >"$build_log"
"$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-malformed.out" 2>"$base/out/gate-malformed.err" || fail "malformed receipt repair failed"
[ "$(built_names)" = eci-command-plan ] && eci_runtime_verify eci-command-plan || fail "malformed receipt not repaired"
rm "$bin/eci-command-plan"
: >"$build_log"
"$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-missing.out" 2>"$base/out/gate-missing.err" || fail "missing planner repair failed"
[ "$(built_names)" = eci-command-plan ] && eci_runtime_verify eci-command-plan || fail "missing planner not repaired"
printf 'interrupted repair' >>"$bin/eci-command-plan"
: >"$bin/.planner-importer.txn.foreign/eci-command-plan.conflict"
cat >"$base/fakebin/mv" <<'EOF'
#!/bin/sh
case "$*" in *eci-command-plan.sha256*".planner-importer.txn."*) exit 1 ;; esac
exec /bin/mv "$@"
EOF
chmod 0755 "$base/fakebin/mv"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-interrupted.out" 2>"$base/out/gate-interrupted.err"; then fail "interrupted gate repair succeeded"; fi
[ -f "$bin/.planner-importer.txn.foreign/eci-command-plan.conflict" ] || fail "interrupted gate repair retired marker"
rm "$base/fakebin/mv"
"$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-retry.out" 2>"$base/out/gate-retry.err" || fail "interrupted gate repair retry failed"
eci_runtime_verify eci-command-plan || fail "gate did not verify after retry"
previous_stages="$(find "$bin" -maxdepth 1 -name '.build.*' -print | LC_ALL=C sort)"
printf 'publication cleanup' >>"$bin/eci-command-plan"
cat >"$base/fakebin/cp" <<'EOF'
#!/bin/sh
case "$*" in *".eci-gate-repair."*".build."*) exit 1 ;; esac
exec /bin/cp "$@"
EOF
chmod 0755 "$base/fakebin/cp"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-copy-fail.out" 2>"$base/out/gate-copy-fail.err"; then fail "injected publication failure succeeded"; fi
[ "$(find "$bin" -maxdepth 1 -name '.build.*' -print | LC_ALL=C sort)" = "$previous_stages" ] || fail "publication stage was not cleaned"
rm "$base/fakebin/cp"
"$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-copy-retry.out" 2>"$base/out/gate-copy-retry.err" || fail "publication failure retry failed"
chmod 0775 "$bin/eci-command-plan"
: >"$build_log"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-unsafe.out" 2>"$base/out/gate-unsafe.err"; then fail "unsafe mode accepted"; fi
[ "$(builds)" = 0 ] && [ -n "$(find "$bin/eci-command-plan" -prune -perm 775 -print)" ] || fail "unsafe planner changed"
chmod 0755 "$bin/eci-command-plan"
chmod 0644 "$bin/eci-command-plan"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-noexec.out" 2>"$base/out/gate-noexec.err"; then fail "nonexecutable planner accepted"; fi
[ "$(builds)" = 0 ] || fail "nonexecutable planner was rebuilt"
chmod 0755 "$bin/eci-command-plan"
mv "$bin/eci-command-plan" "$base/planner-link-target"
ln -s "$base/planner-link-target" "$bin/eci-command-plan"
if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-link.out" 2>"$base/out/gate-link.err"; then fail "symlink planner accepted"; fi
[ -L "$bin/eci-command-plan" ] && [ "$(builds)" = 0 ] || fail "symlink planner changed"
rm "$bin/eci-command-plan"
mv "$base/planner-link-target" "$bin/eci-command-plan"
if [ "$host_os" = Linux ]; then
  rm "$bin/eci-verexec"
  : >"$build_log"
  chmod 0775 "$bin"
  if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-chain.out" 2>"$base/out/gate-chain.err"; then fail "untrusted chain accepted"; fi
  [ ! -e "$bin/eci-verexec" ] || fail "untrusted chain received verifier"
  chmod 0700 "$bin"
  cat >"$base/fakebin/chmod" <<'EOF'
#!/bin/sh
case "$*" in *".eci-gate-repair."*"/eci-verexec"*) exec /bin/chmod 0644 "$3" ;; esac
exec /bin/chmod "$@"
EOF
  chmod 0755 "$base/fakebin/chmod"
  if "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-stage-mode.out" 2>"$base/out/gate-stage-mode.err"; then fail "unexecutable staged verifier accepted"; fi
  grep -Fq 'choose an executable TMPDIR' "$base/out/gate-stage-mode.err" || fail "staged verifier failure did not name TMPDIR"
  [ ! -e "$bin/eci-verexec" ] || fail "failed staged verifier installed"
  rm "$base/fakebin/chmod"
  if [ -n "${ECI_TEST_NOEXEC_TMPDIR:-}" ]; then
    if TMPDIR="$ECI_TEST_NOEXEC_TMPDIR" "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-stage-exec.out" 2>"$base/out/gate-stage-exec.err"; then fail "unexecutable staged verifier accepted"; fi
    grep -Fq 'choose an executable TMPDIR' "$base/out/gate-stage-exec.err" || fail "staged execution failure did not name TMPDIR"
    [ ! -e "$bin/eci-verexec" ] || fail "failed staged verifier installed"
  else
    skip "staged execution failure requires ECI_TEST_NOEXEC_TMPDIR"
  fi
  : >"$build_log"
  "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-verifier.out" 2>"$base/out/gate-verifier.err" || fail "missing verifier repair failed"
  [ "$(built_names)" = eci-verexec ] || fail "missing verifier rebuilt unrelated binary"
  eci_runtime_verify eci-command-plan || fail "gate did not verify after verifier repair"
  printf 'verifier damaged' >>"$bin/eci-verexec"
  printf 'planner damaged' >>"$bin/eci-command-plan"
  cp -p "$bin/eci-verexec" "$base/sequential-verifier"
  cp -p "$bin/eci-verexec.sha256" "$base/sequential-verifier.sha256"
  cp -p "$bin/eci-command-plan" "$base/sequential-planner"
  cp -p "$bin/eci-command-plan.sha256" "$base/sequential-planner.sha256"
  : >"$build_log"
  "$BASH" "$eci/hooks/build-native-binaries.sh" --gate-repair >"$base/out/gate-sequential.out" 2>"$base/out/gate-sequential.err" || fail "sequential repair failed"
  [ "$(cat "$build_log")" = "$(printf 'eci-verexec\neci-command-plan')" ] || fail "wrong sequential build order"
  eci_runtime_verify eci-command-plan || fail "sequential repair not verified"
  for name in eci-verexec eci-command-plan; do
    case "$name" in eci-verexec) expected="$base/sequential-verifier" ;; *) expected="$base/sequential-planner" ;; esac
    found=0
    for retained in "$bin"/.planner-importer.txn.*/"$name"; do
      if cmp -s "$retained" "$expected"; then
        cmp -s "${retained%/*}/$name.sha256" "$expected.sha256" || fail "sequential repair lost $name receipt"
        found=1
      fi
    done
    [ "$found" = 1 ] || fail "sequential repair lost $name binary"
  done
fi
pass "gate repair retains damaged pairs, skips healthy binaries and refuses unsafe modes"

chmod 0700 "$bin"
mv "$bin" "$base/physical-bin"
ln -s "$base/physical-bin" "$bin"
printf s >>"$bin/eci-command-plan"
"$BASH" "$eci/hooks/build-native-binaries.sh" eci-command-plan >"$base/out/symlink-bin.out" 2>"$base/out/symlink-bin.err" || fail "resolved bin deployment failed"
[ -L "$bin" ] && [ -n "$(find "$base/physical-bin" -prune -perm 700 -print)" ] || fail "resolved bin link or mode changed"
[ "$(digest_of "$bin/eci-command-plan")" = "$(digest_of "$base/fixture")" ] || fail "resolved bin did not receive the repaired binary"
pass "resolved bin deployment preserves its link and existing directory mode"

printf 'eci-native-provision: %s checks passed (host %s, bash %s)\n' "$checks" "$host_os" "$BASH_VERSION"
