#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-post-compact-refresh.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

install_configured_home_fixture() {
  # hooks.json deliberately resolves its hook target from HOME.  Give the
  # configured-command tests one canonical HOME/.omp/agent/eci view of this source
  # tree, rather than smuggling the source root back in through ECI_HOME.
  mkdir -p "$TMP_ROOT/home" "$TMP_ROOT/home/.omp/agent"
  if [ -e "$TMP_ROOT/home/.omp/agent/eci" ] || [ -L "$TMP_ROOT/home/.omp/agent/eci" ]; then
    [ -L "$TMP_ROOT/home/.omp/agent/eci" ] && [ "$(readlink -- "$TMP_ROOT/home/.omp/agent/eci")" = "$ROOT" ]
  else
    ln -s "$ROOT" "$TMP_ROOT/home/.omp/agent/eci"
  fi
}

run_post_compact() {
  local proof_root="$1"
  local out="$2"

  mkdir -p "$TMP_ROOT/home/tmp" "$proof_root"
  jq -cn --arg cwd "$ROOT" \
    '{hook_event_name:"PostCompact", session_id:"t00-session", cwd:$cwd, source:"compaction"}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/eci-post-compact-refresh.sh" >"$out"
}

write_direct_marker() {
  local proof_root="$1" scope="${2:-post compact test}"
  printf 'scope: %s\ncwd: %s\nsession_id: t00-session\ncreated_utc: 2026-08-14T00:00:00Z\n' "$scope" "$ROOT" >"$proof_root/t00-session/eci_active"
}

test_post_compact_active_eci_uses_provider_valid_empty_output() {
  local proof_root="$TMP_ROOT/active-proof" out
  mkdir -p "$proof_root/t00-session"
  write_direct_marker "$proof_root"
  out="$TMP_ROOT/active.out"

  run_post_compact "$proof_root" "$out"

  jq -e '
    type == "object" and
    (keys | length == 0) and
    (has("hookSpecificOutput") | not)
  ' "$out" >/dev/null
  if grep -Fq 'hookSpecificOutput' "$ROOT/hooks/eci-post-compact-refresh.sh"; then
    return 1
  fi
}

test_post_compact_inactive_eci_is_silent() {
  local proof_root="$TMP_ROOT/inactive-proof" out
  out="$TMP_ROOT/inactive.out"

  run_post_compact "$proof_root" "$out"

  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
}

test_post_compact_symlink_marker_is_silent() {
  local proof_root="$TMP_ROOT/symlink-proof" out target
  target="$TMP_ROOT/eci-target"
  mkdir -p "$proof_root/t00-session"
  printf '%s\n' 'scope: symlink marker must not activate refresh' >"$target"
  ln -s "$target" "$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/symlink.out"

  run_post_compact "$proof_root" "$out"

  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
}

test_post_compact_does_not_scan_or_mutate_state() {
  local proof_root="$TMP_ROOT/no-write-proof" out before after
  mkdir -p "$proof_root/t00-session"
  write_direct_marker "$proof_root" 'no-write test'
  before="$(find "$proof_root" -mindepth 1 -maxdepth 2 -printf '%P:%y:%s\n' | sort)"
  out="$TMP_ROOT/no-write.out"

  run_post_compact "$proof_root" "$out"

  after="$(find "$proof_root" -mindepth 1 -maxdepth 2 -printf '%P:%y:%s\n' | sort)"
  [ "$before" = "$after" ] || return 1
  [ ! -e "$proof_root/t00-session/eci_refresh_pending" ] &&
    [ ! -L "$proof_root/t00-session/eci_refresh_pending" ] || return 1
  if grep -Eq '(^|[[:space:]])(find|mkdir|rm|sleep|timeout|date)([[:space:]]|$)' \
      "$ROOT/hooks/eci-post-compact-refresh.sh"; then
    return 1
  fi
  if grep -Fq 'transcript' "$ROOT/hooks/eci-post-compact-refresh.sh"; then
    return 1
  fi
}

