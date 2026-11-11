#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-edit-failure.XXXXXX")"
trap 'rm -rf -- "$tmp"' EXIT
hooks="$tmp/hooks"
mkdir -p "$hooks" "$tmp/proof" "$tmp/repo"
cp -a "$ROOT/hooks/." "$hooks/"
# Exercise each real guard's ERR handler with an injected failure in a private copy.
for guard in validate-edit-write eci-active-gate; do
  case "$guard" in
    validate-edit-write) pattern="trap 'edit_guard_error" ;;
    eci-active-gate) pattern="trap '_eci_gate_error_trap" ;;
  esac
  awk -v pattern="$pattern" '{print; if (!injected && index($0, pattern) == 1) {print "false"; injected=1}} END {if (!injected) exit 2}' \
    "$hooks/$guard.sh" >"$hooks/$guard-injected.sh"
  status=0
  ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" bash "$hooks/$guard-injected.sh" </dev/null >"$tmp/$guard.out" 2>"$tmp/$guard.err" || status=$?
  [ "$status" -ne 0 ] && [ ! -s "$tmp/$guard.out" ] && grep -Fq "$guard failed at line" "$tmp/$guard.err" || {
    printf 'guard trap %s did not report failure (exit %s)\n' "$guard" "$status" >&2
    cat "$tmp/$guard.out" "$tmp/$guard.err" >&2
    exit 1
  }
done
# An unusable TMPDIR falls back to ECI_TMPDIR, then /tmp; the real guards still
# run, so a foreign-session target stays denied without a marker.
for fallback in "$tmp" ""; do
  out="$(jq -cn --arg cwd "$tmp/repo" --arg path "$HOME/.agent/context/sessions/other/ordinary.txt" \
      '{tool_name:"Write",session_id:"fixture",cwd:$cwd,tool_input:{file_path:$path,content:"x"}}' |
    ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp/missing-tmp" ECI_TMPDIR="$fallback" ENABLE_SECURITY_REMINDER=0 \
      bash "$hooks/pretooluse-edit-dispatch.sh" 2>"$tmp/tmp-fallback.err")"
  printf '%s' "$out" | jq -e '.hookSpecificOutput.permissionDecision == "deny"' >/dev/null || {
    printf 'unusable TMPDIR (ECI_TMPDIR=%s) skipped the target guards\n' "$fallback" >&2; printf '%s\n' "$out" >&2; cat "$tmp/tmp-fallback.err" >&2; exit 1
  }
done
cat >"$hooks/validate-edit-write.sh" <<'SCRIPT'
#!/usr/bin/env bash
case "${FAIL_VALIDATOR:-0}" in
  1) printf '%s\n' 'synthetic validator failure' >&2; exit 17 ;;
  2) printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"real target denial"}}' ;;
esac
SCRIPT
cat >"$hooks/eci-active-gate.sh" <<'SCRIPT'
#!/usr/bin/env bash
if [ "${FAIL_ACTIVE:-0}" = 1 ]; then printf '%s\n' 'synthetic active failure' >&2; exit 19; fi
SCRIPT
run_case() {
  local name="$1" path="$2" validator="$3" active="$4" expected="$5" tool="${6:-Write}" output="$tmp/$1.out" error="$tmp/$1.err"
  jq -cn --arg tool "$tool" --arg cwd "$tmp/repo" --arg path "$path" '{tool_name:$tool,session_id:"fixture",cwd:$cwd,tool_input:{file_path:$path,content:"update"}}' |
    ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" ENABLE_SECURITY_REMINDER=0 FAIL_VALIDATOR="$validator" FAIL_ACTIVE="$active" bash "$hooks/pretooluse-edit-dispatch.sh" >"$output" 2>"$error"
  case "$expected" in
    allow) [ ! -s "$output" ] && grep -Fxq 'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh' "$error" ;;
    allow-active) [ ! -s "$output" ] && grep -Fxq 'ECI_EDIT_GATE_FAILED_ALLOWED eci-active-gate.sh' "$error" ;;
    deny) jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_EDIT_GATE_FAILED")) and (.hookSpecificOutput.permissionDecisionReason | contains("synthetic validator failure"))' "$output" >/dev/null ;;
    deny-active) jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("ECI_EDIT_GATE_FAILED")) and (.hookSpecificOutput.permissionDecisionReason | contains("synthetic active failure"))' "$output" >/dev/null ;;
    precedence) jq -e '.hookSpecificOutput.permissionDecision == "deny" and .hookSpecificOutput.permissionDecisionReason == "real target denial"' "$output" >/dev/null ;;
  esac || { printf 'failure case %s failed\n' "$name" >&2; cat "$output" "$error" >&2; return 1; }
}
run_case ordinary "$tmp/repo/file.txt" 1 0 allow
mkdir -p "$tmp/proof/other"
ln -s "$tmp/proof/other" "$tmp/repo/proof-alias"
run_case proof-alias "$tmp/repo/proof-alias/ordinary.txt" 1 0 deny
run_case repair-hook "$hooks/eci-active-gate.sh" 1 0 allow
run_case proof "$tmp/proof/other/ordinary.txt" 1 0 deny
run_case default-proof "$HOME/tmp/agent/proof/other/ordinary.txt" 1 0 deny
run_case context "$HOME/.agent/context/sessions/other/ordinary.txt" 1 0 deny
run_case tilde-context '~/.agent/context/sessions/other/ordinary.txt' 1 0 deny
run_case approval "$tmp/repo/.git-commit-approved-once" 1 0 deny
run_case active-guard "$tmp/proof/other/ordinary.txt" 0 1 deny-active
run_case precedence "$tmp/proof/other/ordinary.txt" 2 1 precedence
mkdir -p "$tmp/proof/fixture"
printf 'session_id: fixture\ncwd: %s\n' "$tmp/repo" >"$tmp/proof/fixture/eci_active"
run_case marker "$tmp/repo/file.txt" 1 0 deny
# Under a marker only a failed guard's own source is repairable via Edit/Write.
run_case marker-repair-write "$hooks/validate-edit-write.sh" 1 0 allow
run_case marker-repair-edit "$hooks/validate-edit-write.sh" 1 0 allow Edit
run_case marker-repair-active "$hooks/eci-active-gate.sh" 0 1 allow-active Edit
run_case marker-other-hook "$hooks/eci-active-gate.sh" 1 0 deny
run_case marker-repair-precedence "$hooks/eci-active-gate.sh" 2 1 precedence
run_case marker-repair-lib "$hooks/lib/eci-proof-state.sh" 1 0 allow
# apply_patch routes to its own validator: an update of only its source is
# allowed, while mixed targets and deletes stay denied.
cp "$hooks/validate-edit-write.sh" "$hooks/validate-apply-patch.sh"
for expected in "allow:*** Update File: $hooks/validate-apply-patch.sh" "deny:*** Update File: $hooks/validate-apply-patch.sh
*** Update File: $tmp/repo/file.txt" "deny:*** Delete File: $hooks/validate-apply-patch.sh"; do
  patch="${expected#*:}" expected="${expected%%:*}"
  jq -cn --arg cwd "$tmp/repo" --arg patch "*** Begin Patch
