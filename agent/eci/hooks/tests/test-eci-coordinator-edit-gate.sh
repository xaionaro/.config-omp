#!/usr/bin/env bash

set -euo pipefail

ROOT="${ECI_TEST_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-coordinator-edit.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

fixture_home="$TMP_ROOT/home"
fixture_eci="$fixture_home/.omp/agent/eci"
fixture_hooks="$fixture_eci/hooks"
fixture_tmp="$fixture_home/tmp"
proof_root="$TMP_ROOT/proof"
repo="$TMP_ROOT/repo"
session_id=t00-coordinator-edit
marker="$proof_root/$session_id/eci_active"

mkdir -p -- "$fixture_hooks" "$fixture_tmp" "$proof_root/$session_id" "$repo"
chmod 700 -- "$fixture_home" "$fixture_eci" "$fixture_tmp" "$proof_root" "$proof_root/$session_id"
cp -a -- "$ROOT/hooks/." "$fixture_hooks/"

# Exercise the private dispatcher with a line-2 bypass removed if present,
# and preserve the source bytes.
cp -- "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$TMP_ROOT/dispatcher.before"
sed -i '2{/^exit 0$/d;}' -- "$fixture_hooks/pretooluse-edit-dispatch.sh"
cmp -- "$TMP_ROOT/dispatcher.before" "$ROOT/hooks/pretooluse-edit-dispatch.sh" || {
  printf '%s\n' 'test modified the dispatcher source' >&2
  exit 1
}
cmp -- "$fixture_hooks/pretooluse-edit-dispatch.sh" <(sed '2{/^exit 0$/d;}' -- "$TMP_ROOT/dispatcher.before")

# Simulate only the advisory child validator's health.  The real active-ECI
# gate remains installed beside it, so this fixture proves a malformed or
# failed validator cannot hide a concrete control-target block.
wrap_advisory_validator() {
  local validator_name="$1" validator_path

  validator_path="$fixture_hooks/$validator_name"

  mv -- "$validator_path" "${validator_path%.sh}-real.sh"
  cat >"$validator_path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

validator_base="$(basename -- "${BASH_SOURCE[0]}" .sh)"
case "${FAKE_ADVISORY_VALIDATOR_MODE:-real}" in
  real) exec bash "$(dirname -- "${BASH_SOURCE[0]}")/${validator_base}-real.sh" ;;
  malformed) printf '%s\n' 'not provider JSON' ;;
  fail)
    printf '%s\n' 'simulated validator failure' >&2
    exit 17
    ;;
  ownership-unknown)
    printf '%s\n' 'simulated ownership uncertainty' >&2
    ;;
  *)
    printf 'unexpected fake validator mode: %s\n' "${FAKE_ADVISORY_VALIDATOR_MODE}" >&2
    exit 19
    ;;
esac
EOF
  chmod 755 -- "$validator_path"
}
wrap_advisory_validator validate-edit-write.sh
wrap_advisory_validator validate-apply-patch.sh

git -C "$repo" init -q
printf '%s\n' 'int ordinary_repository_file(void) { return 0; }' >"$repo/normal.c"
printf '%s\n' \
  'scope: coordinator repository edit regression' \
  "cwd: $repo" \
  "session_id: $session_id" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$marker"

run_dispatch() {
  local label="$1" input="$2" role="${3:-}" validator_mode="${4:-real}" output error status

  output="$TMP_ROOT/$label.out"
  error="$TMP_ROOT/$label.err"

  set +e
  HOME="$fixture_home" ECI_HOME="$fixture_eci" ECI_PROOF_ROOT="$proof_root" \
    ECI_ROLE="$role" TMPDIR="$fixture_tmp" ENABLE_SECURITY_REMINDER=0 \
    FAKE_ADVISORY_VALIDATOR_MODE="$validator_mode" \
    bash "$fixture_hooks/pretooluse-edit-dispatch.sh" <"$input" >"$output" 2>"$error"
  status=$?
  set -e
  [ "$status" -eq 0 ] || {
    printf 'fixture dispatcher %s exited %s\n' "$label" "$status" >&2
    cat "$error" >&2
    return 1
  }
  [ ! -s "$error" ] || {
    printf 'fixture dispatcher %s wrote stderr:\n' "$label" >&2
    cat "$error" >&2
    return 1
  }
  printf '%s\n' "$output"
}

