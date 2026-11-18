#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/eci-workspace-test.XXXXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/fixture/app" "$tmp/fixture/member" "$tmp/fixture/decoy" "$tmp/cache"
# godbus v5.1.0 is an unpruned go 1.12 module, so Go loads member@v0.0.0 offline through the
# versioned decoy replacement; the workspace member must still win over the decoy.
printf 'go 1.24.0\nuse (\n ./app\n ./member\n)\nreplace example.test/member v0.0.0 => ./decoy\n' > "$tmp/fixture/go.work"
printf 'module example.test/member\n\ngo 1.24.0\n' > "$tmp/fixture/member/go.mod"
printf 'package member\nconst Value = "one"\n' > "$tmp/fixture/member/value.go"
printf 'module example.test/member\n\ngo 1.24.0\n' > "$tmp/fixture/decoy/go.mod"
printf 'package member\nconst Value = "decoy"\n' > "$tmp/fixture/decoy/value.go"
printf 'module example.test/app\n\ngo 1.24.0\nrequire (\n example.test/member v0.0.0\n github.com/godbus/dbus/v5 v5.1.0\n)\n' > "$tmp/fixture/app/go.mod"
printf 'package main\nimport ("fmt"; "example.test/member"; _ "github.com/godbus/dbus/v5")\nfunc main(){fmt.Print(member.Value)}\n' > "$tmp/fixture/app/main.go"
grep '^github.com/godbus/dbus/v5 v5.1.0' "$root/tools/owned-command/go.sum" > "$tmp/fixture/app/go.sum"

# The caller's caches, resolved before HOME changes, so the wrapper half reuses them.
real_go=$(command -v go)
{ IFS= read -r real_root && IFS= read -r go_cache && IFS= read -r go_modcache && IFS= read -r go_path; } \
  <<<"$(GOWORK=auto "$real_go" env GOROOT GOCACHE GOMODCACHE GOPATH)"

# Recording shims: the launcher on PATH and the compiler under SHIM_ROOT log the GOWORK of every
# builder Go invocation. The compiler shim can also edit a source after a real build (the race).
mkdir -p "$tmp/toolchain/bin" "$tmp/shim"
cat > "$tmp/toolchain/bin/go" <<'EOF'
#!/usr/bin/env bash
set -e
export GOROOT="$REAL_GOROOT"
printf '%s compiler %s\n' "${GOWORK-unset}" "$*" >> "$GO_LOG"
if [ -n "${RACE_SOURCE:-}" ] && [ "$1" = build ] && [ "$2" != -n ]; then
  "$REAL_GO" "$@"
  if [ ! -e "$RACE_MARKER" ]; then
    : > "$RACE_MARKER"
    printf 'package member\nconst Value = "raced"\n' > "$RACE_SOURCE"
  fi
  exit 0
fi
exec "$REAL_GO" "$@"
EOF
chmod 0755 "$tmp/toolchain/bin/go"
cat > "$tmp/shim/go" <<'EOF'
#!/usr/bin/env bash
set -e
printf '%s launcher %s\n' "${GOWORK-unset}" "$*" >> "$GO_LOG"
if [ "$1" = env ] && [ "$2" = GOHOSTOS ]; then
  "$REAL_GO" "$@" | awk -v root="$SHIM_ROOT" 'NR == 3 {$0 = root} {print}'
  exit 0
fi
exec "$REAL_GO" "$@"
EOF
chmod 0755 "$tmp/shim/go"
export REAL_GO="$real_go" REAL_GOROOT="$real_root" SHIM_ROOT="$tmp/toolchain" GO_LOG="$tmp/go.log"
export PATH="$tmp/shim:$PATH"
: > "$GO_LOG"

