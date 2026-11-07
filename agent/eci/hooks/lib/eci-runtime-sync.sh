#!/usr/bin/env bash
# TODO: Reconcile retired ECI lifecycle controls when the remaining marker-dependent hooks migrate.
# Synchronize one provider's hook runtime through an explicit coordinator route.

source "${BASH_SOURCE[0]%/*}/eci-native-binary.sh"
source "${BASH_SOURCE[0]%/*}/eci-file-lock.sh"

eci_runtime_sync_artifact_path() {
  local provider="$1" name="$2" output_root="${3:-$HOME/tmp/eci-runtime}"
  case "$provider" in eci|kimi) ;; *) return 1 ;; esac
  case "$name" in eci-command-plan|eci-safe-import|eci-hook-helper) ;; *) return 1 ;; esac
  printf '%s/%s/%s\n' "$output_root" "$provider" "$name"
}

# Resolve the published Go hook helper, rebuilding it only when absent or
# host-incompatible. Callers treat failure as advisory.
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
  source_root="$(realpath -e -- "$HOME/.omp/agent/eci")" || return 1
  peer_root="$(realpath -e -- "${KIMI_CODE_HOME:-$HOME/.kimi-code}" 2>/dev/null || true)"
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
    resolved_root="$(realpath -e -- "$root")" || return 1
    if [ "$resolved_root" = "$(realpath -e -- "$HOME/.omp/agent/eci")" ]; then
      provider=eci
    elif [ "$resolved_root" = "$(realpath -e -- "${KIMI_CODE_HOME:-$HOME/.kimi-code}")" ]; then
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
      mkdir -p -- "${binary%/*}" && chmod 755 -- "$temporary/$name" &&
        mv -f -- "$temporary/$name" "$binary" || return 1
      rmdir -- "$temporary" || return 1
      temporary=''
    fi
    eci_file_lock_release_fd "$lock_fd" || return $?
  done
)

eci_runtime_sync_fail() {
  local code="$1"
  shift
  printf '[%s] ECI runtime synchronization denied: %s\n' "$code" "$*" >&2
  return 1
}

eci_runtime_sync_tmp_root() {
  local home="${HOME:-}" root
  [ -n "$home" ] || return 1
  root="$home/tmp"
  [ -d "$root" ] || return 1
  root="$(realpath -e -- "$root" 2>/dev/null || true)"
  [ -n "$root" ] || return 1
  # The staging directory is freshly created per run.  A user-selected
  # HOME/tmp -> /tmp alias changes only that scratch location; it does not
  # change the selected provider source or a resolved publish target.
  [ "$root" != / ] || return 1
  printf '%s\n' "$root"
}

eci_runtime_sync_provider_home() {
  local provider="$1" requested_root="$2" home="${HOME:-}" expected_root root

  # The caller may be a deployed or mounted copy of this runtime. Always
  # select the provider's configured source; its spelling is diagnostic, not
  # an admission condition for repair.
  : "$requested_root"
  case "$provider" in
    eci) expected_root="$home/.omp/agent/eci" ;;
    kimi) expected_root="${KIMI_CODE_HOME:-$home/.kimi-code}" ;;
    *) eci_runtime_sync_fail ECI_RUNTIME_SYNC_PROVIDER_UNKNOWN "provider=$provider"; return 1 ;;
  esac
  root="$(realpath -e -- "$expected_root" 2>/dev/null || true)"
  [ -n "$root" ] && [ -d "$root" ] || {
    eci_runtime_sync_fail ECI_RUNTIME_SYNC_SOURCE_INVALID "provider=$provider canonical source=$expected_root could not be resolved"
    return 1
  }
  printf '%s\n' "$root"
}

eci_runtime_sync_resolve_target_root() {
  local provider="$1" requested_root="$2" root

  root="$(realpath -e -- "$requested_root" 2>/dev/null || true)"
  [ -n "$root" ] && [ -d "$root" ] || {
    eci_runtime_sync_fail ECI_RUNTIME_SYNC_TARGET_MISSING "provider=$provider target=$requested_root is unavailable"
    return 1
  }
  case "$provider:$root" in
    eci:*/.omp/agent/eci|kimi:*/.kimi-code) ;;
    *) eci_runtime_sync_fail ECI_RUNTIME_SYNC_TARGET_PROVIDER_MISMATCH "provider=$provider target=$root has the wrong provider directory name"; return 1 ;;
  esac
  printf '%s\n' "$root"
}

