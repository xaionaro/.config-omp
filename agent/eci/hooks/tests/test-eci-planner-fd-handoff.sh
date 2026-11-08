#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_BASE="$(realpath -e -- "${ECI_TMPDIR:-${HOME:?}/tmp}")"
case "$TMP_BASE" in
  /tmp|/tmp/*|/) printf 'planner fd handoff test: unsafe temporary root: %s\n' "$TMP_BASE" >&2; exit 1 ;;
esac
TMP_ROOT="$(mktemp -d "$TMP_BASE/eci-planner-fd-handoff.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

planner="$TMP_ROOT/eci-command-plan"
replacement="$TMP_ROOT/eci-command-plan.replacement"

printf '%s\n' '#!/usr/bin/env bash' 'printf pinned-planner' >"$planner"
printf '%s\n' '#!/usr/bin/env bash' 'printf replacement-planner' >"$replacement"
chmod -- 755 "$planner" "$replacement"

# A path replacement after validation must not change the executable selected
# for this callback. The descriptor remains inherited by the child process,
# so /proc/self/fd/N executes the original inode rather than the new pathname.
exec {planner_fd}<"$planner"
mv -- "$replacement" "$planner"
[ "$(/proc/self/fd/"$planner_fd")" = pinned-planner ] || {
  printf '%s\n' 'planner fd handoff executed the replacement rather than the pinned inode' >&2
  exit 1
}
[ "$("$planner")" = replacement-planner ] || {
  printf '%s\n' 'planner fd handoff fixture did not replace the planner pathname' >&2
  exit 1
}
closed_fd="$planner_fd"
exec {planner_fd}<&-
[ ! -e "/proc/self/fd/$closed_fd" ] || {
  printf '%s\n' 'planner fd handoff did not close the inherited descriptor' >&2
  exit 1
}

# Keep the hook contract explicit: validate-bash opens the validated planner once through
# eci_runtime_open_verified (hooks/lib/eci-native-binary.sh) and execs it through
# ECI_RUNTIME_BINARY_EXEC: /proc/self/fd/N on Linux, the verified private copy on macOS. It
# never execs the planner by pathname (hooks/validate-bash.sh).
open_line="$(grep -n -F 'eci_runtime_open_verified eci-command-plan' "$ROOT/hooks/validate-bash.sh" | cut -d: -f1)"
exec_line="$(grep -n -F '"$ECI_RUNTIME_BINARY_EXEC" --hook' "$ROOT/hooks/validate-bash.sh" | cut -d: -f1)"
[[ "$open_line" =~ ^[0-9]+$ ]] && [[ "$exec_line" =~ ^[0-9]+$ ]] &&
  [ "$open_line" -lt "$exec_line" ] || {
    printf '%s\n' 'validate-bash does not open the planner descriptor before exec through /proc/self/fd' >&2
    exit 1
  }

printf '%s\n' 'ECI planner fd handoff: PASS'