# The foreign caller workspace must not replace auto-discovery for the tool module.
export GOWORK="$tmp/foreign.go.work"
# Workspace members are local and godbus comes from the module cache; never consult a proxy.
export GOPROXY=off
source "$root/hooks/lib/eci-go-tool-build.sh"
sha() { "${eci_sha256_command[@]}" "$1" | awk '{print $1}'; }
# Every file under the cache prefix with its digest, so a check sees additions, removals and rewrites.
cache_listing() { find "$tmp/cache" -type f -exec "${eci_sha256_command[@]}" {} + | LC_ALL=C sort; }
# build_fixture LABEL runs one fixture build with stderr in $tmp/LABEL.err.
build_fixture() { eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment' 2> "$tmp/$1.err"; }
notices() { grep -c '^fixture: building ' "$tmp/$1.err" || true; }
# Go only refreshes an up-to-date -o target's times (cmd/go/internal/work/exec.go). A relink either
# renames a new file over the target or removes and recreates it (cmd/go/internal/work/shell.go).
# The held hard link keeps the old inode alive, so either kind of relink breaks -ef.
stage="$tmp/cache/.fixture-stage/fixture"
hold_stage() {
  rm -f -- "$tmp/stage.hold"
  ln -- "$stage" "$tmp/stage.hold"
  held_sha=$(sha "$stage")
  held_files=$(cache_listing)
}
stage_held() {
  [ "$stage" -ef "$tmp/stage.hold" ] && [ "$(sha "$stage")" = "$held_sha" ] && [ "$(cache_listing)" = "$held_files" ]
}

# A cold call announces itself exactly once.
build_fixture cold
first=$eci_go_tool_executable
[ "$(notices cold)" = 1 ]
[ "$("$first")" = one ]
first_sha=$(sha "$first")

# The wrapper resolves through the same builder from a fixture HOME whose ECI tree is this checkout.
mkdir -p "$tmp/home/.omp/agent"
ln -s -- "$root" "$tmp/home/.omp/agent/eci"
resolved=$(HOME="$tmp/home" GOCACHE="$go_cache" GOMODCACHE="$go_modcache" GOPATH="$go_path" \
  OWNED_COMMAND_SOURCE="$tmp/fixture/app" OWNED_COMMAND_CACHE="$tmp/wrapper-cache" \
  bash "$root/bin/owned-command" --resolve-executable)
[ "$("$resolved")" = one ]

# A warm repeat prints no notice and keeps the stage file.
hold_stage
build_fixture warm
[ "$first" = "$eci_go_tool_executable" ]
[ "$(notices warm)" = 0 ]
stage_held
# Negative control: a same-bytes replacement of the stage is a different file.
cp -- "$stage" "$tmp/stage.copy"
mv -- "$tmp/stage.copy" "$stage"
if [ "$stage" -ef "$tmp/stage.hold" ]; then
  printf '%s\n' 'replaced stage still matched the held link' >&2
  exit 1
fi

# Every builder Go invocation (bootstrap, tuning query, dry run, build) used workspace auto-discovery.
grep -q '^[^ ]* launcher env GOHOSTOS ' "$GO_LOG"
grep -q '^[^ ]* compiler env GO[A-Z0-9]*$' "$GO_LOG"
grep -q '^[^ ]* compiler build -n ' "$GO_LOG"
grep -q '^[^ ]* compiler build -buildvcs=false ' "$GO_LOG"
if grep -v '^auto ' "$GO_LOG" >&2; then
  printf '%s\n' 'a builder Go invocation did not use GOWORK=auto' >&2
  exit 1
fi

# A member edit is announced and relinks the stage.
hold_stage
printf 'package member\nconst Value = "two"\n' > "$tmp/fixture/member/value.go"
build_fixture member
second=$eci_go_tool_executable
[ "$(notices member)" = 1 ]
[ ! "$stage" -ef "$tmp/stage.hold" ]
[ "$first" != "$second" ] && [ "$("$second")" = two ]
[ "$(sha "$first")" = "$first_sha" ]
second_sha=$(sha "$second")
# A workspace replacement, rather than a local go.mod replacement, changes the build.
mkdir -p "$tmp/fixture/replacement"
printf 'module example.test/member\n\ngo 1.24.0\n' > "$tmp/fixture/replacement/go.mod"
printf 'package member\nconst Value = "replacement"\n' > "$tmp/fixture/replacement/value.go"
printf 'go 1.24.0\nuse ./app\nreplace example.test/member => ./replacement\n' > "$tmp/fixture/go.work"
build_fixture replacement
replacement=$eci_go_tool_executable
[ "$replacement" != "$second" ] && [ "$("$replacement")" = replacement ]
[ "$(sha "$second")" = "$second_sha" ]
replacement_sha=$(sha "$replacement")
printf 'package member\nconst Value = "replacement edited"\n' > "$tmp/fixture/replacement/value.go"
build_fixture replacement-edit
[ "$replacement" != "$eci_go_tool_executable" ] && [ "$("$eci_go_tool_executable")" = "replacement edited" ]
[ "$(sha "$replacement")" = "$replacement_sha" ]
# A workspace that does not use the built module fails with Go's error and the rerun line.
printf 'go 1.24.0\nuse ./member\n' > "$tmp/fixture/go.work"
if build_fixture non-member; then
  printf '%s\n' 'non-member workspace unexpectedly built' >&2
  exit 1
fi
grep -q 'not one of the workspace modules' "$tmp/non-member.err"
grep -q 'rerun:' "$tmp/non-member.err"
printf 'go 1.24.0\nuse (\n ./app\n ./member\n)\nreplace example.test/member v0.0.0 => ./decoy\n' > "$tmp/fixture/go.work"
build_fixture restored
[ "$second" = "$eci_go_tool_executable" ]
# Workspace sum metadata with no semantic dependency change preserves the binary and the stage.
hold_stage
: > "$tmp/fixture/go.work.sum"
build_fixture empty-sum
[ "$second" = "$eci_go_tool_executable" ]
stage_held
grep '^github.com/godbus/dbus/v5 v5.1.0/go.mod ' "$root/tools/owned-command/go.sum" > "$tmp/fixture/go.work.sum"
printf '// harmless workspace comment\n' >> "$tmp/fixture/go.work"
build_fixture neutral
[ "$second" = "$eci_go_tool_executable" ]
stage_held
# A bad cached module checksum must fail before anything is published.
: > "$tmp/fixture/go.work.sum"
grep -v '^github.com/godbus/dbus/v5 v5.1.0/go.mod ' "$tmp/fixture/app/go.sum" > "$tmp/app.sum"
printf 'github.com/godbus/dbus/v5 v5.1.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n' >> "$tmp/app.sum"
mv "$tmp/app.sum" "$tmp/fixture/app/go.sum"
if build_fixture checksum; then
  printf '%s\n' 'bad module checksum was accepted' >&2
  exit 1
fi
grep -q 'checksum mismatch' "$tmp/checksum.err"
grep -q 'rerun:' "$tmp/checksum.err"
cat "$tmp/checksum.err" >&2
stage_held
[ "$(sha "$second")" = "$second_sha" ]
grep '^github.com/godbus/dbus/v5 v5.1.0' "$root/tools/owned-command/go.sum" > "$tmp/fixture/app/go.sum"
# Existing content-addressed entries are immutable and must be verified before reuse.
printf 'tampered\n' > "$second"
if build_fixture tamper; then
  printf '%s\n' 'tampered cache entry was accepted' >&2
  exit 1
fi
grep -qF "fixture: cached executable digest mismatch: $second" "$tmp/tamper.err"

# The compiler shim changes a fixture source after the first build completes.
# The second build must detect the changed output and refuse publication.
mkdir -p "$tmp/race-cache"
export RACE_MARKER="$tmp/race-marker" RACE_SOURCE="$tmp/fixture/member/value.go"
if eci_go_tool_build race "$tmp/fixture/app" "$tmp/race-cache/" /race 'a moment' 2> "$tmp/race.err"; then
  printf '%s\n' 'mid-build source edit was accepted' >&2
  exit 1
fi
grep -qF 'race: source changed during build; retry invocation' "$tmp/race.err"
[ -z "$(find "$tmp/race-cache" -type f -name race ! -path '*/.race-stage/*' -print)" ]
printf '%s\n' 'workspace builder: PASS'
