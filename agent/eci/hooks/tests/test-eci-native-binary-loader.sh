#!/usr/bin/env bash
# Loader test for hooks/lib/eci-native-binary.sh: the verified descriptor path (Linux),
# the verified copy path (macOS), the Mach-O header test, the is_native check, the
# darwin receipt check and the receipt-case table. Linux runs every section. macOS runs
# the copy path and the portable sections. Sections that need the Linux descriptor path
# (named descriptors, bash 4.1+) are skipped with a message under older shells.
# Usage: test-eci-native-binary-loader.sh [path/to/eci-native-binary.sh]
set -euo pipefail
# Fixture directories must be private: a group-writable ancestor is denied by the loader, and
# the caller's umask would otherwise make the fixture chain untrusted.
umask 077

tests_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
loader="${1:-$tests_dir/../lib/eci-native-binary.sh}"
host_os="$(uname -s)"
case "$host_os" in
  Linux | Darwin) ;;
  *) printf 'FAIL: unsupported host OS %s\n' "$host_os" >&2; exit 1 ;;
esac

# Parse the loader in a separate shell first: a syntax error must fail the test, not pass it.
if ! "$BASH" -n "$loader"; then
  printf 'FAIL: %s does not parse under bash %s\n' "$loader" "$BASH_VERSION" >&2
  exit 1
fi

checks=0
pass() { checks=$((checks + 1)); printf 'PASS: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
skip() { printf 'SKIP: %s\n' "$*"; }

# Resolve the real Go caches before HOME moves into the fixture.
GOCACHE="$(go env GOCACHE)"; export GOCACHE
GOMODCACHE="$(go env GOMODCACHE)"; export GOMODCACHE
GOPATH="$(go env GOPATH)"; export GOPATH
host_goarch="$(go env GOARCH)"

if [ "$host_os" = Linux ]; then
  base_parent="${XDG_RUNTIME_DIR:?XDG_RUNTIME_DIR must be set: the fixture base must be private}"
else
  base_parent="${TMPDIR:-/tmp}"
fi
base="$(mktemp -d -- "${base_parent%/}/eci-native-binary-loader.XXXXXX")"
base="$(cd -P -- "$base" && pwd -P)"
trap 'chmod -R u+rwx -- "$base" 2>/dev/null || true; rm -rf -- "$base"' EXIT

home="$base/home"
bin="$home/.omp/agent/eci/bin"
fixtures="$base/fixtures"
scratch="$base/scratch"
mkdir -p -- "$bin" "$fixtures" "$base/src" "$base/tmp" "$scratch"
export HOME="$home" TMPDIR="$base/tmp"

linux_descriptor_supported() {  # the Linux path uses named descriptors (bash 4.1+)
  [ "${BASH_VERSINFO[0]}" -gt 4 ] || { [ "${BASH_VERSINFO[0]}" -eq 4 ] && [ "${BASH_VERSINFO[1]}" -ge 1 ]; }
}
full_loader=0
if [ "$host_os" = Darwin ] || linux_descriptor_supported; then full_loader=1; fi

if ! . "$loader" || ! declare -F eci_runtime_open_verified >/dev/null 2>&1; then
  fail "$loader does not define eci_runtime_open_verified under bash $BASH_VERSION"
fi

# The fixture chain must be trusted before any denial below can be read as meaningful.
real_bin="$(cd -P -- "$bin" && pwd -P)"
if [ "$host_os" = Linux ]; then
  chain_fn=eci_runtime_directory_chain_is_trusted
else
  chain_fn=eci_runtime_darwin_directory_chain_is_trusted
fi
"$chain_fn" "$real_bin" || fail "fixture chain $real_bin is not trusted; denials below would be for the wrong reason"
pass "fixture chain trusted ($chain_fn)"

printf 'module eciloaderfixture\n\ngo 1.21\n' >"$base/src/go.mod"
cat >"$base/src/main.go" <<'EOF'
package main

import "os"

func main() {
	os.Stdout.WriteString("eci-native-binary-loader fixture ok\n")
}
EOF
(cd -- "$base/src" && env CGO_ENABLED=0 GOTOOLCHAIN=local go build -buildvcs=false -o "$fixtures/native" .)
(cd -- "$base/src" && env GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 GOTOOLCHAIN=local go build -buildvcs=false -o "$fixtures/macho-arm64" .)
(cd -- "$base/src" && env GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local go build -buildvcs=false -o "$fixtures/macho-amd64" .)
chmod 0755 -- "$fixtures/native" "$fixtures/macho-arm64" "$fixtures/macho-amd64"
if [ "$host_goarch" = arm64 ]; then other_arch_macho="$fixtures/macho-amd64"; else other_arch_macho="$fixtures/macho-arm64"; fi

if [ "$host_os" = Linux ]; then
  format_reason='is not an ELF binary for this host'
else
  format_reason='is not a Mach-O executable for this host'
fi

digest_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -- "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 -- "$1" | cut -d ' ' -f 1
  fi
}
write_receipt() {  # write_receipt <binary> <receipt name>
  printf '%s  %s\n' "$(digest_of "$1")" "$2" >"$1.sha256"
}
install_binary() {  # install_binary <source> installs eci-hook-helper with a matching receipt
  rm -f -- "$bin/eci-hook-helper" "$bin/eci-hook-helper.sha256"
  cp -- "$1" "$bin/eci-hook-helper"
  chmod 0755 -- "$bin/eci-hook-helper"
  write_receipt "$bin/eci-hook-helper" eci-hook-helper
}
reset_bin() {
  rm -rf -- "$bin"
  mkdir -p -- "$bin"
}
byte_at() { od -An -tx1 -j "$2" -N1 -- "$1" | tr -d ' \n'; }
patch_bytes() { printf "$3" | dd of="$1" bs=1 seek="$2" conv=notrunc 2>/dev/null; }
deny_text() {  # deny_text <name> <reason>: the exact message the loader builds
  printf 'ECI native gate binary %s is unavailable or unverified (%s); provision it with "$HOME/.omp/agent/eci/hooks/build-native-binaries.sh" and retry' "$1" "$2"
}

