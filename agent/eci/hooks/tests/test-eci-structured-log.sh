#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$(dirname -- "${BASH_SOURCE[0]}")/portable-test.sh"
source "$ROOT/hooks/lib/eci-proof-state.sh"
eci_session_ledger_basename high_level_log.jsonl
eci_control_basename high_level_log.jsonl
! eci_session_ledger_basename high_level_log.md
! eci_control_basename high_level_log.md
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-structured-log.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
export HOME="$TMP_ROOT/home"
proof_root="$TMP_ROOT/proof"
session_id=t00-structured-log
session_dir="$HOME/.agent/context/sessions/$session_id"
cwd="$TMP_ROOT/repository"
log="$session_dir/high_level_log.jsonl"
mkdir -p -- "$proof_root/$session_id" "$session_dir" "$cwd"
# A removed-name cache is not imported or used as an alias.
old_cache="$session_dir/high_level_log.md"
printf '%s\n' 'obsolete cache sentinel' >"$old_cache"
cp -- "$old_cache" "$TMP_ROOT/old-cache.before"
run_current() {
  (cd -- "$cwd"; ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$session_id" "$ROOT/bin/eci-active" "$@")
}
run_current on 'structured log fixture' >/dev/null
[ -f "$log" ] || { printf '%s\n' 'canonical JSONL was not bootstrapped' >&2; exit 1; }
[ ! -s "$log" ]
cmp -- "$old_cache" "$TMP_ROOT/old-cache.before"
progress='{"event":"worker_progress","actor":{"id":"/root/worker-a","role":"Implementer","turn":"7"},"recorded_by":{"id":"/root","role":"Supervisor"},"summary":"Focused validation passes; requested outcome remains open.","requirements":["R7: worker progress extraction"],"change":{"before":"unknown","after":"focused checks pass"},"reason":"Implemented structured append","evidence":["checks.out"],"next_action":"Run production check","details":{"milestone":"focused checks"},"schema":"spoof","timestamp":"spoof","session_id":"spoof"}'
run_current ledger-append --json "$progress" >/dev/null
jq -e --arg session "$session_id" '.schema == "eci-high-level-log/v1" and .session_id == $session and (.timestamp | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) and .actor.id == "/root/worker-a" and .recorded_by.id == "/root" and .details.milestone == "focused checks"' "$log" >/dev/null
[ "$(wc -l <"$log")" -eq 1 ]
text=$'Quotes " and backslash \\ and Unicode λ\nsecond line'
multiline="$(jq -cn --arg summary "$text" '{event:"decision",summary:$summary}')"
run_current ledger-append --json "$multiline" >/dev/null
tail -n 1 "$log" | jq -e --arg text "$text" '.event == "decision" and .summary == $text' >/dev/null
[ "$(wc -l <"$log")" -eq 2 ]
run_current ledger-append --json '{"event":"review_result","actor":{"id":"/root/critic","role":"Critic"},"summary":"Review pending"}' >/dev/null
changed_role="$(jq -c --arg summary "$text" '.actor.role="Explorer" | .summary=$summary' <<<"$progress")"
run_current ledger-append --json "$changed_role" >/dev/null
tail -n 1 "$log" | jq -e --arg text "$text" '.actor.id == "/root/worker-a" and .actor.role == "Explorer" and .summary == $text' >/dev/null
[ "$(wc -l <"$log")" -eq 4 ]
[ "$(jq -r 'select(.schema == "eci-high-level-log/v1" and .event == "worker_progress" and .actor.id == "/root/worker-a") | .actor.id' "$log" | wc -l)" -eq 2 ]

# Invalid input must not bootstrap, normalize, or detach a log.
for state in regular missing hardlinked; do
  rm -f -- "$log" "$TMP_ROOT/external"
  case "$state" in
    regular) printf '%s' '{"event":"decision","summary":"prior state"}' >"$log" ;;
    hardlinked) printf '%s' '{"event":"decision","summary":"shared state"}' >"$TMP_ROOT/external"; ln -- "$TMP_ROOT/external" "$log" ;;
  esac
  if [ -e "$log" ]; then
    cp -- "$log" "$TMP_ROOT/before"
    inode="$(ls -di -- "$log" | awk '{print $1}')"
  fi
  if run_current ledger-append --json >"$TMP_ROOT/out" 2>"$TMP_ROOT/err"; then
    printf 'accepted --json without a payload (%s log)\n' "$state" >&2; exit 1
  fi
  if [ "$state" = missing ]; then [ ! -e "$log" ]; else
    cmp -- "$log" "$TMP_ROOT/before"
    [ "$(ls -di -- "$log" | awk '{print $1}')" = "$inode" ]
  fi
  for unflagged in 'ordinary text' "$progress"; do
    if run_current ledger-append "$unflagged" >"$TMP_ROOT/out" 2>"$TMP_ROOT/err"; then
      printf 'accepted noncanonical text/positional input (%s log)\n' "$state" >&2; exit 1
    fi
    if [ "$state" = missing ]; then [ ! -e "$log" ]; else
      cmp -- "$log" "$TMP_ROOT/before"
      [ "$(ls -di -- "$log" | awk '{print $1}')" = "$inode" ]
    fi
  done
  for invalid in '' 'ordinary text' '{' '{} {}' 'null' '[]' '{}' \
    "$(jq -c '.event="unknown"' <<<"$progress")" \
    "$(jq -c '.event="note"' <<<"$progress")" \
    "$(jq -c '.actor.id=""' <<<"$progress")" \
    "$(jq -c '.actor.role=1' <<<"$progress")" \
    "$(jq -c '.recorded_by=null' <<<"$progress")" \
    "$(jq -c '.summary=" "' <<<"$progress")" \
    "$(jq -c '.evidence=[]' <<<"$progress")" \
    "$(jq -c '.evidence=[false]' <<<"$progress")" \
    "$(jq -c '.requirements="R7"' <<<"$progress")" \
    "$(jq -c '.change.before=null' <<<"$progress")" \
    "$(jq -c '.reason=7' <<<"$progress")" \
    "$(jq -c '.next_action=[]' <<<"$progress")"; do
    if run_current ledger-append --json "$invalid" >"$TMP_ROOT/out" 2>"$TMP_ROOT/err"; then
      printf 'accepted invalid structured input: %s\n' "$invalid" >&2; exit 1
    fi
    if [ "$state" = missing ]; then [ ! -e "$log" ]; else
      cmp -- "$log" "$TMP_ROOT/before"
      [ "$(ls -di -- "$log" | awk '{print $1}')" = "$inode" ]
    fi
  done
