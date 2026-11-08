#!/usr/bin/env bash
# Hooks tests must run on macOS userland. This scans every hooks test script, outside
# comments, for GNU-only forms executed directly: `sha256sum`; `sed -i`/`--in-place`
# (any option position); `realpath -e/-m/-s/--relative-to`; `readlink -e/-f/-m`;
# `stat -c`/`--format`/`--printf`; `od -w`; `date -d`; `find -printf`; `xargs -r`;
# and `getent`. Tests take portable forms from portable-test.sh
# (hooks/lib/eci-portable.sh). A line may keep one only when it is test data (a command
# string handed to the code under test), listed in ALLOWED below by file and exact line
# text. Files whose GNU forms are themselves the subject (the BSD lint and its planted
# cases, the portable-function oracle) are exempt. A comment starts at a `#` that begins
# the line or follows a blank; `$#`, `${#x}` and `x#y` stay code.
set -euo pipefail

here="$(cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
b='(^|[^[:alnum:]_.-])'
pattern="${b}(sha256sum|getent"
pattern+='|sed[[:space:]]([^|;&]*[[:space:]])?(-[[:alpha:]]*i|--in-place)'
pattern+='|realpath[[:space:]]([^|;&]*[[:space:]])?(-[[:alpha:]]*[ems]|--relative-to)'
pattern+='|readlink[[:space:]]([^|;&]*[[:space:]])?-[[:alpha:]]*[efm]'
pattern+='|stat[[:space:]]([^|;&]*[[:space:]])?(-[[:alpha:]]*c|--format|--printf)'
pattern+='|od[[:space:]]([^|;&]*[[:space:]])?-[[:alpha:]]*w'
pattern+='|date[[:space:]]([^|;&]*[[:space:]])?-[[:alpha:]]*d'
pattern+='|find[[:space:]][^|;&]*-printf'
pattern+='|xargs[[:space:]]([^|;&]*[[:space:]])?-[[:alpha:]]*r)([[:space:]=]|$)'
exempt='lint-bsd-userland.sh test-lint-bsd-userland.sh test-eci-portable.sh test-tests-bsd-userland.sh portable-test.sh'
# PORT-owned tests that the macOS portability group (c) migrates; it removes them from
# this list, leaving it empty. Until then they are outside this check's scope.
pending_group_c='run.sh run-isolated.sh'
exempt="$exempt $(printf '%s' "$pending_group_c" | tr -s ' \n' '  ')"

# file<TAB>exact line text, for test data that only looks like an executed GNU form.
# A command string the planner under test must deny; never executed by the test.
ALLOWED="test-eci-planner-protocol-fallback.sh	    \"sed -i 's/needle/replacement/' \$marker\"; do"
# Command strings the planner or hook under test must classify; never executed.
ALLOWED+="
test-eci-command-plan.sh	        'printf one && printf two || printf three; printf four | sha256sum'"
ALLOWED+="
test-eci-command-plan.sh	    \"git diff --binary -- hooks/validate-bash.sh | sha256sum\""
ALLOWED+="
test-eci-command-syntax-gating.sh	      \"sed -i -e '1p' \$MARKER\" \\"
ALLOWED+="
test-eci-command-syntax-gating.sh	      \"sed -i 's/needle/replacement/' \$MARKER\"; do"
# The Linux-only stand-in for BSD `stat -f` (written to a file, run only on Linux hosts) finds
# the path behind its stdin with GNU readlink; it never runs on macOS.
ALLOWED+="
test-eci-native-binary-loader.sh	  target=\"\$(readlink -f -- /dev/stdin)\" || exit 2"
# sed in-place command strings the planner under test must classify; never executed.
ALLOWED+="
test-normal-git-admission.sh	    \"sed --in-place=.bak 's/old/new/' eci_active\" \\"
ALLOWED+="
test-normal-git-admission.sh	    \"sed -e's/old/new/' -i ordinary-copy eci_active\" \\"
ALLOWED+="
test-normal-git-admission.sh	    'sed -f ordinary-copy -i eci_active' \\"
ALLOWED+="
test-normal-git-admission.sh	    'sed -f eci_active -i ordinary-copy' \\"
ALLOWED+="
test-normal-git-admission.sh	    \"sed -e's/old/new/' -i ordinary-copy\" \\"
# Command strings handed to validate-bash.sh for classification; never executed by the test.
ALLOWED+="
test-validate-bash-classifier.sh	  \"sed -i '1s/^/generated /' hooks/generated-coordinator-source-again\""
ALLOWED+="
test-validate-bash-classifier.sh	  \"readlink -f \$ROOT/sessions\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%i %a %n' \$kimi_root/hooks/validate-bash.sh \$kimi_root/hooks/tests/run.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%i %a %h %n' \$kimi_root/hooks/validate-bash.sh \$kimi_root/hooks/tests/run.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%d:%i %a %h %n' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%y %s %n' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%F %s %n' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%F %N' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%i %a %h %s %n' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%s' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%x' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%x %s %n' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%a %n' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%A %n' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%a %n' \$evidence_file\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%i %a %h %n' \$evidence_file\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"readlink -f \$evidence_file\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"realpath -e \$evidence_file && stat -Lc '%d:%i %a %h %n' \$proof_root\""
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%Q' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '\\\$(printf %s)' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat --printf='%s' \$kimi_root/hooks/validate-bash.sh\""
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%i %a %n' \$kimi_root/hooks/validate-bash.sh > \$TMP_ROOT/stat-output\""
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%i %a %n' \$kimi_root/hooks/../outside\""
ALLOWED+="
test-validate-bash-classifier.sh	assert_allowed \"realpath -e \$evidence_file && rm -f \$compound_cleanup_target\" run_hook_with_canonical_tmpdir"
ALLOWED+="
test-validate-bash-classifier.sh	assert_allowed \"realpath -e \$evidence_file && rm -f \$TMP_ROOT/denied\" run_hook_with_canonical_tmpdir"
ALLOWED+="
test-validate-bash-classifier.sh	compound_broad_output=\"\$(run_hook \"realpath -e \$evidence_file && rm -rf /\")\""
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -Lc '%F %N' \$kimi_root/hooks/validate-bash.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%d:%i %a %h %n' hooks/pre-commit-go-mod.sh \$kimi_root/hooks/pre-commit-go-mod.sh\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"stat -c '%y %n' hooks/validate-bash.sh\""
ALLOWED+="
test-validate-bash-classifier.sh	  \"sed -i '1p' \$high_level_log\" \\"
ALLOWED+="
test-validate-bash-classifier.sh	  \"sed -i '1p' \$proof_root/t00-session/high_level_log.anchor\""

failed=0
for file in "$here"/*.sh; do
  name="${file##*/}"
  case " $exempt " in *" $name "*) continue ;; esac
  while IFS= read -r hit; do
    lineno="${hit%%:*}" text="${hit#*:}"
    code="$(printf '%s\n' "$text" | sed -E 's/(^|[[:space:]])#.*$//')"
    printf '%s\n' "$code" | grep -Eq -- "$pattern" || continue
    case "$ALLOWED" in *"$name	$text"*) continue ;; esac
    printf 'test-tests-bsd-userland: %s:%s: GNU-only form: %s\n' "$name" "$lineno" "$text" >&2
    failed=1
  done < <(grep -nE -- "$pattern" "$file" || true)
done
[ "$failed" = 0 ] || exit 1
printf 'test-tests-bsd-userland: PASS\n'