expect_accept() {  # expect_accept <label>: open, exec through the verified handle, close
  local label="$1" exec_path copy_dir out rc=0
  if ! eci_runtime_open_verified eci-hook-helper; then
    fail "$label: open denied: $ECI_RUNTIME_DENY_REASON"
  fi
  exec_path="$ECI_RUNTIME_BINARY_EXEC"
  copy_dir="$eci_runtime_copy_dir"
  if [ "$host_os" = Linux ]; then
    case "$exec_path" in /proc/self/fd/*) ;; *) fail "$label: Linux exec path is not a descriptor: $exec_path" ;; esac
  fi
  [ -e "$exec_path" ] || fail "$label: verified handle is absent before exec: $exec_path"
  out="$base/accept.out"
  "$exec_path" --check >"$out" 2>&1 || rc=$?
  [ "$rc" -eq 0 ] || fail "$label: verified exec returned $rc"
  [ "$(cat -- "$out")" = 'eci-native-binary-loader fixture ok' ] || fail "$label: verified exec ran something else"
  eci_runtime_close_verified
  eci_runtime_close_verified
  [ ! -e "$exec_path" ] || fail "$label: handle or copy survives close: $exec_path"
  if [ -n "$copy_dir" ] && [ -e "$copy_dir" ]; then fail "$label: copy directory survives close: $copy_dir"; fi
  [ -z "$ECI_RUNTIME_BINARY_FD" ] && [ -z "$ECI_RUNTIME_BINARY_EXEC" ] && [ -z "$eci_runtime_copy_dir" ] ||
    fail "$label: close left state set"
  pass "accept $label: exec rc 0; handle and copy removed by close"
}

expect_deny() {  # expect_deny <label> <reason>: the open must deny with exactly this reason
  local label="$1" reason="$2" want
  ECI_RUNTIME_DENY_REASON=''
  if eci_runtime_open_verified eci-hook-helper; then
    fail "$label: accepted, expected deny ($reason)"
  fi
  want="$(deny_text eci-hook-helper "$reason")"
  [ "$ECI_RUNTIME_DENY_REASON" = "$want" ] || fail "$label: deny text was [$ECI_RUNTIME_DENY_REASON], expected [$want]"
  [ -z "$ECI_RUNTIME_BINARY_FD" ] && [ -z "$ECI_RUNTIME_BINARY_EXEC" ] && [ -z "$eci_runtime_copy_dir" ] ||
    fail "$label: denied open left state set"
  pass "deny $label: $reason"
}

if [ "$full_loader" = 1 ]; then
  pass "full loader sections run (host $host_os, bash $BASH_VERSION)"
  reset_bin; install_binary "$fixtures/native"
  expect_accept native-binary

  reset_bin; install_binary "$fixtures/native"; chmod 0775 -- "$bin/eci-hook-helper"
  expect_deny group-writable-binary 'is group- or other-writable'
  reset_bin; install_binary "$fixtures/native"; chmod 0757 -- "$bin/eci-hook-helper"
  expect_deny other-writable-binary 'is group- or other-writable'
  reset_bin; install_binary "$fixtures/native"; chmod 0770 -- "$home/.omp/agent/eci"
  expect_deny group-writable-ancestor 'bin directory or an ancestor is not owned by the current user or root, or is group/other-writable'
  chmod 0755 -- "$home/.omp/agent/eci"
  reset_bin; install_binary "$fixtures/native"; chmod 0644 -- "$bin/eci-hook-helper"
  expect_deny non-executable 'is not executable'
  reset_bin; install_binary "$fixtures/native"
  printf '%064d  eci-hook-helper\n' 0 >"$bin/eci-hook-helper.sha256"
  expect_deny receipt-hash-altered 'sha256 does not match its receipt'
  reset_bin; install_binary "$fixtures/native"; printf 'tamper' >>"$bin/eci-hook-helper"
  expect_deny binary-tampered-receipt-unchanged 'sha256 does not match its receipt'
  reset_bin; install_binary "$fixtures/native"; rm -f -- "$bin/eci-hook-helper.sha256"
  expect_deny missing-receipt 'has no sha256 receipt'
  reset_bin; install_binary "$fixtures/native"; printf 'not a receipt\n' >"$bin/eci-hook-helper.sha256"
  expect_deny malformed-receipt 'has a malformed sha256 receipt'
  reset_bin; ln -s -- "$fixtures/native" "$bin/eci-hook-helper"
  write_receipt "$fixtures/native" eci-hook-helper; cp -- "$fixtures/native.sha256" "$bin/eci-hook-helper.sha256"
  expect_deny symlink-binary 'is missing or a symlink'
  reset_bin
  expect_deny missing-binary 'is missing or a symlink'
  reset_bin; printf '#!/bin/sh\nexit 0\n' >"$bin/eci-hook-helper"; chmod 0755 -- "$bin/eci-hook-helper"
  write_receipt "$bin/eci-hook-helper" eci-hook-helper
  expect_deny script "$format_reason"
  reset_bin; { printf '\312\376\272\276'; tail -c +5 -- "$fixtures/native"; } >"$bin/eci-hook-helper"
  chmod 0755 -- "$bin/eci-hook-helper"; write_receipt "$bin/eci-hook-helper" eci-hook-helper
  expect_deny fat-magic "$format_reason"
  if [ "$host_os" = Linux ]; then
    reset_bin; cp -- "$fixtures/native" "$bin/eci-hook-helper"
    if [ "$(byte_at "$bin/eci-hook-helper" 18)" = 3e ]; then
      patch_bytes "$bin/eci-hook-helper" 18 '\267\000'
    else
      patch_bytes "$bin/eci-hook-helper" 18 '\076\000'
    fi
    chmod 0755 -- "$bin/eci-hook-helper"; write_receipt "$bin/eci-hook-helper" eci-hook-helper
    expect_deny elf-machine-patched-with-receipt "$format_reason"
  else
    reset_bin; cp -- "$fixtures/native" "$bin/eci-hook-helper"
    if [ "$(byte_at "$bin/eci-hook-helper" 4)" = 0c ]; then
      patch_bytes "$bin/eci-hook-helper" 4 '\007\000\000\001'
    else
      patch_bytes "$bin/eci-hook-helper" 4 '\014\000\000\001'
    fi
    chmod 0755 -- "$bin/eci-hook-helper"; write_receipt "$bin/eci-hook-helper" eci-hook-helper
    expect_deny macho-cputype-patched-with-receipt "$format_reason"
  fi
else
  skip "open-path sections need the Linux descriptor path (bash 4.1+); this shell is bash $BASH_VERSION"
fi

if [ "$host_os" = Linux ] && [ "$full_loader" = 1 ]; then
  # Wrong owner: a root-owned binary is bind-mounted over the fixture in a user namespace chroot.
  foreign=/usr/bin/gnutrue
  if ! unshare -rm true >/dev/null 2>&1; then
    skip "wrong-owner chroot: unshare -rm is unavailable here"
  elif [ ! -f "$foreign" ]; then
    skip "wrong-owner chroot: $foreign is missing"
  else
    root="$base/chroot"
    rm -rf -- "$root"
    mkdir -p -- "$root/usr" "$root/proc" "$root/dev" "$root/etc" "$root/tmp" "$root/opt/eci" \
      "$root/fixture-home/.omp/agent/eci/bin"
    ln -s usr/bin "$root/bin"
    ln -s usr/lib "$root/lib"
    if [ -d /usr/lib64 ]; then ln -s usr/lib64 "$root/lib64"; fi
    cp -- "$loader" "$root/opt/eci/eci-native-binary.sh"
    cat >"$root/opt/eci/driver.sh" <<'EOF'
#!/bin/bash
export HOME=/fixture-home TMPDIR=/tmp PATH=/usr/bin:/bin LC_ALL=C
. /opt/eci/eci-native-binary.sh
if eci_runtime_open_verified eci-hook-helper; then
  rc=0
  "$ECI_RUNTIME_BINARY_EXEC" --check >/tmp/out 2>&1 || rc=$?
  eci_runtime_close_verified
  printf 'ACCEPT exec_rc=%s %s\n' "$rc" "$(cat /tmp/out)"
else
  printf 'DENY %s\n' "$ECI_RUNTIME_DENY_REASON"
fi
EOF
    chmod 0755 -- "$root/opt/eci/driver.sh"
    run_chroot() {  # run_chroot <mode: control|foreign>
      unshare -rm sh -c '
        set -e
        root="$1"; mode="$2"; foreign_src="$3"
        mount --make-rprivate /
        mount --rbind /usr "$root/usr"
        mount --rbind /proc "$root/proc"
        mount --rbind /dev "$root/dev"
        mount --rbind /etc "$root/etc"
        if [ "$mode" = foreign ]; then
          mount --bind "$foreign_src" "$root/fixture-home/.omp/agent/eci/bin/eci-hook-helper"
        fi
        chroot "$root" /bin/bash /opt/eci/driver.sh
      ' sh "$root" "$1" "$foreign" 2>&1 || true
    }
    cp -- "$fixtures/native" "$root/fixture-home/.omp/agent/eci/bin/eci-hook-helper"
    chmod 0755 -- "$root/fixture-home/.omp/agent/eci/bin/eci-hook-helper"
    write_receipt "$root/fixture-home/.omp/agent/eci/bin/eci-hook-helper" eci-hook-helper
    control_out="$(run_chroot control)"
    case "$control_out" in
      ACCEPT\ exec_rc=0*) ;;
      *) skip "wrong-owner chroot: control did not run the loader in the chroot: $control_out" ;;
    esac
    if [ "${control_out%%$'\n'*}" != "" ] && [ "$control_out" != "" ] && [ "${control_out#ACCEPT}" != "$control_out" ]; then
      pass "chroot control accepts an own-owned copy and runs it (exec rc 0)"
      rm -f -- "$root/fixture-home/.omp/agent/eci/bin/eci-hook-helper"
      : >"$root/fixture-home/.omp/agent/eci/bin/eci-hook-helper"
      printf '%s  eci-hook-helper\n' "$(digest_of "$foreign")" >"$root/fixture-home/.omp/agent/eci/bin/eci-hook-helper.sha256"
      foreign_out="$(run_chroot foreign)"
      want_line="DENY $(deny_text eci-hook-helper 'is not owned by the current user')"
      [ "$foreign_out" = "$want_line" ] || fail "wrong-owner chroot: got [$foreign_out], expected [$want_line]"
      pass "deny wrong-owner in chroot (root-owned binary bind-mounted): is not owned by the current user"
    fi
  fi
fi

# is_native checks: the Linux ELF check on Linux, the Mach-O check on macOS, and the
# Mach-O check forced onto Linux in a subshell (the host CPU type is supplied by the test).
nat="$base/is-native"
mkdir -p -- "$nat"
expect_native() {  # expect_native <label> <0|1> <path>
  local label="$1" want="$2" path="$3" rc=0
  if eci_runtime_binary_is_native "$path"; then rc=0; else rc=1; fi
  [ "$rc" -eq "$want" ] || fail "is_native $label returned $rc, expected $want"
  pass "is_native $label: $want"
}
forced_native() {  # forced_native <label> <host cputype> <0|1> <path>
  local label="$1" forced_cpu="$2" want="$3" path="$4" rc=0
  ( eci_runtime_platform=Darwin
    eci_runtime_darwin_host_cputype() { printf '%s\n' "$forced_cpu"; }
    eci_runtime_binary_is_native "$path" ) || rc=$?
  if [ "$rc" -eq 0 ]; then rc=0; else rc=1; fi
  [ "$rc" -eq "$want" ] || fail "forced darwin is_native $label returned $rc, expected $want"
  pass "is_native forced darwin $label: $want"
}
cp -- "$fixtures/native" "$nat/native"; chmod 0755 -- "$nat/native"
printf '#!/bin/sh\nexit 0\n' >"$nat/script"; chmod 0755 -- "$nat/script"
ln -s -- "$nat/native" "$nat/link"
cp -- "$fixtures/native" "$nat/nonexec"; chmod 0644 -- "$nat/nonexec"
if [ "$host_os" = Linux ]; then
  expect_native linux-elf-native 0 "$nat/native"
  cp -- "$fixtures/native" "$nat/elf-machine"
  if [ "$(byte_at "$nat/elf-machine" 18)" = 3e ]; then
    patch_bytes "$nat/elf-machine" 18 '\267\000'
  else
    patch_bytes "$nat/elf-machine" 18 '\076\000'
  fi
  chmod 0755 -- "$nat/elf-machine"
  expect_native linux-elf-machine-patched 1 "$nat/elf-machine"
else
  expect_native darwin-macho-native 0 "$nat/native"
  cp -- "$other_arch_macho" "$nat/other-arch"; chmod 0755 -- "$nat/other-arch"
  expect_native darwin-other-arch 1 "$nat/other-arch"
fi
expect_native script 1 "$nat/script"
expect_native symlink 1 "$nat/link"
expect_native non-executable 1 "$nat/nonexec"
expect_native missing 1 "$nat/missing"

if [ "$host_goarch" = arm64 ]; then host_cpu='0c 00 00 01'; else host_cpu='07 00 00 01'; fi
cp -- "$fixtures/macho-arm64" "$nat/forced-arm64"; chmod 0755 -- "$nat/forced-arm64"
cp -- "$fixtures/macho-amd64" "$nat/forced-amd64"; chmod 0755 -- "$nat/forced-amd64"
forced_native forced-arm64-host-arm64 '0c 00 00 01' 0 "$nat/forced-arm64"
forced_native forced-amd64-host-arm64 '0c 00 00 01' 1 "$nat/forced-amd64"
forced_native forced-amd64-host-amd64 '07 00 00 01' 0 "$nat/forced-amd64"
forced_native forced-arm64-host-amd64 '07 00 00 01' 1 "$nat/forced-arm64"
forced_native forced-script 0c' 00 00 01' 1 "$nat/script"
forced_native forced-symlink '0c 00 00 01' 1 "$nat/link"
forced_native forced-nonexec '0c 00 00 01' 1 "$nat/nonexec"
forced_native forced-missing '0c 00 00 01' 1 "$nat/missing"
cp -- "$fixtures/macho-arm64" "$nat/fat"; { printf '\312\376\272\276'; tail -c +5 -- "$fixtures/macho-arm64"; } >"$nat/fat"
chmod 0755 -- "$nat/fat"
forced_native forced-fat '0c 00 00 01' 1 "$nat/fat"

# Mach-O header test: portable (no descriptor, no BSD-only stat), so it runs under bash 3.2.
header_expect() {  # header_expect <label> <file> <cputype> <0|1>
  local label="$1" file="$2" cpu="$3" want="$4"
  ECI_RUNTIME_DENY_REASON=''
  if eci_runtime_macho_header_check eci-hook-helper "$file" "$cpu"; then
    [ "$want" -eq 0 ] || fail "Mach-O header $label accepted, expected deny"
    pass "Mach-O header $label: accept"
  else
    [ "$want" -eq 1 ] || fail "Mach-O header $label denied: $ECI_RUNTIME_DENY_REASON"
    [ "$ECI_RUNTIME_DENY_REASON" = "$(deny_text eci-hook-helper 'is not a Mach-O executable for this host')" ] ||
      fail "Mach-O header $label: wrong deny text [$ECI_RUNTIME_DENY_REASON]"
    pass "Mach-O header $label: is not a Mach-O executable for this host"
  fi
}
hdr="$base/header"
mkdir -p -- "$hdr"
header_expect arm64-thin-native "$fixtures/macho-arm64" '0c 00 00 01' 0
header_expect amd64-thin-native "$fixtures/macho-amd64" '07 00 00 01' 0
header_expect arm64-file-as-amd64-host "$fixtures/macho-arm64" '07 00 00 01' 1
header_expect amd64-file-as-arm64-host "$fixtures/macho-amd64" '0c 00 00 01' 1
cp -- "$fixtures/macho-arm64" "$hdr/cputype-patched"; patch_bytes "$hdr/cputype-patched" 4 '\007\000\000\001'
header_expect cputype-patched-arm64-host "$hdr/cputype-patched" '0c 00 00 01' 1
{ printf '\312\376\272\276'; tail -c +5 -- "$fixtures/macho-arm64"; } >"$hdr/fat"
header_expect fat-magic "$hdr/fat" '0c 00 00 01' 1
cp -- "$fixtures/macho-arm64" "$hdr/dylib-filetype"; patch_bytes "$hdr/dylib-filetype" 12 '\006'
header_expect filetype-patched-mh-dylib "$hdr/dylib-filetype" '0c 00 00 01' 1
head -c 8 -- "$fixtures/macho-arm64" >"$hdr/truncated"
header_expect truncated-header "$hdr/truncated" '0c 00 00 01' 1
printf '#!/bin/sh\nexit 0\n' >"$hdr/script"
header_expect script "$hdr/script" '0c 00 00 01' 1

# Host CPU type: the sysctl probe wins on Apple silicon, else uname -m decides. The Linux
# run overrides uname only inside a subshell; sysctl reports no hw.optional.arm64 there.
cpu_for_machine() {  # cpu_for_machine <uname -m value> <expected cputype, or DENY>
  # The override reads uname_value, a name the loader never uses. A local named machine in the
  # loader would shadow it, because bash resolves variables dynamically through the call stack.
  local uname_value="$1" want="$2" got='' rc=0
  got="$( ( uname() { printf '%s\n' "$uname_value"; }; eci_runtime_darwin_host_cputype ) )" || rc=$?
  if [ "$want" = DENY ]; then
    [ "$rc" -ne 0 ] || fail "host cputype for uname $uname_value accepted, expected deny"
    pass "host CPU type for uname -m $uname_value: unsupported"
  else
    [ "$rc" -eq 0 ] && [ "$got" = "$want" ] || fail "host cputype for uname $uname_value was [$got] rc $rc, expected $want"
    pass "host CPU type for uname -m $uname_value: $want"
  fi
}
if [ "$host_os" = Linux ]; then
  cpu_for_machine arm64 '0c 00 00 01'
  cpu_for_machine x86_64 '07 00 00 01'
  cpu_for_machine riscv64 DENY
else
  real_cpu="$(eci_runtime_darwin_host_cputype)" || fail "host CPU type probe failed on macOS"
  if [ "$(/usr/sbin/sysctl -n hw.optional.arm64 2>/dev/null || true)" = 1 ]; then
    [ "$real_cpu" = '0c 00 00 01' ] || fail "sysctl reports arm64 but host CPU type is [$real_cpu]"
  elif [ "$host_goarch" = arm64 ]; then
    [ "$real_cpu" = '0c 00 00 01' ] || fail "arm64 host CPU type is [$real_cpu]"
  else
    [ "$real_cpu" = '07 00 00 01' ] || fail "x86_64 host CPU type is [$real_cpu]"
  fi
  pass "host CPU type on macOS: $real_cpu"
fi

# Darwin receipt check: shasum is the macOS hasher; this runs wherever shasum exists.
if command -v shasum >/dev/null 2>&1; then
  dr="$base/darwin-receipt"
  mkdir -p -- "$dr"
  cp -- "$fixtures/native" "$dr/eci-hook-helper"; chmod 0755 -- "$dr/eci-hook-helper"
  dr_copy="$dr/eci-hook-helper"
  dr_receipt="$dr/eci-hook-helper.sha256"
  dr_digest="$(shasum -a 256 -- "$dr_copy" | cut -d ' ' -f 1)"
  receipt_expect() {  # receipt_expect <label> <name> <0|1> [reason]
    local label="$1" name="$2" want="$3" reason="${4:-}" rc=0
    ECI_RUNTIME_DENY_REASON=''
    _eci_runtime_darwin_receipt_verified "$name" "$dr_receipt" "$dr_copy" "$scratch" || rc=$?
    if [ "$want" -eq 0 ]; then
      [ "$rc" -eq 0 ] || fail "darwin receipt $label denied: $ECI_RUNTIME_DENY_REASON"
      pass "darwin receipt $label: accept"
    else
      [ "$rc" -ne 0 ] || fail "darwin receipt $label accepted, expected deny"
      [ "$ECI_RUNTIME_DENY_REASON" = "$(deny_text "$name" "$reason")" ] ||
        fail "darwin receipt $label: deny text [$ECI_RUNTIME_DENY_REASON]"
      pass "darwin receipt $label: $reason"
    fi
  }
  printf '%s  eci-hook-helper\n' "$dr_digest" >"$dr_receipt"
  receipt_expect valid eci-hook-helper 0
  printf '%064d  eci-hook-helper\n' 0 >"$dr_receipt"
  receipt_expect wrong-hash eci-hook-helper 1 'sha256 does not match its receipt'
  printf 'not a receipt\n' >"$dr_receipt"
  receipt_expect malformed eci-hook-helper 1 'has a malformed sha256 receipt'
  printf '%s  eci-hook-helper\n' "$dr_digest" >"$dr_receipt"
  receipt_expect name-metachar eci.hook 1 'is not an allowed artifact name'
  rm -f -- "$dr_receipt"
  receipt_expect missing-receipt eci-hook-helper 1 'has no sha256 receipt'
else
  skip "darwin receipt check: shasum is not installed here"
fi

# Denials decided before any binary is touched: unsupported OS and an unsupported CPU type.
bad_os_reason="$( ( eci_runtime_platform=FreeBSD; eci_runtime_open_verified eci-hook-helper >/dev/null 2>&1 || true
  printf '%s' "$ECI_RUNTIME_DENY_REASON" ) )"
[ "$bad_os_reason" = "$(deny_text eci-hook-helper 'host OS FreeBSD is not supported')" ] ||
  fail "unsupported host OS: got [$bad_os_reason]"
pass "deny unsupported host OS (forced FreeBSD): host OS FreeBSD is not supported"
bad_cpu_reason="$( ( eci_runtime_platform=Darwin; eci_runtime_darwin_host_cputype() { return 1; }
  eci_runtime_open_verified eci-hook-helper >/dev/null 2>&1 || true; printf '%s' "$ECI_RUNTIME_DENY_REASON" ) )"
[ "$bad_cpu_reason" = "$(deny_text eci-hook-helper 'host CPU type is not supported')" ] ||
  fail "unsupported CPU type: got [$bad_cpu_reason]"
pass "deny unsupported host CPU type (forced Darwin, probe fails): host CPU type is not supported"

# Darwin open path, reached only through the platform switch at the top of eci_runtime_open_verified.
# Bash 3.2 has no named descriptors, so this must return (accept, or a loader denial with a reason)
# under every bash. A shell that dies here fails the case through the subshell status.
reset_bin
install_binary "$fixtures/native"
darwin_open_case() {  # darwin_open_case <label> <cputype, or FAIL> <exact reason, or ANY>
  local label="$1" forced="$2" want="$3" out='' st=0
  out="$( ( eci_runtime_platform=Darwin
    if [ "$forced" = FAIL ]; then
      eci_runtime_darwin_host_cputype() { return 1; }
    else
      forced_cpu="$forced"
      eci_runtime_darwin_host_cputype() { printf '%s\n' "$forced_cpu"; }
    fi
    if eci_runtime_open_verified eci-hook-helper; then
      printf 'OPEN rc=0\n'
    else
      printf 'OPEN rc=1 reason=%s\n' "$ECI_RUNTIME_DENY_REASON"
    fi ) 2>&1 )" || st=$?
  [ "$st" -eq 0 ] || fail "$label: the shell died (status $st) instead of returning: $out"
  if [ "$want" = ANY ]; then
    case "$out" in
      'OPEN rc=0' | 'OPEN rc=1 reason=ECI native gate binary eci-hook-helper is unavailable or unverified ('*)
        pass "$label: returned with accept or a loader denial with reason" ;;
      *) fail "$label: unexpected output: $out" ;;
    esac
  else
    [ "$out" = "OPEN rc=1 reason=$(deny_text eci-hook-helper "$want")" ] ||
      fail "$label: expected a deny with reason [$want], got [$out]"
    pass "$label: deny with reason: $want"
  fi
}
darwin_open_case darwin-open-unsupported-cpu FAIL 'host CPU type is not supported'
darwin_open_case darwin-open-copy-path '0c 00 00 01' ANY

# Darwin copy path end to end. Each case is a child process with its own TMPDIR, so no state carries
# between cases. Linux has no BSD stat, so a test-only stand-in answers the three stat forms the path
# uses; the cases that need it run on Linux only. The temporary-directory denial runs on both hosts.
dstand="$base/darwin-stand"
dreal_stat="$(command -v stat)"
mkdir -p -- "$dstand/bin"
cat >"$dstand/bin/stat" <<'STAND'
#!/bin/sh
# Test-only stand-in for macOS `stat -f`, so the darwin copy path runs on Linux. It answers only the
# forms the path uses and exits 2 on anything else:
#   stat -f '%u %Lp' -- PATH        lstat: owner and octal mode
#   stat -f '%d %i' -- PATH         lstat: device and inode
#   stat -f '%d %i %u %Lp %HT' <&N  fstat of stdin: device, inode, owner, mode, type
# NT_STAT_FAKE_PATH with NT_STAT_FAKE_UID or NT_STAT_FAKE_MODE overrides that path only.
# NT_STAT_SWAP (symlink or rename) replaces NT_STAT_SWAP_PATH once, during the fstat, from NT_STAT_SWAP_TO.
[ "${1:-}" = -f ] || exit 2
fmt="$2"
shift 2
[ "${1:-}" = -- ] && shift
real="${NT_REAL_STAT:?NT_REAL_STAT must name the real stat}"
fake_uid=''
fake_mode=''
fake_apply() {
  fake_uid=''
  fake_mode=''
  if [ -n "${NT_STAT_FAKE_PATH:-}" ] && [ "$1" = "$NT_STAT_FAKE_PATH" ]; then
    fake_uid="${NT_STAT_FAKE_UID:-}"
    fake_mode="${NT_STAT_FAKE_MODE:-}"
  fi
}
if [ "$#" -gt 0 ]; then
  path="$1"
  case "$fmt" in
    '%u %Lp')
      line="$("$real" -c '%u %a' -- "$path")" || exit 2
      set -- $line
      uid="$1"
      mode="$2"
      fake_apply "$path"
      [ -n "$fake_uid" ] && uid="$fake_uid"
      [ -n "$fake_mode" ] && mode="$fake_mode"
      printf '%s %s\n' "$uid" "$mode"
      ;;
    '%d %i') exec "$real" -c '%d %i' -- "$path" ;;
    *) exit 2 ;;
  esac
else
  [ "$fmt" = '%d %i %u %Lp %HT' ] || exit 2
  target="$(readlink -f -- /dev/stdin)" || exit 2
  if [ -n "${NT_STAT_SWAP:-}" ] && [ "$target" = "${NT_STAT_SWAP_PATH:-}" ]; then
    case "$NT_STAT_SWAP" in
      symlink) rm -f -- "$target" && ln -s -- "$NT_STAT_SWAP_TO" "$target" || exit 2 ;;
      rename) cp -- "$NT_STAT_SWAP_TO" "$target.swap" && mv -f -- "$target.swap" "$target" || exit 2 ;;
      *) exit 2 ;;
    esac
  fi
  line="$("$real" -L -c '%d %i %u %a %F' -- /dev/stdin)" || exit 2
  set -- $line
  dev="$1"
  ino="$2"
  uid="$3"
  mode="$4"
  shift 4
  kind="$*"
  fake_apply "$target"
  [ -n "$fake_uid" ] && uid="$fake_uid"
  [ -n "$fake_mode" ] && mode="$fake_mode"
  case "$kind" in
    'regular file' | 'regular empty file') type='Regular File' ;;
    directory) type='Directory' ;;
    'symbolic link') type='Symbolic Link' ;;
    *) type='Other' ;;
  esac
  printf '%s %s %s %s %s\n' "$dev" "$ino" "$uid" "$mode" "$type"
fi
STAND
chmod 0755 -- "$dstand/bin/stat"
cat >"$dstand/child.sh" <<'CHILD'
#!/bin/bash
# Child for the darwin cases: source the loader, force the Darwin route and the arm64 CPU type, open,
# report what the open left behind, then close. $1 is the loader, $2 the installed binary.
. "$1" || { echo 'child: loader did not source'; exit 90; }
eci_runtime_platform=Darwin
eci_runtime_darwin_host_cputype() { printf '%s\n' '0c 00 00 01'; }
if eci_runtime_open_verified eci-hook-helper; then
  printf 'OPEN rc=0\n'
  printf 'EXEC=%s\n' "$ECI_RUNTIME_BINARY_EXEC"
  printf 'COPY_MODE=%s\n' "$("$NT_REAL_STAT" -c %a -- "$ECI_RUNTIME_BINARY_EXEC")"
  printf 'DIR_MODE=%s\n' "$("$NT_REAL_STAT" -c %a -- "$eci_runtime_copy_dir")"
  if cmp -s -- "$ECI_RUNTIME_BINARY_EXEC" "$2"; then printf 'BYTES=equal\n'; else printf 'BYTES=differ\n'; fi
  eci_runtime_close_verified
  printf 'CLOSED\n'
else
  printf 'OPEN rc=1 reason=%s\n' "$ECI_RUNTIME_DENY_REASON"
fi
CHILD
chmod 0755 -- "$dstand/child.sh"
child_out=''
child_st=0
child_field() {  # child_field <key>: the value of the child's "<key>=" line
  local line=''
  while IFS= read -r line; do
    case "$line" in "$1="*) printf '%s\n' "${line#"$1="}"; return 0 ;; esac
  done <<<"$child_out"
  return 1
}
darwin_child() {  # darwin_child <tmpdir> [VAR=value ...]: runs the child; sets child_out and child_st
  local ctmp="$1" prefix=''
  shift 1
  if [ "$host_os" = Linux ]; then prefix="$dstand/bin:"; fi
  child_st=0
  child_out="$(env -i HOME="$home" TMPDIR="$ctmp" PATH="${prefix}/usr/bin:/bin" NT_REAL_STAT="$dreal_stat" "$@" "$BASH" "$dstand/child.sh" "$loader" "$bin/eci-hook-helper" 2>&1)" || child_st=$?
}
fresh_tmp() {  # fresh_tmp <name>: a new private TMPDIR, printed
  local d="$dstand/tmp-$1"
  rm -rf -- "$d"
  mkdir -p -- "$d"
  chmod 0700 -- "$d"
  printf '%s\n' "$d"
}
darwin_accept_check() {  # darwin_accept_check <label> <tmpdir> <what>: accept, copy 0500, dir 0700, closed
  local label="$1" ctmp="$2" copy='' dir=''
  [ "$child_st" -eq 0 ] || fail "$label: child died (status $child_st): $child_out"
  case "$child_out" in 'OPEN rc=0'*) ;; *) fail "$label: expected accept, got: $child_out" ;; esac
  copy="$(child_field EXEC)" || fail "$label: no EXEC line in the child output"
  dir="${copy%/*}"
  [ "$(child_field COPY_MODE)" = 500 ] || fail "$label: copy mode is $(child_field COPY_MODE), expected 500"
  [ "$(child_field DIR_MODE)" = 700 ] || fail "$label: copy directory mode is $(child_field DIR_MODE), expected 700"
  [ "$(child_field BYTES)" = equal ] || fail "$label: copy bytes differ from the installed binary"
  case "$child_out" in *CLOSED*) ;; *) fail "$label: close did not complete" ;; esac
  [ ! -e "$copy" ] && [ ! -e "$dir" ] || fail "$label: close left the copy or its directory behind"
  [ -z "$(ls -A -- "$ctmp")" ] || fail "$label: TMPDIR is not empty after close"
  pass "$label: $3"
}
darwin_deny_check() {  # darwin_deny_check <label> <tmpdir> <reason>: exact deny, nothing left behind
  local label="$1" ctmp="$2" want="$3"
  [ "$child_st" -eq 0 ] || fail "$label: child died (status $child_st): $child_out"
  [ "$child_out" = "OPEN rc=1 reason=$(deny_text eci-hook-helper "$want")" ] ||
    fail "$label: expected deny [$want], got [$child_out]"
  [ -z "$(ls -A -- "$ctmp")" ] || fail "$label: denial left files in TMPDIR"
  pass "$label: deny with reason: $want"
}
if [ "$host_os" = Linux ]; then
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp accept)"
  darwin_child "$ctmp"
  darwin_accept_check darwin-copy-accept "$ctmp" 'copy mode 0500 in a 0700 directory, bytes equal, close removes both'
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp owner)"
  darwin_child "$ctmp" NT_STAT_FAKE_PATH="$real_bin/eci-hook-helper" NT_STAT_FAKE_UID=4242
  darwin_deny_check darwin-copy-wrong-owner "$ctmp" 'is not owned by the current user'
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp group)"
  darwin_child "$ctmp" NT_STAT_FAKE_PATH="$real_bin/eci-hook-helper" NT_STAT_FAKE_MODE=775
  darwin_deny_check darwin-copy-group-writable "$ctmp" 'is group- or other-writable'
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp other)"
  darwin_child "$ctmp" NT_STAT_FAKE_PATH="$real_bin/eci-hook-helper" NT_STAT_FAKE_MODE=757
  darwin_deny_check darwin-copy-other-writable "$ctmp" 'is group- or other-writable'
  install_binary "$fixtures/macho-arm64"
  printf 'tamper' >>"$bin/eci-hook-helper"
  ctmp="$(fresh_tmp tampered)"
  darwin_child "$ctmp"
  darwin_deny_check darwin-copy-tampered "$ctmp" 'sha256 does not match its receipt'
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp symlink)"
  darwin_child "$ctmp" NT_STAT_SWAP=symlink NT_STAT_SWAP_PATH="$real_bin/eci-hook-helper" NT_STAT_SWAP_TO="$fixtures/macho-arm64"
  darwin_deny_check darwin-copy-symlink-swap "$ctmp" 'resolves outside the verified bin directory'
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp rename)"
  darwin_child "$ctmp" NT_STAT_SWAP=rename NT_STAT_SWAP_PATH="$real_bin/eci-hook-helper" NT_STAT_SWAP_TO="$fixtures/macho-arm64"
  darwin_deny_check darwin-copy-rename-swap "$ctmp" 'resolves outside the verified bin directory'
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp sticky)"
  real_ctmp="$(cd -P -- "$ctmp" && pwd -P)"
  darwin_child "$ctmp" NT_STAT_FAKE_PATH="$real_ctmp" NT_STAT_FAKE_UID=0 NT_STAT_FAKE_MODE=1777
  darwin_accept_check darwin-temp-root-sticky-accept "$ctmp" 'root-owned sticky TMPDIR accepted; copy under it, close removes it'
  install_binary "$fixtures/macho-arm64"
  ctmp="$(fresh_tmp root-777)"
  real_ctmp="$(cd -P -- "$ctmp" && pwd -P)"
  darwin_child "$ctmp" NT_STAT_FAKE_PATH="$real_ctmp" NT_STAT_FAKE_UID=0 NT_STAT_FAKE_MODE=777
  darwin_deny_check darwin-temp-root-777-denied "$ctmp" 'temporary directory is not trusted: owned by another user, or group/other-writable without a root-owned sticky bit'
else
  skip "darwin copy cases that need the BSD stat stand-in run on Linux only"
fi
install_binary "$fixtures/macho-arm64"
temp777="$dstand/tmp-777"
rm -rf -- "$temp777"
mkdir -p -- "$temp777"
chmod 0777 -- "$temp777"
darwin_child "$temp777"
darwin_deny_check darwin-temp-777-not-sticky-denied "$temp777" 'temporary directory is not trusted: owned by another user, or group/other-writable without a root-owned sticky bit'

# Receipt-case table (Linux). Fixed descriptor 8 keeps it runnable under bash 3.2.
if [ "$host_os" = Linux ]; then
  tbase="$base/table"
  mkdir -p -- "$tbase/scratch"
  tbin="$tbase/eci-hook-helper"
  cp -- "$fixtures/native" "$tbin"; chmod 0755 -- "$tbin"
  treal="$(cd -P -- "$tbase" && pwd -P)"
  trecpt="$treal/eci-hook-helper.sha256"
  tdigest="$(digest_of "$tbin")"
  tzero="$(printf '%064d' 0)"
  tupper="$(printf '%s' "$tdigest" | tr 'a-f' 'A-F')"
  tcases=0
  tfailures=0
  table_case() {  # table_case <label> <receipt line, or NONE> <verified name> <expected outcome>
    local label="$1" content="$2" name="$3" expected="$4" got reason
    if [ "$content" = NONE ]; then
      rm -f -- "$trecpt"
    else
      printf '%s\n' "$content" >"$trecpt"
    fi
    ECI_RUNTIME_DENY_REASON=''
    exec 8<"$tbin"
    if eci_runtime_verify_fd "$name" 8 "$treal/eci-hook-helper" "$tbase/scratch" "$trecpt"; then
      got=accept
    else
      reason=${ECI_RUNTIME_DENY_REASON#*\(}
      reason=${reason%%\)*}
      got="deny|$reason"
    fi
    exec 8<&-
    tcases=$((tcases + 1))
    if [ "$got" = "$expected" ]; then
      pass "receipt $label: $got"
    else
      tfailures=$((tfailures + 1))
      printf 'FAIL: receipt %s: got %s, expected %s\n' "$label" "$got" "$expected" >&2
    fi
  }
  table_case valid-receipt "$tdigest  eci-hook-helper" eci-hook-helper accept
  table_case wrong-hash "$tzero  eci-hook-helper" eci-hook-helper 'deny|sha256 does not match its receipt'
  table_case wrong-name "$tdigest  eci-command-plan" eci-hook-helper 'deny|has a malformed sha256 receipt'
  table_case extra-suffix "$tdigest  eci-hook-helper extra" eci-hook-helper 'deny|has a malformed sha256 receipt'
  table_case uppercase-hex "$tupper  eci-hook-helper" eci-hook-helper 'deny|has a malformed sha256 receipt'
  table_case missing-two-spaces "$tdigest eci-hook-helper" eci-hook-helper 'deny|has a malformed sha256 receipt'
  table_case missing-receipt NONE eci-hook-helper 'deny|has no sha256 receipt'
  table_case name-metachar "$tdigest  eci-hook" eci.hook 'deny|is not an allowed artifact name'
  [ "$tfailures" -eq 0 ] || fail "receipt-case table: $tfailures of $tcases cases failed"
fi

printf 'eci-native-binary-loader: %s checks passed (host %s, bash %s)\n' "$checks" "$host_os" "$BASH_VERSION"
