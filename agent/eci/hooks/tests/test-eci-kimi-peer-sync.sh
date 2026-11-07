#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HOST_TMP="${HOME:?HOME must be set}/tmp"
TMP_BASE="$(realpath -e -- "$HOST_TMP" 2>/dev/null || true)"
case "$TMP_BASE" in
  /tmp|/tmp/*|/) printf 'peer-sync test: unsafe temporary root: %s\n' "$TMP_BASE" >&2; exit 1 ;;
esac

TEST_ROOT="$(mktemp -d "$TMP_BASE/eci-kimi-peer-sync.XXXXXX")"
trap 'rm -rf -- "$TEST_ROOT"' EXIT HUP INT TERM

TEST_HOME="$TEST_ROOT/home"
ECI_HOME="$TEST_HOME/.omp/agent/eci"
KIMI_HOME="$TEST_HOME/.kimi-code"
SYNC="$ECI_HOME/bin/eci-runtime-sync"

mkdir -p -- "$TEST_HOME/tmp" "$ECI_HOME" "$KIMI_HOME/bin" "$KIMI_HOME/hooks/lib"

# The fixture source is a complete canonical ECI runtime authority, while
# the peer target starts with a provider-owned Kimi configuration and stale
# copies of the shared hook/bin surface.
cp -a -- "$ROOT/bin" "$ECI_HOME/bin"
cp -a -- "$ROOT/hooks" "$ECI_HOME/hooks"
cp -a -- "$ROOT/AGENTS.md" "$ROOT/config.toml" "$ROOT/hooks.json" "$ECI_HOME/"

printf '%s\n' 'provider = "kimi"' 'model = "preserve-this-kimi-config"' >"$KIMI_HOME/config.toml"
cp -- "$KIMI_HOME/config.toml" "$TEST_ROOT/kimi-config.before"
for stale_path in \
  bin/eci-active \
  bin/eci-active-dispatch \
  bin/eci-runtime-sync \
  hooks/validate-bash.sh \
  hooks/lib/eci-diagnostic.sh; do
  mkdir -p -- "$KIMI_HOME/${stale_path%/*}"
  printf 'stale shared runtime: %s\n' "$stale_path" >"$KIMI_HOME/$stale_path"
done

planner_binary_relative='hooks/lib/eci-command-plan-go/eci-command-plan'
planner_receipt_relative='hooks/lib/eci-command-plan-go/.eci-command-plan.provenance'
planner_source="$ECI_HOME/$planner_binary_relative"
planner_target="$KIMI_HOME/$planner_binary_relative"
[ -f "$planner_source" ] && [ ! -L "$planner_source" ] || {
  printf 'peer-sync test: source planner binary is missing or unsafe\n' >&2
  exit 1
}
[ -f "$ECI_HOME/$planner_receipt_relative" ] && [ ! -L "$ECI_HOME/$planner_receipt_relative" ] || {
  printf 'peer-sync test: source planner provenance receipt is missing or unsafe\n' >&2
  exit 1
}
mkdir -p -- "${planner_target%/*}"
ln -- "$planner_source" "$planner_target"

shared_runtime_paths() {
  (
    cd -- "$ECI_HOME"
    find bin hooks -type f \
      ! -path 'hooks/tests' ! -path 'hooks/tests/*' \
      ! -path 'hooks/lib/eci-command-plan-go/eci-command-plan' \
      ! -path 'hooks/lib/eci-command-plan-go/.eci-command-plan.provenance' \
      ! -path 'hooks/lib/eci-command-plan-go/.eci-command-plan.publish.lock' \
      ! -path '*/__pycache__/*' \
      ! -name '*.pyc' ! -name '*.pyo' ! -name '*.go' ! -name '*.bak*' \
      -print | LC_ALL=C sort
  )
}

assert_shared_runtime_matches_source() {
  local relative source target

  while IFS= read -r relative; do
    [ -n "$relative" ] || continue
    source="$ECI_HOME/$relative"
    target="$KIMI_HOME/$relative"
    cmp -- "$source" "$target" || {
      printf 'peer-sync test: shared runtime differs: %s\n' "$relative" >&2
      exit 1
    }
    [ "$(stat -c '%a' -- "$source")" = "$(stat -c '%a' -- "$target")" ] || {
      printf 'peer-sync test: shared runtime mode differs: %s\n' "$relative" >&2
      exit 1
    }
  done < <(shared_runtime_paths)
}

assert_peer_receipt() {
  local receipt="$KIMI_HOME/.eci-kimi-peer-sync-manifest"

  [ -f "$receipt" ] && [ ! -L "$receipt" ] || {
    printf 'peer-sync test: peer receipt is missing or unsafe\n' >&2
    exit 1
  }
  [ "$(stat -c '%a' -- "$receipt")" = 600 ] || {
    printf 'peer-sync test: peer receipt mode is not 0600\n' >&2
    exit 1
  }
  grep -Fq $'bin/eci-active\t' "$receipt" || {
    printf 'peer-sync test: peer receipt omitted a managed runtime path\n' >&2
    exit 1
  }
  if grep -Fq "$planner_binary_relative" "$receipt" ||
    grep -Fq "$planner_receipt_relative" "$receipt"; then
    printf 'peer-sync test: peer receipt includes planner-owned assets\n' >&2
    exit 1
  fi
}

apply_output="$TEST_ROOT/peer-apply-output"
if ! HOME="$TEST_HOME" "$SYNC" peer-apply --target "$KIMI_HOME" >"$apply_output" 2>&1; then
  printf 'peer-sync test: peer-apply did not synchronize the shared runtime:\n' >&2
  cat -- "$apply_output" >&2
  exit 1
fi

assert_shared_runtime_matches_source
[ "$planner_source" -ef "$planner_target" ] || {
  printf 'peer-sync test: peer sync broke the planner hard-link invariant\n' >&2
  exit 1
}
[ ! -e "$KIMI_HOME/$planner_receipt_relative" ] && [ ! -L "$KIMI_HOME/$planner_receipt_relative" ] || {
  printf 'peer-sync test: peer sync copied the source-only planner provenance receipt\n' >&2
  exit 1
}
assert_peer_receipt
cmp -- "$TEST_ROOT/kimi-config.before" "$KIMI_HOME/config.toml"
for eci_only_config in AGENTS.md hooks.json; do
  [ ! -e "$KIMI_HOME/$eci_only_config" ] && [ ! -L "$KIMI_HOME/$eci_only_config" ] || {
    printf 'peer-sync test: copied ECI-only configuration into Kimi home: %s\n' "$eci_only_config" >&2
    exit 1
  }
done

# A managed target symlink must be rejected rather than followed. The outside
# file proves the peer route did not overwrite data beyond the Kimi home.
outside="$TEST_ROOT/outside"
printf 'outside data must remain unchanged\n' >"$outside"
cp -- "$outside" "$TEST_ROOT/outside.before"
rm -f -- "$KIMI_HOME/hooks/validate-bash.sh"
ln -s -- "$outside" "$KIMI_HOME/hooks/validate-bash.sh"
unsafe_output="$TEST_ROOT/peer-apply-unsafe-output"
if HOME="$TEST_HOME" "$SYNC" peer-apply --target "$KIMI_HOME" >"$unsafe_output" 2>&1; then
  printf 'peer-sync test: peer-apply accepted a managed target symlink\n' >&2
  exit 1
fi
grep -Fq 'ECI_RUNTIME_SYNC_TARGET_UNSAFE' "$unsafe_output" || {
  printf 'peer-sync test: target-symlink rejection did not use the target safety diagnostic:\n' >&2
  cat -- "$unsafe_output" >&2
  exit 1
}
cmp -- "$TEST_ROOT/outside.before" "$outside"
cmp -- "$TEST_ROOT/kimi-config.before" "$KIMI_HOME/config.toml"

# The peer receipt is itself a managed target boundary: accidental-write
# safety requires rejecting a symlink rather than replacing its outside target.
rm -f -- "$KIMI_HOME/hooks/validate-bash.sh"
cp -- "$ECI_HOME/hooks/validate-bash.sh" "$KIMI_HOME/hooks/validate-bash.sh"
chmod -- "$(stat -c '%a' -- "$ECI_HOME/hooks/validate-bash.sh")" "$KIMI_HOME/hooks/validate-bash.sh"
peer_receipt="$KIMI_HOME/.eci-kimi-peer-sync-manifest"
receipt_outside="$TEST_ROOT/peer-receipt-outside"
printf 'peer receipt outside data must remain unchanged\n' >"$receipt_outside"
cp -- "$receipt_outside" "$TEST_ROOT/peer-receipt-outside.before"
rm -f -- "$peer_receipt"
ln -s -- "$receipt_outside" "$peer_receipt"
receipt_unsafe_output="$TEST_ROOT/peer-apply-receipt-unsafe-output"
if HOME="$TEST_HOME" "$SYNC" peer-apply --target "$KIMI_HOME" >"$receipt_unsafe_output" 2>&1; then
  printf 'peer-sync test: peer-apply accepted a peer receipt symlink\n' >&2
  exit 1
fi
grep -Fq 'ECI_RUNTIME_SYNC_TARGET_UNSAFE' "$receipt_unsafe_output" || {
  printf 'peer-sync test: peer-receipt rejection did not use the target safety diagnostic:\n' >&2
  cat -- "$receipt_unsafe_output" >&2
  exit 1
}
cmp -- "$TEST_ROOT/peer-receipt-outside.before" "$receipt_outside"
cmp -- "$TEST_ROOT/kimi-config.before" "$KIMI_HOME/config.toml"

printf 'eci-kimi-peer-sync: PASS\n'
