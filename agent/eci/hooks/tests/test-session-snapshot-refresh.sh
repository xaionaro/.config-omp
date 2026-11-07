#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT/hooks/lib/eci-file-lock.sh"
TMP_ROOT="$(realpath -e -- "$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-session-refresh.XXXXXX")")"
trap 'chmod -R u+w -- "$TMP_ROOT" 2>/dev/null; rm -rf -- "$TMP_ROOT"' EXIT

run_snapshot() {
  local proof_root="$1"
  local input_source="$2"
  local out="$3"
  local session_id="${4:-t00-session}"

  mkdir -p "$TMP_ROOT/home/tmp" "$proof_root"
  jq -cn \
    --arg session_id "$session_id" \
    --arg source "$input_source" \
    --arg cwd "$ROOT" \
    '{session_id:$session_id, transcript_path:"/tmp/session.jsonl", cwd:$cwd, source:$source}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/session-snapshot.sh" >"$out"
}

write_direct_marker() {
  local proof_root="$1" scope="${2:-refresh test}"
  printf 'scope: %s\ncwd: %s\nsession_id: t00-session\ncreated_utc: 2026-08-14T00:00:00Z\n' "$scope" "$ROOT" >"$proof_root/t00-session/eci_active"
}

# An inactive session's output is the baseline. Compare with a fresh inactive
# capture instead of pinning its wording.
assert_matches_inactive_baseline() {
  local out="$1" name="$2"
  local baseline="$TMP_ROOT/$name-baseline.out"
  run_snapshot "$TMP_ROOT/$name-baseline-proof" resume "$baseline"
  jq -e --slurpfile base "$baseline" '
    (.hookSpecificOutput.additionalContext == $base[0].hookSpecificOutput.additionalContext) and
    ((.hookSpecificOutput.additionalContext | contains("ECI is active")) | not)
  ' "$out" >/dev/null
}

test_active_eci_refresh_signal_for_session_start_reminder() {
  local proof_root="$TMP_ROOT/active-proof" out
  mkdir -p "$proof_root/t00-session"
  write_direct_marker "$proof_root"
  out="$TMP_ROOT/active.out"

  run_snapshot "$proof_root" resume "$out"

  jq -e '
    (.hookSpecificOutput.hookEventName == "SessionStart") and
    (.hookSpecificOutput.additionalContext | contains("ECI is active")) and
    (.hookSpecificOutput.additionalContext | contains("ECI refresh signal (not proof of compaction)")) and
    (.hookSpecificOutput.additionalContext | contains("re-read the entire skills/explore-critique-implement/SKILL.md")) and
    (.hookSpecificOutput.additionalContext | contains("re-invoke it before the next decision/tool"))
  ' "$out" >/dev/null
}

test_session_start_rejects_marker_owner_mismatch() {
  local proof_root="$TMP_ROOT/mismatched-owner-proof" out
  mkdir -p "$proof_root/t00-session"
  write_direct_marker "$proof_root" 'mismatched owner must not refresh'
  sed -i 's/^session_id: t00-session$/session_id: t00-other/' "$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/mismatched-owner.out"
  run_snapshot "$proof_root" resume "$out"
  assert_matches_inactive_baseline "$out" mismatched-owner
}

test_nested_eci_refresh_signal_is_explicit() {
  local proof_root="$TMP_ROOT/nested-proof" out
  mkdir -p "$proof_root/t00-session"
  printf 'scope: nested outer\ncwd: %s\nsession_id: t00-session\ncreated_utc: 2026-08-14T00:00:00Z\n' "$ROOT" >"$proof_root/t00-session/eci_active"
  printf 'outer_session_id: t00-session\nouter_marker: %s/eci_active\nowner: ate\nwriter_session_id: t00-session\nacceptance_version: 1\nstep: 4\niteration: 1\nstate: active\n' "$proof_root/t00-session" >"$proof_root/t00-session/ate_nested_eci_active"
  out="$TMP_ROOT/nested.out"
  run_snapshot "$proof_root" resume "$out"
  jq -e '.hookSpecificOutput.additionalContext | contains("ECI is active")' "$out" >/dev/null
}

