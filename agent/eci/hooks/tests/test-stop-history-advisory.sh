#!/usr/bin/env bash

# Historical teardown/proof/reviewer records can inform a later review, but
# must not become Stop admission. This fixture runs a copied hook only.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_PARENT="${ECI_TMPDIR:-${TMPDIR:-${HOME:?}/tmp}}"
mkdir -p -- "$TMP_PARENT"
TMP_ROOT="$(mktemp -d "$TMP_PARENT/eci-stop-history.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT

FIXTURE_HOME="$TMP_ROOT/home"
FIXTURE_ECI="$FIXTURE_HOME/.omp/agent/eci"
REPO="$TMP_ROOT/repo"
SESSION=history-session
INPUT="$TMP_ROOT/input.json"

mkdir -p -- "$FIXTURE_ECI" "$FIXTURE_HOME/tmp" "$REPO"
cp -a -- "$ROOT/hooks" "$FIXTURE_ECI/hooks"

git -C "$REPO" init -q
git -C "$REPO" config user.email 'eci-stop-history@example.invalid'
git -C "$REPO" config user.name 'ECI stop history test'
printf 'base\n' >"$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -qm 'initial fixture'

jq -cn --arg session_id "$SESSION" --arg cwd "$REPO" \
  '{session_id:$session_id,cwd:$cwd,transcript_path:"/fixture/transcript.jsonl",stop_hook_active:false}' >"$INPUT"

run_stop() {
  local proof_root="$1" output="$2"

  env -u ECI_HOME -u ECI_ROLE \
    HOME="$FIXTURE_HOME" ECI_PROOF_ROOT="$proof_root" \
    bash "$FIXTURE_ECI/hooks/stop-gate.sh" <"$INPUT" >"$output"
}

assert_continue() {
  local output="$1"

  jq -e '.continue == true and (has("decision") | not)' "$output" >/dev/null || {
    cat "$output" >&2
    exit 1
  }
}

write_minimal_proof() {
  local proof="$1"
  mkdir -p -- "${proof%/*}"
  printf '%s\n' '# Stale proof note' >"$proof"
}

write_violation_proof() {
  local proof="$1" commit="$2"
  mkdir -p -- "${proof%/*}"
  printf '%s\n' '# Historical proof' >"$proof"
  printf '%s\n' '## Summary' >>"$proof"
  printf '%s\n' 'old summary' >>"$proof"
  printf '%s\n' '## Verification' >>"$proof"
  printf '%s\n' 'old verification' >>"$proof"
  printf '%s\n' '## Requirements' >>"$proof"
  printf '%s\n' 'old requirements' >>"$proof"
  printf '%s\n' '## Root Cause' >>"$proof"
  printf '%s\n' 'old cause' >>"$proof"
  printf '%s\n' '## Claim Inventory' >>"$proof"
  printf '%s\n' 'old claims' >>"$proof"
  printf '%s\n' '## Pre-Mortem' >>"$proof"
  printf '%s\n' 'old risks' >>"$proof"
  printf '%s\n' '## Adversarial Critique' >>"$proof"
  printf '%s\n' 'old critique' >>"$proof"
  printf '%s\n' '## Rule-Compliance Self-Audit' >>"$proof"
  printf '%s\n' 'Violation:' >>"$proof"
  printf 'commit: %s\n' "$commit" >>"$proof"
  printf '%s\n' '## Gaps' >>"$proof"
  printf '%s\n' 'old gaps' >>"$proof"
}

write_clean_audit_proof() {
  local proof="$1"
  mkdir -p -- "${proof%/*}"
  printf '%s\n' '# Historical proof' >"$proof"
  printf '%s\n' '## Summary' >>"$proof"
  printf '%s\n' 'old summary' >>"$proof"
  printf '%s\n' '## Verification' >>"$proof"
  printf '%s\n' 'old verification' >>"$proof"
  printf '%s\n' '## Requirements' >>"$proof"
  printf '%s\n' 'old requirements' >>"$proof"
  printf '%s\n' '## Root Cause' >>"$proof"
  printf '%s\n' 'old cause' >>"$proof"
  printf '%s\n' '## Claim Inventory' >>"$proof"
  printf '%s\n' 'old claims' >>"$proof"
  printf '%s\n' '## Pre-Mortem' >>"$proof"
  printf '%s\n' 'old risks' >>"$proof"
  printf '%s\n' '## Adversarial Critique' >>"$proof"
  printf '%s\n' 'old critique' >>"$proof"
  printf '%s\n' '## Rule-Compliance Self-Audit' >>"$proof"
  printf '%s\n' 'clean-scan: source-a, source-b, AGENTS.md' >>"$proof"
  printf '%s\n' '## Gaps' >>"$proof"
  printf '%s\n' 'old gaps' >>"$proof"
}

# A malformed teardown receipt is stale historical state, not an active
# marker. Current code blocks before ordinary continuation reaches the task.
receipt_root="$TMP_ROOT/receipt-proof"
mkdir -p -- "$receipt_root/$SESSION"
printf '%s\n' 'broken receipt' >"$receipt_root/$SESSION/eci-teardown-complete"
receipt_output="$TMP_ROOT/receipt.json"
run_stop "$receipt_root" "$receipt_output"
assert_continue "$receipt_output"

# Missing old proof headings are an audit reminder, not teardown admission.
heading_root="$TMP_ROOT/heading-proof"
write_minimal_proof "$heading_root/$SESSION/proof.md"
heading_output="$TMP_ROOT/heading.json"
run_stop "$heading_root" "$heading_output"
assert_continue "$heading_output"

# An unreachable historical commit cited in an old audit must not trap Stop.
commit_root="$TMP_ROOT/commit-proof"
write_violation_proof "$commit_root/$SESSION/proof.md" deadbeef
commit_output="$TMP_ROOT/commit.json"
run_stop "$commit_root" "$commit_output"
assert_continue "$commit_output"

# A byte-identical historical audit after a clean HEAD advance is stale
# evidence, not a condition requiring a fresh command or a user recovery.
history_root="$TMP_ROOT/history-proof"
history_proof="$history_root/$SESSION/proof.md"
history_seed_output="$TMP_ROOT/history-seed.json"
write_clean_audit_proof "$history_proof"
run_stop "$history_root" "$history_seed_output"
printf 'advance\n' >>"$REPO/file.txt"
git -C "$REPO" add file.txt
git -C "$REPO" commit -qm 'advance fixture head'
write_clean_audit_proof "$history_proof"
history_output="$TMP_ROOT/history.json"
run_stop "$history_root" "$history_output"
assert_continue "$history_output"

# A reviewer artifact can be surfaced elsewhere, but its JSON block cannot
# become a Stop-loop admission result in this callback.
reviewer="$FIXTURE_ECI/hooks/system-prompt-reviewer.sh"
printf '%s\n' '#!/usr/bin/env bash' >"$reviewer"
printf '%s\n' 'printf "%s\\n" '\''{"decision":"block","reason":"stale reviewer artifact"}'\''' >>"$reviewer"
chmod +x -- "$reviewer"
reviewer_root="$TMP_ROOT/reviewer-proof"
mkdir -p -- "$reviewer_root/$SESSION"
printf '%s\n' 'fast exit' >"$reviewer_root/$SESSION/proof.md"
reviewer_output="$TMP_ROOT/reviewer.json"
run_stop "$reviewer_root" "$reviewer_output"
assert_continue "$reviewer_output"

printf '%s\n' 'stop history advisory assertions: PASS'
