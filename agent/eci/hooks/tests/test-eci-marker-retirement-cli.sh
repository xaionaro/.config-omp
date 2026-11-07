#!/usr/bin/env bash
set -euo pipefail
base_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
fixture="$(realpath -e -- "$(mktemp -d)")"
trap 'chmod -R u+w -- "$fixture" 2>/dev/null; rm -rf -- "$fixture"' EXIT
export HOME="$fixture/home"
export ECI_PROOF_ROOT="$fixture/proof" ECI_SESSION_ID=retirement_test
mkdir -p "$ECI_PROOF_ROOT/$ECI_SESSION_ID" "$HOME/.agent/context/sessions/$ECI_SESSION_ID"
for command in on off wait wait-repair resume nested-enter nested-accept nested-exit aggregate-off; do
  if output="$("$base_dir/bin/eci-active" "$command" ignored 2>&1)"; then
    echo "obsolete lifecycle command unexpectedly succeeded: $command" >&2; exit 1
  fi
  [[ "$output" == *ECI_LIFECYCLE_RETIRED* ]] || { echo "$output" >&2; exit 1; }
done
"$base_dir/bin/eci-active" ledger-append --json '{"event":"decision","summary":"marker-free coordination"}'
jq -e 'select(.summary == "marker-free coordination" and .session_id == "retirement_test")' "$HOME/.agent/context/sessions/$ECI_SESSION_ID/high_level_log.jsonl" >/dev/null
if env -u ECI_SESSION_ID -u ECI_THREAD_ID "$base_dir/bin/eci-active" ledger-append --json '{"event":"decision","summary":"wrong owner"}' >/dev/null 2>&1; then
  echo 'coordination selected an arbitrary session' >&2; exit 1
fi
[ ! -e "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active" ]
[ ! -e "$ECI_PROOF_ROOT/$ECI_SESSION_ID/ate_nested_eci_active" ]
mkdir -p "$fixture/repository"
git init -q "$fixture/repository"
"$base_dir/bin/eci-active" repository-allow-on "$fixture/repository" 'dependency repair' >/dev/null
"$base_dir/bin/eci-active" repository-allow-off "$fixture/repository" >/dev/null
printf 'existing record\n' > "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-additional-repository"
if output="$("$base_dir/bin/eci-active" repository-allow-off "$fixture/repository" 2>&1)"; then exit 1; fi
[[ "$output" == *'No matching repository allowance'* ]]
[ "$(cat "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-additional-repository")" = 'existing record' ]
for command in coordinator-edit-on aggregate-migrate aggregate-stage aggregate-manifest-write aggregate-commit; do
  if output="$("$base_dir/bin/eci-active" "$command" ignored 2>&1)"; then exit 1; fi
  [[ "$output" == *ECI_COORDINATION_DEFERRED* ]] || { echo "$output" >&2; exit 1; }
done
if output="$("$base_dir/bin/eci-active" permissive-on session test operation request 60 2>&1)"; then exit 1; fi
[[ "$output" == *ECI_COORDINATION_DEFERRED* ]]
[ ! -e "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-coordinator-edit" ]
[ ! -e "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-permissive-mode" ]
[ ! -e "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-aggregate-plan.json" ]
printf 'stale marker\n' > "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active"
"$base_dir/bin/eci-active" status | grep -q 'lifecycle is managed by the Supervisor'
[ "$(cat "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active")" = 'stale marker' ]
# Lifecycle commands neither consume nor rewrite old marker paths.
for command in on off nested-enter nested-accept nested-exit; do
  if "$base_dir/bin/eci-active" "$command" ignored >/dev/null 2>&1; then exit 1; fi
  [ "$(cat "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active")" = 'stale marker' ]
done
echo 'PASS marker-free ledger and retired lifecycle CLI'
# Marker-free aggregate review accepts only its outer cwd or the selected child.
mkdir -p "$fixture/aggregate"
outer="$(realpath -e -- "$fixture/aggregate")"
repo_a="$outer/repo-a"
repo_b="$outer/repo-b"
mkdir -p "$repo_a/nested" "$repo_b" "$fixture/outside"
git init -q "$repo_a"
git init -q "$repo_b"
printf 'review target\n' > "$repo_a/target.txt"
git -C "$repo_a" add -- target.txt
jq -cn '{current_target_path:"target.txt"}' \
  > "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-aggregate.repo-a.required-critics.json"
rm -- "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active"
jq -cn --arg outer "$outer" --arg a "$repo_a" --arg b "$repo_b" \
  '{outer_cwd:$outer,repositories:[{id:"repo-a",repo_root:$a},{id:"repo-b",repo_root:$b}]}' \
  > "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-aggregate-plan.json"
for phase in prewrite final; do
for caller in "$outer" "$repo_a" "$repo_a/nested"; do
  if ! output="$(cd -- "$caller" && "$base_dir/bin/eci-active" aggregate-review "$phase" repo-a 2>&1)"; then
    printf 'selected aggregate caller rejected: %s\n%s\n' "$caller" "$output" >&2
    exit 1
  fi
  [[ "$output" == *"ECI review gate passed: phase=$phase target=$repo_a/target.txt;"* ]] || {
    printf 'aggregate review did not reach the selected target: %s\n' "$output" >&2; exit 1;
  }
done
mkdir -p "$outer/repo-a-similar"
for caller in "$repo_b" "$fixture/outside" "$outer/repo-a-similar"; do
  if (cd -- "$caller" && "$base_dir/bin/eci-active" aggregate-review "$phase" repo-a >/dev/null 2>&1); then
    printf 'unselected aggregate caller accepted: %s\n' "$caller" >&2
    exit 1
  fi
done
if (cd -- "$outer" && "$base_dir/bin/eci-active" aggregate-review "$phase" unknown >/dev/null 2>&1); then
  echo 'unknown aggregate repository accepted' >&2; exit 1
fi
# Pre-migration source 575bb81 advisory parity: a selected caller without a current manifest gets a refresh reminder (exit 0), and no manifest is written.
(cd -- "$repo_b" && "$base_dir/bin/eci-active" aggregate-review "$phase" repo-b >"$fixture/refresh-$phase.out" 2>&1)
grep -Fq "ECI review refresh needed: phase=$phase" "$fixture/refresh-$phase.out"
[ ! -e "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci-aggregate.repo-b.required-critics.json" ]
done
[ ! -e "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active" ]
# Historical markers never regain authority or change during review.
printf 'historic marker\n' > "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active"
(cd -- "$repo_a" && "$base_dir/bin/eci-active" aggregate-review final repo-a >/dev/null)
[ "$(cat "$ECI_PROOF_ROOT/$ECI_SESSION_ID/eci_active")" = 'historic marker' ]
echo 'PASS marker-free aggregate review caller selection'