test_inactive_session_keeps_baseline_context() {
  local proof_root="$TMP_ROOT/inactive-proof" out
  out="$TMP_ROOT/inactive.out"

  run_snapshot "$proof_root" startup "$out"

  jq -e '.hookSpecificOutput.additionalContext | type == "string" and length > 0' "$out" >/dev/null
  assert_matches_inactive_baseline "$out" inactive-session
  if jq -e '.hookSpecificOutput.additionalContext | contains("ECI is active")' "$out" >/dev/null; then
    return 1
  fi
}

test_session_start_matcher_uses_supported_lifecycle_sources() {
  jq -e '
    (.hooks.SessionStart | length > 0) and
    (.hooks.SessionStart | all(.matcher == "startup|resume|clear"))
  ' "$ROOT/hooks.json" >/dev/null
}

test_session_start_rejects_malformed_types() {
  local proof_root="$TMP_ROOT/malformed-proof" out
  mkdir -p "$proof_root"
  out="$TMP_ROOT/malformed.out"
  printf '%s\n' '{"session_id":[],"transcript_path":"/tmp/x","cwd":"/tmp"}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" bash "$ROOT/hooks/session-snapshot.sh" >"$out"
  [ ! -s "$out" ]
  printf '%s\n' '{"session_id":"t00-session","transcript_path":"/tmp/x","cwd":[]}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" bash "$ROOT/hooks/session-snapshot.sh" >"$out"
  [ ! -s "$out" ]
}

test_session_start_rejects_root_and_session_symlinks() {
  local root_link="$TMP_ROOT/root-link" root_target="$TMP_ROOT/root-target" out target
  mkdir -p "$root_target/t00-session" "$TMP_ROOT/home/tmp"
  ln -s "$root_target" "$root_link"
  out="$TMP_ROOT/root-link.out"
  jq -cn '{session_id:"t00-session",transcript_path:"/tmp/x",cwd:"/tmp"}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$root_link" bash "$ROOT/hooks/session-snapshot.sh" >"$out"
  [ ! -s "$out" ]

  root_target="$TMP_ROOT/session-link-root"
  target="$TMP_ROOT/session-link-target"
  mkdir -p "$root_target" "$target"
  ln -s "$target" "$root_target/t00-session"
  out="$TMP_ROOT/session-link.out"
  jq -cn '{session_id:"t00-session",transcript_path:"/tmp/x",cwd:"/tmp"}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$root_target" bash "$ROOT/hooks/session-snapshot.sh" >"$out"
  [ ! -s "$out" ]
}

test_baseline_uses_fixed_git_and_resolves_head() {
  local proof_root="$TMP_ROOT/poisoned-git-proof" fakebin="$TMP_ROOT/poisoned-git-bin" out baseline expected source_repo
  source_repo="${ECI_SOURCE_REPO:?run-isolated.sh must export ECI_SOURCE_REPO}"
  mkdir -p "$proof_root" "$fakebin" "$TMP_ROOT/home/tmp"
  printf '#!/usr/bin/env bash\nprintf poisoned-baseline\\n' >"$fakebin/git"
  chmod +x "$fakebin/git"
  out="$TMP_ROOT/poisoned-git.out"
  jq -cn --arg cwd "$source_repo" '{session_id:"t00-session",transcript_path:"/tmp/x",cwd:$cwd}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" GIT_DIR="$TMP_ROOT/poisoned-git-dir" PATH="$fakebin:$PATH" \
      bash "$ROOT/hooks/session-snapshot.sh" >"$out"
  baseline="$proof_root/t00-session/baseline_head"
  expected="$(git -C "$source_repo" rev-parse HEAD)"
  [ "$(cat "$baseline")" = "$expected" ]
  binding="$proof_root/t00-session/baseline_head.binding"
  [ -f "$binding" ] && [ ! -L "$binding" ]
  grep -Fxq 'schema: eci-baseline-binding/v1' "$binding"
  grep -Fxq 'session_id: t00-session' "$binding"
  grep -Fxq "base_oid: $expected" "$binding"
}

