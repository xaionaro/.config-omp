#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(eci_realpath_canonical "$(mktemp -d "${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}/eci-safe-import-lifecycle.XXXXXX")")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

proof_root="$TMP_ROOT/proof"
cwd="$TMP_ROOT/work"
mkdir -p -- "$proof_root" "$cwd"

source_one="$TMP_ROOT/source-one"
source_two="$TMP_ROOT/source-two"
printf '%s\n' '{"required_critics":["first"]}' >"$source_one"
printf '%s\n' '{"required_critics":["second"]}' >"$source_two"
source_alias_directory="$TMP_ROOT/source-alias-directory"
source_alias_parent="$TMP_ROOT/source-alias-parent"
source_alias_final="$TMP_ROOT/source-alias-final"
mkdir -p -- "$source_alias_directory"
printf '%s\n' '{"required_critics":[]}' >"$source_alias_directory/source"
ln -s -- "$source_alias_directory" "$source_alias_parent"
ln -s -- "$source_alias_directory/source" "$source_alias_final"

run_active() {
  local sid="$1"
  shift

  (
    cd -- "$cwd"
    ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
      TMPDIR="$TMP_ROOT" "$ROOT/bin/eci-active" "$@"
  )
}

# new_session creates the session proof directory that manifest-write publishes into.
new_session() {
  mkdir -p -- "$proof_root/$1"
}

assert_existing_regular_rewrite() {
  local sid="safe-import-existing" destination

  new_session "$sid"
  run_active "$sid" manifest-write "$source_one" >/dev/null
  run_active "$sid" manifest-write "$source_two" >/dev/null
  destination="$proof_root/$sid/eci-required-critics.json"
  cmp -s -- "$source_two" "$destination" || {
    printf '%s\n' 'existing regular manifest was not rewritten' >&2
    exit 1
  }
}

assert_source_alias_import() {
  local alias_kind="$1" sid="safe-import-alias-$1" source

  case "$alias_kind" in
  parent) source="$source_alias_parent/source" ;;
  final) source="$source_alias_final" ;;
  *)
    printf 'unknown source alias kind=%s\n' "$alias_kind" >&2
    exit 1
    ;;
  esac

  new_session "$sid"
  run_active "$sid" manifest-write "$source" >/dev/null
  cmp -s -- "$source_alias_directory/source" "$proof_root/$sid/eci-required-critics.json" || {
    printf 'source alias was not copied: alias=%s\n' "$alias_kind" >&2
    exit 1
  }
}

assert_unsafe_destination_is_preserved() {
  local kind="$1" sid="safe-import-unsafe-$1" destination foreign

  new_session "$sid"
  destination="$proof_root/$sid/eci-required-critics.json"
  foreign="$TMP_ROOT/$sid.foreign"
  printf 'foreign %s bytes\n' "$kind" >"$foreign"
  case "$kind" in
  symlink) ln -s -- "$foreign" "$destination" ;;
  directory) mkdir -- "$destination" ;;
  hardlink) ln -- "$foreign" "$destination" ;;
  *)
    printf 'unknown unsafe-destination kind=%s\n' "$kind" >&2
    exit 1
    ;;
  esac

  if run_active "$sid" manifest-write "$source_one" >/dev/null 2>&1; then
    printf 'manifest-write accepted unsafe %s fixed leaf\n' "$kind" >&2
    exit 1
  fi

  case "$kind" in
  symlink)
    [ -L "$destination" ] && grep -Fqx "foreign $kind bytes" "$foreign" || exit 1
    ;;
  directory)
    [ -d "$destination" ] && [ ! -L "$destination" ] || exit 1
    ;;
  hardlink)
    cmp -s -- "$foreign" "$destination" && [ "$(ls -ld -- "$foreign" | awk '{print $2}')" -eq 2 ] || exit 1
    ;;
  esac
}

assert_existing_regular_rewrite

for alias_kind in parent final; do
  assert_source_alias_import "$alias_kind"
done

for kind in symlink directory hardlink; do
  assert_unsafe_destination_is_preserved "$kind"
done

printf '%s\n' 'ECI safe importer manifest-write integration: PASS'
