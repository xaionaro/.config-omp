#!/usr/bin/env bash
# Shared build step for on-demand ECI Go tools. Sourced, never executed.
# Adapted from pre-migration source 575bb81 bin/owned-command:39-149. Every deviation is a row in
# hooks/tests/TEST-DISPOSITION.md under "On-demand build wrappers (Job AUTOBUILD)".
#
#   eci_go_tool_build NAME SOURCE_DIR EXECUTABLE_PREFIX EXECUTABLE_SUFFIX FIRST_BUILD_ESTIMATE
#
# The caller holds the build lock for the directory of EXECUTABLE_PREFIX. On success the
# function sets eci_go_tool_executable to PREFIX<key>SUFFIX and returns 0; an executable that
# already exists for the key is reused without a build. It returns 127 after printing one line
# when no go is on PATH, and 1 after printing the exact rerun command when a go step fails.
# Module downloads use the caller's GOPROXY and stay verified by go.sum and the checksum
# database. This file never disables checksum verification.
# Bash 3.2 with BSD userland: no mapfile, realpath, readlink -f, mv -T, stat -c, or sort without LC_ALL=C.

# Digest command as an array so xargs can run it. macOS ships shasum, not sha256sum.
if [ "$(uname -s)" != Darwin ] && command -v sha256sum >/dev/null 2>&1; then
  eci_sha256_command=(sha256sum)
else
  eci_sha256_command=(shasum -a 256)
fi

# Absolute path of this file. Its digest enters every fingerprint, so a recipe change rebuilds.
case ${BASH_SOURCE[0]} in
  */*) eci_go_tool_library=$(cd -P -- "${BASH_SOURCE[0]%/*}" && pwd -P)/${BASH_SOURCE[0]##*/} ;;
  *) eci_go_tool_library=$PWD/${BASH_SOURCE[0]} ;;
esac

# eci_go_tool_failed NAME SOURCE_DIR RC WHAT COMMAND... prints the exact rerun command, then returns 1.
eci_go_tool_failed() {
  local name=$1 source_dir=$2 rc=$3 what=$4 quoted
  shift 4
  printf -v quoted ' %q' "$@"
  printf '%s: %s failed (exit %s); rerun: cd %q &&%s\n' "$name" "$what" "$rc" "$source_dir" "$quoted" >&2
  return 1
}

# eci_go_tool_go_directive DIR prints the go version named by DIR/go.mod, or nothing.
eci_go_tool_go_directive() {
  local key value rest
  while read -r key value rest || [ -n "$key" ]; do
    if [ "$key" = go ]; then
      printf '%s\n' "$value"
      return 0
    fi
  done <"$1/go.mod"
  return 0
}

# eci_go_tool_fingerprint reads the locals of eci_go_tool_build through dynamic scope. It runs in
# a subshell, so its cd does not leak. Fingerprint as in pre-migration source 575bb81, with the cache format bumped and the
# library digest added so that an earlier recipe's cache entries are not reused.
eci_go_tool_fingerprint() (
  cd -- "$source_dir" || return 1
  printf '%s\n' "$name cache format 3; native host/default ISA; GOFLAGS=space; GO_EXTLINK_ENABLED=0; -buildvcs=false"
  printf '%s\n' "$source_dir"
  printf '%s\n' "$go_launcher" "$compiler" "$compiler_root" "$compiler_version" "$requested_toolchain" "$tuning" "$library_digest"
  "${eci_sha256_command[@]}" -- "$go_launcher" "$compiler" || return 1
  "${native_env[@]}" "$compiler" version || return 1
  "${native_env[@]}" "$compiler" env GOOS GOARCH GO111MODULE GOWORK CGO_ENABLED GOFLAGS GO_EXTLINK_ENABLED \
    GOPATH GOMODCACHE GOEXPERIMENT GOFIPS140 || return 1
  if [[ -n $tuning ]]; then
    "${native_env[@]}" "$compiler" env "$tuning" || return 1
  fi
  find . -type f \( -name '*.go' -o -name go.mod -o -name go.sum \) -print0 |
    LC_ALL=C sort -z | xargs -0 "${eci_sha256_command[@]}"
)