test_baseline_pair_recovers_after_publication_boundary() {
  local proof_root="$TMP_ROOT/baseline-recovery-proof" out baseline binding
  mkdir -p "$proof_root" "$TMP_ROOT/home/tmp"
  baseline="$proof_root/t00-session/baseline_head"
  binding="$proof_root/t00-session/baseline_head.binding"
  out="$TMP_ROOT/baseline-recovery.out"

  if jq -cn --arg cwd "$ROOT" '{session_id:"t00-session",transcript_path:"/tmp/session.jsonl",cwd:$cwd}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_SNAPSHOT_FAIL_AFTER=baseline \
      bash "$ROOT/hooks/session-snapshot.sh" >"$out"; then
    return 1
  fi
  [ -f "$baseline" ] && [ ! -L "$baseline" ]
  [ ! -e "$binding" ] && [ ! -L "$binding" ]

  # A retry repairs the missing binding from the validated baseline instead of
  # leaving the review gate wedged on a half-published pair.
  run_snapshot "$proof_root" resume "$out"
  [ -f "$baseline" ] && [ -f "$binding" ]
  grep -Fxq "base_oid: $(cat "$baseline")" "$binding"

  proof_root="$TMP_ROOT/baseline-binding-recovery-proof"
  baseline="$proof_root/t00-session/baseline_head"
  binding="$proof_root/t00-session/baseline_head.binding"
  mkdir -p "$proof_root"
  if jq -cn --arg cwd "$ROOT" '{session_id:"t00-session",transcript_path:"/tmp/session.jsonl",cwd:$cwd}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_SNAPSHOT_FAIL_AFTER=binding \
      bash "$ROOT/hooks/session-snapshot.sh" >"$out"; then
    return 1
  fi
  [ -f "$baseline" ] && [ -f "$binding" ]
  run_snapshot "$proof_root" resume "$out"
  [ -f "$baseline" ] && [ -f "$binding" ]
}

test_eci_active_mutations_fail_closed_when_lock_is_busy() {
  local proof_root="$TMP_ROOT/mutation-lock-proof" session_dir log_path lock_fd entry
  session_dir="$proof_root/t00-session"
  mkdir -p "$session_dir" "$TMP_ROOT/home/tmp" "$TMP_ROOT/home/.agent/context/sessions/t00-session"
  printf 'scope: lock test\n' >"$session_dir/eci_active"
  log_path="$TMP_ROOT/home/.agent/context/sessions/t00-session/high_level_log.jsonl"
  entry='{"event":"checkpoint","summary":"append after lock release"}'
  printf '%s\n' '{"event":"checkpoint","summary":"existing entry"}' >"$log_path"
  cp "$log_path" "$TMP_ROOT/log-before-lock"
  exec {lock_fd}>>"$proof_root/.eci-active.lock"
  eci_file_lock_acquire_fd "$lock_fd" nonblocking

  if HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID=t00-session \
      "$ROOT/bin/eci-active" ledger-append --json "$entry" >"$TMP_ROOT/append-lock.out" 2>"$TMP_ROOT/append-lock.err"; then
    return 1
  fi
  printf 'ECI mutation lock is busy; refusing state mutation: %s/.eci-active.lock\n' "$(cd "$proof_root" && pwd -P)" \
    >"$TMP_ROOT/expected-lock.err"
  cmp "$TMP_ROOT/expected-lock.err" "$TMP_ROOT/append-lock.err"
  cmp "$TMP_ROOT/log-before-lock" "$log_path"
  eci_file_lock_release_fd "$lock_fd"
  HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID=t00-session \
    "$ROOT/bin/eci-active" ledger-append --json "$entry" >"$TMP_ROOT/append-unlocked.out"
  jq -se 'length == 2 and (.[1] | .schema == "eci-high-level-log/v1" and
    .event == "checkpoint" and .summary == "append after lock release" and
    .session_id == "t00-session")' "$log_path" >/dev/null
}

test_session_start_skips_pruning_when_mutation_lock_is_busy() {
  local proof_root="$TMP_ROOT/lock-proof" out old_dir lock_fd
  old_dir="$proof_root/019df400-0000-7000-8000-000000000002"
  mkdir -p "$old_dir"
  printf 'old marker\n' >"$old_dir/eci_active"
  touch -t 202001010000 "$old_dir" "$old_dir/eci_active"
  mkdir -p "$proof_root"
  exec {lock_fd}>>"$proof_root/.eci-active.lock"
  eci_file_lock_acquire_fd "$lock_fd" nonblocking
  out="$TMP_ROOT/lock.out"
  run_snapshot "$proof_root" resume "$out" t00-session
  [ -e "$old_dir/eci_active" ]
  [ ! -e "$proof_root/t00-session" ]
  eci_file_lock_release_fd "$lock_fd"
  run_snapshot "$proof_root" resume "$out" t00-session
  [ -s "$proof_root/t00-session/baseline_head" ]
  [ -e "$old_dir/eci_active" ]
}

