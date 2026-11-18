#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
disabled_workspace='GOWORK=o''ff'
if grep -n "$disabled_workspace" \
  "$root/hooks/lib/eci-go-tool-build.sh" \
  "$root/hooks/build-native-binaries.sh" \
  "$root/tools/eci-active/planner.go" \
  "$root/tools/owned-command/README.md" \
  "$root/bin/owned-command" \
  "$root/hooks/tests/test-eci-native-provision.sh" \
  "$root/hooks/tests/test-eci-native-binary-loader.sh"; then
  printf '%s\n' 'shared tool build disables workspace discovery' >&2
  exit 1
fi
tmp=$(mktemp -d "${TMPDIR:-/tmp}/eci-workspace-test.XXXXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
printf '%s\n' "$disabled_workspace" > "$tmp/negative-control"
grep -q "$disabled_workspace" "$tmp/negative-control" || {
  printf '%s\n' 'static negative control was not detected' >&2
  exit 1
}
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
# The foreign caller workspace must not replace auto-discovery for the tool module.
export GOWORK="$tmp/foreign.go.work"
# Workspace members are local and godbus comes from the module cache; never consult a proxy.
export GOPROXY=off
source "$root/hooks/lib/eci-go-tool-build.sh"
# Every file under the cache prefix with its digest, so a check sees additions, removals and rewrites.
cache_listing() { find "$tmp/cache" -type f -exec "${eci_sha256_command[@]}" {} + | LC_ALL=C sort; }
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
first=$eci_go_tool_executable
[ "$("$first")" = one ]
first_sha=$("${eci_sha256_command[@]}" "$first")
resolved=$(OWNED_COMMAND_SOURCE="$tmp/fixture/app" OWNED_COMMAND_CACHE="$tmp/wrapper-cache" \
  bash "$root/bin/owned-command" --resolve-executable)
[ "$("$resolved")" = one ]
stage="$tmp/cache/.fixture-stage/fixture"
# Go only refreshes an up-to-date -o target's mtime (cmd/go/internal/work/exec.go); a relink renames or recreates the file, so an unchanged stage inode and digest prove reuse.
before=$(stat -c %i "$stage"; "${eci_sha256_command[@]}" "$stage")
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
[ "$first" = "$eci_go_tool_executable" ]
[ "$(stat -c %i "$stage"; "${eci_sha256_command[@]}" "$stage")" = "$before" ]
printf 'package member\nconst Value = "two"\n' > "$tmp/fixture/member/value.go"
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
second=$eci_go_tool_executable
[ "$first" != "$second" ] && [ "$("$second")" = two ]
[ "$("${eci_sha256_command[@]}" "$first")" = "$first_sha" ]
second_sha=$("${eci_sha256_command[@]}" "$second")
# A workspace replacement, rather than a local go.mod replacement, changes the build.
mkdir -p "$tmp/fixture/replacement"
printf 'module example.test/member\n\ngo 1.24.0\n' > "$tmp/fixture/replacement/go.mod"
printf 'package member\nconst Value = "replacement"\n' > "$tmp/fixture/replacement/value.go"
printf 'go 1.24.0\nuse ./app\nreplace example.test/member => ./replacement\n' > "$tmp/fixture/go.work"
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
replacement=$eci_go_tool_executable
[ "$replacement" != "$second" ] && [ "$("$replacement")" = replacement ]
[ "$("${eci_sha256_command[@]}" "$second")" = "$second_sha" ]
replacement_sha=$("${eci_sha256_command[@]}" "$replacement")
printf 'package member\nconst Value = "replacement edited"\n' > "$tmp/fixture/replacement/value.go"
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
[ "$replacement" != "$eci_go_tool_executable" ] && [ "$("$eci_go_tool_executable")" = "replacement edited" ]
[ "$("${eci_sha256_command[@]}" "$replacement")" = "$replacement_sha" ]
# A workspace that does not use the built module fails.
printf 'go 1.24.0\nuse ./member\n' > "$tmp/fixture/go.work"
if eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'; then
  printf '%s\n' 'non-member workspace unexpectedly built' >&2
  exit 1
fi
printf 'go 1.24.0\nuse (\n ./app\n ./member\n)\nreplace example.test/member v0.0.0 => ./decoy\n' > "$tmp/fixture/go.work"
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
[ "$second" = "$eci_go_tool_executable" ]
before=$(stat -c %i "$stage"; "${eci_sha256_command[@]}" "$stage")
files=$(cache_listing)
# Workspace sum metadata with no semantic dependency change preserves the binary.
: > "$tmp/fixture/go.work.sum"
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
[ "$second" = "$eci_go_tool_executable" ]
[ "$(stat -c %i "$stage"; "${eci_sha256_command[@]}" "$stage")" = "$before" ]
[ "$(cache_listing)" = "$files" ]
grep '^github.com/godbus/dbus/v5 v5.1.0/go.mod ' "$root/tools/owned-command/go.sum" > "$tmp/fixture/go.work.sum"
printf '// harmless workspace comment\n' >> "$tmp/fixture/go.work"
eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'
[ "$second" = "$eci_go_tool_executable" ]
[ "$(stat -c %i "$stage"; "${eci_sha256_command[@]}" "$stage")" = "$before" ]
[ "$(cache_listing)" = "$files" ]
# A bad cached module checksum must fail before anything is published.
: > "$tmp/fixture/go.work.sum"
stage_sha=$("${eci_sha256_command[@]}" "$stage")
grep -v '^github.com/godbus/dbus/v5 v5.1.0/go.mod ' "$tmp/fixture/app/go.sum" > "$tmp/app.sum"
printf 'github.com/godbus/dbus/v5 v5.1.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n' >> "$tmp/app.sum"
mv "$tmp/app.sum" "$tmp/fixture/app/go.sum"
if GOPROXY=off eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment' 2>"$tmp/checksum.err"; then
  printf '%s\n' 'bad module checksum was accepted' >&2
  exit 1
fi
grep -q 'checksum mismatch' "$tmp/checksum.err"
grep -q 'rerun:' "$tmp/checksum.err"
cat "$tmp/checksum.err" >&2
[ "$("${eci_sha256_command[@]}" "$stage")" = "$stage_sha" ]
[ "$("${eci_sha256_command[@]}" "$second")" = "$second_sha" ]
[ "$(cache_listing)" = "$files" ]
grep '^github.com/godbus/dbus/v5 v5.1.0' "$root/tools/owned-command/go.sum" > "$tmp/fixture/app/go.sum"
# Existing content-addressed entries are immutable and must be verified before reuse.
printf 'tampered\n' > "$second"
if eci_go_tool_build fixture "$tmp/fixture/app" "$tmp/cache/" /fixture 'a moment'; then
  printf '%s\n' 'tampered cache entry was accepted' >&2
  exit 1
fi

# A toolchain shim changes a fixture source after the first build completes.
# The second build must detect the changed output and refuse publication.
real_go=$(command -v go)
real_root=$(GOWORK=auto "$real_go" env GOROOT)
mkdir -p "$tmp/toolchain/bin" "$tmp/shim" "$tmp/race-cache"
cat > "$tmp/toolchain/bin/go" <<'EOF'
#!/usr/bin/env bash
set -e
export GOROOT="$REAL_GOROOT"
if [ "$1" = build ]; then
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
if [ "$1" = env ] && [ "$2" = GOHOSTOS ]; then
  "$REAL_GO" "$@" | awk -v root="$SHIM_ROOT" 'NR == 3 {$0 = root} {print}'
  exit 0
fi
exec "$REAL_GO" "$@"
EOF
chmod 0755 "$tmp/shim/go"
export REAL_GO="$real_go" REAL_GOROOT="$real_root" SHIM_ROOT="$tmp/toolchain"
export RACE_MARKER="$tmp/race-marker" RACE_SOURCE="$tmp/fixture/member/value.go"
if PATH="$tmp/shim:$PATH" eci_go_tool_build race "$tmp/fixture/app" "$tmp/race-cache/" /race 'a moment'; then
  printf '%s\n' 'mid-build source edit was accepted' >&2
  exit 1
fi
[ -z "$(find "$tmp/race-cache" -type f -name race ! -path '*/.race-stage/*' -print)" ]
printf '%s\n' 'workspace builder: PASS'