run_active_gate_as_coordinator() {
  local label="$1" input="$2" output error status

  output="$TMP_ROOT/$label.out"
  error="$TMP_ROOT/$label.err"
  set +e
  HOME="$fixture_home" ECI_HOME="$fixture_eci" ECI_PROOF_ROOT="$proof_root" \
    ECI_ROLE=coordinator TMPDIR="$fixture_tmp" \
    bash "$fixture_hooks/eci-active-gate.sh" <"$input" >"$output" 2>"$error"
  status=$?
  set -e
  [ "$status" -eq 0 ] || {
    printf 'fixture active gate %s exited %s\n' "$label" "$status" >&2
    cat "$error" >&2
    return 1
  }
  [ ! -s "$error" ] || {
    printf 'fixture active gate %s wrote stderr:\n' "$label" >&2
    cat "$error" >&2
    return 1
  }
  printf '%s\n' "$output"
}

assert_empty_output() {
  local label="$1" output="$2"

  [ ! -s "$output" ] || {
    printf 'expected %s to allow a normal coordinator repository edit, got:\n' "$label" >&2
    cat "$output" >&2
    return 1
  }
}

assert_control_deny() {
  local label="$1" output="$2"

  jq -e '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_CONTROL_OWNER_REQUIRED")) and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_MAIN_THREAD_EDIT_DELEGATION_REQUIRED") | not) and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_ORCHESTRATOR_ROLE_DENIED") | not)
  ' "$output" >/dev/null || {
    printf 'expected %s to deny only the ECI control artifact:\n' "$label" >&2
    cat "$output" >&2
    return 1
  }
}

assert_cross_session_deny() {
  local label="$1" output="$2" owner="$3"

  jq -e --arg owner "$owner" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($owner))
  ' "$output" >/dev/null || {
    printf 'expected %s to retain the resolved cross-session proof boundary:\n' "$label" >&2
    cat "$output" >&2
    return 1
  }
}

assert_marker_deny() {
  local label="$1" output="$2" code="$3"

  jq -e --arg code "$code" '
    .hookSpecificOutput.permissionDecision == "deny" and
    (.hookSpecificOutput.permissionDecisionReason | contains($code)) and
    (.hookSpecificOutput.permissionDecisionReason | contains("ECI_MAIN_THREAD_EDIT_DELEGATION_REQUIRED") | not)
  ' "$output" >/dev/null || {
    printf 'expected %s to retain %s marker protection:\n' "$label" "$code" >&2
    cat "$output" >&2
    return 1
  }
}

normal_input="$TMP_ROOT/normal.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$repo/normal.c" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"updated"}}' \
  >"$normal_input"
normal_output="$(run_dispatch normal "$normal_input")"
assert_empty_output normal "$normal_output"

mkdir -p -- "$repo/docs/plans" "$repo/vendor/lib" "$repo/submodule"
printf 'gitdir: %s/modules/submodule\n' "$repo/.git" >"$repo/submodule/.git"
printf '%s\n' 'module example' >"$repo/go.mod"
for policy_path in \
  "$repo/docs/plans/ordinary.md" \
  "$repo/vendor/lib/ordinary.c" \
  "$repo/submodule/ordinary.c"; do
  policy_input="$TMP_ROOT/$(basename -- "$policy_path").json"
  jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$policy_path" \
    '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"ordinary update"}}' \
    >"$policy_input"
  policy_output="$(run_dispatch "policy-$(basename -- "$policy_path")" "$policy_input")"
  assert_empty_output "policy-$(basename -- "$policy_path")" "$policy_output"
done
go_mod_input="$TMP_ROOT/go-mod.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$repo/go.mod" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"replace example.local/module => ../module"}}' \
  >"$go_mod_input"
go_mod_output="$(run_dispatch go-mod "$go_mod_input")"
assert_empty_output go-mod "$go_mod_output"

normal_patch_input="$TMP_ROOT/normal-patch.json"
normal_patch="$(printf '*** Begin Patch\n*** Update File: %s\n@@\n-old\n+new\n*** End Patch\n' "$repo/normal.c")"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg patch "$normal_patch" \
  '{tool_name:"apply_patch",session_id:$session_id,cwd:$cwd,tool_input:{patch:$patch}}' \
  >"$normal_patch_input"
normal_patch_output="$(run_dispatch normal-patch "$normal_patch_input")"
assert_empty_output normal-patch "$normal_patch_output"

