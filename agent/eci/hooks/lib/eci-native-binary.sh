# Host compatibility for the managed Linux ELF runtime artifacts.
# shellcheck shell=bash
# Host header cache. Reset on every sourcing so an inherited value cannot stand in for /proc/$$/exe.
eci_native_host_header=''

eci_runtime_binary_is_native() {
  local path="$1" candidate_header host_header index
  local -a candidate=() host=()
  [ -f "$path" ] && [ ! -L "$path" ] && [ -x "$path" ] || return 1
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

# Resolve a published ECI runtime artifact path. Nothing is built here.
eci_runtime_artifact_path() {
  local name="${1:-}" output_root="${HOME:-}/tmp/eci-runtime"
  [ -n "${HOME:-}" ] || return 1
  case "$name" in eci-command-plan|eci-safe-import|eci-hook-helper) ;; *) return 1 ;; esac
  printf '%s/eci/%s\n' "$output_root" "$name"
}

# Resolve the host-compatible Go hook helper. Callers treat failure as advisory.
eci_runtime_hook_helper_path() {
  local binary
  binary="$(eci_runtime_artifact_path eci-hook-helper)" || return 1
  eci_runtime_binary_is_native "$binary" || return 1
  printf '%s\n' "$binary"
}
