#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/eci-review-least-restriction.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
source "$ROOT/hooks/lib/eci-file-lock.sh"

cp -- "$ROOT/hooks/validate-bash.sh" "$TMP_ROOT/validate-bash.before"
cp -- "$ROOT/hooks/pretooluse-edit-dispatch.sh" "$TMP_ROOT/dispatcher.before"

fixture_root="$TMP_ROOT/fixture-home"
repo="$TMP_ROOT/repo"
proof_root="$TMP_ROOT/proof"
session_id='least-restriction-session'
aggregate_parent="$TMP_ROOT/aggregate-parent"
aggregate_repo="$aggregate_parent/repo-a"

fail() {
  printf 'least-restriction review-gate test failed: %s\n' "$*" >&2
  exit 1
}

setup_fixture() {
  local target="$repo/current.txt"

  rm -rf -- "$fixture_root" "$repo" "$proof_root"
  mkdir -p "$fixture_root/hooks/lib" "$repo" "$proof_root/$session_id"
  cp -- "$ROOT/hooks/eci-review-gate.sh" "$fixture_root/hooks/eci-review-gate.sh"
  cp -- "$ROOT/hooks/lib/eci-proof-state.sh" "$fixture_root/hooks/lib/eci-proof-state.sh"
  cp -- "$ROOT/hooks/lib/eci-file-lock.sh" "$fixture_root/hooks/lib/eci-file-lock.sh"
  cp -- "$ROOT/hooks/lib/eci-diagnostic.sh" "$fixture_root/hooks/lib/eci-diagnostic.sh"
  chmod +x "$fixture_root/hooks/eci-review-gate.sh"

  git -C "$repo" init -q
  git -C "$repo" config user.name 'ECI least-restriction fixture'
  git -C "$repo" config user.email 'eci-least-restriction@example.invalid'
  printf 'before\n' >"$target"
  git -C "$repo" add current.txt
  git -C "$repo" commit -qm 'fixture base'
  printf 'after\n' >>"$target"

  # Deliberately pretty and incomplete: this is stale historical review
  # metadata, not an authorization record. The only actionable current fact
  # is the concrete target path in this repository.
  jq -n --arg target "$target" '
    {
      current_target_path: $target,
      current_diff_sha256: ("0" * 64),
      legacy_receipt: "obsolete",
      rows: ["A", "B", "C"] | map({
        critic_role: .,
        critic_provider: "retired-provider",
        critic_model: "retired-model",
        critic_semantic_role: "obsolete reviewer label",
        critic_provenance: "historical-only",
        report_sha256: ("f" * 64)
      })
    }
  ' >"$proof_root/$session_id/eci-required-critics.json"

  printf 'stale anchor\n' >"$proof_root/$session_id/eci-acceptance-anchor"
  printf 'stale receipt\n' >"$proof_root/$session_id/eci-required-critics.final.99.ledger"
  printf 'stale identity\n' >"$proof_root/$session_id/eci-critic-identities.ledger"
}

run_singleton_gate() {
  local phase="$1"

  ECI_PROOF_ROOT="$proof_root" ECI_REVIEW_CWD="$repo" \
    "$fixture_root/hooks/eci-review-gate.sh" "$phase" "$session_id"
}

run_gate() {
  run_singleton_gate final
}

write_marker() {
  local cwd="$1"

  {
    printf 'scope: eci\n'
    printf 'cwd: %s\n' "$cwd"
    printf 'session_id: %s\n' "$session_id"
    printf 'created_utc: 2026-08-28T00:00:00Z\n'
  } >"$proof_root/$session_id/eci_active"
  chmod 600 "$proof_root/$session_id/eci_active"
}

