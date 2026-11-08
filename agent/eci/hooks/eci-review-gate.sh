#!/usr/bin/env bash
# TODO: Remove teardown's ECI marker dependency when this hook is migrated;
# native goals own continuation and the Supervisor owns ECI lifecycle.
# Acceptance boundary for the required-critic ledger.
#
# This script is intentionally absent from Stop's active-marker fast path.  It
# may inspect bounded repository/artifact state and acquire the mutation lock;
# Stop callbacks must remain marker-only and read-only.

set -euo pipefail

HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HOOK_DIR/lib/eci-proof-state.sh"
. "$HOOK_DIR/lib/eci-diagnostic.sh"

usage() {
  local subject="phase=$(eci_diagnostic_value "${1:-<missing>}"),session=$(eci_diagnostic_value "${2:-<missing>}"),cwd=$(eci_diagnostic_value "${ECI_REVIEW_CWD:-$PWD}")"
  local detail="Usage: eci-review-gate.sh <commit|final|off|prewrite> <session-id>"
  printf '%s\n' "$(eci_diagnostic_reason "$(eci_diagnostic_code_for_reason "$detail")" "Stop" "review-gate-cli" "$subject" "$detail" "invoke the review gate with one supported phase and a valid session id")" >&2
}

fail_gate() {
  local detail="${1:-unspecified review-gate failure}"
  local subject="session=${session_id:-<missing>},cwd=$(eci_diagnostic_value "${ECI_REVIEW_CWD:-$PWD}"),manifest=$(eci_diagnostic_value "${manifest:-${root:-<unresolved>}/${session_id:-<missing>}/eci-required-critics.json}")"
  printf '%s\n' "$(eci_diagnostic_reason "$(eci_diagnostic_code_for_reason "$detail")" "${phase:-startup}" "${phase:-unknown-operation}" "$subject" "$detail" "correct the reported gate input or evidence for the identified session/cwd/manifest, then retry the ${phase:-requested} acceptance operation")" >&2
  exit 1
}

review_refresh_needed() {
  local reason="$1" target="${2:-<unresolved>}"

  # Review history is a reminder, not an authorization mechanism.  The
  # coordinator workflow routes fresh review work; this gate must not turn an
  # old receipt, role label, hash, or lock into a normal-work denial.
  printf 'ECI review refresh needed: phase=%s target=%s reason=%s; ordinary work continues; workflow must route one fresh named least-restriction critic for the current diff.\n' \
    "$phase" "$target" "$reason"
}

review_metadata_file_or_refresh() {
  local metadata_path="$1" missing_reason="$2" shape_reason="$3" target="${4:-$1}"

  # Historical review metadata is not an authority.  Do not follow a link
  # merely to decide whether ordinary work may continue: report a refresh
  # need and leave current repository/target checks to regular metadata.
  if [ -L "$metadata_path" ]; then
    review_refresh_needed "$shape_reason" "$target"
    return 1
  fi
  if [ ! -e "$metadata_path" ]; then
    review_refresh_needed "$missing_reason" "$target"
    return 1
  fi
  if [ ! -f "$metadata_path" ]; then
    review_refresh_needed "$shape_reason" "$target"
    return 1
  fi
  return 0
}

