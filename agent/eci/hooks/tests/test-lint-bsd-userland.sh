#!/usr/bin/env bash
# Planted cases for hooks/tests/lint-bsd-userland.sh. Each must-FAIL case must fail with its expected reason; each
# must-PASS control must pass. Run under bash 3.2 and bash 5: the lint runs under the same shell ($BASH).
# Usage: test-lint-bsd-userland.sh
set -u
umask 077

tests_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
lint="$tests_dir/lint-bsd-userland.sh"
loader="$tests_dir/../lib/eci-native-binary.sh"
provision="$tests_dir/../lib/eci-native-provision.sh"
base="$(mktemp -d -- "${TMPDIR:-/tmp}/test-lint-bsd-userland.XXXXXX")" || exit 1
trap 'rm -rf -- "$base"' EXIT

checks=0
failures=0
pass() { checks=$((checks + 1)); printf 'PASS: %s\n' "$*"; }
fail() { failures=$((failures + 1)); printf 'FAIL: %s\n' "$*" >&2; }

if ! "$BASH" -n "$lint"; then
  printf 'FAIL: %s does not parse under bash %s\n' "$lint" "$BASH_VERSION" >&2
  exit 1
fi
pass "lint parses under bash $BASH_VERSION"

case_number=0
# lint_file FILE: runs the lint on FILE; sets out and rc.
lint_file() {
  rc=0
  out=$("$BASH" "$lint" "$1" 2>&1) || rc=$?
}
# must_fail REASON LABEL CONTENT: a script with CONTENT must FAIL, and the output must contain REASON.
must_fail() {
  local reason="$1" label="$2" file
  case_number=$((case_number + 1))
  file="$base/case$case_number.sh"
  printf '#!/usr/bin/env bash\n%s\n' "$3" >"$file"
  lint_file "$file"
  if [ "$rc" != 0 ] && printf '%s\n' "$out" | grep -F -q -- "$reason"; then
    pass "must-FAIL ($reason): $label"
  else
    fail "must-FAIL ($reason): $label: rc $rc: $out"
  fi
}
# must_pass LABEL CONTENT: a script with CONTENT must PASS.
must_pass() {
  local label="$1" file
  case_number=$((case_number + 1))
  file="$base/case$case_number.sh"
  printf '#!/usr/bin/env bash\n%s\n' "$2" >"$file"
  lint_file "$file"
  if [ "$rc" = 0 ]; then pass "must-PASS: $label"; else fail "must-PASS: $label: rc $rc: $out"; fi
}
# copy_fail REASON LABEL FILE: an already-written FILE must FAIL with REASON.
copy_fail() {
  lint_file "$3"
  if [ "$rc" != 0 ] && printf '%s\n' "$out" | grep -F -q -- "$1"; then
    pass "must-FAIL ($1): $2"
  else
    fail "must-FAIL ($1): $2: rc $rc: $out"
  fi
}