eci_runtime_sync_validate_target_root() {
  eci_runtime_sync_resolve_target_root "$@" >/dev/null
}

eci_runtime_sync_validate_source_root() {
  local provider="$1" root="$2" required required_files

  case "$provider" in
    eci) required_files='hooks.json bin/eci-active bin/eci-active-dispatch bin/eci-runtime-sync hooks/validate-bash.sh' ;;
    kimi) required_files='config.toml bin/eci-active bin/eci-active-dispatch bin/eci-runtime-sync hooks/validate-bash.sh' ;;
    *) eci_runtime_sync_fail ECI_RUNTIME_SYNC_PROVIDER_UNKNOWN "provider=$provider"; return 1 ;;
  esac
  for required in $required_files; do
    [ -f "$root/$required" ] || {
      eci_runtime_sync_fail ECI_RUNTIME_SYNC_SOURCE_IDENTITY "provider=$provider source=$root lacks managed runtime file $required"
      return 1
    }
  done
}

eci_runtime_sync_add_root() {
  local provider="$1" candidate="$2" root
  [ -n "$candidate" ] || return 0
  root="$(eci_runtime_sync_resolve_target_root "$provider" "$candidate" 2>/dev/null || true)"
  [ -n "$root" ] || return 0
  case "\n${eci_runtime_sync_roots:-}\n" in
    *$'\n'"$root"$'\n'*) return 0 ;;
  esac
  eci_runtime_sync_roots="${eci_runtime_sync_roots:-}$root
"
}

eci_runtime_sync_discover_targets() {
  local provider="$1" source_root="$2" configured mount candidate relative variable value
  local explicit_roots=""
  local -a configured_roots=()
  eci_runtime_sync_roots=""
  eci_runtime_sync_add_root "$provider" "$source_root"
  case "$provider" in
    eci) variable=ECI_RUNTIME_ROOTS ;;
    kimi) variable=KIMI_RUNTIME_ROOTS ;;
  esac
  configured="${!variable:-${ECI_RUNTIME_ROOTS:-}}"
  if [ -n "$configured" ]; then
    IFS=: read -r -a configured_roots <<<"$configured"
    for value in "${configured_roots[@]}"; do
      candidate="$(eci_runtime_sync_resolve_target_root "$provider" "$value")" || return 1
      explicit_roots="${explicit_roots}${candidate}"$'\n'
      eci_runtime_sync_add_root "$provider" "$candidate"
    done
  fi
  # Derive alternate mount-backed spellings from mountinfo instead of baking a
  # particular /mnt path into policy.  Only existing provider-shaped roots are
  # admitted; unrelated mounts are ignored before any write is attempted.
  relative="${source_root#/}"
  while IFS= read -r mount; do
    [ -n "$mount" ] || continue
    case "$mount" in *[![:print:]]*|*\ *|*\\*) continue ;; esac
    candidate="/$mount/$relative"
    eci_runtime_sync_add_root "$provider" "$candidate"
  done < <(awk '{print $5}' /proc/self/mountinfo 2>/dev/null || true)
  while IFS= read -r root; do
    [ -n "$root" ] || continue
    case "\n$explicit_roots" in
      *$'\n'"$root"$'\n'*) ;;
      *) eci_runtime_sync_validate_target_root "$provider" "$root" || continue ;;
    esac
    printf '%s\n' "$root"
  done <<<"${eci_runtime_sync_roots:-}"
}