setup_aggregate_fixture() {
  local target="$aggregate_repo/current.txt" parent_cwd

  rm -rf -- "$fixture_root" "$aggregate_parent" "$proof_root"
  mkdir -p "$fixture_root/hooks/lib" "$aggregate_repo" "$proof_root/$session_id"
  cp -- "$ROOT/hooks/eci-review-gate.sh" "$fixture_root/hooks/eci-review-gate.sh"
  cp -- "$ROOT/hooks/lib/eci-proof-state.sh" "$fixture_root/hooks/lib/eci-proof-state.sh"
  cp -- "$ROOT/hooks/lib/eci-file-lock.sh" "$fixture_root/hooks/lib/eci-file-lock.sh"
  cp -- "$ROOT/hooks/lib/eci-diagnostic.sh" "$fixture_root/hooks/lib/eci-diagnostic.sh"
  chmod +x "$fixture_root/hooks/eci-review-gate.sh"

  git -C "$aggregate_repo" init -q
  git -C "$aggregate_repo" config user.name 'ECI aggregate least-restriction fixture'
  git -C "$aggregate_repo" config user.email 'eci-aggregate-least-restriction@example.invalid'
  printf 'before\n' >"$target"
  git -C "$aggregate_repo" add current.txt
  git -C "$aggregate_repo" commit -qm 'aggregate fixture base'
  printf 'after\n' >>"$target"
  parent_cwd="$(cd -- "$aggregate_parent" && pwd -P)"
  write_marker "$parent_cwd"

  # Deliberately noncanonical, incomplete historical plan/manifest data. It
  # still names one actual current repository and changed target. Neither the
  # old byte form nor missing A/B/C evidence is a reason to halt work.
  jq -n --arg repo "$aggregate_repo" '
    {
      historical_plan_receipt: "obsolete",
      repositories: [{id: "repo-a", repo_root: $repo}]
    }
  ' >"$proof_root/$session_id/eci-aggregate-plan.json"
  jq -n --arg target "$target" '
    {
      current_target_path: $target,
      legacy_receipt: "obsolete",
      rows: []
    }
  ' >"$proof_root/$session_id/eci-aggregate.repo-a.required-critics.json"
  printf 'stale aggregate anchor\n' >"$proof_root/$session_id/eci-aggregate.repo-a.acceptance-anchor"
  printf 'stale aggregate identity\n' >"$proof_root/$session_id/eci-aggregate.repo-a.critic-identities.ledger"
  printf 'stale aggregate commit receipt\n' >"$proof_root/$session_id/eci-aggregate.repo-a.commit-admitted.1"
}

run_aggregate_gate() {
  local phase="$1" parent_cwd

  parent_cwd="$(cd -- "$aggregate_parent" && pwd -P)"

  (
    cd -- "$parent_cwd"
    ECI_PROOF_ROOT="$proof_root" ECI_REVIEW_CWD="$parent_cwd" \
      ECI_AGGREGATE_PARENT_CWD="$parent_cwd" ECI_AGGREGATE_REPO_ID=repo-a \
      "$fixture_root/hooks/eci-review-gate.sh" "$phase" "$session_id"
  )
}

assert_live_hooks_unchanged() {
  cmp -s -- "$TMP_ROOT/validate-bash.before" "$ROOT/hooks/validate-bash.sh" ||
    fail 'validate-bash source was changed'
  cmp -s -- "$TMP_ROOT/dispatcher.before" "$ROOT/hooks/pretooluse-edit-dispatch.sh" ||
    fail 'pretooluse edit-dispatch source was changed'
}

test_stale_review_history_is_nonblocking() {
  local out="$TMP_ROOT/stale-history.out" err="$TMP_ROOT/stale-history.err"
  local lock_ready="$TMP_ROOT/lock-ready" holder

  setup_fixture
  (
    exec 9>>"$proof_root/.eci-active.lock"
    eci_file_lock_acquire_fd 9 nonblocking
    : >"$lock_ready"
    sleep 0.2
  ) &
  holder=$!
  while [ ! -e "$lock_ready" ]; do sleep 0.01; done

  if ! run_gate >"$out" 2>"$err"; then
    cat "$err" >&2
    wait "$holder" || true
    fail 'stale history blocked the current in-repository diff'
  fi
  wait "$holder"
  grep -Fq 'fresh named least-restriction critic' "$out" || {
    cat "$out" >&2
    fail 'the nonblocking review reminder omitted the required workflow critic'
  }
}

