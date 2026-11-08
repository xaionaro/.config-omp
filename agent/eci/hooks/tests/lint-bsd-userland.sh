#!/usr/bin/env bash
# Lint for GNU-only shell forms in scripts that must run on macOS (/bin/bash 3.2, BSD userland). Runs on Linux;
# parses under bash 3.2. Usage: lint-bsd-userland.sh [FILE...]
# With no FILE it lints the on-demand native provisioning path: lib/eci-native-provision.sh, bin/eci-active,
# build-native-binaries.sh, tests/test-eci-native-provision.sh, this lint, and lib/eci-native-binary.sh.
# Each file prints PASS or FAIL; any FAIL exits 1. hooks/tests/test-lint-bsd-userland.sh holds the planted cases.
# Before matching, backslash-continued lines are joined and comments are removed. Quoted text is reduced to a
# placeholder ('x', "x", or '' when empty), except what the shell still expands inside double quotes: command
# substitutions ($(...) and backticks) and parameter expansions (${...}) are kept. The /proc path form is matched
# with quoted text kept, since paths are quoted.
# - sha256sum outside the loader passes only in a file that probes `command -v sha256sum` and falls back to
#   `shasum -a 256`.
# - GNU long options: any --option that is not in lint_long_allow FAILs.
# - lib/eci-native-binary.sh: a GNU form passes only inside a Linux-only function of the allow-list below, matched by
#   enclosing function and form, never by line number. The lint also checks the guard that makes each of those
#   functions Linux-only: the platform switch before the use, or the single Linux-only caller.
# - No other linted file may call the loader's Linux-only functions.
# - eci_runtime_close_verified is allowed only as the paired release of eci_runtime_open_verified (lint_paired_release):
#   a file that calls it must also call eci_runtime_open_verified as a command.
# Patterns use bracket expressions so that this file does not match itself.
# Documented limits (not detected): GNU forms inside strings that a shell later runs (eval, sh -c, bash -c,
# sh <<<); flags written as quoted words ("-c"); a ")" inside quotes within "$(...)", which ends the kept
# substitution early; a quoted "}" inside ${...}, which ends the kept expansion early (echo "${x:-"}"}" hides
# what follows on that line); runtime-only bash 4/5 features (coproc, globstar, $EPOCHSECONDS, wait -n); flags
# held in variables.
set -u

lint_dir="$(cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
hooks_dir="$(cd -P -- "$lint_dir/.." && pwd -P)"
if [ "$#" -eq 0 ]; then
  set -- "$hooks_dir/lib/eci-native-provision.sh" "$hooks_dir/../bin/eci-active" \
    "$hooks_dir/build-native-binaries.sh" "$hooks_dir/tests/test-eci-native-provision.sh" \
    "$hooks_dir/tests/lint-bsd-userland.sh" "$hooks_dir/lib/eci-native-binary.sh"
fi