# GNU-only forms, one per script.
must_fail 'stat-c' 'stat -c' 'mode=$(stat -c %a "$f")'
must_fail 'stat-c' 'stat -L -c' "mode=\$(stat -L -c '%u' \"\$f\")"
must_fail 'realpath-e' 'realpath -e' 'r=$(realpath -e -- "$f")'
must_fail 'assoc-array' 'declare -A' 'declare -A seen=()'
must_fail 'assoc-array' 'local -A' 'f() { local -A states=(); }'
must_fail 'sed-i-no-suffix' 'sed -i without a suffix' "sed -i 's/a/b/' \"\$f\""
must_fail 'date-d' 'date -d' 'when=$(date -d yesterday +%s)'
must_fail 'readlink-f' 'readlink -f' 'p=$(readlink -f "$f")'
must_fail 'xargs-r' 'xargs -r' 'find . -print0 | xargs -0 -r rm -f'
must_fail 'mv-T' 'mv -fT' 'mv -fT -- a b'
must_fail 'sort-z' 'sort -z' 'find . -print0 | sort -z'
must_fail 'od-w' 'od -w' 'od -An -tx1 -w20 -N20 -- "$f"'
must_fail 'proc-path' '/proc path' 'head -c 20 /proc/$$/exe'
must_fail 'proc-path' 'quoted /proc path' 'head -c 20 "/proc/$$/exe"'
must_fail 'sha256sum' 'sha256sum without a shasum fallback' 'sum=$(sha256sum -- "$f")'
must_fail 'exec-named-fd' 'exec {fd}' 'exec {fd}<"$f"'
must_fail 'array-read-builtin' 'mapfile' 'mapfile -t lines <"$f"'
must_fail 'array-read-builtin' 'readarray' 'readarray -t lines <"$f"'
must_fail 'case-modification' '${v,,}' 'lower=${name,,}'
must_fail 'case-modification' '${v^^}' 'upper=${name^^}'
must_fail 'grep-P' 'grep -P' "grep -P '\\d' \"\$f\""
must_fail 'long option --parents' 'cp --parents' 'cp --parents a b'
must_fail 'long option --no-copy' 'mv --no-copy' 'mv --no-copy -f -- a b'
# Forms added from the Gap17 review.
must_fail 'stat-c' 'stat split by a backslash continuation' 'mode=$(stat \
  -c %a "$f")'
must_fail 'find-printf' 'find -printf' "find . -type f -printf '%p\\n'"
must_fail 'timeout-command' 'timeout' 'timeout 5 sleep 10'
must_fail 'nproc-command' 'nproc' 'jobs=$(nproc)'
must_fail 'ln-relative' 'ln -sr' 'ln -sr a b'
must_fail 'ln-relative' 'ln -r' 'ln -r -s a b'
must_fail 'head-negative-count' 'head -n -N' 'head -n -5 "$f"'
must_fail 'du-b' 'du -b' 'size=$(du -b "$f")'
must_fail 'mktemp-p' 'mktemp -p' 'd=$(mktemp -p "$TMPDIR")'
must_fail 'base64-w' 'base64 -w' 'b=$(base64 -w0 <"$f")'
must_fail 'long option --no-run-if-empty' 'xargs --no-run-if-empty' 'printf a | xargs --no-run-if-empty echo'
must_fail 'test-v' '[[ -v ]]' 'if [[ -v name ]]; then :; fi'
must_fail 'nameref' 'declare -n' 'declare -n ref=name'
must_fail 'nameref' 'local -n' 'f() { local -n ref=$1; }'
must_fail 'negative-index' '${arr[-1]}' 'last=${arr[-1]}'
must_fail 'long option --reference' 'an unlisted GNU long option' 'touch --reference=a b'
must_fail 'stat-c' 'stat -c inside $(...) inside double quotes' 'echo "$(stat -c %a f)"'
must_fail 'stat-c' 'stat -c inside backticks inside double quotes' 'echo "`stat -c %a f`"'
must_fail 'stat-c' 'stat -c inside bare backticks' 'm=`stat -c %a f`'
# Bash 4 expansions inside double quotes still expand (Gap17Reviewer P2 on 14e039a).
must_fail 'case-modification' '${v,,} inside double quotes' 'lower="${name,,}"'
must_fail 'case-modification' '${v^^} inside double quotes' 'up="${name^^}"'
must_fail 'negative-index' '${arr[-1]} inside double quotes' 'echo "${arr[-1]}"'
must_fail 'test-v' '[[ -v ]] inside "$(...)"' 'echo "$(if [[ -v name ]]; then echo set; fi)"'
must_fail 'case-modification' '${v,,} inside "$(...)"' 'echo "$(printf %s "${name,,}")"'
# Pairing rule: the open counts only as a command.
must_fail 'without eci_runtime_open_verified' 'close with the open only in a comment' 'eci_runtime_close_verified  # pairs with eci_runtime_open_verified'
must_fail 'without eci_runtime_open_verified' 'close with the open only in a string' 'echo "call eci_runtime_open_verified first"
eci_runtime_close_verified'
must_fail 'without eci_runtime_open_verified' 'close with no open' 'eci_runtime_close_verified'