done

# Valid structured input preserves the marker/CWD ownership check.
cp -- "$log" "$TMP_ROOT/wrong-cwd.before"
if (cd -- "$TMP_ROOT"; ECI_PROOF_ROOT="$proof_root" ECI_SESSION_ID="$session_id" "$ROOT/bin/eci-active" ledger-append --json "$progress") >"$TMP_ROOT/out" 2>"$TMP_ROOT/err"; then
  printf '%s\n' 'accepted structured append from wrong CWD' >&2; exit 1
fi
cmp -- "$log" "$TMP_ROOT/wrong-cwd.before"

# Existing conforming JSONL stays immutable; plain jq filters other events.
rm -f -- "$log"
printf '%s\n' '{"schema":"eci-high-level-log/v1","event":"decision","summary":"Prior decision"}' >"$log"
cp -- "$log" "$TMP_ROOT/prefix"
run_current ledger-append --json "$progress" >/dev/null
head -n 1 "$log" | cmp -- - "$TMP_ROOT/prefix"
query='select(.schema == "eci-high-level-log/v1" and .event == "worker_progress" and .actor.id == "/root/worker-a") | .summary'
[ "$(jq -r "$query" "$log")" = 'Focused validation passes; requested outcome remains open.' ]
cmp -- "$old_cache" "$TMP_ROOT/old-cache.before"
grep -Fxq "bytes: $(wc -c <"$log" | tr -d '[:space:]')" "$session_dir/high_level_log.anchor"
printf '%s\n' 'eci structured log assertions: PASS'