test_old_uuid_session_with_active_eci_marker_survives_cleanup() {
  local proof_root="$TMP_ROOT/old-uuid-proof" session_dir out
  session_dir="$proof_root/019df400-0000-7000-8000-000000000001"
  mkdir -p "$session_dir"
  printf '%s\n' 'baseline' >"$session_dir/baseline_head"
  printf '%s\n' 'scope: old active session' >"$session_dir/eci_active"
  touch -t 202001010000 "$session_dir" "$session_dir/baseline_head" "$session_dir/eci_active"
  out="$TMP_ROOT/old-uuid.out"

  run_snapshot "$proof_root" compaction "$out" 019df400-0000-7000-8000-000000000001

  [ -s "$session_dir/baseline_head" ] &&
    [ -s "$session_dir/eci_active" ] &&
    grep -q '^scope: old active session$' "$session_dir/eci_active"
}

test_old_marker_dir_with_symlink_eci_marker_is_pruned() {
  local proof_root="$TMP_ROOT/old-symlink-proof" marker_dir target out
  marker_dir="$proof_root/eci/sessions/stale-session"
  target="$TMP_ROOT/stale-eci-target"
  mkdir -p "$marker_dir"
  printf '%s\n' 'scope: stale symlink' >"$target"
  ln -s "$target" "$marker_dir/eci_active"
  touch -t 202001010000 "$marker_dir"
  out="$TMP_ROOT/old-symlink.out"

  run_snapshot "$proof_root" compaction "$out"

  [ ! -e "$marker_dir" ] && [ ! -L "$marker_dir" ] && [ -s "$target" ]
}