eci_runtime_sync_collect() {
  local provider="$1" source_root="$2" stage_root="$3" relative source mode parent sha live_sha live_mode
  : >"$stage_root/manifest.tsv"
  {
    case "$provider" in
      eci) printf '%s\n' hooks.json ;;
      kimi) printf '%s\n' config.toml ;;
      *) eci_runtime_sync_fail ECI_RUNTIME_SYNC_PROVIDER_UNKNOWN "provider=$provider has no registered configuration entrypoint"; return 1 ;;
    esac
    # Both providers register the coordinator lifecycle through this binary;
    # the provider-specific config above is the only root-level difference.
    printf '%s\n' bin/eci-active
    printf '%s\n' bin/eci-active-dispatch
    printf '%s\n' bin/eci-runtime-sync
    [ -f "$source_root/bin/eci-command-gate-mode" ] && printf '%s\n' bin/eci-command-gate-mode
    [ -f "$source_root/bin/eci-worker-git" ] && printf '%s\n' bin/eci-worker-git
    # Planner builds stage short-lived Go files here; pruning this known tree
    # prevents an accidental sync race without excluding ordinary source.
    find "$source_root/hooks" \( -type d -name '.eci-command-plan.txn.*' -o -name '.eci-go-build.*' -o -name '.eci-go-build.lock' \) -prune -o -type f ! -path "$source_root/hooks/tests/*" ! -path '*/__pycache__/*' ! -name '*.pyc' ! -name '*.pyo' ! -name '.eci-command-plan.lock' ! -name '.eci-command-plan.publish.lock' -printf 'hooks/%P\n' 2>/dev/null
  } | LC_ALL=C sort -u | while IFS= read -r relative; do
    [ -n "$relative" ] || continue
    case "$relative" in
      /*|*..*|*[![:print:]]*) eci_runtime_sync_fail ECI_RUNTIME_SYNC_SOURCE_PATH "source=$source_root relative=$relative is not a safe manifest path"; return 1 ;;
    esac
    source="$source_root/$relative"
    [ -f "$source" ] || {
      # A source file disappearing during collection is ordinary concurrent
      # editing, not an authority problem. Leave it for the next sync.
      printf 'ECI runtime maintenance advisory: source disappeared during collection: %s\n' "$source" >&2
      continue
    }
    mode="$(stat -c '%a' -- "$source" 2>/dev/null || true)"
    case "$mode" in
      ''|*[!0-9]*)
        printf 'ECI runtime maintenance advisory: using mode 644 for unreadable source metadata: %s\n' "$source" >&2
        mode=644
        ;;
    esac
    parent="$stage_root/${relative%/*}"
    [ "$parent" = "$stage_root/$relative" ] && parent="$stage_root"
    mkdir -p -- "$parent"
    install -m "$mode" -- "$source" "$stage_root/$relative"
    # The staged file is this run's snapshot. A later live-source edit is
    # ordinary concurrent work for the next sync, not a reason to publish a
    # mixed manifest or fail after publishing some target files.
    sha="$(sha256sum -- "$stage_root/$relative" | awk '{print $1}')"
    if [ -f "$source" ] && [ ! -L "$source" ]; then
      live_sha="$(sha256sum -- "$source" 2>/dev/null | awk '{print $1}')"
      live_mode="$(stat -c '%a' -- "$source" 2>/dev/null || true)"
      if [ "$live_sha" != "$sha" ] || [ "$live_mode" != "$mode" ]; then
        printf 'ECI runtime maintenance advisory: source changed after staging; published snapshot remains current until the next sync: %s\n' "$source" >&2
      fi
    else
      printf 'ECI runtime maintenance advisory: source changed or disappeared after staging; published snapshot remains current until the next sync: %s\n' "$source" >&2
    fi
    printf '%s\t%s\t%s\n' "$relative" "$sha" "$mode" >>"$stage_root/manifest.tsv"
  done
}

eci_runtime_sync_validate_parent() {
  local root="$1" relative="$2" parent="$root" component
  parent="${relative%/*}"
  [ "$parent" = "$relative" ] && return 0
  while [ "$parent" != . ] && [ -n "$parent" ]; do
    component="${parent%%/*}"
    if [ "$component" = "$parent" ]; then
      parent=""
    else
      parent="${parent#*/}"
    fi
  done
  [ -d "$root/${relative%/*}" ] && [ ! -L "$root/${relative%/*}" ] || return 1
  [ "$(realpath -e -- "$root/${relative%/*}" 2>/dev/null || true)" = "$root/${relative%/*}" ]
}

eci_runtime_sync_publish_target() {
  local provider="$1" source_root="$2" target_root="$3" stage_root="$4" target_stage relative sha mode source target parent target_sha target_mode receipt
  local target_tmp="$target_root/.eci-runtime-sync.$$.${RANDOM}"
  receipt="$target_root/.eci-runtime-sync-manifest"
  [ ! -e "$target_tmp" ] && [ ! -L "$target_tmp" ] || {
    eci_runtime_sync_fail ECI_RUNTIME_SYNC_TRANSACTION_EXISTS "provider=$provider target=$target_root transaction=$target_tmp already exists"
    return 1
  }
  mkdir -- "$target_tmp" || {
    eci_runtime_sync_fail ECI_RUNTIME_SYNC_TRANSACTION_CREATE "provider=$provider target=$target_root transaction=$target_tmp could not be created"
    return 1
  }
  if (
    while IFS=$'\t' read -r relative sha mode; do
      source="$stage_root/$relative"
      target="$target_root/$relative"
      parent="${target%/*}"
      # A valid target root may be freshly mounted and contain no copied
      # runtime tree yet. Create missing descendants beneath that root, then
      # retain the alias/type checks for every existing component.
      if [ ! -e "$parent" ] && [ ! -L "$parent" ]; then
        mkdir -p -- "$parent" || {
          eci_runtime_sync_fail ECI_RUNTIME_SYNC_TARGET_PARENT "provider=$provider target=$target parent could not be created"
          return 1
        }
      fi
      [ -d "$parent" ] && [ ! -L "$parent" ] || {
        eci_runtime_sync_fail ECI_RUNTIME_SYNC_TARGET_PARENT "provider=$provider target=$target parent is missing or aliased"
        return 1
      }
      [ "$(realpath -e -- "$parent" 2>/dev/null || true)" = "$parent" ] || {
        eci_runtime_sync_fail ECI_RUNTIME_SYNC_TARGET_PARENT "provider=$provider target=$target parent is not canonical"
        return 1
      }
      if [ -e "$target" ] || [ -L "$target" ]; then
        [ -f "$target" ] && [ ! -L "$target" ] || {
          [ -L "$target" ] || {
            eci_runtime_sync_fail ECI_RUNTIME_SYNC_TARGET_FILE "provider=$provider target=$target is not a replaceable file"
            return 1
          }
        }
      fi
      if [[ "$relative" == */* ]]; then
        mkdir -p -- "$target_tmp/${relative%/*}" || return 1
      fi
      install -m "$mode" -- "$source" "$target_tmp/$relative" || {
        eci_runtime_sync_fail ECI_RUNTIME_SYNC_STAGE_TARGET "provider=$provider target=$target could not be staged"
        return 1
      }
      mv -f -- "$target_tmp/$relative" "$target" || {
        eci_runtime_sync_fail ECI_RUNTIME_SYNC_PUBLISH "provider=$provider source=$source target=$target atomic publish failed"
        return 1
      }
      [ -f "$target" ] && [ ! -L "$target" ] || {
        eci_runtime_sync_fail ECI_RUNTIME_SYNC_VERIFY "provider=$provider target=$target is not a regular file after publish"
        return 1
      }
      target_sha="$(sha256sum -- "$target" | awk '{print $1}')"
      target_mode="$(stat -c '%a' -- "$target" 2>/dev/null || true)"
      [ "$target_sha" = "$sha" ] && [ "$target_mode" = "$mode" ] || {
        eci_runtime_sync_fail ECI_RUNTIME_SYNC_VERIFY "provider=$provider source=$source target=$target digest_or_mode_mismatch source_sha=$sha target_sha=$target_sha source_mode=$mode target_mode=$target_mode"
        return 1
      }
    done <"$stage_root/manifest.tsv"
    # Receipts are descriptive output, never a condition for the managed
    # files above. A stale directory cannot be safely replaced as a leaf, so
    # leave it intact and report the skipped metadata refresh.
    if [ -e "$receipt" ] || [ -L "$receipt" ]; then
      if [ -f "$receipt" ] || [ -L "$receipt" ]; then
        if ! rm -f -- "$receipt"; then
          printf 'ECI runtime maintenance advisory: could not replace target receipt: %s\n' "$receipt" >&2
          receipt=''
        fi
      else
        printf 'ECI runtime maintenance advisory: target receipt is not a replaceable file: %s\n' "$receipt" >&2
        receipt=''
      fi
    fi
    if [ -n "$receipt" ]; then
      if ! install -m 600 -- "$stage_root/manifest.tsv" "$target_tmp/.eci-runtime-sync-manifest" ||
        ! mv -f -- "$target_tmp/.eci-runtime-sync-manifest" "$receipt"; then
        printf 'ECI runtime maintenance advisory: could not publish target receipt: %s\n' "$receipt" >&2
        rm -f -- "$target_tmp/.eci-runtime-sync-manifest" 2>/dev/null || true
      fi
    fi
  ); then
    :
  else
    local status=$?
    rm -rf -- "$target_tmp"
    return "$status"
  fi
  rm -rf -- "$target_tmp"
  printf 'ECI runtime synchronized: provider=%s source=%s target=%s files=%s receipt=%s inode_relation=%s\n' \
    "$provider" "$source_root" "$target_root" "$(wc -l <"$stage_root/manifest.tsv")" "$receipt" \
    "$(if [ "$source_root" -ef "$target_root" ] 2>/dev/null; then printf same-root; else printf separate-root; fi)"
}