test_symlinked_current_target_escape_still_fails() {
  local manifest="$proof_root/$session_id/eci-required-critics.json"
  local outside="$TMP_ROOT/outside.txt" target="$repo/current-escape.txt"
  local out="$TMP_ROOT/outside.out" err="$TMP_ROOT/outside.err"

  setup_fixture
  printf 'outside\n' >"$outside"
  ln -s -- "$outside" "$target"
  jq --arg target "$target" '.current_target_path = $target' "$manifest" >"$manifest.tmp"
  mv -- "$manifest.tmp" "$manifest"
  if run_gate >"$out" 2>"$err"; then
    fail 'a current-target alias escaping the repository was accepted'
  fi
  grep -Fq 'outside the current repository' "$err" || {
    cat "$err" >&2
    fail 'resolved escape rejection was not specific'
  }
}

test_unparseable_historical_manifest_is_a_reminder() {
  local manifest="$proof_root/$session_id/eci-required-critics.json"
  local out="$TMP_ROOT/unparseable.out" err="$TMP_ROOT/unparseable.err"

  setup_fixture
  printf 'old malformed review metadata\n' >"$manifest"
  if ! run_gate >"$out" 2>"$err"; then
    cat "$err" >&2
    fail 'unparseable historical metadata blocked ordinary work'
  fi
  grep -Fq 'review refresh needed' "$out" || {
    cat "$out" >&2
    fail 'unparseable history did not become a review reminder'
  }
}

assert_singleton_manifest_shape_is_a_reminder() {
  local shape="$1" manifest="$proof_root/$session_id/eci-required-critics.json"
  local outside_metadata="$TMP_ROOT/singleton-$shape-metadata.json"
  local outside_target="$TMP_ROOT/singleton-$shape-outside-target"
  local out="$TMP_ROOT/singleton-$shape.out" err="$TMP_ROOT/singleton-$shape.err"

  setup_fixture
  case "$shape" in
    symlink)
      # If the review gate followed this link, the external target would be a
      # concrete repository escape. Metadata shape itself must be advisory,
      # so the link must instead be ignored without reading its destination.
      jq -n --arg target "$outside_target" '{current_target_path: $target}' >"$outside_metadata"
      rm -f -- "$manifest"
      ln -s -- "$outside_metadata" "$manifest"
      ;;
    directory)
      rm -f -- "$manifest"
      mkdir -- "$manifest"
      ;;
    *) fail "unknown singleton metadata shape: $shape" ;;
  esac

  if ! run_gate >"$out" 2>"$err"; then
    cat "$err" >&2
    fail "singleton $shape review metadata blocked ordinary work"
  fi
  grep -Fq 'review refresh needed' "$out" || {
    cat "$out" >&2
    fail "singleton $shape metadata did not become a review reminder"
  }
  grep -Fq 'review metadata is not a regular local file' "$out" || {
    cat "$out" >&2
    fail "singleton $shape metadata reminder did not identify local metadata shape"
  }
}

test_singleton_manifest_shapes_are_advisory() {
  assert_singleton_manifest_shape_is_a_reminder symlink
  assert_singleton_manifest_shape_is_a_reminder directory
}

test_symlinked_current_review_cwd_resolves_within_repo() {
  local link="$TMP_ROOT/current-review-cwd-link" out="$TMP_ROOT/review-cwd-link.out" err="$TMP_ROOT/review-cwd-link.err"
  local resolved_target

  setup_fixture
  ln -s -- "$repo" "$link"
  if ! ECI_PROOF_ROOT="$proof_root" ECI_REVIEW_CWD="$link" \
    "$fixture_root/hooks/eci-review-gate.sh" final "$session_id" >"$out" 2>"$err"; then
    cat "$err" >&2
    fail 'a review-cwd alias resolving to the current repository was rejected'
  fi
  resolved_target="$(eci_realpath_existing "$repo/current.txt")"
  grep -Fq "target=$resolved_target" "$out" || {
    cat "$out" >&2
    fail 'review-cwd alias did not resolve to the current repository target'
  }
}

