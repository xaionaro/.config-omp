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
  local name="$1" path="$2" validator="$3" active="$4" expected="$5" output="$tmp/$1.out" error="$tmp/$1.err"
  jq -cn --arg cwd "$tmp/repo" --arg path "$path" '{tool_name:"Write",session_id:"fixture",cwd:$cwd,tool_input:{file_path:$path,content:"update"}}' |
    ECI_PROOF_ROOT="$tmp/proof" TMPDIR="$tmp" ENABLE_SECURITY_REMINDER=0 FAIL_VALIDATOR="$validator" FAIL_ACTIVE="$active" bash "$hooks/pretooluse-edit-dispatch.sh" >"$output" 2>"$error"
  case "$expected" in
    allow) [ ! -s "$output" ] && grep -Fxq 'ECI_EDIT_GATE_FAILED_ALLOWED validate-edit-write.sh' "$error" ;;
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
printf '%s\n' 'edit guard failure assertions: PASS'