# Form names and their extended regular expressions, one pair per line, separated by a tab.
w='(^|[^[:alnum:]_.-])'
c='(^|[^[:alnum:]_.$/-])'
lint_forms="stat-c	${w}sta[t]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*c
realpath-e	${w}realpat[h][[:space:]]+-[[:alpha:]]*[em]
assoc-array	(^|[^[:alnum:]_])(declar[e]|loca[l]|typese[t])[[:space:]]+(-[[:alpha:]]+[[:space:]]+)*-[[:alpha:]]*A
nameref	(^|[^[:alnum:]_])(declar[e]|loca[l]|typese[t])[[:space:]]+(-[[:alpha:]]+[[:space:]]+)*-[[:alpha:]]*n
sed-i-no-suffix	${w}se[d]([[:space:]]+-[[:alpha:]]+)*[[:space:]]+-[[:alpha:]]*i([[:space:]]+([^'\"[:space:]]|'[^']|\"[^\"])|[[:space:]]*$)
date-d	${w}dat[e]([[:space:]]+[^|;&]*)?[[:space:]]-[[:alpha:]]*d
readlink-f	${w}readlin[k][[:space:]]+-[[:alpha:]]*[fem]
xargs-r	${w}xarg[s]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*r
mv-T	${w}m[v]([[:space:]]+-[[:alnum:]-]+)*[[:space:]]+-[[:alpha:]]*T
sort-z	${w}sor[t]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*z
od-w	${w}o[d]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*w
find-printf	${w}fin[d]([[:space:]][^|;&]*)?[[:space:]]-print[f]
timeout-command	${c}timeou[t]([[:space:]]|$)
nproc-command	${c}npro[c]([[:space:]]|[)]|$)
ln-relative	${w}l[n]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*r
head-negative-count	${w}hea[d]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[nc][[:space:]]*-[0-9]
du-b	${w}d[u]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*b
mktemp-p	${w}mkte[m]p([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*p
base64-w	${w}base6[4]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*w
test-v	[[][[][[:space:]]+(![[:space:]]+)?-v[[:space:]]
negative-index	[$][{][A-Za-z_][A-Za-z0-9_]*[[][[:space:]]*-[0-9]
proc-path	/pro[c]/
sha256sum	${w}sha256su[m]([^[:alnum:]_-]|$)
exec-named-fd	(^|[^[:alnum:]_])exe[c][[:space:]]+[{]|[{][A-Za-z_][A-Za-z0-9_]*[}][<>]
array-read-builtin	(^|[^[:alnum:]_-])(mapfil[e]|readarra[y])([^[:alnum:]_-]|$)
case-modification	[$][{][A-Za-z_][A-Za-z0-9_]*(,,|\\^\\^|,|\\^)[}]
grep-P	${w}gre[p]([[:space:]]+-[[:alnum:]]+)*[[:space:]]+-[[:alpha:]]*P"

# Long options the linted files use, NAME<TAB>REASON. Any other --option FAILs.
lint_long_allow="help	bin/eci-active matches its own --help argument; it is not a coreutils option
deny-fd	eci_runtime_verexec passes it to bin/eci-verexec, the ECI native verifier; it is not a coreutils option
check	eci_runtime_verify passes it to bin/eci-verexec (verify without running); it is not a coreutils option"
long_re='(^|[[:space:]|(])-[-]([A-Za-z][A-Za-z0-9-]*)'

# Allow-list for lib/eci-native-binary.sh: FUNCTION<TAB>FORMS<TAB>GUARD. GUARD is either
#   before:TEXT          - TEXT (a platform-switch line) occurs in FUNCTION before every allowed use;
#   caller:NAME          - FUNCTION is called only from NAME, which is itself Linux-only;
#   caller:NAME:TEXT     - called only from NAME, after TEXT (a platform-switch line in NAME).
# The switch line: G3 eci_runtime_binary_is_native returns the Darwin twin, or 1 for another OS, before its
# Linux part. Linux verification is native (bin/eci-verexec), so the loader has no other Linux-only form.
lint_loader_allow="eci_runtime_binary_is_native	od-w proc-path	before:    Darwin) _eci_runtime_binary_is_native_darwin \"\$1\"; return ;;"
# Linux-only loader functions that no other linted file may call. Linux verification moved to bin/eci-verexec,
# so none remain; the check stays for any that are added.
lint_linux_only=''
# RELEASE:ACQUIRE. eci_runtime_close_verified is the paired, idempotent release of eci_runtime_open_verified (macOS)
# and has no Linux branch. A file may call it only if it also runs the open as a command (a mention in a comment or a
# string does not count).
lint_paired_release='eci_runtime_close_verified:eci_runtime_open_verified'
paired_release_re=$(printf '(^|[^[:alnum:]_])%s([^[:alnum:]_]|$)' "${lint_paired_release%%:*}")
paired_acquire_re=$(printf '(^|[;&|])[[:space:]]*((if|while|until)[[:space:]]+)?(![[:space:]]+)?%s([[:space:]]|;|$)' "${lint_paired_release#*:}")

func_re='^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*[(][)]'
end_re='^[})]'
word_re() { printf '(^|[^[:alnum:]_])%s([^[:alnum:]_]|$)' "$1"; }

# Split the tables once into indexed arrays (bash 3.2 has no associative arrays).
form_count=0
while IFS='	' read -r form re; do
  form_name[$form_count]=$form
  form_re[$form_count]=$re
  form_count=$((form_count + 1))
done <<EOF
$lint_forms
EOF
allow_count=0
while IFS='	' read -r entry_func forms guard; do
  allow_func[$allow_count]=$entry_func
  allow_forms[$allow_count]=" $forms "
  allow_guard[$allow_count]=$guard
  allow_count=$((allow_count + 1))
done <<EOF
$lint_loader_allow
EOF
long_allowed=' '
while IFS='	' read -r long_name long_reason; do
  [ -z "$long_name" ] || long_allowed="$long_allowed$long_name "
done <<EOF
$lint_long_allow
EOF
linux_only_count=0
for linux_func in $lint_linux_only; do
  linux_only_name[$linux_only_count]=$linux_func
  linux_only_re[$linux_only_count]=$(word_re "$linux_func")
  linux_only_count=$((linux_only_count + 1))
done
lint_self="$lint_dir/${BASH_SOURCE[0]##*/}"

# strip_line TEXT: sets strip_code (comment removed, quotes kept) and strip_text (comment removed, quoted text
# reduced to a placeholder, command substitutions kept).
strip_line() {
  local s="$1" i=0 len ch prev state=n filled=0 depth
  strip_code=''
  strip_text=''
  case $s in
    *[\'\"\`#\\]*) ;;
    *) strip_code=$s; strip_text=$s; return 0 ;;
  esac
  len=${#s}
  while [ "$i" -lt "$len" ]; do
    ch=${s:$i:1}
    case $state in
      n)
        case $ch in
          \\) strip_code="$strip_code${s:$i:2}"; strip_text="$strip_text${s:$i:2}"; i=$((i + 2)); continue ;;
          \') state=s; filled=0; strip_code="$strip_code$ch" ;;
          \") state=d; filled=0; strip_code="$strip_code$ch" ;;
          \#)
            prev=''
            [ "$i" -eq 0 ] || prev=${s:$((i - 1)):1}
            case $prev in ''|' '|'	'|';'|'&'|'|'|'(') return 0 ;; esac
            strip_code="$strip_code$ch"; strip_text="$strip_text$ch" ;;
          *) strip_code="$strip_code$ch"; strip_text="$strip_text$ch" ;;
        esac
        ;;
      s)
        strip_code="$strip_code$ch"
        if [ "$ch" = "'" ]; then
          if [ "$filled" = 1 ]; then strip_text="$strip_text'x'"; else strip_text="$strip_text''"; fi
          state=n
        else
          filled=1
        fi
        ;;
      d)
        case $ch in
          \\) strip_code="$strip_code${s:$i:2}"; filled=1; i=$((i + 2)); continue ;;
          \")
            strip_code="$strip_code$ch"
            if [ "$filled" = 1 ]; then strip_text="$strip_text\"x\""; else strip_text="$strip_text\"\""; fi
            state=n
            ;;
          \`)
            # A backtick substitution runs inside double quotes: keep it in both forms.
            depth=$i
            i=$((i + 1))
            while [ "$i" -lt "$len" ] && [ "${s:$i:1}" != '`' ]; do i=$((i + 1)); done
            strip_code="$strip_code${s:$depth:$((i - depth + 1))}"
            strip_text="$strip_text ${s:$((depth + 1)):$((i - depth - 1))} "
            filled=1
            ;;
          \$)
            if [ "${s:$((i + 1)):1}" = '(' ]; then
              # A $(...) substitution runs inside double quotes: keep it, up to the matching parenthesis.
              local start=$i level=0
              while [ "$i" -lt "$len" ]; do
                case ${s:$i:1} in
                  '(') level=$((level + 1)) ;;
                  ')') level=$((level - 1)); [ "$level" -gt 0 ] || break ;;
                esac
                i=$((i + 1))
              done
              strip_code="$strip_code${s:$start:$((i - start + 1))}"
              strip_text="$strip_text ${s:$start:$((i - start + 1))} "
            elif [ "${s:$((i + 1)):1}" = '{' ]; then
              # A ${...} expansion runs inside double quotes: keep it, up to the matching brace.
              local start=$i level=0
              while [ "$i" -lt "$len" ]; do
                case ${s:$i:1} in
                  '{') level=$((level + 1)) ;;
                  '}') level=$((level - 1)); [ "$level" -gt 0 ] || break ;;
                esac
                i=$((i + 1))
              done
              strip_code="$strip_code${s:$start:$((i - start + 1))}"
              strip_text="$strip_text ${s:$start:$((i - start + 1))} "
            else
              strip_code="$strip_code$ch"
            fi
            filled=1
            ;;
          *) strip_code="$strip_code$ch"; filled=1 ;;
        esac
        ;;
    esac
    i=$((i + 1))
  done
  # An unterminated quote (a string continued on the next line) keeps its placeholder.
  case $state in
    s) strip_text="$strip_text'x" ;;
    d) strip_text="$strip_text\"x" ;;
  esac
}

