#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-edit-dispatch-json.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

dispatch_root="$TMP_ROOT/hooks"
mkdir -p "$dispatch_root"
cp -a -- "$ROOT/hooks/lib" "$dispatch_root/lib"
cp -- "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$dispatch_root/pretooluse-edit-dispatch.sh"

# Exercise the private dispatcher with a line-2 bypass removed if present,
# and preserve the source bytes.
cp -- "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$TMP_ROOT/dispatcher.before"
eci_sed_inplace '2{/^exit 0$/d;}' "$dispatch_root/pretooluse-edit-dispatch.sh"
cmp -- "$TMP_ROOT/dispatcher.before" "$ROOT/hooks/pretooluse-edit-dispatch.sh" || {
  printf '%s\n' 'test modified the dispatcher source' >&2
  exit 1
}
cmp -- "$dispatch_root/pretooluse-edit-dispatch.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/dispatcher.before")

cat >"$dispatch_root/validate-edit-write.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "${FAKE_DISPATCH_MODE:-allow}" in
  fields)
    mapfile -d '' -t fields <"${ECI_EDIT_DISPATCH_FIELDS:?}"
    [ "${#fields[@]}" -eq 8 ] && [ "${fields[3]}" = notes.txt ] &&
      [ "${fields[6]}" = yes ] && [ "${fields[7]}" = ok ] || exit 17
    printf '%s\n' ok >"${FIELDS_CHECK_RESULT:?}"
    ;;
  allow) exit 0 ;;
  allow-envelope)
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"advisory allow"}}'
    ;;
  deny)
    printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"synthetic validator denial"}}'
    ;;
  malformed) printf '%s\n' 'not provider JSON' ;;
  multi)
    printf '%s\n' \
      '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"first"}}' \
      '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"second"}}'
    ;;
  fail)
    printf '%s\n' 'validator diagnostic stays on stderr' >&2
    exit 17
    ;;
  ownership-unknown)
    printf '%s\n' 'ownership could not be determined' >&2
    exit 0
    ;;
  *) exit 19 ;;
esac
EOF
cat >"$dispatch_root/eci-active-gate.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exit 0
EOF
chmod 755 "$dispatch_root/validate-edit-write.sh" "$dispatch_root/eci-active-gate.sh"

input="$TMP_ROOT/input.json"
jq -cn --arg cwd "$ROOT" '{session_id:"t00-dispatch",cwd:$cwd,tool_name:"Edit",tool_input:{file_path:"notes.txt",old_string:"old",new_string:"new"}}' >"$input"

run_case() {
  local mode="$1" expected="$2" output error status
  output="$TMP_ROOT/$mode.out"
  error="$TMP_ROOT/$mode.err"
  set +e
  FAKE_DISPATCH_MODE="$mode" FIELDS_CHECK_RESULT="$TMP_ROOT/fields-ok" ENABLE_SECURITY_REMINDER=0 \
    bash "$dispatch_root/pretooluse-edit-dispatch.sh" <"$input" >"$output" 2>"$error"
  status=$?
  set -e
  [ "$status" -eq 0 ] || {
    printf 'dispatcher case %s exited %s\n' "$mode" "$status" >&2
    return 1
  }
  case "$expected" in
    empty)
      [ ! -s "$output" ] || { cat "$output" >&2; return 1; }
      ;;
    envelope)
      jq -s -e '
        (length == 1) and
        (.[0] |
          type == "object" and
          (keys | sort) == ["hookSpecificOutput"] and
          .hookSpecificOutput.hookEventName == "PreToolUse" and
          .hookSpecificOutput.permissionDecision == "deny" and
          (.hookSpecificOutput.permissionDecisionReason | type == "string") and
          ((.hookSpecificOutput.permissionDecisionReason | test("override|permissive|escape hatch"; "i")) | not)
        )
      ' "$output" >/dev/null || { cat "$output" >&2; return 1; }
      ;;
  esac
}

run_home_authority_case() {
  local label="$1"
  shift
  local output="$TMP_ROOT/$label.out"
  local error="$TMP_ROOT/$label.err"
  local status
  set +e
  env "$@" ENABLE_SECURITY_REMINDER=0 \
    bash "$dispatch_root/pretooluse-edit-dispatch.sh" <"$input" >"$output" 2>"$error"
  status=$?
  set -e
  [ "$status" -eq 0 ] || {
    printf 'dispatcher %s-HOME case exited %s\n' "$label" "$status" >&2
    return 1
  }
  [ ! -s "$output" ] || {
    printf 'dispatcher %s-HOME case unexpectedly blocked a normal edit:\n' "$label" >&2
    cat -- "$output" >&2
    return 1
  }
  [ ! -s "$error" ] || {
    printf 'dispatcher %s-HOME case wrote unexpected stderr:\n' "$label" >&2
    cat -- "$error" >&2
    return 1
  }
}

run_home_authority_case missing -u HOME
run_home_authority_case relative HOME=relative

run_case allow empty
run_case fields empty
[ -f "$TMP_ROOT/fields-ok" ] || { printf '%s\n' 'dispatcher fields were not validated' >&2; exit 1; }
run_case allow-envelope empty
run_case deny envelope
run_case malformed empty
run_case multi empty
run_case fail empty
run_case ownership-unknown empty
[ ! -s "$TMP_ROOT/fail.err" ]
[ ! -s "$TMP_ROOT/ownership-unknown.err" ]

printf '%s\n' 'pretooluse edit-dispatch JSON assertions: PASS'
