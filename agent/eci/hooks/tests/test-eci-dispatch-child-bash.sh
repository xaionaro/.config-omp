#!/usr/bin/env bash
# pretooluse-edit-dispatch.sh runs its gate children with its own interpreter ($BASH),
# never with the first bash on PATH. The omp runner may start the dispatch with
# ECI_BASH (a bash >= 4.4) while an older bash is first on PATH; children must not
# fall back to it. A stand-in `bash` first on PATH records any use; the stubbed gate
# children record the interpreter that ran them.
set -euo pipefail
umask 077

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-dispatch-child-bash.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
fail() { printf 'eci-dispatch-child-bash: FAIL %s\n' "$*" >&2; exit 1; }

hooks="$TMP_ROOT/hooks"
cp -a -- "$ROOT/hooks" "$hooks"
for gate in validate-edit-write.sh validate-apply-patch.sh eci-active-gate.sh; do
  printf '#!/usr/bin/env bash\ncat >/dev/null\nprintf "%%s\\n" "$BASH" >>"%s/ran-%s"\n' "$TMP_ROOT" "$gate" >"$hooks/$gate"
  chmod 755 "$hooks/$gate"
done

decoy="$TMP_ROOT/decoy-bin"
mkdir -p "$decoy"
printf '#!/bin/sh\nprintf "%%s\\n" "$*" >>"%s/path-bash-used"\nexit 97\n' "$TMP_ROOT" >"$decoy/bash"
chmod 755 "$decoy/bash"

guarded="$(command -v bash)"
payload='{"session_id":"s","cwd":"/","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/x","old_string":"a","new_string":"b"}}'
printf '%s' "$payload" | HOME="$TMP_ROOT" PATH="$decoy:$PATH" "$guarded" "$hooks/pretooluse-edit-dispatch.sh" \
  >"$TMP_ROOT/out" 2>"$TMP_ROOT/err" || true

[ ! -e "$TMP_ROOT/path-bash-used" ] || fail "a gate child ran on the first bash on PATH: $(cat "$TMP_ROOT/path-bash-used")"
ran=0
for record in "$TMP_ROOT"/ran-*; do
  [ -e "$record" ] || continue
  ran=$((ran + 1))
  while IFS= read -r used; do
    [ "$used" = "$guarded" ] || fail "${record##*/ran-} ran on $used, not the dispatch's $guarded"
  done <"$record"
done
[ "$ran" -ge 2 ] || fail "expected the edit validator and the active gate to run; ran: $ran (stderr: $(head -c 300 "$TMP_ROOT/err"))"
printf 'eci-dispatch-child-bash: PASS\n'
