#!/usr/bin/env bash
# TODO: Reconcile retired ECI lifecycle controls when the remaining marker-dependent hooks migrate.
# Resolve and build the Go runtime tools that ECI hooks execute; planner-pair tools are verified, never compiled here.

source "${BASH_SOURCE[0]%/*}/eci-native-binary.sh"
source "${BASH_SOURCE[0]%/*}/eci-file-lock.sh"
source "${BASH_SOURCE[0]%/*}/eci-portable.sh"

# Resolve the native hook helper in bin/. A missing or host-incompatible helper
# is never compiled here: ensure only verifies the canonical pair that
# hooks/build-native-binaries.sh provisioned. Callers treat failure as advisory.
eci_runtime_hook_helper_path() {
  local root="${HOME:-}" binary
  [ -n "$root" ] || return 1
  binary="$(eci_runtime_artifact_path eci-hook-helper)" || return 1
  if ! eci_runtime_binary_is_native "$binary"; then
    eci_runtime_build_missing "$root/.omp/agent/eci" eci-hook-helper || return 1
    eci_runtime_binary_is_native "$binary" || return 1
  fi
  printf '%s\n' "$binary"
}

# Source the original publisher only inside this owned child. The publisher
# alone acquires its pair lock, and rechecks requested tools after waiting.
eci_runtime_ensure_pair() (
  local provider="$1" source_root peer_root
  shift
  source_root="$(eci_realpath_existing "$HOME/.omp/agent/eci")" || return 1
  peer_root="$(eci_realpath_existing "${KIMI_CODE_HOME:-$HOME/.kimi-code}" 2>/dev/null || true)"
  source "$source_root/bin/eci-runtime-sync" || return 1
  planner_sync ensure "$source_root" "$peer_root" "$provider" "$@"
)

# Build absent Go runtime tools from this provider's sources. Compatible
# executables stay in place; explicit maintenance owns source refreshes.
# The planner pair is not compiled here: hooks/build-native-binaries.sh
# provisions it, and ensure only verifies or copies the canonical pair.
eci_runtime_build_missing() (
  local root="$1" name module binary compiler lock_fd temporary='' provider resolved_root
  local -a pair_tools=()
  shift
  [ "$#" -gt 0 ] || set -- eci-command-gate-mode eci-command-plan eci-safe-import eci-hook-helper eci-worker-git
  for name in "$@"; do
    case "$name" in
      eci-command-plan|eci-safe-import|eci-hook-helper) pair_tools+=("$name") ;;
      eci-command-gate-mode|eci-worker-git) ;;
      *) printf 'ECI runtime build: unknown tool: %s\n' "$name" >&2; return 1 ;;
    esac
  done
  if [ "${#pair_tools[@]}" -gt 0 ]; then
    resolved_root="$(eci_realpath_existing "$root")" || return 1
    if [ "$resolved_root" = "$(eci_realpath_existing "$HOME/.omp/agent/eci")" ]; then
      provider=eci
    elif [ "$resolved_root" = "$(eci_realpath_existing "${KIMI_CODE_HOME:-$HOME/.kimi-code}")" ]; then
      provider=kimi
    else
      printf 'ECI runtime build: unknown provider: %s\n' "$root" >&2
      return 1
    fi
    eci_runtime_ensure_pair "$provider" "${pair_tools[@]}" || return 1
  fi
  trap '[ -z "$temporary" ] || rm -rf -- "$temporary"' EXIT
  for name in "$@"; do
    case "$name" in
      eci-command-gate-mode|eci-worker-git) binary="$root/bin/$name" ;;
      eci-command-plan|eci-safe-import|eci-hook-helper) continue ;;
      *) printf 'ECI runtime build: unknown tool: %s\n' "$name" >&2; return 1 ;;
    esac
    [ ! -x "$binary" ] || continue
    module="$root/hooks/lib/$name-go"
    if [ ! -f "$module/go.mod" ]; then
      printf 'ECI runtime build: could not build %s: source module missing: %s\n' "$name" "$module" >&2
      return 1
    fi
    # Concurrent session starts serialize per module and recheck after waiting.
    exec {lock_fd}>"$module/.eci-go-build.lock" || return 1
    eci_file_lock_acquire_fd "$lock_fd" blocking || return 1
    if [ ! -x "$binary" ]; then
      compiler="$(command -v go)" || {
        printf 'ECI runtime build: could not build %s: install Go and put go on PATH\n' "$name" >&2
        return 1
      }
      temporary="$(mktemp -d "$module/.eci-go-build.XXXXXX")" || return 1
      printf 'Building missing Go tool: %s\n' "$binary" >&2
      if ! (
        cd -- "$module" &&
          env -u GOOS -u GOARCH -u GOARM -u GOAMD64 \
            GOWORK=off GOFLAGS= CGO_ENABLED=0 "$compiler" build \
            -mod=readonly -trimpath -buildvcs=false -o "$temporary/$name" . >&2
      ); then
        printf 'ECI runtime build: could not build %s from %s\n' "$name" "$module" >&2
        return 1
      fi
      mkdir -p -- "${binary%/*}" && chmod -- 755 "$temporary/$name" &&
        mv -f -- "$temporary/$name" "$binary" || return 1
      rmdir -- "$temporary" || return 1
      temporary=''
    fi
    eci_file_lock_release_fd "$lock_fd" || return $?
  done
)