eci_runtime_sync_publish_source_receipt() {
  local provider="$1" source_root="$2" stage_root="$3" receipt="$source_root/.eci-runtime-sync-manifest"
  local receipt_tmp="$source_root/.eci-runtime-sync-source.$$.${RANDOM}"
  if [ -e "$receipt_tmp" ] || [ -L "$receipt_tmp" ]; then
    printf 'ECI runtime maintenance advisory: source receipt temporary path already exists: %s\n' "$receipt_tmp" >&2
    return 0
  fi
  if [ -e "$receipt" ] || [ -L "$receipt" ]; then
    if [ -f "$receipt" ] || [ -L "$receipt" ]; then
      if ! rm -f -- "$receipt"; then
        printf 'ECI runtime maintenance advisory: could not replace source receipt: %s\n' "$receipt" >&2
        return 0
      fi
    else
      printf 'ECI runtime maintenance advisory: source receipt is not a replaceable file: %s\n' "$receipt" >&2
      return 0
    fi
  fi
  if ! mkdir -- "$receipt_tmp"; then
    printf 'ECI runtime maintenance advisory: could not stage source receipt: %s\n' "$receipt_tmp" >&2
    return 0
  fi
  if install -m 600 -- "$stage_root/manifest.tsv" "$receipt_tmp/manifest" &&
    mv -f -- "$receipt_tmp/manifest" "$receipt"; then
    rmdir -- "$receipt_tmp"
    printf 'ECI runtime source receipt refreshed: provider=%s source=%s receipt=%s\n' \
      "$provider" "$source_root" "$receipt"
    return 0
  fi
  rm -rf -- "$receipt_tmp"
  printf 'ECI runtime maintenance advisory: could not publish source receipt: %s\n' "$receipt" >&2
  return 0
}

