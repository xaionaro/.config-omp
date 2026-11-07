#!/usr/bin/env bash
# Installs the ECI launcher as symlinks at ~/.local/bin/eci and ~/bin/eci, and
# eci-active as a symlink at ~/.local/bin/eci-active. Run once after checkout,
# after hooks/build-native-binaries.sh has built ~/.omp/agent/eci/bin/eci-active:
#   bash ~/.omp/agent/eci/launchers/install-launchers.sh
# Idempotent. Refuses to clobber any existing non-matching file or symlink.
# This step does not enable hooks.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
rc=0

# link_command LINK TARGET links LINK to TARGET. TARGET must already exist, so
# no link dangles before the build has produced it. Errexit is suspended for a
# function called in an || list, so each fallible step returns explicitly.
link_command() {
  local link="$1" target="$2"
  if [ ! -x "$target" ]; then
    echo "missing $target; run hooks/build-native-binaries.sh first" >&2
    return 1
  fi
  if [ -L "$link" ] && [ "$(readlink -f "$link")" = "$(readlink -f "$target")" ]; then
    echo "ok $link"
    return 0
  fi
  if [ -e "$link" ] || [ -L "$link" ]; then
    echo "refuse $link" >&2
    return 1
  fi
  mkdir -p "$(dirname "$link")" || return 1
  ln -s "$target" "$link" || return 1
  echo "linked $link"
}

link_command "$HOME/.local/bin/eci" "$script_dir/eci" || rc=1
link_command "$HOME/bin/eci" "$script_dir/eci" || rc=1
link_command "$HOME/.local/bin/eci-active" "$HOME/.omp/agent/eci/bin/eci-active" || rc=1

exit "$rc"
