#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-security-reminder-opt-in.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT HUP INT TERM

dispatch_root="$TMP_ROOT/hooks"
mkdir -p -- "$dispatch_root"
cp -- "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$dispatch_root/pretooluse-edit-dispatch.sh"
ln -s -- "$ROOT/hooks/lib" "$dispatch_root/lib"
source "$ROOT/hooks/lib/eci-runtime-sync.sh"
helper="$(eci_runtime_hook_helper_path)" || { printf '%s\n' 'eci-hook-helper unavailable' >&2; exit 1; }

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
exit 0
EOF
cat >"$dispatch_root/eci-active-gate.sh" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod -- 755 \
  "$dispatch_root/pretooluse-edit-dispatch.sh" \
  "$dispatch_root/validate-edit-write.sh" \
  "$dispatch_root/eci-active-gate.sh"

input="$TMP_ROOT/risk-review-edit.json"
jq -cn '{
  session_id: "t00-risk-review",
  cwd: "/tmp/t00-risk-review",
  tool_name: "Edit",
  tool_input: {
    file_path: "src/example.js",
    old_string: "old",
    new_string: "eval(dynamicExpression)"
  }
}' >"$input"

assert_empty() {
  local path="$1" label="$2"
  [ ! -s "$path" ] || {
    printf '%s unexpectedly contained output:\n' "$label" >&2
    cat -- "$path" >&2
    return 1
  }
}

default_dispatch_proof="$TMP_ROOT/default-dispatch-proof"
default_dispatch_out="$TMP_ROOT/default-dispatch.out"
default_dispatch_err="$TMP_ROOT/default-dispatch.err"
env -u ENABLE_SECURITY_REMINDER ECI_PROOF_ROOT="$default_dispatch_proof" \
  bash "$dispatch_root/pretooluse-edit-dispatch.sh" <"$input" \
  >"$default_dispatch_out" 2>"$default_dispatch_err"
assert_empty "$default_dispatch_out" 'default dispatcher stdout'
assert_empty "$default_dispatch_err" 'default dispatcher stderr'
[ ! -e "$default_dispatch_proof/security-warnings-t00-risk-review.json" ] || {
  printf '%s\n' 'default dispatcher created optional reminder state' >&2
  exit 1
}

default_direct_proof="$TMP_ROOT/default-direct-proof"
default_direct_out="$TMP_ROOT/default-direct.out"
default_direct_err="$TMP_ROOT/default-direct.err"
env -u ENABLE_SECURITY_REMINDER ECI_PROOF_ROOT="$default_direct_proof" \
  "$helper" security-reminder <"$input" \
  >"$default_direct_out" 2>"$default_direct_err"
assert_empty "$default_direct_out" 'default reminder stdout'
assert_empty "$default_direct_err" 'default reminder stderr'
[ ! -e "$default_direct_proof/security-warnings-t00-risk-review.json" ] || {
  printf '%s\n' 'default reminder created state' >&2
  exit 1
}

opt_in_proof="$TMP_ROOT/explicit-opt-in-proof"
opt_in_out="$TMP_ROOT/explicit-opt-in.out"
opt_in_err="$TMP_ROOT/explicit-opt-in.err"
ENABLE_SECURITY_REMINDER=1 ECI_PROOF_ROOT="$opt_in_proof" \
  bash "$dispatch_root/pretooluse-edit-dispatch.sh" <"$input" \
  >"$opt_in_out" 2>"$opt_in_err"
assert_empty "$opt_in_out" 'explicit opt-in dispatcher stdout'
jq -e '.systemMessage | contains("eval executes arbitrary code")' "$opt_in_err" >/dev/null
jq -e '. == ["src/example.js:eval"]' \
  "$opt_in_proof/security-warnings-t00-risk-review.json" >/dev/null

printf '%s\n' 'security reminder opt-in assertions: PASS'