$patch
*** End Patch" '{tool_name:"apply_patch",session_id:"fixture",cwd:$cwd,tool_input:{command:$patch}}' |
    ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" ENABLE_SECURITY_REMINDER=0 FAIL_VALIDATOR=1 bash "$hooks/pretooluse-edit-dispatch.sh" >"$tmp/patch.out" 2>"$tmp/patch.err"
  if [ "$expected" = allow ]; then
    [ ! -s "$tmp/patch.out" ] && grep -Fxq 'ECI_EDIT_GATE_FAILED_ALLOWED validate-apply-patch.sh' "$tmp/patch.err"
  else
    jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$tmp/patch.out" >/dev/null
  fi || { printf 'marker patch not %s: %s\n' "$expected" "$patch" >&2; cat "$tmp/patch.out" "$tmp/patch.err" >&2; exit 1; }
done
# A dispatcher failure denies under a marker, because Codex and Claude Code run
# the edit after a hook exit other than 2; without a marker it keeps its status.
awk '{print; if (!injected && index($0, "trap '\''_dispatch_err_trap") == 1) {print "false"; injected=1}} END {if (!injected) exit 2}' \
  "$hooks/pretooluse-edit-dispatch.sh" >"$hooks/pretooluse-edit-dispatch-injected.sh"