# Loads FILE: backslash-continued lines are joined into the first line's slot. line_raw holds the joined raw
# text, line_code and line_text the two stripped forms, line_func the enclosing function.
load_file() {
  local line number=0 func='' slot=0 pending=''
  line_raw=()
  line_code=()
  line_text=()
  line_func=()
  while IFS= read -r line || [ -n "$line" ]; do
    number=$((number + 1))
    line_raw[$number]=''
    line_code[$number]=''
    line_text[$number]=''
    line_func[$number]=$func
    if [ -n "$pending" ]; then
      pending="$pending $line"
    else
      slot=$number
      pending=$line
    fi
    case $pending in
      *\\) pending=${pending%\\}; continue ;;
    esac
    if [[ ${line_raw[$slot]:-$pending} =~ $func_re ]] && [ "$slot" = "$number" ]; then
      func=${BASH_REMATCH[1]}
    elif [[ $pending =~ $end_re ]]; then
      func=''
    fi
    line_func[$slot]=$func
    [ "$slot" = "$number" ] || line_func[$slot]=${line_func[$slot]}
    line_raw[$slot]=$pending
    strip_line "$pending"
    line_code[$slot]=$strip_code
    line_text[$slot]=$strip_text
    pending=''
  done <"$1"
  if [ -n "$pending" ]; then
    line_raw[$slot]=$pending
    strip_line "$pending"
    line_code[$slot]=$strip_code
    line_text[$slot]=$strip_text
  fi
  line_count=$number
}

