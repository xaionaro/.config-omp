#!/usr/bin/env bash
# Builds the native ECI tools on first use. bin/eci-active calls eci_native_provision before exec. Sourced, never executed.
# The set is the loader-verified gate binaries (eci-command-plan, eci-hook-helper, eci-safe-import), eci-worker-git,
# whose receipt the contract keeps informational, and on Linux eci-verexec, the native verifier the gate binaries run
# through. Each lives in $HOME/.omp/agent/eci/bin with a <name>.sha256 receipt.
# A name is rebuilt only when its binary or receipt is absent, or when the binary is a regular file that is not native
# to this host and so can never run; this is the rule of pre-migration source 575bb81 (hooks/lib/eci-runtime-sync.sh:8,18-19).
# Nothing is built over a symlink or a non-regular entry. A present gate binary that the loader (lib/eci-native-binary.sh,
# eci_runtime_verify) denies stops the run before anything is built, except in the Linux verifier bootstrap described at
# eci_native_provision_locked; a stuck eci-worker-git or eci-verexec is reported and skipped.
# hooks/build-native-binaries.sh is the only builder. Hooks never build: they verify every use with the loader.
# The warm path checks file type, receipt presence and host format only; full loader verification runs on the cold
# path, under bin/.native-provision.lock. Every message goes to stderr.
# Bash 3.2 with BSD userland: fixed descriptor 7 (bin/eci-active holds 9, the Darwin loader opens 8), no empty-array
# expansion, no exec {fd}, mapfile, realpath, readlink -f or stat -c.

eci_native_provision_lib=$(cd -P -- "${BASH_SOURCE[0]%/*}" && pwd -P)
source "$eci_native_provision_lib/eci-native-binary.sh"
source "$eci_native_provision_lib/eci-file-lock.sh"
eci_native_gate_set='eci-command-plan eci-hook-helper eci-safe-import'
eci_native_receipt_only='eci-worker-git'
# Linux runs the gate binaries through bin/eci-verexec, which checks itself on every run; provisioning only
# makes sure it is present, native and has a receipt, like eci-worker-git.
if [ "$eci_runtime_platform" = Linux ]; then eci_native_receipt_only='eci-worker-git eci-verexec'; fi

# Sets eci_native_class for bin/$1:
#   stuck   - the binary or its receipt is a symlink or a non-regular entry; never built over;
#   build   - the binary or receipt is absent, or the binary is regular but not native to this host;
#   present - otherwise: regular binary, native, receipt present.
eci_native_classify() {
  local path="$HOME/.omp/agent/eci/bin/$1"
  eci_native_class=stuck
  if [ -L "$path" ] || { [ -e "$path" ] && [ ! -f "$path" ]; }; then return 0; fi
  if [ -L "$path.sha256" ] || { [ -e "$path.sha256" ] && [ ! -f "$path.sha256" ]; }; then return 0; fi
  if [ ! -e "$path" ] || [ ! -e "$path.sha256" ] || ! eci_runtime_binary_is_native "$path"; then
    eci_native_class=build
    return 0
  fi
  eci_native_class=present
}

eci_native_is_gate() {
  case " $eci_native_gate_set " in *" $1 "*) return 0 ;; esac
  return 1
}

# Gate binaries pass the unchanged loader. eci-worker-git, outside the loader allow-list, must classify as present.
eci_native_check() {
  if eci_native_is_gate "$1"; then
    if ! eci_runtime_verify "$1"; then
      printf '%s\n' "$ECI_RUNTIME_DENY_REASON" >&2
      return 1
    fi
    return 0
  fi
  eci_native_classify "$1"
  [ "$eci_native_class" = present ] && return 0
  printf 'eci-active: %s is not a host-native binary with a receipt\n' "$HOME/.omp/agent/eci/bin/$1" >&2
  return 1
}

eci_native_stuck_advisory() {
  printf 'eci-active: %s or its receipt is a symlink or not a regular file; not rebuilt\n' "$HOME/.omp/agent/eci/bin/$1" >&2
}

# Under the lock: decide again, stop on a denied or stuck gate binary, build the rest, then check what was built.
# Gate binaries are verified through eci-verexec on Linux. When eci-verexec itself must be built (first use,
# or a missing or foreign verifier), nothing can verify the present gate binaries yet: that bootstrap builds
# every missing name in one run, verifier included, and then verifies every gate binary, present or built.
eci_native_provision_locked() {
  local name build='' start rc=0 bootstrap=0 deferred=''
  case " $eci_native_receipt_only " in
    *" eci-verexec "*)
      eci_native_classify eci-verexec
      [ "$eci_native_class" != build ] || bootstrap=1
      ;;
  esac
  for name in $eci_native_gate_set $eci_native_receipt_only; do
    eci_native_classify "$name"
    case $eci_native_class in
      build) build="$build $name" ;;
      stuck)
        if eci_native_is_gate "$name"; then
          if [ "$bootstrap" = 1 ]; then
            deferred="$deferred $name"
            continue
          fi
          eci_native_check "$name" || true
          return 1
        fi
        eci_native_stuck_advisory "$name"
        ;;
      present)
        if eci_native_is_gate "$name"; then
          if [ "$bootstrap" = 1 ]; then
            deferred="$deferred $name"
            continue
          fi
          eci_native_check "$name" || return 1
        fi
        ;;
    esac
  done
  [ -n "$build" ] || return 0
  printf 'eci-active: building native ECI tools (%s); this takes about 1-2 minutes\n' "${build# }" >&2
  start=$SECONDS
  # Word splitting of $build is intended: each word is one allowed name.
  "$BASH" "$eci_native_provision_lib/../build-native-binaries.sh" $build >&2 || return $?
  printf 'eci-active: built native ECI tools in %ss\n' "$((SECONDS - start))" >&2
  for name in $build $deferred; do
    eci_native_check "$name" || rc=1
  done
  return "$rc"
}

# A subshell, so descriptor 7 and the variables stay contained. Returns 0 when nothing is missing.
eci_native_provision() (
  local name build='' bin lock rc=0
  case ${HOME:-} in /*) ;; *) printf 'eci-active: HOME must be an absolute path\n' >&2; return 1 ;; esac
  for name in $eci_native_gate_set $eci_native_receipt_only; do
    eci_native_classify "$name"
    case $eci_native_class in
      build) build=1 ;;
      stuck)
        if eci_native_is_gate "$name"; then
          build=1
        else
          eci_native_stuck_advisory "$name"
        fi
        ;;
    esac
  done
  [ -n "$build" ] || return 0
  bin="$HOME/.omp/agent/eci/bin"
  lock="$bin/.native-provision.lock"
  mkdir -p -- "$bin" && chmod -- 0755 "$bin" || return 1
  if [ -L "$lock" ] || { [ -e "$lock" ] && [ ! -f "$lock" ]; }; then
    printf 'eci-active: %s is a symlink or not a regular file; native provisioning stopped\n' "$lock" >&2
    return 1
  fi
  exec 7>>"$lock" || return 1
  if ! eci_file_lock_acquire_fd 7 nonblocking 2>/dev/null; then
    printf 'eci-active: waiting for a concurrent native build\n' >&2
    eci_file_lock_acquire_fd 7 blocking || return 1
  fi
  eci_native_provision_locked || rc=$?
  eci_file_lock_release_fd 7 || [ "$rc" -ne 0 ] || rc=1
  return "$rc"
)