run_injected_dispatcher() {
  local status=0
  jq -cn --arg cwd "$tmp/repo" '{tool_name:"Write",session_id:"fixture",cwd:$cwd,tool_input:{file_path:"file.txt"}}' |
    ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" ENABLE_SECURITY_REMINDER=0 bash "$hooks/pretooluse-edit-dispatch-injected.sh" >"$tmp/injected.out" 2>"$tmp/injected.err" || status=$?
  printf '%s\n' "$status"
}
[ "$(run_injected_dispatcher)" = 0 ] &&
  jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_HOOK_FAIL_CLOSED]"))' "$tmp/injected.out" >/dev/null &&
  grep -Fq 'pretooluse-edit-dispatch failed at line' "$tmp/injected.err" || {
  printf '%s\n' 'dispatcher failure under a marker did not deny' >&2; cat "$tmp/injected.out" "$tmp/injected.err" >&2; exit 1
}
rm -- "$tmp/proof/fixture/eci_active"
[ "$(run_injected_dispatcher)" = 1 ] && [ ! -s "$tmp/injected.out" ] && grep -Fq 'pretooluse-edit-dispatch failed at line' "$tmp/injected.err" || {
  printf '%s\n' 'marker-free dispatcher failure did not keep its status' >&2; cat "$tmp/injected.out" "$tmp/injected.err" >&2; exit 1
}
# A proof-state load-chain lib that fails to load denies everything except
# Edit or Write of exactly one lib in that chain, and the denial names the
# libs and a Bash diagnosis.
lib_case() {
  local dir="$1" expected="$2" tool="$3" path="$4" cwd="${5:-$tmp/repo}"
  jq -cn --arg tool "$tool" --arg cwd "$cwd" --arg path "$path" \
    '{tool_name:$tool,session_id:"fixture",cwd:$cwd,tool_input:{file_path:$path,content:"x",command:"*** Begin Patch\n*** Update File: \($path)\n*** End Patch"}}' |
    ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" ENABLE_SECURITY_REMINDER=0 bash "$dir/pretooluse-edit-dispatch.sh" >"$tmp/lib.out" 2>"$tmp/lib.err" || {
    printf 'broken lib case %s %s %s exited %s\n' "$dir" "$tool" "$path" "$?" >&2; cat "$tmp/lib.out" "$tmp/lib.err" >&2; exit 1
  }
  if [ "$expected" = allow ]; then
    [ ! -s "$tmp/lib.out" ] && grep -Fq 'ECI_HOOK_LIB_REPAIR_ALLOWED' "$tmp/lib.err"
  else
    jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | contains("bash -n") and contains("/lib/eci-portable.sh'\''"))' "$tmp/lib.out" >/dev/null
  fi || { printf 'broken lib case %s %s %s not %s\n' "$dir" "$tool" "$path" "$expected" >&2; cat "$tmp/lib.out" "$tmp/lib.err" >&2; exit 1; }
}
broken="$tmp/broken-hooks"
cp -a "$ROOT/hooks/." "$broken/"
printf '%s\n' 'if then' >>"$broken/lib/eci-proof-state.sh"
lib_case "$broken" allow Write "$broken/lib/eci-proof-state.sh"
lib_case "$broken" allow Edit "$broken/lib/../lib/eci-proof-state.sh"
lib_case "$broken" allow Edit lib/eci-proof-state.sh "$broken"
lib_case "$broken" deny Write "$tmp/repo/file.txt"
lib_case "$broken" deny Write "$broken/lib/eci-tmp.sh"
lib_case "$broken" deny apply_patch "$broken/lib/eci-proof-state.sh"
lib_case "$broken" deny Write "$tmp/proof/missing-parent/x.txt"
lib_case "$broken" deny Edit lib/eci-proof-state.sh "$broken
$tmp/repo"
portable="$tmp/portable-hooks"
cp -a "$ROOT/hooks/." "$portable/"
printf '%s\n' 'if then' >>"$portable/lib/eci-portable.sh"
lib_case "$portable" allow Write "$portable/lib/eci-portable.sh"
lib_case "$portable" deny Write "$tmp/repo/file.txt"
for guard in validate-edit-write eci-active-gate validate-apply-patch; do
  ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" bash "$broken/$guard.sh" </dev/null 2>/dev/null |
    jq -e '.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_HOOK_LIB_UNAVAILABLE]") and contains("lib/eci-proof-state.sh") and contains("bash -n")' >/dev/null || {
    printf 'guard %s lib-load denial is not actionable\n' "$guard" >&2; exit 1
  }
done
# A guard lib-load denial is a guard failure: under a marker the broken shared
# lib is repairable through Write and any other target stays denied; without a
# marker ordinary targets stay editable and protected targets stay denied.
diag="$tmp/diag-hooks"
cp -a "$ROOT/hooks/." "$diag/"
printf '%s\n' 'if then' >>"$diag/lib/eci-diagnostic.sh"
for spec in "marker:allow:$diag/lib/eci-diagnostic.sh" "marker:deny:$tmp/repo/file.txt" \
  "none:allow:$tmp/repo/file.txt" "none:deny:$tmp/proof/other/ordinary.txt"; do
  marker="${spec%%:*}" rest="${spec#*:}"; expected="${rest%%:*}" path="${rest#*:}"
  if [ "$marker" = marker ]; then
    printf 'session_id: fixture\ncwd: %s\n' "$tmp/repo" >"$tmp/proof/fixture/eci_active"
  else
    rm -f -- "$tmp/proof/fixture/eci_active"
  fi
  jq -cn --arg cwd "$tmp/repo" --arg path "$path" '{tool_name:"Write",session_id:"fixture",cwd:$cwd,tool_input:{file_path:$path,content:"x"}}' |
    ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" ENABLE_SECURITY_REMINDER=0 bash "$diag/pretooluse-edit-dispatch.sh" >"$tmp/diag.out" 2>"$tmp/diag.err"
  if [ "$expected" = allow ]; then
    [ ! -s "$tmp/diag.out" ] && grep -Fxq 'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh' "$tmp/diag.err"
  else
    jq -e '.hookSpecificOutput.permissionDecision == "deny" and (.hookSpecificOutput.permissionDecisionReason | startswith("[ECI_EDIT_GATE_FAILED]") and contains("bash -n") and contains("lib/eci-diagnostic.sh"))' "$tmp/diag.out" >/dev/null
  fi || { printf 'broken diagnostic lib case %s not %s\n' "$spec" "$expected" >&2; cat "$tmp/diag.out" "$tmp/diag.err" >&2; exit 1; }
done
printf '%s\n' 'edit guard failure assertions: PASS'