# first_in FUNCTION TEXT: the first line number in FUNCTION whose raw text equals TEXT, or nothing.
first_in() {
  local n=1
  while [ "$n" -le "$line_count" ]; do
    if [ "${line_func[$n]}" = "$1" ] && [ "${line_raw[$n]}" = "$2" ]; then
      printf '%s\n' "$n"
      return 0
    fi
    n=$((n + 1))
  done
}

# guard_holds FUNCTION GUARD LINE: GUARD (see lint_loader_allow) holds for a use at LINE in FUNCTION.
guard_holds() {
  local func="$1" guard="$2" use="$3" at caller text re n found=0
  case "$guard" in
    before:*)
      at=$(first_in "$func" "${guard#before:}")
      [ -n "$at" ] && [ "$at" -lt "$use" ]
      return
      ;;
    caller:*)
      caller=${guard#caller:}
      text=''
      case "$caller" in *:*) text=${caller#*:}; caller=${caller%%:*} ;; esac
      re=$(word_re "$func")
      n=1
      while [ "$n" -le "$line_count" ]; do
        if [[ ${line_text[$n]} =~ $re ]] && ! [[ ${line_raw[$n]} =~ $func_re ]] && [ "${line_func[$n]}" != "$func" ]; then
          [ "${line_func[$n]}" = "$caller" ] || return 1
          if [ -n "$text" ]; then
            at=$(first_in "$caller" "$text")
            [ -n "$at" ] && [ "$at" -lt "$n" ] || return 1
          fi
          found=1
        fi
        n=$((n + 1))
      done
      [ "$found" = 1 ]
      return
      ;;
  esac
  return 1
}