test_post_compact_rejects_malformed_session_or_cwd() {
  local proof_root="$TMP_ROOT/malformed-proof" out
  mkdir -p "$proof_root/t00-session"
  write_direct_marker "$proof_root" 'malformed input must not refresh'
  out="$TMP_ROOT/malformed.out"
  printf '%s\n' '{"session_id":"t00-session","cwd":[],"source":"compaction"}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/eci-post-compact-refresh.sh" >"$out"
  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
}

test_post_compact_rejects_invalid_json_and_never_leaks_noise() {
  local proof_root="$TMP_ROOT/invalid-json-proof" out command
  mkdir -p "$proof_root/t00-session" "$TMP_ROOT/home"
  write_direct_marker "$proof_root" 'invalid JSON must stay silent'
  out="$TMP_ROOT/invalid-json.out"
  printf '%s' '{"hook_event_name":"PostCompact"' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/eci-post-compact-refresh.sh" >"$out"
  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null || return 1

  # The configured command uses non-login bash, so an accidental .bashrc
  # print cannot prefix the one JSON object emitted by an active refresh.
  printf '%s\n' 'printf startup-noise >&2' 'printf stdout-noise' >"$TMP_ROOT/home/.bashrc"
  install_configured_home_fixture
  command="$(jq -r '.hooks.PostCompact[0].hooks[0].command' "$ROOT/hooks.json")"
  out="$TMP_ROOT/configured.out"
  jq -cn --arg cwd "$ROOT" \
    '{hook_event_name:"PostCompact",session_id:"t00-session",cwd:$cwd,trigger:"manual"}' |
    ECI_HOME="$ROOT" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash -c "$command" >"$out"
  [ "$(wc -l <"$out")" -eq 1 ] || return 1
  jq -e 'type == "object" and (keys | length == 0) and (has("hookSpecificOutput") | not)' "$out" >/dev/null
}

test_post_compact_full_lifecycle_payload_has_exact_provider_output() {
  local proof_root="$TMP_ROOT/full-lifecycle-proof" out err expected command transcript
  mkdir -p "$proof_root/t00-session" "$TMP_ROOT/home"
  write_direct_marker "$proof_root" 'full lifecycle payload must stay provider-valid'
  transcript="$TMP_ROOT/transcript.jsonl"
  printf '%s\n' '{"type":"message","role":"assistant"}' >"$transcript"
  expected="$TMP_ROOT/full-lifecycle.expected"
  printf '{}\n' >"$expected"
  out="$TMP_ROOT/full-lifecycle.out"
  err="$TMP_ROOT/full-lifecycle.err"
  install_configured_home_fixture
  command="$(jq -r '.hooks.PostCompact[0].hooks[0].command' "$ROOT/hooks.json")"

  jq -cn --arg cwd "$ROOT" --arg transcript "$transcript" \
    '{session_id:"t00-session",transcript_path:$transcript,cwd:$cwd,hook_event_name:"PostCompact",trigger:"auto",source:"compaction"}' |
    ECI_HOME="$ROOT" HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash -c "$command" >"$out" 2>"$err"

  cmp -- "$expected" "$out"
  [ ! -s "$err" ]
  jq -e 'type == "object" and (keys | length == 0) and (has("hookSpecificOutput") | not)' "$out" >/dev/null
}

test_post_compact_requires_event_and_trigger_contract() {
  local proof_root="$TMP_ROOT/event-contract-proof" out
  mkdir -p "$proof_root/t00-session"
  write_direct_marker "$proof_root" 'event contract'
  out="$TMP_ROOT/event-contract.out"
  jq -cn --arg cwd "$ROOT" '{hook_event_name:"SessionStart",session_id:"t00-session",cwd:$cwd}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/eci-post-compact-refresh.sh" >"$out"
  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
  jq -cn --arg cwd "$ROOT" '{hook_event_name:"PostCompact",trigger:"timer",session_id:"t00-session",cwd:$cwd}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/eci-post-compact-refresh.sh" >"$out"
  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
  jq -cn --arg cwd "$ROOT" '{hook_event_name:"PostCompact",trigger:"manual",session_id:"t00-session",cwd:$cwd}' |
    HOME="$TMP_ROOT/home" ECI_PROOF_ROOT="$proof_root" \
      bash "$ROOT/hooks/eci-post-compact-refresh.sh" >"$out"
  jq -e 'type == "object" and (keys | length == 0) and (has("hookSpecificOutput") | not)' "$out" >/dev/null
}

