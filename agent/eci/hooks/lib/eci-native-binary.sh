# Trusted launcher for the native ECI gate binaries.
# shellcheck shell=bash
# Only three names resolve, each from $HOME/.omp/agent/eci/bin. A binary runs only
# through the descriptor verified here: it must be a regular file owned by the
# current user and not group/other-writable, match the host ELF format, and hash to
# the receipt written by hooks/build-native-binaries.sh. Ancestor directories must be
# owned by the current user or root and not group/other-writable.

# Host header cache. Reset on every sourcing so an inherited value cannot stand in for /proc/$$/exe.
eci_native_host_header=''
# Set when a verification fails; names the binary and the provisioning step.
ECI_RUNTIME_DENY_REASON=''
# Set when a verification succeeds; execute /proc/self/fd/$ECI_RUNTIME_BINARY_FD.
ECI_RUNTIME_BINARY_FD=''

eci_runtime_build_step='$HOME/.omp/agent/eci/hooks/build-native-binaries.sh'

eci_runtime_denial() {
  ECI_RUNTIME_DENY_REASON="ECI native gate binary $1 is unavailable or unverified ($2); provision it with \"$eci_runtime_build_step\" and retry"
}

# Resolve $HOME/.omp/agent/eci/bin/<name> for the three allowed names. Nothing is built here.
eci_runtime_artifact_path() {
  local name="${1:-}"
  case "${HOME:-}" in /*) ;; *) return 1 ;; esac
  case "$name" in eci-command-plan|eci-safe-import|eci-hook-helper) ;; *) return 1 ;; esac
  printf '%s/.omp/agent/eci/bin/%s\n' "$HOME" "$name"
}

# The file at $1 must be an ELF executable whose class, byte order and machine match the host.
eci_runtime_elf_matches_host() {
  local path="$1" candidate_header host_header index
  local -a candidate=() host=()
  candidate_header="$(od -An -v -tx1 -w20 -N20 -- "$path" 2>/dev/null)" || return 1
  if [ -z "$eci_native_host_header" ]; then
    eci_native_host_header="$(od -An -v -tx1 -w20 -N20 -- "/proc/$$/exe" 2>/dev/null)" || return 1
  fi
  host_header="$eci_native_host_header"
  read -r -a candidate <<<"$candidate_header"
  read -r -a host <<<"$host_header"
  [ "${#candidate[@]}" -eq 20 ] && [ "${#host[@]}" -eq 20 ] || return 1
  [ "${candidate[*]:0:4}" = '7f 45 4c 46' ] || return 1
  # ELF class, byte order and machine identify the host execution format.
  # Ignore the executable/shared-object distinction and padding bytes.
  for index in 4 5 18 19; do
    [ "${candidate[$index]}" = "${host[$index]}" ] || return 1
  done
  case "${candidate[7]}" in 00|03) ;; *) return 1 ;; esac
}

# Every directory from $1 up to / must be owned by the current user or root and must not be group/other-writable.
eci_runtime_directory_chain_is_trusted() {
  local dir="$1" uid owner mode
  uid="$(id -u)" || return 1
  while :; do
    read -r owner mode < <(stat -c '%u %a' -- "$dir" 2>/dev/null) || return 1
    [[ "$mode" =~ ^[0-7]{3,4}$ ]] || return 1
    [ "$owner" = "$uid" ] || [ "$owner" = 0 ] || return 1
    (( 8#$mode & 022 )) && return 1
    [ "$dir" = / ] && return 0
    dir="$(dirname -- "$dir")"
  done
}

# Verify the open descriptor $2 for artifact $1 whose resolved path must be $3.
eci_runtime_verify_fd() {
  local name="$1" fd="$2" expected_path="$3" fdpath="/proc/self/fd/$2"
  local target owner mode kind receipt_path receipt_line expected actual

  target="$(readlink -- "$fdpath" 2>/dev/null)" || { eci_runtime_denial "$name" "descriptor cannot be resolved"; return 1; }
  [ "$target" = "$expected_path" ] || { eci_runtime_denial "$name" "resolves outside the verified bin directory"; return 1; }
  read -r owner mode kind < <(stat -L -c '%u %a %F' -- "$fdpath" 2>/dev/null) || { eci_runtime_denial "$name" "cannot be stat'ed"; return 1; }
  case "$kind" in 'regular file'|'regular empty file') ;; *) eci_runtime_denial "$name" "is not a regular file"; return 1 ;; esac
  [ "$owner" = "$(id -u)" ] || { eci_runtime_denial "$name" "is not owned by the current user"; return 1; }
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] || { eci_runtime_denial "$name" "has an unreadable mode"; return 1; }
  (( 8#$mode & 022 )) && { eci_runtime_denial "$name" "is group- or other-writable"; return 1; }
  (( 8#$mode & 0111 )) || { eci_runtime_denial "$name" "is not executable"; return 1; }
  eci_runtime_elf_matches_host "$fdpath" || { eci_runtime_denial "$name" "is not an ELF binary for this host"; return 1; }

  receipt_path="$(eci_runtime_artifact_path "$name").sha256"
  [ -f "$receipt_path" ] && [ ! -L "$receipt_path" ] || { eci_runtime_denial "$name" "has no sha256 receipt"; return 1; }
  IFS= read -r receipt_line <"$receipt_path" || true
  [[ "$receipt_line" =~ ^([0-9a-f]{64})"  $name"$ ]] || { eci_runtime_denial "$name" "has a malformed sha256 receipt"; return 1; }
  expected="${BASH_REMATCH[1]}"
  actual="$(sha256sum -- "$fdpath")" || { eci_runtime_denial "$name" "cannot be hashed"; return 1; }
  actual="${actual%% *}"
  [ "$actual" = "$expected" ] || { eci_runtime_denial "$name" "sha256 does not match its receipt"; return 1; }
}

# Open and verify $HOME/.omp/agent/eci/bin/<name>. On success the verified descriptor is
# left open in ECI_RUNTIME_BINARY_FD. On failure ECI_RUNTIME_DENY_REASON is set.
eci_runtime_open_verified() {
  local name="${1:-}" path bin_dir real_bin fd
  ECI_RUNTIME_BINARY_FD=''
  ECI_RUNTIME_DENY_REASON=''

  path="$(eci_runtime_artifact_path "$name")" || { eci_runtime_denial "$name" "not an allowed artifact name or HOME is not absolute"; return 1; }
  bin_dir="${path%/*}"
  real_bin="$(realpath -e -- "$bin_dir" 2>/dev/null)" || { eci_runtime_denial "$name" "bin directory is missing"; return 1; }
  eci_runtime_directory_chain_is_trusted "$real_bin" || { eci_runtime_denial "$name" "bin directory or an ancestor is not owned by the current user or root, or is group/other-writable"; return 1; }
  if [ -L "$path" ] || [ ! -f "$path" ]; then
    eci_runtime_denial "$name" "is missing or a symlink"
    return 1
  fi
  if ! exec {fd}<"$path"; then
    eci_runtime_denial "$name" "cannot be opened"
    return 1
  fi
  if ! eci_runtime_verify_fd "$name" "$fd" "$real_bin/$name"; then
    exec {fd}<&-
    return 1
  fi
  ECI_RUNTIME_BINARY_FD="$fd"
}