eci_runtime_sync_run() {
  local provider="$1" source_root="$2" tmp_root stage_root target_root targets_file count=0
  source_root="$(eci_runtime_sync_provider_home "$provider" "$source_root")" || return 1
  eci_runtime_sync_validate_source_root "$provider" "$source_root" || return 1
  tmp_root="$(eci_runtime_sync_tmp_root)" || {
    eci_runtime_sync_fail ECI_RUNTIME_SYNC_TMP_INVALID "provider=$provider requires a usable temporary staging directory at ${HOME:-<missing>}/tmp"
    return 1
  }
  stage_root="$(mktemp -d "$tmp_root/eci-runtime-sync.XXXXXX")" || {
    eci_runtime_sync_fail ECI_RUNTIME_SYNC_STAGE_CREATE "provider=$provider could not create staging directory under $tmp_root"
    return 1
  }
  if ! eci_runtime_sync_collect "$provider" "$source_root" "$stage_root"; then
    rm -rf -- "$stage_root"
    return 1
  fi
  targets_file="$stage_root/targets"
  if ! eci_runtime_sync_discover_targets "$provider" "$source_root" >"$targets_file"; then
    rm -rf -- "$stage_root"
    return 1
  fi
  while IFS= read -r target_root; do
    [ -n "$target_root" ] || continue
    [ "$target_root" = "$source_root" ] && continue
    if ! eci_runtime_sync_publish_target "$provider" "$source_root" "$target_root" "$stage_root"; then
      rm -rf -- "$stage_root"
      return 1
    fi
    count=$((count + 1))
  done <"$targets_file"
  if ! eci_runtime_sync_publish_source_receipt "$provider" "$source_root" "$stage_root"; then
    rm -rf -- "$stage_root"
    return 1
  fi
  rm -rf -- "$stage_root"
  printf 'ECI runtime synchronization complete: provider=%s source=%s targets=%s staging=cleaned\n' "$provider" "$source_root" "$count"
}
