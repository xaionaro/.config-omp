#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(realpath -m -- "$(mktemp -d "${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}/eci-safe-import-lifecycle.XXXXXX")")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

proof_root="$TMP_ROOT/proof"
cwd="$TMP_ROOT/work"
outer="$TMP_ROOT/outer"
repo="$outer/member"
mkdir -p -- "$proof_root" "$cwd" "$repo"

git -C "$repo" init -q
git -C "$repo" config user.name 'ECI safe importer test'
git -C "$repo" config user.email 'eci-safe-import-test@example.invalid'
printf '%s\n' baseline >"$repo/tracked.txt"
git -C "$repo" add -- tracked.txt
git -C "$repo" commit -qm baseline

source_one="$TMP_ROOT/source-one"
source_two="$TMP_ROOT/source-two"
printf '%s\n' 'first opened source' >"$source_one"
printf '%s\n' 'second opened source' >"$source_two"
source_alias_directory="$TMP_ROOT/source-alias-directory"
source_alias_parent="$TMP_ROOT/source-alias-parent"
source_alias_final="$TMP_ROOT/source-alias-final"
mkdir -p -- "$source_alias_directory"
printf '%s\n' 'ordinary aliased source' >"$source_alias_directory/source"
ln -s -- "$source_alias_directory" "$source_alias_parent"
ln -s -- "$source_alias_directory/source" "$source_alias_final"

run_active() {
  local sid="$1" workdir="$2"
  shift 2

  (
    cd -- "$workdir"
    ECI_HOME="$ROOT" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$sid" \
      TMPDIR="$TMP_ROOT" "$ROOT/bin/eci-active" "$@"
  )
}

aggregate_session() {
  local sid="$1" plan="$TMP_ROOT/$sid.plan"

  jq -cn --arg repo "$repo" \
    '{repositories:[{id:"member",repo_root:$repo}]}' >"$plan"
  run_active "$sid" "$outer" on 'aggregate safe importer fixture' >/dev/null
  run_active "$sid" "$outer" aggregate-migrate "$plan" >/dev/null
}

assert_existing_regular_rewrite() {
  local route="$1" sid="safe-import-existing-$1" session_dir destination

  case "$route" in
  wait)
    run_active "$sid" "$cwd" on 'wait safe importer fixture' >/dev/null
    run_active "$sid" "$cwd" wait "$source_one" >/dev/null
    run_active "$sid" "$cwd" wait "$source_two" >/dev/null
    destination="$proof_root/$sid/eci_user_owned_wait.md"
    ;;
  wait-repair)
    run_active "$sid" "$cwd" on 'wait repair safe importer fixture' >/dev/null
    run_active "$sid" "$cwd" wait-repair "$source_one" >/dev/null
    run_active "$sid" "$cwd" wait-repair "$source_two" >/dev/null
    destination="$proof_root/$sid/eci_user_owned_wait.md"
    ;;
  singleton-manifest)
    run_active "$sid" "$cwd" on 'singleton safe importer fixture' >/dev/null
    run_active "$sid" "$cwd" manifest-write "$source_one" >/dev/null
    run_active "$sid" "$cwd" manifest-write "$source_two" >/dev/null
    destination="$proof_root/$sid/eci-required-critics.json"
    ;;
  aggregate-manifest)
    aggregate_session "$sid"
    run_active "$sid" "$repo" aggregate-manifest-write member "$source_one" >/dev/null
    run_active "$sid" "$repo" aggregate-manifest-write member "$source_two" >/dev/null
    destination="$proof_root/$sid/eci-aggregate.member.required-critics.json"
    ;;
  *)
    printf 'unknown existing-destination route=%s\n' "$route" >&2
    exit 1
    ;;
  esac

  cmp -s -- "$source_two" "$destination" || {
    printf 'existing regular fixed leaf was not rewritten through route=%s\n' "$route" >&2
    exit 1
  }
}

