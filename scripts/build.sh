#!/usr/bin/env bash
# Build every component. `make` is not installed on the dev machine and is not
# worth a dependency for five build lines, so this replaces the Makefile the
# plan called for.
#
#   ./scripts/build.sh          build everything that exists
#   ./scripts/build.sh relay    build one component
#
# Server binaries are cross-compiled for Linux from Windows; that is why
# CGO is off. Output lands in bin/.
set -uo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. scripts/env.sh

mkdir -p bin
FAIL=0

build() { # module  outfile  goos  pkg
  local mod="$1" out="$2" goos="$3" pkg="$4"
  if ! ls "$mod/$pkg"/*.go >/dev/null 2>&1; then
    printf '  SKIP  %s (%s not written yet)\n' "$out" "$mod/$pkg"
    return 0
  fi
  if (cd "$mod" && CGO_ENABLED=0 GOOS="$goos" GOARCH=amd64 \
        go build -trimpath -o "../bin/$out" "./$pkg"); then
    printf '  OK    bin/%s\n' "$out"
  else
    printf '  FAIL  bin/%s\n' "$out"
    FAIL=1
  fi
}

target="${1:-all}"

case "$target" in
  all|relay)       build relay       relay          linux   cmd/relay ;;&
  all|coordinator) build coordinator coordinator    linux   cmd/coordinator ;;&
  all|netservice)  build netservice  netservice.exe windows cmd/netservice ;;&
  all|lobbycli)    build lobbycli    lobbycli.exe   windows . ;;&
  all|loadtest)    build loadtest    loadtest       linux   . ;;&
  all|relay|coordinator|netservice|lobbycli|loadtest) ;;
  *) echo "unknown target: $target" >&2; exit 2 ;;
esac

exit "$FAIL"
