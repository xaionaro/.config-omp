# Sourced by hooks tests that run Go. Resolves go from PATH once into ECI_TEST_GO
# and exits with a clear message when go is absent, not an absolute executable, or
# older than the newest `go` directive among the runtime's go.mod files. Versions
# compare numerically in awk (a missing patch counts as 0), so no `sort -V` is needed.
# A devel toolchain reports no release version and is rejected; a pre-release such
# as go1.26rc1 compares by its numeric prefix, 1.26, which equals 1.26.0.
#
# Usage: . "$(dirname "${BASH_SOURCE[0]}")/require-go.sh"
# Example: on a host whose PATH holds Go 1.26, ECI_TEST_GO=/usr/local/go/bin/go.

eci_test_require_go() {
  local root required version
  root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
  ECI_TEST_GO="$(command -v go)" || {
    printf 'hooks test: go not found on PATH; install Go and put it on PATH\n' >&2
    exit 1
  }
  case "$ECI_TEST_GO" in
    /*) [ -x "$ECI_TEST_GO" ] || { printf 'hooks test: %s is not executable\n' "$ECI_TEST_GO" >&2; exit 1; } ;;
    *) printf "hooks test: go resolves to '%s', not an absolute path\n" "$ECI_TEST_GO" >&2; exit 1 ;;
  esac
  required="$({ cat -- "$root"/hooks/lib/*/go.mod "$root"/tools/*/go.mod 2>/dev/null || true; } |
    awk '$1 == "go" { n = split($2, p, "."); k = sprintf("%05d%05d%05d", p[1], p[2], p[3]); if (k > best) { best = k; v = $2 } } END { print v }')"
  [ -n "$required" ] || { printf 'hooks test: no go directive found under %s\n' "$root" >&2; exit 1; }
  version="$(cd / && GOFLAGS=' ' GOTOOLCHAIN=local "$ECI_TEST_GO" env GOVERSION 2>/dev/null)" || version=''
  case "$version" in go[0-9]*) version="${version#go}" ;; *) version='' ;; esac
  version="${version%%[!0-9.]*}"
  if [ -z "$version" ] || ! awk -v have="$version" -v need="$required" 'BEGIN {
      split(have, h, "."); split(need, n, ".")
      exit !(sprintf("%05d%05d%05d", h[1], h[2], h[3]) >= sprintf("%05d%05d%05d", n[1], n[2], n[3]))
    }'; then
    printf 'hooks test: %s reports Go %s; the runtime go.mod files require Go %s or newer\n' \
      "$ECI_TEST_GO" "${version:-unknown}" "$required" >&2
    exit 1
  fi
  export ECI_TEST_GO
}

eci_test_require_go