policy_patch_input="$TMP_ROOT/policy-patch.json"
policy_patch="$(printf '*** Begin Patch\n*** Update File: %s\n@@\n-old\n+new\n*** Update File: %s\n@@\n-old\n+new\n*** Update File: %s\n@@\n-old\n+replace example.local/module => ../module\n*** Update File: %s\n@@\n-old\n+new\n*** End Patch\n' "$repo/docs/plans/ordinary.md" "$repo/vendor/lib/ordinary.c" "$repo/go.mod" "$repo/submodule/ordinary.c")"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg patch "$policy_patch" \
  '{tool_name:"apply_patch",session_id:$session_id,cwd:$cwd,tool_input:{patch:$patch}}' \
  >"$policy_patch_input"
policy_patch_output="$(run_dispatch policy-patch "$policy_patch_input")"
assert_empty_output policy-patch "$policy_patch_output"

# Child-health failures are advisory for an ordinary repository target.  The
# dispatcher must not convert either malformed JSON, a non-zero validator, or
# unavailable ownership information into a user-facing denial.
malformed_normal_output="$(run_dispatch malformed-normal "$normal_input" '' malformed)"
assert_empty_output malformed-normal "$malformed_normal_output"
failed_normal_output="$(run_dispatch failed-normal "$normal_input" '' fail)"
assert_empty_output failed-normal "$failed_normal_output"
unknown_normal_output="$(run_dispatch unknown-normal "$normal_input" '' ownership-unknown)"
assert_empty_output unknown-normal "$unknown_normal_output"
malformed_patch_output="$(run_dispatch malformed-patch "$normal_patch_input" '' malformed)"
assert_empty_output malformed-patch "$malformed_patch_output"
failed_patch_output="$(run_dispatch failed-patch "$normal_patch_input" '' fail)"
assert_empty_output failed-patch "$failed_patch_output"

# The full copied dispatcher must also remain non-blocking for a coordinator
# ordinary repository edit.  Before the ATE gate fix this reproduces its
# independent ECI_ORCHESTRATOR_ROLE_DENIED response.
coordinator_dispatch_output="$(run_dispatch coordinator-dispatch "$normal_input" coordinator)"
assert_empty_output coordinator-dispatch "$coordinator_dispatch_output"

# The active edit gate itself must stay role-neutral too, so exercise it
# directly with the coordinator role as well as through the dispatcher.
coordinator_role_output="$(run_active_gate_as_coordinator coordinator-role "$normal_input")"
assert_empty_output coordinator-role "$coordinator_role_output"

control_input="$TMP_ROOT/control.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$marker" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"forged"}}' \
  >"$control_input"
control_output="$(run_dispatch control "$control_input")"
assert_control_deny control "$control_output"
direct_marker_output="$(run_active_gate_as_coordinator direct-marker "$control_input")"
assert_control_deny direct-marker "$direct_marker_output"

teardown_path="$proof_root/$session_id/disengage.md"
teardown_input="$TMP_ROOT/teardown.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$teardown_path" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"forged teardown"}}' \
  >"$teardown_input"
teardown_output="$(run_active_gate_as_coordinator teardown-control "$teardown_input")"
assert_control_deny teardown-control "$teardown_output"

# The same advisory child failure must not hide a real resolved control target.
malformed_control_output="$(run_dispatch malformed-control "$control_input" '' malformed)"
assert_control_deny malformed-control "$malformed_control_output"
failed_control_output="$(run_dispatch failed-control "$control_input" '' fail)"
assert_control_deny failed-control "$failed_control_output"

control_patch_input="$TMP_ROOT/control-patch.json"
control_patch="$(printf '*** Begin Patch\n*** Update File: %s\n@@\n-old\n+forged\n*** End Patch\n' "$marker")"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg patch "$control_patch" \
  '{tool_name:"apply_patch",session_id:$session_id,cwd:$cwd,tool_input:{patch:$patch}}' \
  >"$control_patch_input"
malformed_control_patch_output="$(run_dispatch malformed-control-patch "$control_patch_input" '' malformed)"
assert_control_deny malformed-control-patch "$malformed_control_patch_output"
failed_control_patch_output="$(run_dispatch failed-control-patch "$control_patch_input" '' fail)"
assert_control_deny failed-control-patch "$failed_control_patch_output"

# A healthy validator still blocks a proof target that resolves to another
# session. This is target ownership, not a role or artifact requirement.
other_session=other-session
cross_session_path="$proof_root/$other_session/ordinary.md"
mkdir -p -- "$(dirname -- "$cross_session_path")"
cross_session_input="$TMP_ROOT/cross-session.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$cross_session_path" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"wrong session"}}' \
  >"$cross_session_input"
