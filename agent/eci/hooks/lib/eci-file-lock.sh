#!/usr/bin/env bash

# Acquire a lock on an already-open descriptor with the host's native utility.
# Darwin's descriptor form of lockf and Linux flock both retain the lock while
# the caller keeps its descriptor open.
# Descriptors must be canonical decimal numbers >= 3. Darwin's command cleanup
# closes its standard streams before unlocking, so those cannot be lock owners.
eci_file_lock_acquire_fd() {
  local descriptor="${1:-}" mode="${2:-}" platform

  [[ "$descriptor" =~ ^([3-9]|[1-9][0-9]+)$ ]] || return 2
  case "$mode" in blocking|nonblocking) ;; *) return 2 ;; esac
  platform="$(command uname -s)" || return $?

  case "$platform:$mode" in
    Darwin:blocking) command lockf -s "$descriptor" ;;
    Darwin:nonblocking) command lockf -s -t 0 "$descriptor" ;;
    Linux:blocking) command flock -x "$descriptor" ;;
    Linux:nonblocking) command flock -n -x "$descriptor" ;;
    *)
      printf 'unsupported file-lock platform or mode: %s/%s\n' "$platform" "$mode" >&2
      return 1
      ;;
  esac
}

# Explicitly unlock before closing: inherited or duplicated descriptors may
# otherwise retain the lock after this caller releases its descriptor.
eci_file_lock_release_fd() {
  local descriptor="${1:-}" platform status=0 close_status
  [[ "$descriptor" =~ ^([3-9]|[1-9][0-9]+)$ ]] || return 2
  platform="$(command uname -s)" || status=$?
  if [ "$status" -eq 0 ]; then
    case "$platform" in
      Darwin)
        # Apple's descriptor command form unlocks in its cleanup handler.
        # Do not wait if this is cleanup after an unsuccessful acquisition.
        command lockf -s -t 0 "/dev/fd/$descriptor" /usr/bin/true || status=$?
        ;;
      Linux) command flock -u "$descriptor" || status=$? ;;
      *)
        printf 'unsupported file-lock platform: %s\n' "$platform" >&2
        status=1
        ;;
    esac
  fi
  eval "exec ${descriptor}>&-" || {
    close_status=$?
    [ "$status" -ne 0 ] || status="$close_status"
  }
  return "$status"
}