test_post_compact_rejects_marker_owner_mismatch() {
  local proof_root="$TMP_ROOT/mismatched-owner-proof" out
  mkdir -p "$proof_root/t00-session"
  write_direct_marker "$proof_root" 'mismatched owner must not refresh'
  sed -i 's/^session_id: t00-session$/session_id: t00-other/' "$proof_root/t00-session/eci_active"
  out="$TMP_ROOT/mismatched-owner.out"
  run_post_compact "$proof_root" "$out"
  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
}

test_post_compact_nested_marker_is_explicit_and_bounded() {
  local proof_root="$TMP_ROOT/nested-proof" out
  mkdir -p "$proof_root/t00-session"
  printf 'scope: nested outer\ncwd: %s\nsession_id: t00-session\ncreated_utc: 2026-08-14T00:00:00Z\n' "$ROOT" >"$proof_root/t00-session/eci_active"
  printf 'outer_session_id: t00-session\nouter_marker: %s/eci_active\nowner: ate\nwriter_session_id: t00-session\nacceptance_version: 1\nstep: 2\niteration: 3\nstate: active\n' "$proof_root/t00-session" >"$proof_root/t00-session/ate_nested_eci_active"
  out="$TMP_ROOT/nested.out"
  run_post_compact "$proof_root" "$out"
  jq -e 'type == "object" and (keys | length == 0) and (has("hookSpecificOutput") | not)' "$out" >/dev/null
  rm -f -- "$proof_root/t00-session/eci_active"
  printf 'outer_session_id: wrong-session\nouter_marker: %s/eci_active\nowner: ate\nwriter_session_id: t00-session\nacceptance_version: 1\nstep: 2\niteration: 3\nstate: active\n' "$proof_root/t00-session" >"$proof_root/t00-session/ate_nested_eci_active"
  run_post_compact "$proof_root" "$out"
  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
  printf 'outer_session_id: t00-session\nstep: 2\niteration: 3\n' >"$proof_root/t00-session/ate_nested_eci_active"
  run_post_compact "$proof_root" "$out"
  jq -e 'type == "object" and (keys | length == 0)' "$out" >/dev/null
}

test_post_compact_hook_is_registered_and_session_start_is_restricted() {
  local postcompact_hook_state
  jq -e '
    (.hooks.SessionStart | all(.matcher == "startup|resume|clear")) and
    ([.hooks.PostCompact[]?.hooks[]?.command]
      | any(contains("/hooks/eci-post-compact-refresh.sh")))
  ' "$ROOT/hooks.json" >/dev/null
  postcompact_hook_state="[hooks.state.\"$ROOT/hooks.json:post_compact:0:0\"]"
  # This is only the existing user-owned trust-state key. The test exercises
  # source command shape directly and must not claim that the changed command
  # has already been trusted by a fresh ECI launch.
  grep -Fq "$postcompact_hook_state" "$ROOT/config.toml"
}

test_post_compact_active_eci_uses_provider_valid_empty_output
test_post_compact_inactive_eci_is_silent
test_post_compact_symlink_marker_is_silent
test_post_compact_does_not_scan_or_mutate_state
test_post_compact_rejects_malformed_session_or_cwd
test_post_compact_rejects_invalid_json_and_never_leaks_noise
test_post_compact_full_lifecycle_payload_has_exact_provider_output
test_post_compact_requires_event_and_trigger_contract
test_post_compact_rejects_marker_owner_mismatch
test_post_compact_nested_marker_is_explicit_and_bounded
test_post_compact_hook_is_registered_and_session_start_is_restricted
printf '%s\n' 'PostCompact ECI refresh tests: PASS'
