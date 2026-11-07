#!/usr/bin/env bash
# Installs the ECI launcher as symlinks at ~/.local/bin/eci and ~/bin/eci.
# Run once after checkout:  bash ~/.omp/agent/eci/launchers/install-launchers.sh
# Idempotent. Refuses to clobber any existing non-matching file or symlink.
# This step does not enable hooks.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
src="$script_dir/eci"
rc=0

for target in "$HOME/.local/bin/eci" "$HOME/bin/eci"; do
  if [ -L "$target" ] && [ "$(readlink -f "$target")" = "$(readlink -f "$src")" ]; then
    echo "ok $target"
    continue
  fi
  if [ -e "$target" ] || [ -L "$target" ]; then
    echo "refuse $target" >&2
    rc=1
    continue
  fi
  mkdir -p "$(dirname "$target")"
  ln -s "$src" "$target"
  echo "linked $target"
done

exit "$rc"
