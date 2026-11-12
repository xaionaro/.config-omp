#!/usr/bin/env bash
# Published YAML guidance, native edits and actual installed actor/tool paths.
set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-understanding-yaml.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
export HOME="$TMP_ROOT/home"
source "$ROOT/hooks/lib/eci-proof-state.sh"
eci_session_ledger_basename project-understanding.yaml
eci_control_basename project-understanding.yaml
for extension in md json yml; do
  ! eci_session_ledger_basename "project-understanding.$extension"
  ! eci_control_basename "project-understanding.$extension"
done
node -p 'require.resolve("yaml")' >/dev/null
parse() {
  node -e 'const fs=require("fs"), YAML=require("yaml"); const value=YAML.parse(fs.readFileSync(process.argv[1],"utf8"),{uniqueKeys:true,stringKeys:true,maxAliasCount:0}); process.stdout.write(JSON.stringify(value));' "$1"
}
example="$ROOT/../skills/context-ledger/examples/project-understanding.yaml"
parse "$example" > "$TMP_ROOT/before.json"
# Same native record edit on a latest owned snapshot; no YAML serialization.
cp -- "$example" "$TMP_ROOT/edited.yaml"
eci_sed_inplace 's/value: 1000000/value: 2000000/; s/title: "Receiver knowledge"/title: "Receiver configuration"/; s/state: "active"/state: "completed"/; /next_action:/d; /forecast_ref:/d; /state: "completed"/a\
\      completed_at: "2000-01-01T00:00:00Z"' "$TMP_ROOT/edited.yaml"
# Synthetic source confirmation resolves only this fixture unknown.
awk '/^  "unknown:receiver:units":/{drop=1} /^  "verification:receiver:clock":/{drop=0} !drop' "$TMP_ROOT/edited.yaml" > "$TMP_ROOT/no-unknown.yaml"
mv -- "$TMP_ROOT/no-unknown.yaml" "$TMP_ROOT/edited.yaml"
parse "$TMP_ROOT/edited.yaml" > "$TMP_ROOT/after.json"
jq -e --slurpfile before "$TMP_ROOT/before.json" '
  . as $after |
  .schema=="project-understanding/v1" and
  .records["context:receiver:clock"].data=={value:2000000,unit:"Hz"} and
  .records["work:receiver:clock"].data.state=="completed" and
  .records["work:receiver:clock"].data.completed_at=="2000-01-01T00:00:00Z" and
  (.records["work:receiver:clock"].data | has("next_action") or has("forecast_ref") | not) and
  (.records | has("unknown:receiver:units") | not) and
  .sections.hardware.title=="Receiver configuration" and
  .records["requirement:receiver:R1:clock"].data.source_text=="Preserve the receiver clock setting.\nShow the evidence limitations." and
  (["requirement:receiver:R1:clock","outcome:receiver:clock","scope:receiver:clock","constraint:receiver:channels","decision:receiver:proof","guard:receiver:measurement","verification:receiver:clock"] | all(.[]; . as $id | $after.records[$id]==$before[0].records[$id])) and
  ([.records[] | .section as $section | $after.sections | has($section)] | all) and
  ([.records[] | (.links // {})[] | .[] as $id | $after.records | has($id)] | all)
' "$TMP_ROOT/after.json" >/dev/null
head -n 1 "$TMP_ROOT/edited.yaml" | cmp -- - <(head -n 1 "$example")
# Published query: parser errors propagate through Bash pipefail.
for invalid in $'same: 1\nsame: 2' $'1: "numeric key"\n"1": "string key"' $'a: &shared 1\nb: *shared' $'a: 1\n---\nb: 2'; do
  printf '%s\n' "$invalid" > "$TMP_ROOT/invalid.yaml"
  if parse "$TMP_ROOT/invalid.yaml" 2>"$TMP_ROOT/parser.err" | jq . >/dev/null; then
    printf '%s\n' 'invalid YAML unexpectedly accepted by read-only query' >&2; exit 1
  fi
  [ -s "$TMP_ROOT/parser.err" ]
done

# Real canonical CLI uses an isolated owned fixture, not a current live session.
proof_root="$TMP_ROOT/proof"
session_id=t00-understanding-yaml
session_dir="$HOME/.agent/context/sessions/$session_id"
cwd="$TMP_ROOT/repository"
mkdir -p -- "$session_dir" "$proof_root/$session_id" "$proof_root/other" "$cwd"
run_current() {
  (cd -- "$cwd"; ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$session_id" "$ROOT/bin/eci-active" "$@")
}
# TODO: the test exits here: this HOME has no .omp, so eci-active fails with "ECI runtime not found", and its "on" verb is retired (ECI_LIFECYCLE_RETIRED; TEST-DISPOSITION.md). Write the own-session eci_active marker directly, as done below for "other", and give eci-active a HOME that contains .omp.
run_current on 'YAML understanding fixture' >/dev/null
cp -- "$example" "$session_dir/project-understanding.yaml"
cp -- "$example" "$proof_root/other/project-understanding.yaml"
printf 'scope: foreign fixture\ncwd: %s\nsession_id: other\ncreated_utc: 2000-01-01T00:00:00Z\n' "$cwd" > "$proof_root/other/eci_active"
# Exercise the actually selected installed hook; no module-local assumption.
run_bash_hook() {
  local command="$1" role="${2:-worker}"
  jq -cn --arg session "$session_id" --arg cwd "$cwd" --arg command "$command" '{session_id:$session,cwd:$cwd,tool_name:"Bash",tool_input:{command:$command}}' |
    ECI_PROOF_ROOT="$proof_root" ECI_ROLE="$role" ECI_HOOK_IS_SUBAGENT="$([ "$role" = worker ] && printf true || printf false)" bash "$ROOT/hooks/validate-bash.sh"
}
allowed() {
  local output
  output="$(run_bash_hook "$1" "${2:-worker}")"
  [ -z "$output" ] || { printf 'expected allowed: %s\n%s\n' "$1" "$output" >&2; exit 1; }
}
denied() {
  run_bash_hook "$1" | jq -e '.hookSpecificOutput.permissionDecision=="deny"' >/dev/null
}
# TODO: context-ledger no longer publishes this snapshot cp/mv flow; exercise the own-session "yq4 -i" edit here and update the labels at lines 2 and 22.
snapshot_tmp=$(mktemp "$session_dir/.understanding-snapshot.XXXXXX")
allowed "cp '$session_dir/project-understanding.yaml' '$snapshot_tmp'"
cp -- "$session_dir/project-understanding.yaml" "$snapshot_tmp"
eci_sed_inplace 's/value: 1000000/value: 2000000/' "$snapshot_tmp"
allowed "mv '$snapshot_tmp' '$session_dir/project-understanding.yaml'"
mv -- "$snapshot_tmp" "$session_dir/project-understanding.yaml"
parse "$session_dir/project-understanding.yaml" | jq -e '.records["context:receiver:clock"].data.value==2000000' >/dev/null
denied "touch '$proof_root/other/project-understanding.yaml'"
for extension in md json yml; do
  allowed "touch '$proof_root/other/project-understanding.$extension'"
done
ln -s -- "$proof_root/other/project-understanding.yaml" "$cwd/foreign-yaml-alias"
denied "touch '$cwd/foreign-yaml-alias'"
ln -- "$proof_root/other/eci_active" "$cwd/control-hardlink"
denied "touch '$cwd/control-hardlink'"
# Foreign canonical handoff hardlink admission predates this rename and is
# excluded; markers retain inode protection. Do not assert a repaired gate.
for tool in Edit Write apply_patch; do
  case "$tool" in
    apply_patch)
      input=$(jq -cn --arg session "$session_id" --arg cwd "$cwd" --arg patch "*** Begin Patch
*** Update File: $session_dir/project-understanding.yaml
@@
-      value: 2000000
+      value: 1000000
*** End Patch" '{session_id:$session,cwd:$cwd,tool_name:"apply_patch",tool_input:{patch:$patch}}')
      validator=pretooluse-edit-dispatch.sh ;;
    *)
      input=$(jq -cn --arg session "$session_id" --arg cwd "$cwd" --arg tool "$tool" --arg path "$session_dir/project-understanding.yaml" --arg content "$(cat "$session_dir/project-understanding.yaml")" '{session_id:$session,cwd:$cwd,tool_name:$tool,tool_input:{file_path:$path,content:$content,old_string:"value: 2000000",new_string:"value: 1000000"}}')
      validator=pretooluse-edit-dispatch.sh ;;
  esac
  output=$(ECI_PROOF_ROOT="$proof_root" ECI_ROLE=coordinator ECI_HOOK_IS_SUBAGENT=false bash "$ROOT/hooks/$validator" <<< "$input")
  [ -z "$output" ] || { printf 'own coordinator %s unexpectedly denied: %s\n' "$tool" "$output" >&2; exit 1; }
  for target in "$proof_root/other/project-understanding.yaml" "$cwd/control-hardlink"; do
    case "$tool" in
      apply_patch)
        foreign_input=$(jq --arg own "$session_dir/project-understanding.yaml" --arg target "$target" '.tool_input.patch |= (split($own) | join($target))' <<< "$input") ;;
      *) foreign_input=$(jq --arg target "$target" '.tool_input.file_path=$target' <<< "$input") ;;
    esac
    ECI_PROOF_ROOT="$proof_root" ECI_ROLE=coordinator ECI_HOOK_IS_SUBAGENT=false bash "$ROOT/hooks/$validator" <<< "$foreign_input" |
      jq -e '.hookSpecificOutput.permissionDecision=="deny"' >/dev/null
  done
done
log="$session_dir/high_level_log.jsonl"
run_current ledger-append --json '{"event":"decision","summary":"YAML native fixture edit verified"}' >/dev/null
cp -- "$log" "$TMP_ROOT/log.before"
run_current ledger-append --json '{"event":"verification_result","summary":"YAML fixture and actor/tool paths checked"}' >/dev/null
head -n 1 "$log" | cmp -- - "$TMP_ROOT/log.before"
jq -e 'select(.event=="verification_result") | .schema=="eci-high-level-log/v1"' "$log" >/dev/null
printf '%s\n' 'eci understanding YAML assertions: PASS'