# A source-only checkout has no native tools. SessionStart must not compile them;
# hooks/build-native-binaries.sh provisions the canonical pair into bin/, and
# bin/eci-runtime-sync verifies that pair and provisions the Kimi peer copy.
test_source_checkout_provisions_native_tools_into_bin() {
  local HOME="$TMP_ROOT/home"
  local fixture="$HOME/.omp/agent/eci" module tool importer_saved
  local kimi="$TMP_ROOT/kimi/.kimi-code"
  local -a tools=(eci-command-plan eci-safe-import eci-hook-helper)
  mkdir -p "$fixture/hooks/lib" "$fixture/bin" "$HOME/tmp" "$kimi"
  cp "$ROOT/hooks/session-snapshot.sh" "$ROOT/hooks/build-native-binaries.sh" "$fixture/hooks/"
  cp "$ROOT/bin/eci-active" "$ROOT/bin/eci-runtime-sync" "$fixture/bin/"
  cp "$ROOT/hooks/lib/"*.sh "$fixture/hooks/lib/"
  cp -a "$ROOT/tools" "$fixture/tools"
  for module in eci-command-gate-mode-go eci-command-plan-go eci-safe-import-go eci-hook-helper-go eci-worker-git-go; do
    cp -a "$ROOT/hooks/lib/$module" "$fixture/hooks/lib/$module"
  done

  # SessionStart reports nothing and compiles nothing.
  printf '%s\n' '{"session_id":"build-test","cwd":"/tmp"}' |
    bash "$fixture/hooks/session-snapshot.sh" >"$TMP_ROOT/build.out"
  [ ! -s "$TMP_ROOT/build.out" ]
  for tool in "${tools[@]}"; do
    [ ! -e "$fixture/bin/$tool" ]
    [ ! -e "$fixture/bin/$tool.sha256" ]
  done

  bash "$fixture/hooks/build-native-binaries.sh" >"$TMP_ROOT/provision.out"
  for tool in "${tools[@]}"; do
    [ -x "$fixture/bin/$tool" ]
    [ -f "$fixture/bin/$tool.sha256" ]
  done
  KIMI_CODE_HOME="$kimi" "$fixture/bin/eci-runtime-sync" planner-check --target "$kimi" >"$TMP_ROOT/check.out"
  [ "$(cat "$TMP_ROOT/check.out")" = "$(printf 'OK\tmanaged pair\teci')" ]
  KIMI_CODE_HOME="$kimi" "$fixture/bin/eci-runtime-sync" planner-apply --target "$kimi" >"$TMP_ROOT/peer.out"
  for tool in "${tools[@]}"; do
    cmp -- "$fixture/bin/$tool" "$kimi/bin/$tool"
    cmp -- "$fixture/bin/$tool.sha256" "$kimi/bin/$tool.sha256"
  done

  printf '%s\n' '{"provider":"omp","role":"coordinator","cwd":"/tmp","marker":"inactive","command":"pwd"}' |
    "$fixture/bin/eci-command-plan" |
    jq -e '.decision == "allow"' >/dev/null
  mkdir -p "$TMP_ROOT/import-proof/build-test"
  printf '%s\n' 'build test report' >"$TMP_ROOT/import-report"
  "$fixture/bin/eci-safe-import" \
    --proof-root "$(realpath "$TMP_ROOT/import-proof")" --session-dir "$(realpath "$TMP_ROOT/import-proof/build-test")" \
    --leaf wait-report --source "$TMP_ROOT/import-report"
  cmp "$TMP_ROOT/import-report" "$TMP_ROOT/import-proof/build-test/eci_user_owned_wait.md"

  # manifest-write never recovers a missing importer: it fails closed and names
  # the provisioning step. Restoring the provisioned binary then succeeds.
  importer_saved="$TMP_ROOT/eci-safe-import.provisioned"
  cp -p -- "$fixture/bin/eci-safe-import" "$importer_saved"
  rm -- "$fixture/bin/eci-safe-import"
  mkdir -p "$TMP_ROOT/import-proof/import-on-demand"
  printf '%s\n' '{"critics":[]}' >"$TMP_ROOT/import-manifest.json"
  if (
    cd "$fixture"
    export ECI_PROOF_ROOT="$(realpath "$TMP_ROOT/import-proof")" ECI_SESSION_ID=import-on-demand
    "$fixture/bin/eci-active" manifest-write "$TMP_ROOT/import-manifest.json" >/dev/null 2>"$TMP_ROOT/manifest.err"
  ); then
    return 1
  fi
  grep -Fq 'requires the native eci-safe-import binary; run hooks/build-native-binaries.sh.' "$TMP_ROOT/manifest.err"
  cp -p -- "$importer_saved" "$fixture/bin/eci-safe-import"
  (
    cd "$fixture"
    export ECI_PROOF_ROOT="$(realpath "$TMP_ROOT/import-proof")" ECI_SESSION_ID=import-on-demand
    "$fixture/bin/eci-active" manifest-write "$TMP_ROOT/import-manifest.json" >/dev/null
  )
  cmp "$TMP_ROOT/import-manifest.json" "$TMP_ROOT/import-proof/import-on-demand/eci-required-critics.json"
}

if [ "${1:-}" = --lock-paths-only ]; then
  test_eci_active_mutations_fail_closed_when_lock_is_busy
  test_session_start_skips_pruning_when_mutation_lock_is_busy
  printf '%s\n' 'SessionStart and proof-state lock paths: PASS'
  exit 0
fi

test_source_checkout_provisions_native_tools_into_bin
if [ "${1:-}" = --provision-only ]; then
  printf '%s\n' 'source checkout native provisioning tests: PASS'
  exit 0
fi
test_active_eci_refresh_signal_for_session_start_reminder
test_session_start_rejects_marker_owner_mismatch
test_nested_eci_refresh_signal_is_explicit
test_inactive_session_keeps_baseline_context
test_session_start_matcher_uses_supported_lifecycle_sources
test_session_start_rejects_malformed_types
test_session_start_rejects_root_and_session_symlinks
test_baseline_uses_fixed_git_and_resolves_head
test_baseline_pair_recovers_after_publication_boundary
test_eci_active_mutations_fail_closed_when_lock_is_busy
test_session_start_skips_pruning_when_mutation_lock_is_busy
test_old_uuid_session_with_active_eci_marker_survives_cleanup
test_old_marker_dir_with_symlink_eci_marker_is_pruned
printf '%s\n' 'session-snapshot refresh tests: PASS'