cross_session_output="$(run_dispatch cross-session "$cross_session_input")"
assert_cross_session_deny cross-session "$cross_session_output" "$other_session"

context_root="$fixture_home/.agent/context/sessions"
cross_session_document="$context_root/$other_session/project-understanding.yaml"
cross_session_document_input="$TMP_ROOT/cross-session-document.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$cross_session_document" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"wrong session document"}}' \
  >"$cross_session_document_input"
cross_session_document_output="$(run_active_gate_as_coordinator cross-session-document "$cross_session_document_input")"
assert_control_deny cross-session-document "$cross_session_document_output"
cross_session_document_dispatch_output="$(run_dispatch cross-session-document-dispatch "$cross_session_document_input")"
assert_cross_session_deny cross-session-document-dispatch "$cross_session_document_dispatch_output" "$other_session"

# Both report formats retain current-session editing and foreign-session ownership.
for report in latest-status-report.md latest-status-report.yaml; do
  for tool in Write Edit; do
    for owner in "$session_id" "$other_session"; do
      report_input="$TMP_ROOT/report-$report-$tool-$owner.json"
      jq -cn --arg tool "$tool" --arg session_id "$session_id" --arg cwd "$repo" --arg path "$context_root/$owner/$report" \
        '{tool_name:$tool,session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"updated",old_string:"old",new_string:"updated"}}' \
        >"$report_input"
      report_output="$(run_dispatch "report-$report-$tool-$owner" "$report_input")"
      if [ "$owner" = "$session_id" ]; then
        assert_empty_output "report-$report-$tool-$owner" "$report_output"
      else
        assert_cross_session_deny "report-$report-$tool-$owner" "$report_output" "$other_session"
      fi
    done
  done
done

ledger="$context_root/$session_id/high_level_log.jsonl"
mkdir -p -- "$(dirname -- "$ledger")"
printf '%s\n' '{"event":"decision","summary":"immutable history"}' >"$ledger"
ledger_input="$TMP_ROOT/ledger.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$ledger" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"forged"}}' \
  >"$ledger_input"
ledger_output="$(run_dispatch ledger "$ledger_input")"
assert_empty_output ledger "$ledger_output"

# The coordinator owns its current session's derived anchor too.  This gate
# has no post-write phase, so it must not turn anchor content into a hash or
# record-shaped admission prerequisite.  A bad anchor is a bounded local
# coordination error; it cannot modify a foreign session or lifecycle target.
anchor="$context_root/$session_id/high_level_log.anchor"
anchor_input="$TMP_ROOT/anchor.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$anchor" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"coordinator anchor update"}}' \
  >"$anchor_input"
anchor_output="$(run_dispatch anchor "$anchor_input")"
assert_empty_output anchor "$anchor_output"

anchor_patch_input="$TMP_ROOT/anchor-patch.json"
anchor_patch="$(printf '*** Begin Patch\n*** Update File: %s\n@@\n-old\n+coordinator anchor update\n*** End Patch\n' "$anchor")"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg patch "$anchor_patch" \
  '{tool_name:"apply_patch",session_id:$session_id,cwd:$cwd,tool_input:{patch:$patch}}' \
  >"$anchor_patch_input"
anchor_patch_output="$(run_dispatch anchor-patch "$anchor_patch_input")"
assert_empty_output anchor-patch "$anchor_patch_output"

other_anchor="$context_root/$other_session/high_level_log.anchor"
other_anchor_input="$TMP_ROOT/other-anchor.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$other_anchor" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:"wrong session anchor update"}}' \
  >"$other_anchor_input"
other_anchor_output="$(run_active_gate_as_coordinator other-anchor "$other_anchor_input")"
assert_control_deny other-anchor "$other_anchor_output"

malformed_marker="$proof_root/$session_id/eci_active"
cp -- "$malformed_marker" "$TMP_ROOT/marker.before"
sed -i 's/^session_id: .*/session_id: another-session/' -- "$malformed_marker"
malformed_output="$(run_dispatch malformed-marker "$normal_input")"
# Marker metadata is discovery state, not the ordinary repository target.
# Keep a malformed sibling/current marker advisory here; the healthy direct
# marker/ledger/cross-session target assertions above still prove concrete
# control targets are denied.
assert_empty_output malformed-marker "$malformed_output"
mv -- "$TMP_ROOT/marker.before" "$malformed_marker"