test_symlinked_current_target_resolves_within_repo() {
  local manifest="$proof_root/$session_id/eci-required-critics.json"
  local target="$repo/symlink-current.txt" out="$TMP_ROOT/symlink-current.out" err="$TMP_ROOT/symlink-current.err"
  local resolved_target

  setup_fixture
  ln -s -- "$repo/current.txt" "$target"
  jq --arg target "$target" '.current_target_path = $target' "$manifest" >"$manifest.tmp"
  mv -- "$manifest.tmp" "$manifest"
  if ! run_gate >"$out" 2>"$err"; then
    cat "$err" >&2
    fail 'a current-target alias resolving within the repository was rejected'
  fi
  resolved_target="$(eci_realpath_existing "$repo/current.txt")"
  grep -Fq "target=$resolved_target" "$out" || {
    cat "$out" >&2
    fail 'current-target alias did not resolve to the changed file'
  }
}

test_singleton_commit_history_and_lock_are_nonblocking() {
  local out="$TMP_ROOT/commit.out" err="$TMP_ROOT/commit.err"
  local lock_ready="$TMP_ROOT/commit-lock-ready" holder receipt_before identity_before

  setup_fixture
  printf 'stale commit receipt\n' >"$proof_root/$session_id/eci-commit-admitted"
  receipt_before="$(eci_sha256_hex "$proof_root/$session_id/eci-commit-admitted")"
  identity_before="$(eci_sha256_hex "$proof_root/$session_id/eci-critic-identities.ledger")"
  (
    exec 9>>"$proof_root/.eci-active.lock"
    eci_file_lock_acquire_fd 9 nonblocking
    : >"$lock_ready"
    sleep 0.2
  ) &
  holder=$!
  while [ ! -e "$lock_ready" ]; do sleep 0.01; done

  if ! run_singleton_gate commit >"$out" 2>"$err"; then
    cat "$err" >&2
    wait "$holder" || true
    fail 'historical singleton commit state or lock blocked current work'
  fi
  wait "$holder"
  grep -Fq 'fresh named least-restriction critic' "$out" || fail 'commit did not route a fresh critic reminder'
  [ "$(eci_sha256_hex "$proof_root/$session_id/eci-commit-admitted")" = "$receipt_before" ] ||
    fail 'commit created or rewrote a legacy permission receipt'
  [ "$(eci_sha256_hex "$proof_root/$session_id/eci-critic-identities.ledger")" = "$identity_before" ] ||
    fail 'commit rewrote historical identities'
}

test_singleton_off_history_and_lock_are_nonblocking() {
  local out="$TMP_ROOT/off.out" err="$TMP_ROOT/off.err"
  local lock_ready="$TMP_ROOT/off-lock-ready" holder marker_before anchor_before

  setup_fixture
  write_marker "$repo"
  marker_before="$(eci_sha256_hex "$proof_root/$session_id/eci_active")"
  anchor_before="$(eci_sha256_hex "$proof_root/$session_id/eci-acceptance-anchor")"
  (
    exec 9>>"$proof_root/.eci-active.lock"
    eci_file_lock_acquire_fd 9 nonblocking
    : >"$lock_ready"
    sleep 0.2
  ) &
  holder=$!
  while [ ! -e "$lock_ready" ]; do sleep 0.01; done

  if ! run_singleton_gate off >"$out" 2>"$err"; then
    cat "$err" >&2
    wait "$holder" || true
    fail 'historical singleton off state or lock blocked current work'
  fi
  wait "$holder"
  grep -Fq 'fresh named least-restriction critic' "$out" || fail 'off did not route a fresh critic reminder'
  [ "$(eci_sha256_hex "$proof_root/$session_id/eci_active")" = "$marker_before" ] ||
    fail 'off gate rewrote the current marker'
  [ "$(eci_sha256_hex "$proof_root/$session_id/eci-acceptance-anchor")" = "$anchor_before" ] ||
    fail 'off gate rewrote historical anchor state'
}

