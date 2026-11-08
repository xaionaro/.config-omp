#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_BASE="$(eci_realpath_existing "${ECI_TMPDIR:-${HOME:?}/tmp}")"
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

# Keep the hook contract explicit: validate-bash runs the validated planner once through
# eci_runtime_verexec (hooks/lib/eci-native-binary.sh): on Linux bin/eci-verexec verifies it and
# execveat()s the verified descriptor; on macOS the verified private copy runs. It never execs the
# planner by pathname (hooks/validate-bash.sh).
[ "$(grep -c -F 'eci_runtime_verexec eci-command-plan - -- --hook' "$ROOT/hooks/validate-bash.sh")" = 1 ] &&
  ! grep -q -e 'ECI_RUNTIME_BINARY_EXEC' -e 'bin/eci-command-plan' "$ROOT/hooks/validate-bash.sh" || {
    printf '%s\n' 'validate-bash does not run the planner exactly once through eci_runtime_verexec' >&2
    exit 1
  }

printf '%s\n' 'ECI planner fd handoff: PASS'