# Controls.
must_pass 'sed -i with an empty suffix' "sed -i '' 's/a/b/' \"\$f\""
must_pass 'sed -i with an attached suffix' "sed -i.bak 's/a/b/' \"\$f\""
must_pass 'BSD stat -f' 'mode=$(stat -f %Lp "$f")'
must_pass 'a GNU form named inside a string' 'echo "do not use stat -c"'
must_pass 'a GNU form named in a trailing comment' 'x=1  # stat -c'
must_pass 'tail -n +N (BSD has it)' 'tail -n +2 "$f"'
must_pass 'sha256sum with a shasum fallback' 'if command -v sha256sum >/dev/null; then s=$(sha256sum -- "$f"); else s=$(shasum -a 256 -- "$f"); fi'
must_pass 'open and close as commands' 'if ! eci_runtime_open_verified eci-hook-helper; then exit 1; fi
eci_runtime_close_verified'
must_pass 'the allowed --help case pattern' 'case $1 in --help|-h) ;; esac'

# The new lib with one planted line in eci_native_classify.
mkdir -p "$base/lib1"
awk '{print} /^eci_native_classify\(\) \{/{print "  mode=$(stat -c %a \"$HOME\")"}' "$provision" >"$base/lib1/eci-native-provision.sh"
copy_fail 'stat-c in eci_native_classify' 'stat -c planted in the new lib' "$base/lib1/eci-native-provision.sh"

# Loader copies; the file name keeps the allow-list in force.
for k in 1 2 3 7; do mkdir -p "$base/loader$k"; done
awk '{print} /^_eci_runtime_darwin_copy_inode\(\) \{/{print "  x=$(stat -c %a \"$2\")"}' "$loader" >"$base/loader1/eci-native-binary.sh"
copy_fail 'stat-c in _eci_runtime_darwin_copy_inode' 'stat -c planted in a darwin function' "$base/loader1/eci-native-binary.sh"
awk '{print} /^eci_runtime_darwin_directory_chain_is_trusted\(\) \{/{print "  read -r owner mode < <(stat -c '"'"'%u %a'"'"' -- \"$dir\" 2>/dev/null) || return 1"}' "$loader" >"$base/loader2/eci-native-binary.sh"
copy_fail 'stat-c in eci_runtime_darwin_directory_chain_is_trusted' 'stat -c planted in the darwin chain function' "$base/loader2/eci-native-binary.sh"
grep -v -F 'Darwin) _eci_runtime_binary_is_native_darwin "$1"; return ;;' "$loader" >"$base/loader3/eci-native-binary.sh"
copy_fail 'od-w in eci_runtime_binary_is_native' 'G3 Darwin dispatch removed' "$base/loader3/eci-native-binary.sh"
awk '{print} /^_eci_runtime_darwin_stage_verified_copy\(\) \{/{print "  x=$(stat \\"; print "    -c %a \"$1\")"}' "$loader" >"$base/loader7/eci-native-binary.sh"
copy_fail 'stat-c in _eci_runtime_darwin_stage_verified_copy' 'a continued stat -c in a darwin function' "$base/loader7/eci-native-binary.sh"

# The real loader and the real lib pass.
lint_file "$loader"
if [ "$rc" = 0 ]; then pass "must-PASS: the real loader"; else fail "must-PASS: the real loader: $out"; fi
lint_file "$provision"
if [ "$rc" = 0 ]; then pass "must-PASS: the real lib"; else fail "must-PASS: the real lib: $out"; fi

if [ "$failures" -ne 0 ]; then
  printf 'test-lint-bsd-userland: %s of %s checks FAILED (bash %s)\n' "$failures" "$((checks + failures))" "$BASH_VERSION" >&2
  exit 1
fi
printf 'test-lint-bsd-userland: %s checks passed (bash %s)\n' "$checks" "$BASH_VERSION"
