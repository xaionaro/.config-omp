#!/usr/bin/env bash

# Regression coverage for the legacy dirty-worktree Stop path. Gitleaks is a
# useful accidental-mistake diagnostic, never a reason to hold Stop itself.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-stop-gitleaks.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

FIXTURE_HOME="$TMP_ROOT/home"
FIXTURE_ECI="$FIXTURE_HOME/.omp/agent/eci"
PROOF_ROOT="$TMP_ROOT/proof"
REPO="$TMP_ROOT/repo"
INPUT="$TMP_ROOT/input.json"

mkdir -p -- "$FIXTURE_ECI" "$FIXTURE_HOME/tmp" "$PROOF_ROOT" "$REPO"
cp -a -- "$ROOT/hooks" "$FIXTURE_ECI/hooks"

# The normal inactive path intentionally returns before legacy dirty-worktree
# checklist handling. Exercise that legacy handoff branch in a private copy,
# changing no production behavior other than its own early-return routing.
python3 - "$FIXTURE_ECI/hooks/stop-gate.sh" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
source = path.read_text()
old = '''# An inactive, marker-free session is outside ECI's dirty-worktree admission
# boundary. Active ECI and ATE paths have already returned above.
if [ "$changed" = "true" ] && [ "$stop_recursive_callback_unvalidated" != true ]; then
  json_continue
  exit 0
fi
'''
if old not in source:
    raise SystemExit("expected private dirty-worktree early return was not found")
path.write_text(source.replace(old, "# Private test fixture: exercise the legacy dirty-worktree handoff below.\n", 1))
PY

git -C "$REPO" init -q
git -C "$REPO" config user.email 'eci-stop-gitleaks@example.invalid'
git -C "$REPO" config user.name 'ECI stop gitleaks test'
printf 'base\n' >"$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -qm 'initial'
printf 'changed\n' >>"$REPO/file.txt"

jq -cn --arg cwd "$REPO" '{
  session_id: "stop-gitleaks",
  cwd: $cwd,
  transcript_path: "/fixture/transcript.jsonl",
  stop_hook_active: false
}' >"$INPUT"

make_fake_gitleaks() {
  local mode="$1"
  local bin="$TMP_ROOT/bin-$mode"

  mkdir -p -- "$bin"
  cat >"$bin/gitleaks" <<'SCRIPT'
#!/usr/bin/env bash
set -u

report=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --report-path)
      shift
      report="${1:-}"
      ;;
  esac
  shift || break
done

case "${FAKE_GITLEAKS_MODE:?}" in
  finding)
    cat >"$report" <<'JSON'
[{"Description":"Possible credential-like text","StartLine":2,"File":"file.txt","RuleID":"fixture-rule"}]
JSON
    exit 1
    ;;
  broken)
    printf '%s\n' 'RAW_FIXTURE_VALUE_MUST_NOT_BE_REPORTED' >&2
    exit 2
    ;;
  *)
    exit 99
    ;;
esac
SCRIPT
  chmod +x "$bin/gitleaks"
  printf '%s\n' "$bin"
}

run_stop() {
  local output="$1" command_path="$2" mode="${3:-}" bash_env="${4:-}"

  env -u ECI_HOME -u ECI_ROLE \
    HOME="$FIXTURE_HOME" PATH="$command_path" ECI_PROOF_ROOT="$PROOF_ROOT" \
    FAKE_GITLEAKS_MODE="$mode" BASH_ENV="$bash_env" \
    bash "$FIXTURE_ECI/hooks/stop-gate.sh" <"$INPUT" >"$output"
}

assert_normal_dirty_handoff() {
  local output="$1" expected_status="$2"

  jq -e '
    .decision == "block" and
    ((.reason // "") | contains("Automated stop checks found changed git state")) and
    ((.reason // "") | contains("Automated secret scan") | not)
  ' "$output" >/dev/null || {
    cat "$output" >&2
    return 1
  }
  grep -F "Secret scan: $expected_status" "$PROOF_ROOT/stop-gitleaks/instructions.md" >/dev/null
}

# Before the repair, both of these exit through a Gitleaks-specific Stop
# denial rather than the normal dirty-worktree handoff.
finding_bin="$(make_fake_gitleaks finding)"
finding_output="$TMP_ROOT/finding.json"
run_stop "$finding_output" "$finding_bin:$PATH" finding
assert_normal_dirty_handoff "$finding_output" 'advisory: redacted candidate reported by gitleaks' || exit 1
grep -F 'file.txt:2 fixture-rule' \
  "$PROOF_ROOT/stop-gitleaks/gitleaks-findings.txt" >/dev/null
! grep -F 'Possible credential-like text' \
  "$PROOF_ROOT/stop-gitleaks/gitleaks-findings.txt" >/dev/null

rm -rf -- "$PROOF_ROOT/stop-gitleaks"
broken_bin="$(make_fake_gitleaks broken)"
broken_output="$TMP_ROOT/broken.json"
run_stop "$broken_output" "$broken_bin:$PATH" broken
assert_normal_dirty_handoff "$broken_output" 'advisory: gitleaks unavailable or incomplete' || exit 1
! grep -R -F 'RAW_FIXTURE_VALUE_MUST_NOT_BE_REPORTED' "$PROOF_ROOT/stop-gitleaks" >/dev/null

rm -rf -- "$PROOF_ROOT/stop-gitleaks"
missing_bash_env="$TMP_ROOT/missing-gitleaks.bashenv"
printf '%s\n' 'command() {' >"$missing_bash_env"
printf '%s\n' '  if [ "${1:-}" = "-v" ] && [ "${2:-}" = "gitleaks" ]; then return 1; fi' >>"$missing_bash_env"
printf '%s\n' '  builtin command "$@"' >>"$missing_bash_env"
printf '%s\n' '}' >>"$missing_bash_env"
missing_output="$TMP_ROOT/missing.json"
run_stop "$missing_output" "$PATH" '' "$missing_bash_env"
assert_normal_dirty_handoff "$missing_output" 'advisory: gitleaks unavailable or incomplete' || exit 1

printf '%s\n' 'stop gitleaks advisory assertions: PASS'