eci_go_tool_build() {
  if [ "$#" -ne 5 ]; then
    printf 'usage: eci_go_tool_build NAME SOURCE_DIR EXECUTABLE_PREFIX EXECUTABLE_SUFFIX FIRST_BUILD_ESTIMATE\n' >&2
    return 2
  fi
  local name=$1 requested=$2 prefix=$3 suffix=$4 estimate=$5
  local source_dir resolved go_launcher go_dir go_version goenv home_real shown
  local bootstrap host_os host_arch compiler_root compiler_version requested_toolchain compiler
  local tuning native_default library_digest fingerprint after_fingerprint after key executable
  local build_dir rc
  local tuning_var
  local -a go_config bootstrap_argv tuning_names defaults_env tuning_argv native_env

  eci_go_tool_executable=
  command -v "${eci_sha256_command[0]}" >/dev/null 2>&1 || {
    printf '%s: %s is required\n' "$name" "${eci_sha256_command[0]}" >&2
    return 1
  }

  # The module must be complete and canonical before any go command runs inside it.
  resolved=$(cd -P -- "$requested" 2>/dev/null && pwd -P) || resolved=
  if [ -z "$resolved" ] || [ ! -f "$resolved/go.mod" ] || [ ! -f "$resolved/main.go" ]; then
    printf '%s: source module missing at %s\n' "$name" "$requested" >&2
    return 1
  fi
  source_dir=$resolved

  # type -P selects a PATH executable even when the caller exports a go function.
  go_launcher=$(type -P go 2>/dev/null) || go_launcher=
  if [ -z "$go_launcher" ]; then
    go_version=$(eci_go_tool_go_directive "$source_dir")
    if [ -n "$go_version" ]; then
      printf '%s: Go is not on PATH; install Go %s or newer from https://go.dev/dl/ and retry\n' "$name" "$go_version" >&2
    else
      printf '%s: Go is not on PATH; install Go from https://go.dev/dl/ and retry\n' "$name" >&2
    fi
    return 127
  fi
  case $go_launcher in
    */*) go_dir=${go_launcher%/*} ;;
    *) go_dir=. ;;
  esac
  [ -n "$go_dir" ] || go_dir=/
  go_dir=$(cd -P -- "$go_dir" && pwd -P) || {
    printf '%s: cannot resolve the directory of %s\n' "$name" "$go_launcher" >&2
    return 1
  }
  go_launcher=$go_dir/${go_launcher##*/}

  # A caller-relative GOENV is resolved here, before any cd into the module.
  go_config=()
  if [ -n "${GOENV:-}" ] && [ "$GOENV" != off ]; then
    goenv=$GOENV
    case $goenv in /*) ;; *) goenv=$PWD/$goenv ;; esac
    go_config=("GOENV=$goenv")
  fi

  # The bootstrap keeps dependency transport, caches and the requested toolchain selection.
  bootstrap_argv=(env ${go_config[@]+"${go_config[@]}"} GO111MODULE=on GOWORK=off CGO_ENABLED=0 GOFLAGS=' ' \
    GO_EXTLINK_ENABLED=0 "$go_launcher" env GOHOSTOS GOHOSTARCH GOROOT GOVERSION GOTOOLCHAIN)
  rc=0
  bootstrap=$(cd -- "$source_dir" && "${bootstrap_argv[@]}") || rc=$?
  if [ "$rc" -ne 0 ]; then
    eci_go_tool_failed "$name" "$source_dir" "$rc" 'go env' "${bootstrap_argv[@]}"
    return 1
  fi
  { IFS= read -r host_os && IFS= read -r host_arch && IFS= read -r compiler_root &&
    IFS= read -r compiler_version && IFS= read -r requested_toolchain; } <<<"$bootstrap" || {
    printf '%s: go env did not report the compiler selection\n' "$name" >&2
    return 1
  }
  compiler=$compiler_root/bin/go
  if [ -z "$host_os" ] || [ -z "$host_arch" ] || [ -z "$compiler_root" ] ||
    [ -z "$compiler_version" ] || [ ! -x "$compiler" ]; then
    printf '%s: selected native Go compiler unavailable\n' "$name" >&2
    return 1
  fi

  # Ask the selected compiler for its baseline ISA, without caller target tuning.
  tuning_names=(GO386 GOAMD64 GOARM GOARM64 GOMIPS GOMIPS64 GOPPC64 GORISCV64 GOWASM)
  defaults_env=(env)
  for tuning_var in "${tuning_names[@]}"; do
    defaults_env+=(-u "$tuning_var")
  done
  case $host_arch in
    386) tuning=GO386 ;;
    amd64) tuning=GOAMD64 ;;
    arm) tuning=GOARM ;;
    arm64) tuning=GOARM64 ;;
    mips|mipsle) tuning=GOMIPS ;;
    mips64|mips64le) tuning=GOMIPS64 ;;
    ppc64|ppc64le) tuning=GOPPC64 ;;
    riscv64) tuning=GORISCV64 ;;
    wasm) tuning=GOWASM ;;
    *) tuning= ;;
  esac
  # Every query and compilation shares this exact compiler and native policy.
  native_env=(env ${go_config[@]+"${go_config[@]}"} GOROOT="$compiler_root" GOTOOLCHAIN=local GOOS="$host_os" \
    GOARCH="$host_arch" GO111MODULE=on GOWORK=off CGO_ENABLED=0 GOFLAGS=' ' GO_EXTLINK_ENABLED=0)
  if [ -n "$tuning" ]; then
    tuning_argv=("${defaults_env[@]}" GOROOT="$compiler_root" GOENV=off GOTOOLCHAIN=local GOOS="$host_os" \
      GOARCH="$host_arch" GO111MODULE=on GOWORK=off CGO_ENABLED=0 GOFLAGS=' ' GO_EXTLINK_ENABLED=0 \
      "$compiler" env "$tuning")
    rc=0
    native_default=$(cd -- "$source_dir" && "${tuning_argv[@]}") || rc=$?
    if [ "$rc" -ne 0 ]; then
      eci_go_tool_failed "$name" "$source_dir" "$rc" 'go env' "${tuning_argv[@]}"
      return 1
    fi
    if [ -z "$native_default" ]; then
      printf '%s: native Go architecture default missing for %s\n' "$name" "$tuning" >&2
      return 1
    fi
    native_env+=("$tuning=$native_default")
  fi

  library_digest=$("${eci_sha256_command[@]}" -- "$eci_go_tool_library") || {
    printf '%s: cannot digest %s\n' "$name" "$eci_go_tool_library" >&2
    return 1
  }
  library_digest=${library_digest%% *}
  fingerprint=$(eci_go_tool_fingerprint) || {
    printf '%s: cannot fingerprint %s\n' "$name" "$source_dir" >&2
    return 1
  }
  key=$(printf '%s\n' "$fingerprint" | "${eci_sha256_command[@]}") || return 1
  key=${key%% *}
  executable=$prefix$key$suffix
  if [ -x "$executable" ]; then
    eci_go_tool_executable=$executable
    return 0
  fi

  # One line before a cold build. Paths under the canonical $HOME/.omp print relative to it.
  home_real=$(cd -P -- "${HOME:-/nonexistent}" 2>/dev/null && pwd -P) || home_real=
  shown=$source_dir
  case $shown in "$home_real/.omp/"*) shown=${shown#"$home_real/.omp/"} ;; esac
  printf '%s: building %s for %s/%s; this takes %s\n' "$name" "$shown" "$host_os" "$host_arch" "$estimate" >&2

  build_dir=$(mktemp -d "${prefix%/*}/.$name-build.XXXXXXXX") || {
    printf '%s: cannot create a build directory under %s\n' "$name" "${prefix%/*}" >&2
    return 1
  }
  rc=0
  (cd -- "$source_dir" && "${native_env[@]}" "$compiler" build -buildvcs=false -o "$build_dir/$name" .) >&2 || rc=$?
  if [ "$rc" -ne 0 ]; then
    rm -rf -- "$build_dir"
    # The rerun uses -o /dev/null: it reproduces the diagnostics without writing an executable.
    eci_go_tool_failed "$name" "$source_dir" "$rc" build "${native_env[@]}" "$compiler" build -buildvcs=false -o /dev/null .
    return 1
  fi

  # Source writers do not share this lock. Refuse a snapshot that changed during the build.
  after_fingerprint=$(eci_go_tool_fingerprint) || after_fingerprint=
  after=
  if [ -n "$after_fingerprint" ]; then
    after=$(printf '%s\n' "$after_fingerprint" | "${eci_sha256_command[@]}") || after=
    after=${after%% *}
  fi
  if [ "$after" != "$key" ]; then
    rm -rf -- "$build_dir"
    printf '%s: source changed during build; retry invocation\n' "$name" >&2
    return 1
  fi

  if ! mkdir -p -- "${executable%/*}" || ! mv -f -- "$build_dir/$name" "$executable" || ! rmdir -- "$build_dir"; then
    rm -rf -- "$build_dir"
    printf '%s: cannot install %s\n' "$name" "$executable" >&2
    return 1
  fi
  eci_go_tool_executable=$executable
  return 0
}