assert_aggregate_history_phase_is_nonblocking() {
  local phase="$1" out="$TMP_ROOT/aggregate-$1.out" err="$TMP_ROOT/aggregate-$1.err"
  local lock_ready="$TMP_ROOT/aggregate-$1-lock-ready" holder anchor_before

  setup_aggregate_fixture
  anchor_before="$(eci_sha256_hex "$proof_root/$session_id/eci-aggregate.repo-a.acceptance-anchor")"
  (
    exec 9>>"$proof_root/.eci-active.lock"
    eci_file_lock_acquire_fd 9 nonblocking
    : >"$lock_ready"
    sleep 0.2
  ) &
  holder=$!
  while [ ! -e "$lock_ready" ]; do sleep 0.01; done

  if ! run_aggregate_gate "$phase" >"$out" 2>"$err"; then
    cat "$err" >&2
    wait "$holder" || true
    fail "historical aggregate $phase state or lock blocked current work"
  fi
  wait "$holder"
  grep -Fq 'fresh named least-restriction critic' "$out" || fail "aggregate $phase did not route a fresh critic reminder"
  [ "$(eci_sha256_hex "$proof_root/$session_id/eci-aggregate.repo-a.acceptance-anchor")" = "$anchor_before" ] ||
    fail "aggregate $phase gate rewrote historical anchor state"
}

test_aggregate_history_and_lock_are_nonblocking() {
  assert_aggregate_history_phase_is_nonblocking final
  assert_aggregate_history_phase_is_nonblocking commit
  assert_aggregate_history_phase_is_nonblocking off
}

test_aggregate_outside_current_target_still_fails() {
  local manifest="$proof_root/$session_id/eci-aggregate.repo-a.required-critics.json"
  local outside="$TMP_ROOT/aggregate-outside.txt" out="$TMP_ROOT/aggregate-outside.out" err="$TMP_ROOT/aggregate-outside.err"

  setup_aggregate_fixture
  printf 'outside\n' >"$outside"
  jq --arg outside "$outside" '.current_target_path = $outside' "$manifest" >"$manifest.tmp"
  mv -- "$manifest.tmp" "$manifest"
  if run_aggregate_gate final >"$out" 2>"$err"; then
    fail 'an aggregate outside current target was accepted'
  fi
  grep -Fq 'outside the current repository' "$err" || {
    cat "$err" >&2
    fail 'aggregate outside-target rejection was not specific'
  }
}

test_aggregate_symlink_current_target_resolves_within_repo() {
  local manifest="$proof_root/$session_id/eci-aggregate.repo-a.required-critics.json"
  local target="$aggregate_repo/symlink-current.txt"
  local out="$TMP_ROOT/aggregate-symlink.out" err="$TMP_ROOT/aggregate-symlink.err"
  local resolved_target

  setup_aggregate_fixture
  ln -s -- "$aggregate_repo/current.txt" "$target"
  jq --arg target "$target" '.current_target_path = $target' "$manifest" >"$manifest.tmp"
  mv -- "$manifest.tmp" "$manifest"
  if ! run_aggregate_gate final >"$out" 2>"$err"; then
    cat "$err" >&2
    fail 'an aggregate target alias resolving within the selected repository was rejected'
  fi
  resolved_target="$(eci_realpath_existing "$aggregate_repo/current.txt")"
  grep -Fq "target=$resolved_target" "$out" || {
    cat "$out" >&2
    fail 'aggregate target alias did not resolve to the changed file'
  }
}

test_aggregate_repository_alias_escape_still_fails() {
  local plan="$proof_root/$session_id/eci-aggregate-plan.json"
  local outside_repo="$TMP_ROOT/aggregate-external-repo" link="$aggregate_parent/repo-external-link"
  local out="$TMP_ROOT/aggregate-external.out" err="$TMP_ROOT/aggregate-external.err"

  setup_aggregate_fixture
  mkdir -p "$outside_repo"
  git -C "$outside_repo" init -q
  ln -s -- "$outside_repo" "$link"
  jq --arg repo "$link" '.repositories[0].repo_root = $repo' "$plan" >"$plan.tmp"
  mv -- "$plan.tmp" "$plan"
  if run_aggregate_gate final >"$out" 2>"$err"; then
    fail 'an aggregate repository alias escaping its current parent was accepted'
  fi
  grep -Fq 'outside the current aggregate parent cwd' "$err" || {
    cat "$err" >&2
    fail 'aggregate parent-scope rejection was not specific'
  }
}