# Callback transport metadata is not a target.  With a valid active marker,
# an otherwise ordinary repository edit must still pass through when the
# callback omitted a typed session id; the concrete marker/ledger/cross-session
# target checks above remain the boundaries.
malformed_identity_input="$TMP_ROOT/malformed-identity.json"
jq -cn --arg cwd "$repo" --arg path "$repo/normal.c" \
  '{tool_name:"Write",session_id:17,cwd:$cwd,tool_input:{file_path:$path,content:"ordinary update"}}' \
  >"$malformed_identity_input"
malformed_identity_output="$(run_active_gate_as_coordinator malformed-identity "$malformed_identity_input")"
assert_empty_output malformed-identity "$malformed_identity_output"

# A valid direct marker owns ordinary Edit/Write routing.  Bad sibling marker
# records and callback metadata are advisory: they must not turn a normal
# repository/documentation/status edit into a recovery task. Concrete target
# guards above still deny the direct marker and teardown state.
peer_session_dir="session_$session_id"
peer_marker="$proof_root/$peer_session_dir/eci_active"
mkdir -p -- "$proof_root/$peer_session_dir"
printf '%s\n' \
  'scope: same-session alias peer' \
  "cwd: $repo" \
  "session_id: $session_id" \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$peer_marker"

malformed_sibling="$proof_root/advisory-malformed/eci_active"
mkdir -p -- "${malformed_sibling%/*}"
printf '%s\n' 'not a marker' >"$malformed_sibling"

unrelated_sibling="$proof_root/advisory-peer/eci_active"
mkdir -p -- "${unrelated_sibling%/*}"
printf '%s\n' \
  'scope: unrelated sibling' \
  "cwd: $repo" \
  'session_id: advisory-peer' \
  'created_utc: 2026-08-28T00:00:00Z' \
  >"$unrelated_sibling"

mkdir -p -- "$fixture_eci/sessions"
callback_transcript="$fixture_eci/sessions/advisory-sibling.jsonl"
printf '%s\n' \
  '{"timestamp":"2026-08-28T00:00:00.000Z","type":"session_meta","payload":{"id":"t00-coordinator-edit","source":{"subagent":{"thread_spawn":{"parent_thread_id":"advisory-parent","depth":1,"agent_nickname":"Fixture","agent_role":"default"}}}}}' \
  >"$callback_transcript"
advisory_normal_input="$TMP_ROOT/advisory-normal.json"
jq -cn --arg session_id "$session_id" --arg cwd "$repo" --arg path "$repo/normal.c" --arg transcript "$callback_transcript" \
  '{tool_name:"Write",session_id:$session_id,cwd:$cwd,transcript_path:$transcript,tool_input:{file_path:$path,content:"updated"}}' \
  >"$advisory_normal_input"
advisory_normal_output="$(run_active_gate_as_coordinator advisory-normal "$advisory_normal_input")"
assert_empty_output advisory-normal "$advisory_normal_output"
advisory_normal_dispatch_output="$(run_dispatch advisory-normal-dispatch "$advisory_normal_input")"
assert_empty_output advisory-normal-dispatch "$advisory_normal_dispatch_output"

for advisory_path in \
  "$repo/docs/status.md" \
  "$context_root/$session_id/project-understanding.yaml" \
  "$context_root/$session_id/latest-status-report.md" \
  "$context_root/$session_id/latest-status-report.yaml" \
  "$proof_root/$session_id/handoff.md"; do
  advisory_tool=Write
  [ "$advisory_path" = "$repo/docs/status.md" ] && advisory_tool=Edit
  advisory_input="$TMP_ROOT/advisory-$(basename -- "$advisory_path").json"
  jq -cn --arg tool "$advisory_tool" --arg session_id "$session_id" --arg cwd "$repo" --arg path "$advisory_path" \
    '{tool_name:$tool,session_id:$session_id,cwd:$cwd,tool_input:{file_path:$path,content:(if $path | endswith("/project-understanding.yaml") then "schema: project-understanding/v1\nsections:\n  context:\n    title: \"Context\"\nrecords: {}\n" else "ordinary update" end)}}' \
    >"$advisory_input"
  advisory_output="$(run_active_gate_as_coordinator "advisory-$(basename -- "$advisory_path")" "$advisory_input")"
  assert_empty_output "advisory-$(basename -- "$advisory_path")" "$advisory_output"
  advisory_dispatch_output="$(run_dispatch "advisory-$(basename -- "$advisory_path")-dispatch" "$advisory_input")"
  assert_empty_output "advisory-$(basename -- "$advisory_path")-dispatch" "$advisory_dispatch_output"
done

printf '%s\n' 'eci coordinator edit gate assertions: PASS'
