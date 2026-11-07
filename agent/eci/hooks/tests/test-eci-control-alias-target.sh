#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp="$(mktemp -d)"
tmp="$(cd "$tmp" && pwd -P)"
trap 'rm -rf -- "$tmp"' EXIT
export ECI_PROOF_ROOT="$tmp/proof"
. "$ROOT/hooks/lib/eci-proof-state.sh"

mkdir -p "$ECI_PROOF_ROOT/current" "$ECI_PROOF_ROOT/foreign" "$ECI_PROOF_ROOT/inactive/history"
for sid in current foreign; do
  printf 'scope: alias fixture\ncwd: %s\nsession_id: %s\n' "$ROOT" "$sid" >"$ECI_PROOF_ROOT/$sid/eci_active"
done
printf ordinary >"$tmp/ordinary"
printf control >"$ECI_PROOF_ROOT/foreign/goal_state"
ln "$ECI_PROOF_ROOT/foreign/goal_state" "$tmp/foreign-alias"
ln "$ECI_PROOF_ROOT/current/eci_active" "$tmp/current-alias"
printf hidden >"$ECI_PROOF_ROOT/foreign/.eci-permissive-mode.pending"
ln "$ECI_PROOF_ROOT/foreign/.eci-permissive-mode.pending" "$tmp/hidden-alias"
printf historical >"$ECI_PROOF_ROOT/inactive/history/goal_state"
ln "$ECI_PROOF_ROOT/inactive/history/goal_state" "$tmp/historical-alias"
printf shared >"$tmp/shared"
ln "$tmp/shared" "$tmp/shared-alias"
mkdir -p "$ECI_PROOF_ROOT/legacy"
printf 'cwd: %s\nsession_id: legacy\nlegacy_note: %5000s\n' "$ROOT" historical >"$ECI_PROOF_ROOT/legacy/eci_active"
ln "$ECI_PROOF_ROOT/legacy/eci_active" "$tmp/legacy-marker-alias"
printf control >"$ECI_PROOF_ROOT/legacy/goal_state"
ln "$ECI_PROOF_ROOT/legacy/goal_state" "$tmp/legacy-control-alias"
for ((index = 0; index < 2050; index++)); do
  printf control >"$ECI_PROOF_ROOT/foreign/eci-required-critics.json.$index"
done

# Record traversal attempts without changing their result. Ordinary edits
# must not enumerate proof storage, and shared targets must not use find.
find() {
  printf '%s\n' find >>"$tmp/find-calls"
  command find "$@"
}
eval "$(declare -f eci_proof_root | sed '1s/eci_proof_root/eci_proof_root_original/')"
eci_proof_root() {
  printf '%s\n' root >>"$tmp/root-calls"
  eci_proof_root_original
}
expect_ordinary() {
  if eci_path_is_eci_control_alias "$1"; then
    printf 'ordinary target incorrectly protected: %s\n' "$1" >&2
    exit 1
  fi
}
expect_ordinary "$tmp/ordinary"
if [ -e "$tmp/find-calls" ] || [ -e "$tmp/root-calls" ]; then
  printf '%s\n' 'FAIL: single-link edit enumerated proof storage' >&2
  exit 1
fi
eci_path_is_eci_control_file "$ECI_PROOF_ROOT/foreign/goal_state"
eci_path_is_eci_control_alias "$tmp/foreign-alias"
eci_path_is_eci_control_alias "$tmp/current-alias"
eci_path_is_eci_control_alias "$tmp/hidden-alias"
eci_path_is_eci_control_alias "$tmp/legacy-marker-alias"
eci_path_is_eci_control_alias "$tmp/legacy-control-alias"
expect_ordinary "$tmp/shared-alias"
expect_ordinary "$tmp/historical-alias"
expect_ordinary "$tmp/missing"
[ ! -e "$tmp/find-calls" ]

# Exercise real Write and apply_patch routing for exact controls and aliases.
mkdir "$tmp/bin"
printf '#!/usr/bin/env bash\nprintf find >>"%s/find-executable-calls"\nexit 97\n' "$tmp" >"$tmp/bin/find"
chmod +x "$tmp/bin/find"
export PATH="$tmp/bin:$PATH"
printf ordinary >"$tmp/ordinary.md"
for tool in Write apply_patch; do
  for target in "$tmp/ordinary.md" "$ECI_PROOF_ROOT/foreign/goal_state" "$tmp/foreign-alias" "$tmp/current-alias" "$tmp/hidden-alias" "$tmp/legacy-marker-alias" "$tmp/legacy-control-alias"; do
    if [ "$tool" = Write ]; then
      tool_input="$(jq -cn --arg path "$target" '{file_path:$path,content:"fixture"}')"
    else
      tool_input="$(jq -cn --arg path "$target" '{patch:("*** Begin Patch\n*** Update File: " + $path + "\n@@\n-fixture\n+changed\n*** End Patch") }')"
    fi
    jq -cn --arg tool "$tool" --arg cwd "$ROOT" --argjson tool_input "$tool_input" \
      '{session_id:"current",tool_name:$tool,cwd:$cwd,tool_input:$tool_input}' |
      bash "$ROOT/hooks/eci-active-gate.sh" >"$tmp/hook-output"
    if [ "$target" = "$tmp/ordinary.md" ]; then
      [ ! -s "$tmp/hook-output" ]
    else
      jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$tmp/hook-output" >/dev/null
    fi
  done
done
[ ! -e "$tmp/find-executable-calls" ]
printf '%s\n' 'PASS: ordinary targets avoid proof traversal; current/foreign active aliases protected'