test_aggregate_symlinked_repository_target_resolves_within_parent() {
  local plan="$proof_root/$session_id/eci-aggregate-plan.json"
  local link="$aggregate_parent/repo-link" out="$TMP_ROOT/aggregate-repo-link.out" err="$TMP_ROOT/aggregate-repo-link.err"
  local resolved_target

  setup_aggregate_fixture
  ln -s -- "$aggregate_repo" "$link"
  jq --arg repo "$link" '.repositories[0].repo_root = $repo' "$plan" >"$plan.tmp"
  mv -- "$plan.tmp" "$plan"
  if ! run_aggregate_gate final >"$out" 2>"$err"; then
    cat "$err" >&2
    fail 'an aggregate repository alias resolving within the parent was rejected'
  fi
  resolved_target="$(eci_realpath_existing "$aggregate_repo/current.txt")"
  grep -Fq "target=$resolved_target" "$out" || {
    cat "$out" >&2
    fail 'aggregate repository alias did not resolve to the selected target'
  }
}

assert_aggregate_plan_shape_is_a_reminder() {
  local shape="$1" plan="$proof_root/$session_id/eci-aggregate-plan.json"
  local outside_repo="$TMP_ROOT/aggregate-$shape-outside-repo"
  local outside_plan="$TMP_ROOT/aggregate-$shape-plan.json"
  local out="$TMP_ROOT/aggregate-$shape-plan.out" err="$TMP_ROOT/aggregate-$shape-plan.err"

  setup_aggregate_fixture
  case "$shape" in
    symlink)
      # If followed, this plan names a real repository outside the aggregate
      # parent and would trigger the concrete escape boundary. The symlink is
      # historical metadata, so it must be ignored without following it.
      mkdir -- "$outside_repo"
      git -C "$outside_repo" init -q
      jq -n --arg repo "$outside_repo" '{repositories: [{id: "repo-a", repo_root: $repo}]}' >"$outside_plan"
      rm -f -- "$plan"
      ln -s -- "$outside_plan" "$plan"
      ;;
    directory)
      rm -f -- "$plan"
      mkdir -- "$plan"
      ;;
    *) fail "unknown aggregate plan shape: $shape" ;;
  esac

  if ! run_aggregate_gate final >"$out" 2>"$err"; then
    cat "$err" >&2
    fail "aggregate $shape plan metadata blocked ordinary work"
  fi
  grep -Fq 'review refresh needed' "$out" || {
    cat "$out" >&2
    fail "aggregate $shape plan did not become a review reminder"
  }
  grep -Fq 'aggregate repository metadata is not a regular local file' "$out" || {
    cat "$out" >&2
    fail "aggregate $shape plan reminder did not identify local metadata shape"
  }
}

test_aggregate_plan_shapes_are_advisory() {
  assert_aggregate_plan_shape_is_a_reminder symlink
  assert_aggregate_plan_shape_is_a_reminder directory
}

assert_live_hooks_unchanged
test_stale_review_history_is_nonblocking
test_symlinked_current_target_escape_still_fails
test_unparseable_historical_manifest_is_a_reminder
test_singleton_manifest_shapes_are_advisory
test_symlinked_current_review_cwd_resolves_within_repo
test_symlinked_current_target_resolves_within_repo
test_singleton_commit_history_and_lock_are_nonblocking
test_singleton_off_history_and_lock_are_nonblocking
test_aggregate_history_and_lock_are_nonblocking
test_aggregate_outside_current_target_still_fails
test_aggregate_symlink_current_target_resolves_within_repo
test_aggregate_repository_alias_escape_still_fails
test_aggregate_symlinked_repository_target_resolves_within_parent
test_aggregate_plan_shapes_are_advisory
assert_live_hooks_unchanged
printf '%s\n' 'ECI least-restriction review-gate tests: PASS'
