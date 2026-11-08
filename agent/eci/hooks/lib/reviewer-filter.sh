# Shellcheck-friendly library: drop reviewer violations not grounded in rules.
# shellcheck shell=bash

reviewer_filter_migration_summary="$HOME/.omp/agent/eci/memories/migration-import/ACTIVE-SUMMARY.md"
reviewer_filter_legacy_summary="$HOME/.omp/agent/eci/memories/claude"'-import/ACTIVE-SUMMARY.md'

if [ -z "${REVIEWER_FILTER_CORPUS_FILES+x}" ]; then
  REVIEWER_FILTER_CORPUS_FILES="$HOME/.omp/agent/AGENTS.md $HOME/.omp/agent/eci/hooks/stop-checklist.md $reviewer_filter_migration_summary"
  if [ -f "$reviewer_filter_legacy_summary" ]; then
    REVIEWER_FILTER_CORPUS_FILES="$REVIEWER_FILTER_CORPUS_FILES $reviewer_filter_legacy_summary"
  fi
fi
: "${REVIEWER_FILTER_THRESHOLD:=0.15}"

reviewer_filter_lib_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"

# Publishes the filtered reviewer result in REVIEWER_FILTER_RESULT and prints
# nothing. The verified helper is opened and used in the caller's shell, so
# callers read the global and must never capture this function in $(...).
filter_violations() {
  local result_json="$1"
  shift || true
  REVIEWER_FILTER_RESULT=""
  [ -n "$result_json" ] || return 0

  local static_corpus_text extra_text filtered=""
  static_corpus_text=$(
    # shellcheck disable=SC2086 # Space-separated corpus list is intentional.
    cat $REVIEWER_FILTER_CORPUS_FILES 2>/dev/null || true
  )
  extra_text=$(
    for corpus_file in "$@"; do
      if [ -n "$corpus_file" ]; then
        cat "$corpus_file" 2>/dev/null || true
      fi
    done
  )

  local filter_ok=0 filter_rc=0 private_dir=""
  if . "$reviewer_filter_lib_dir/eci-native-binary.sh" 2>/dev/null; then
    private_dir="$(mktemp -d -- "${TMPDIR:-${ECI_TMPDIR:-/tmp}}/eci-reviewer-filter.XXXXXX")" || filter_rc=1
    if [ "$filter_rc" -eq 0 ]; then
      printf '%s' "$result_json" >"$private_dir/input" || filter_rc=1
    fi
    if [ "$filter_rc" -eq 0 ]; then
      eci_runtime_verexec eci-hook-helper "$private_dir/deny" -- reviewer-filter "$static_corpus_text" "$extra_text" "$REVIEWER_FILTER_THRESHOLD" <"$private_dir/input" >"$private_dir/output" 2>/dev/null || filter_rc=$?
      if [ "$filter_rc" -eq 125 ] && [ -n "$ECI_RUNTIME_DENY_REASON" ]; then
        printf '%s\n' "$ECI_RUNTIME_DENY_REASON" >&2
      fi
    fi
    if [ "$filter_rc" -eq 0 ]; then
      filtered="$(cat -- "$private_dir/output")" && filter_ok=1
    fi
    [ -z "$private_dir" ] || rm -rf -- "$private_dir"
  else
    printf '%s\n' "ECI native gate binary eci-hook-helper is unavailable: lib/eci-native-binary.sh failed to load; provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" >&2
  fi
  if [ "$filter_ok" = 1 ] && [ -n "$filtered" ]; then
    REVIEWER_FILTER_RESULT="$filtered"
  else
    # Fail closed: the filter could not verify any violation, so none are returned.
    # Verdict stays "fail", so the caller still blocks the stop.
    printf '%s\n' "ECI native gate binary eci-hook-helper reviewer-filter produced no result; provision it with \"\$HOME/.omp/agent/eci/hooks/build-native-binaries.sh\" and retry" >&2
    REVIEWER_FILTER_RESULT='{"verdict":"fail","violations":[]}'
  fi
  return 0
}