allow_current_target_or_refresh() {
  local review_cwd_raw="$1" review_cwd="$1" manifest_path="$2" repo_root_hint="${3:-}"
  local repo_root target target_candidate target_path relative actual_repo_root

  # Spelling a current directory through an in-scope symlink is not an
  # accidental mistake. Resolve it first; the resolved repository boundary
  # below decides whether it actually escapes the review scope.
  review_cwd="$(eci_realpath_existing "$review_cwd_raw" 2>/dev/null || true)"
  [ -n "$review_cwd" ] && [ -d "$review_cwd" ] ||
    fail_gate 'ECI required-critic review gate rejected an unsafe review cwd.'
  if [ -n "$repo_root_hint" ]; then
    repo_root="$(eci_realpath_existing "$repo_root_hint" 2>/dev/null || true)"
    [ -n "$repo_root" ] && [ -d "$repo_root" ] ||
      fail_gate 'ECI required-critic review gate rejected an unsafe current repository target.'
  else
    repo_root="$(git -C "$review_cwd" rev-parse --show-toplevel 2>/dev/null || true)"
    if [ -z "$repo_root" ]; then
      review_refresh_needed 'no current repository is available for review' "$review_cwd"
      return 0
    fi
  fi
  repo_root="$(eci_realpath_existing "$repo_root" 2>/dev/null || true)"
  [ -n "$repo_root" ] && [ -d "$repo_root" ] ||
    fail_gate 'ECI required-critic review gate rejected an unsafe current repository target.'
  actual_repo_root="$(git -C "$repo_root" rev-parse --show-toplevel 2>/dev/null || true)"
  if [ -z "$actual_repo_root" ]; then
    review_refresh_needed 'no current repository is available for review' "$repo_root"
    return 0
  fi
  actual_repo_root="$(eci_realpath_existing "$actual_repo_root" 2>/dev/null || true)"
  [ -n "$actual_repo_root" ] && [ -d "$actual_repo_root" ] ||
    fail_gate 'ECI required-critic review gate rejected an unsafe current repository target.'
  repo_root="$actual_repo_root"

  if ! review_metadata_file_or_refresh \
    "$manifest_path" \
    'no current review metadata exists' \
    'historical review metadata is not a regular local file'; then
    return 0
  fi
  if ! jq -e . "$manifest_path" >/dev/null 2>&1; then
    review_refresh_needed 'historical review metadata is not parseable'
    return 0
  fi

  target="$(jq -r 'if (.current_target_path? | type) == "string" then .current_target_path else empty end' "$manifest_path" 2>/dev/null || true)"
  if [ -z "$target" ]; then
    review_refresh_needed 'review metadata has no current target'
    return 0
  fi
  case "$target" in
    /*) target_candidate="$target" ;;
    *) target_candidate="$repo_root/$target" ;;
  esac
  target_path="$(eci_realpath_existing "$target_candidate" 2>/dev/null || true)"
  if [ -z "$target_path" ]; then
    target_path="$(eci_realpath_canonical "$target_candidate" 2>/dev/null || true)"
    review_refresh_needed 'the current target no longer exists' "$target_path"
    return 0
  fi
  case "$target_path" in
    "$repo_root"/*) ;;
    *) fail_gate "ECI required-critic review gate rejected a current target outside the current repository: $target" ;;
  esac
  if [ ! -f "$target_path" ]; then
    review_refresh_needed 'the current target is not a regular file' "$target_path"
    return 0
  fi

  relative="${target_path#"$repo_root"/}"
  if git -C "$repo_root" diff --quiet -- "$relative" &&
    git -C "$repo_root" diff --cached --quiet -- "$relative"; then
    review_refresh_needed 'the target is no longer part of the current diff' "$target_path"
    return 0
  fi

  printf 'ECI review gate passed: phase=%s target=%s; ordinary work continues; workflow must route one fresh named least-restriction critic for the current diff.\n' \
    "$phase" "$target_path"
}

allow_aggregate_current_target_or_refresh() {
  local parent_cwd_raw="$1" parent_cwd="$1" repo_id="$2" plan_path="$3" manifest_path="$4"
  local repo_root actual_repo_root

  parent_cwd="$(eci_realpath_existing "$parent_cwd_raw" 2>/dev/null || true)"
  [ -n "$parent_cwd" ] && [ -d "$parent_cwd" ] ||
    fail_gate 'ECI aggregate review gate rejected an unsafe current aggregate parent cwd.'

  if ! review_metadata_file_or_refresh \
    "$plan_path" \
    'no current aggregate repository metadata exists' \
    'historical aggregate repository metadata is not a regular local file' \
    "$parent_cwd"; then
    return 0
  fi
  if ! jq -e . "$plan_path" >/dev/null 2>&1; then
    review_refresh_needed 'historical aggregate repository metadata is not parseable' "$parent_cwd"
    return 0
  fi

  repo_root="$(jq -r --arg id "$repo_id" '
    [.repositories?[]? | select((.id? | type) == "string" and .id == $id) |
      select((.repo_root? | type) == "string") | .repo_root] | first // empty
  ' "$plan_path" 2>/dev/null || true)"
  if [ -z "$repo_root" ]; then
    review_refresh_needed 'aggregate metadata has no current selected repository' "$parent_cwd"
    return 0
  fi
  repo_root="$(eci_realpath_existing "$repo_root" 2>/dev/null || true)"
  if [ -z "$repo_root" ] || [ ! -d "$repo_root" ]; then
    review_refresh_needed 'the selected aggregate repository no longer exists' "$parent_cwd"
    return 0
  fi

  actual_repo_root="$(git -C "$repo_root" rev-parse --show-toplevel 2>/dev/null || true)"
  if [ -z "$actual_repo_root" ]; then
    review_refresh_needed 'the selected aggregate repository is no longer a current Git worktree' "$repo_root"
    return 0
  fi
  actual_repo_root="$(eci_realpath_existing "$actual_repo_root" 2>/dev/null || true)"
  [ -n "$actual_repo_root" ] && [ -d "$actual_repo_root" ] ||
    fail_gate 'ECI aggregate review gate rejected an unsafe current aggregate repository target.'
  case "$actual_repo_root" in
    "$parent_cwd"/*) ;;
    *) fail_gate 'ECI aggregate review gate rejected a current repository outside the current aggregate parent cwd.' ;;
  esac

  allow_current_target_or_refresh "$parent_cwd" "$manifest_path" "$actual_repo_root"
}

phase="${1:-}"
session_id="${2:-${ECI_SESSION_ID:-${ECI_THREAD_ID:-}}}"
case "$phase" in
  commit|final|off|prewrite) ;;
  *) usage; exit 2 ;;
esac
eci_valid_session_id "$session_id" || fail_gate 'ECI required-critic review gate needs a valid session id.'

root="$(eci_proof_root)"
eci_proof_root_is_safe || fail_gate "ECI required-critic review gate rejected an unsafe proof root: $root"
session_dir="$root/$session_id"
eci_session_dir_is_safe "$root" "$session_id" ||
  fail_gate "ECI required-critic review gate rejected an unsafe session directory: $session_dir"
[ -d "$session_dir" ] && [ ! -L "$session_dir" ] ||
  fail_gate "ECI required-critic review gate needs the canonical session directory: $session_dir"

aggregate_mode=false
aggregate_repo_id="${ECI_AGGREGATE_REPO_ID:-}"
aggregate_parent_cwd=""
if [ -n "$aggregate_repo_id" ]; then
  eci_aggregate_repo_id_is_valid "$aggregate_repo_id" ||
    fail_gate 'ECI aggregate review gate requires a bounded repository ID.'
  aggregate_parent_cwd="$(eci_canonical_cwd "$PWD")"
  [ "${ECI_AGGREGATE_PARENT_CWD:-}" = "$aggregate_parent_cwd" ] ||
    fail_gate 'ECI aggregate review gate requires its caller to remain at the parent marker cwd.'
  aggregate_mode=true
elif [ -n "${ECI_AGGREGATE_PARENT_CWD:-}" ]; then
  fail_gate 'ECI required-critic review gate rejected an aggregate parent cwd without an aggregate repository ID.'
elif [ -e "$session_dir/eci-aggregate-plan.json" ] || [ -L "$session_dir/eci-aggregate-plan.json" ]; then
  fail_gate 'ECI required-critic review gate rejected a singleton lifecycle invocation for an aggregate session.'
fi

if [ "$phase" = off ]; then
  marker="$session_dir/eci_active"
  [ -f "$marker" ] && [ ! -L "$marker" ] ||
    fail_gate "ECI required-critic review gate cannot validate teardown without the regular active marker: $marker"
  if [ "$aggregate_mode" = true ]; then
    off_cwd="$aggregate_parent_cwd"
  else
    off_cwd="${ECI_REVIEW_CWD:-$PWD}"
  fi
  off_cwd="$(eci_canonical_cwd "$off_cwd")"
  eci_marker_path_owner_is_valid "$marker" ||
    fail_gate 'ECI required-critic review gate rejected teardown: the direct marker path/owner binding is malformed; marker retained.'
  eci_marker_is_valid_for_cwd "$marker" "$off_cwd" ||
    fail_gate 'ECI required-critic review gate rejected teardown: the direct marker cwd binding is malformed or belongs to another cwd; marker retained.'
fi

# R11 review is intentionally current-diff-only. Old manifests, anchors,
# receipts, identity ledgers, role/provider/model/provenance metadata, hashes,
# canonical JSON, and locks describe historical workflow attempts; they cannot
# block ordinary progress. A current unsafe cwd/repository/target still fails,
# while a missing or stale current review is a fresh-critic workflow reminder.
if [ "$aggregate_mode" = true ]; then
  allow_aggregate_current_target_or_refresh \
    "$aggregate_parent_cwd" "$aggregate_repo_id" \
    "$session_dir/eci-aggregate-plan.json" \
    "$session_dir/eci-aggregate.$aggregate_repo_id.required-critics.json"
  exit 0
fi
if [ "$aggregate_mode" = false ]; then
  allow_current_target_or_refresh \
    "${ECI_REVIEW_CWD:-$PWD}" "$session_dir/eci-required-critics.json"
  exit 0
fi