# loader_allowed FUNCTION FORM LINE: FORM at LINE in FUNCTION is an allow-listed Linux-branch use whose guards hold.
loader_allowed() {
  local func="$1" form="$2" use="$3" i matched=0
  i=0
  while [ "$i" -lt "$allow_count" ]; do
    if [ "${allow_func[$i]}" = "$func" ]; then
      case "${allow_forms[$i]}" in *" $form "*) matched=1 ;; esac
    fi
    i=$((i + 1))
  done
  [ "$matched" = 1 ] || return 1
  # Every guard listed for the function must hold, not only the entry that names the form.
  i=0
  while [ "$i" -lt "$allow_count" ]; do
    if [ "${allow_func[$i]}" = "$func" ]; then
      guard_holds "$func" "${allow_guard[$i]}" "$use" || return 1
    fi
    i=$((i + 1))
  done
}

status=0
for file in "$@"; do
  if [ ! -f "$file" ]; then
    printf 'FAIL %s (missing)\n' "$file"
    status=1
    continue
  fi
  load_file "$file"
  is_loader=0
  case "${file##*/}" in eci-native-binary.sh) is_loader=1 ;; esac
  # This lint names the Linux-only functions as data; every other file is checked for calls to them.
  check_calls=1
  [ "$is_loader" = 0 ] || check_calls=0
  [ "$(cd -P -- "$(dirname -- "$file")" && pwd -P)/${file##*/}" != "$lint_self" ] || check_calls=0
  has_fallback=0
  if grep -F -q -- 'command -v sha256sum' "$file" && grep -F -q -- 'shasum -a 256' "$file"; then has_fallback=1; fi
  hits=''
  release_hits=''
  has_acquire=0
  n=1
  while [ "$n" -le "$line_count" ]; do
    line=${line_text[$n]}
    if [ -z "$line" ] && [ -z "${line_code[$n]}" ]; then n=$((n + 1)); continue; fi
    i=0
    while [ "$i" -lt "$form_count" ]; do
      form=${form_name[$i]}
      re=${form_re[$i]}
      i=$((i + 1))
      if [ "$form" = proc-path ]; then
        [[ ${line_code[$n]} =~ $re ]] || continue
      else
        [[ $line =~ $re ]] || continue
      fi
      if [ "$is_loader" = 1 ]; then
        if loader_allowed "${line_func[$n]}" "$form" "$n"; then
          printf '  allowed %s:%s %s in %s (Linux branch)\n' "${file##*/}" "$n" "$form" "${line_func[$n]}"
          continue
        fi
      elif [ "$form" = sha256sum ] && [ "$has_fallback" = 1 ]; then
        continue
      fi
      hits="$hits  ${file##*/}:$n: $form in ${line_func[$n]:-top level}: ${line_raw[$n]}
"
    done
    rest=$line
    while [[ $rest =~ $long_re ]]; do
      long_name=${BASH_REMATCH[2]}
      rest=${rest#*"${BASH_REMATCH[0]}"}
      case "$long_allowed" in
        *" $long_name "*) ;;
        *) hits="$hits  ${file##*/}:$n: long option --$long_name is not in lint_long_allow: ${line_raw[$n]}
" ;;
      esac
    done
    if [ "$check_calls" = 1 ]; then
      i=0
      while [ "$i" -lt "$linux_only_count" ]; do
        re=${linux_only_re[$i]}
        if [[ $line =~ $re ]]; then
          hits="$hits  ${file##*/}:$n: calls the loader's Linux-only ${linux_only_name[$i]}: ${line_raw[$n]}
"
        fi
        i=$((i + 1))
      done
      [[ $line =~ $paired_acquire_re ]] && has_acquire=1
      if [[ $line =~ $paired_release_re ]]; then
        release_hits="$release_hits  ${file##*/}:$n: calls ${lint_paired_release%%:*} without ${lint_paired_release#*:} as a command in the same file: ${line_raw[$n]}
"
      fi
    fi
    n=$((n + 1))
  done
  [ "$has_acquire" = 1 ] || hits="$hits$release_hits"
  if [ -n "$hits" ]; then
    printf 'FAIL %s\n%s' "$file" "$hits"
    status=1
  else
    printf 'PASS %s\n' "$file"
  fi
done
exit "$status"
