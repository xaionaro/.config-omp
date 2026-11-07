#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_ROOT="$(mktemp -d "${ECI_TMPDIR:-${HOME:?}/tmp}/eci-provider-dispatch.XXXXXX")"
trap 'rm -rf -- "$TMP_ROOT"' EXIT
controlled_home="$TMP_ROOT/home"
eci_home="$controlled_home/.omp/agent/eci"
alternate_eci="$TMP_ROOT/alternate-eci"
kimi_home="$TMP_ROOT/kimi"
mkdir -p "$eci_home/bin" "$alternate_eci/bin" "$kimi_home/bin"
printf '#!/usr/bin/env bash\nprintf "eci:%%s\\n" "$1"\n' >"$eci_home/bin/eci-active"
printf '#!/usr/bin/env bash\nprintf "alternate:%%s\\n" "$1"\n' >"$alternate_eci/bin/eci-active"
printf '#!/usr/bin/env bash\nprintf "kimi:%%s\\n" "$1"\n' >"$TMP_ROOT/kimi/bin/eci-active"
chmod 700 "$eci_home/bin/eci-active" "$alternate_eci/bin/eci-active" "$kimi_home/bin/eci-active"

dispatch="$ROOT/bin/eci-active-dispatch"
[ "$(env -u KIMI_SESSION_ID -u KIMI_THREAD_ID -u KIMI_PROOF_ROOT HOME="$controlled_home" ECI_HOME="$alternate_eci" KIMI_CODE_HOME="$kimi_home" ECI_SESSION_ID=eci-session PATH="$kimi_home/bin:$PATH" "$dispatch" status)" = eci:status ]
[ "$(env -u ECI_SESSION_ID -u ECI_THREAD_ID -u ECI_PROOF_ROOT HOME="$controlled_home" ECI_HOME="$alternate_eci" KIMI_CODE_HOME="$kimi_home" KIMI_SESSION_ID=kimi-session PATH="$eci_home/bin:$PATH" "$dispatch" status)" = kimi:status ]
[ "$(env -u ECI_SESSION_ID -u ECI_THREAD_ID -u ECI_PROOF_ROOT -u KIMI_SESSION_ID -u KIMI_THREAD_ID -u KIMI_PROOF_ROOT HOME="$controlled_home" ECI_HOME="$alternate_eci" KIMI_CODE_HOME="$kimi_home" "$dispatch" status)" = eci:status ]
[ "$(env HOME="$controlled_home" ECI_HOME="$alternate_eci" KIMI_CODE_HOME="$kimi_home" ECI_SESSION_ID=eci-session KIMI_SESSION_ID=kimi-session KIMI_PROOF_ROOT="$TMP_ROOT/kimi-proof" "$dispatch" status)" = eci:status ]
printf '%s\n' 'ECI provider dispatch assertions: PASS'