assert_source_alias_import() {
  local route="$1" alias_kind="$2" sid="safe-import-alias-$route-$alias_kind" destination source

  case "$alias_kind" in
  parent) source="$source_alias_parent/source" ;;
  final) source="$source_alias_final" ;;
  *)
    printf 'unknown source alias kind=%s\n' "$alias_kind" >&2
    exit 1
    ;;
  esac

  case "$route" in
  wait)
    run_active "$sid" "$cwd" on 'wait source alias fixture' >/dev/null
    run_active "$sid" "$cwd" wait "$source" >/dev/null
    destination="$proof_root/$sid/eci_user_owned_wait.md"
    ;;
  wait-repair)
    run_active "$sid" "$cwd" on 'wait repair source alias fixture' >/dev/null
    run_active "$sid" "$cwd" wait-repair "$source" >/dev/null
    destination="$proof_root/$sid/eci_user_owned_wait.md"
    ;;
  singleton-manifest)
    run_active "$sid" "$cwd" on 'singleton source alias fixture' >/dev/null
    run_active "$sid" "$cwd" manifest-write "$source" >/dev/null
    destination="$proof_root/$sid/eci-required-critics.json"
    ;;
  aggregate-manifest)
    aggregate_session "$sid"
    run_active "$sid" "$repo" aggregate-manifest-write member "$source" >/dev/null
    destination="$proof_root/$sid/eci-aggregate.member.required-critics.json"
    ;;
  *)
    printf 'unknown source-alias route=%s\n' "$route" >&2
    exit 1
    ;;
  esac

  cmp -s -- "$source_alias_directory/source" "$destination" || {
    printf 'source alias was not copied through route=%s alias=%s\n' "$route" "$alias_kind" >&2
    exit 1
  }
}

assert_unsafe_destination_is_preserved() {
  local route="$1" kind="$2" sid="safe-import-$route-$kind" session_dir destination foreign

  case "$route" in
  wait|wait-repair)
    run_active "$sid" "$cwd" on "safe importer $route $kind fixture" >/dev/null
    session_dir="$proof_root/$sid"
    destination="$session_dir/eci_user_owned_wait.md"
    ;;
  singleton-manifest)
    run_active "$sid" "$cwd" on "safe importer singleton $kind fixture" >/dev/null
    session_dir="$proof_root/$sid"
    destination="$session_dir/eci-required-critics.json"
    ;;
  aggregate-manifest)
    aggregate_session "$sid"
    session_dir="$proof_root/$sid"
    destination="$session_dir/eci-aggregate.member.required-critics.json"
    ;;
  *)
    printf 'unknown unsafe-destination route=%s\n' "$route" >&2
    exit 1
    ;;
  esac

  foreign="$TMP_ROOT/$sid.foreign"
  printf 'foreign %s %s bytes\n' "$route" "$kind" >"$foreign"
  case "$kind" in
  symlink) ln -s -- "$foreign" "$destination" ;;
  directory) mkdir -- "$destination" ;;
  hardlink) ln -- "$foreign" "$destination" ;;
  *)
    printf 'unknown unsafe-destination kind=%s\n' "$kind" >&2
    exit 1
    ;;
  esac

  case "$route" in
  wait) if run_active "$sid" "$cwd" wait "$source_one" >/dev/null 2>&1; then false; else true; fi ;;
  wait-repair) if run_active "$sid" "$cwd" wait-repair "$source_one" >/dev/null 2>&1; then false; else true; fi ;;
  singleton-manifest) if run_active "$sid" "$cwd" manifest-write "$source_one" >/dev/null 2>&1; then false; else true; fi ;;
  aggregate-manifest) if run_active "$sid" "$repo" aggregate-manifest-write member "$source_one" >/dev/null 2>&1; then false; else true; fi ;;
  esac || {
    printf 'lifecycle importer accepted unsafe %s fixed leaf through route=%s\n' "$kind" "$route" >&2
    exit 1
  }

  case "$kind" in
  symlink)
    [ -L "$destination" ] && grep -Fqx "foreign $route $kind bytes" "$foreign" || exit 1
    ;;
  directory)
    [ -d "$destination" ] && [ ! -L "$destination" ] || exit 1
    ;;
  hardlink)
    cmp -s -- "$foreign" "$destination" && [ "$(stat -c '%h' -- "$foreign")" -eq 2 ] || exit 1
    ;;
  esac
}

for route in wait wait-repair singleton-manifest aggregate-manifest; do
  assert_existing_regular_rewrite "$route"
done

for route in wait wait-repair singleton-manifest aggregate-manifest; do
  for alias_kind in parent final; do
    assert_source_alias_import "$route" "$alias_kind"
  done
done

for route in wait wait-repair singleton-manifest aggregate-manifest; do
  for kind in symlink directory hardlink; do
    assert_unsafe_destination_is_preserved "$route" "$kind"
  done
done

printf '%s\n' 'ECI safe importer lifecycle integration: PASS'
