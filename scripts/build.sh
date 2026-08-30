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

# Client binaries are stamped with the server they belong to, so a player
# never types an address or an access code. scripts/publish.sh sets these
# from the real server; building by hand leaves them empty, which the app
# reports as a developer build rather than failing mysteriously.
: "${FL_VERSION:=dev}"
: "${FL_COORDINATOR:=}"
: "${FL_AUTH_TOKEN:=}"
: "${FL_DOWNLOAD_BASE:=}"

stamp() {
  printf -- '-X lobbybaz/client/build.Version=%s ' "$FL_VERSION"
  printf -- '-X lobbybaz/client/build.Coordinator=%s ' "$FL_COORDINATOR"
  printf -- '-X lobbybaz/client/build.AuthToken=%s ' "$FL_AUTH_TOKEN"
  printf -- '-X lobbybaz/client/build.DownloadBase=%s' "$FL_DOWNLOAD_BASE"
}

# pack compresses a built executable into the installer's payload. The
# installer embeds whatever is sitting there when it is built, so this always
# runs immediately before it.
pack() {
  local name="$1"
  # A failed build a moment ago would otherwise be papered over by an old
  # binary still sitting in bin/, and the installer would ship the last
  # version that happened to compile. That is exactly the class of mistake
  # that had us testing a stale build for twenty minutes once already.
  if [ "$FAIL" -ne 0 ]; then
    printf '  SKIP  payload/%s (an earlier build failed)' "$name"; echo
    return
  fi
  if [ ! -f "bin/$name" ]; then
    printf '  FAIL  payload/%s (not built)' "$name"; echo
    FAIL=1
    return
  fi
  gzip -9 -c "bin/$name" > "installer/payload/$name.gz"
  printf '  OK    installer/payload/%s.gz (%s)' "$name" "$(du -h "installer/payload/$name.gz" | cut -f1)"; echo
}

# desktop builds the window the player actually opens (D45, D67).
#
# It is a Rust/Tauri binary rather than a Go one, so it does not go through
# build(). It is built here, with everything else, because it is not a side
# project any more: it is the only executable a player is ever meant to run.
# lobbyapp.exe opens a console and a browser tab, which is exactly what the
# owner reported seeing when the shortcut pointed at it.
build_desktop() {
  if [ "$FAIL" -ne 0 ]; then
    printf '  SKIP  bin/lobbybaz.exe (an earlier build failed)\n'
    return
  fi
  if ! command -v cargo >/dev/null 2>&1; then
    # Not a skip. An installer without the shell reinstates the console and
    # the browser tab, and looks like a successful build while doing it.
    printf '  FAIL  bin/lobbybaz.exe (cargo is not installed)\n'
    FAIL=1
    return
  fi
  # Tauri wants the Go binary under its externalBin name at build time. Give
  # it the stamped one that was just built, so the copy inside any future
  # bundle is the same binary the installer writes beside it.
  mkdir -p desktop/binaries
  if [ -f bin/lobbyapp.exe ]; then
    cp bin/lobbyapp.exe desktop/binaries/lobbyapp-x86_64-pc-windows-msvc.exe
    cp bin/lobbyapp.exe desktop/binaries/lobbyapp.exe
  fi
  if (cd desktop && cargo build --release --quiet); then
    cp desktop/target/release/lobbybaz.exe bin/lobbybaz.exe
    printf '  OK    bin/lobbybaz.exe\n'
  else
    printf '  FAIL  bin/lobbybaz.exe\n'
    FAIL=1
  fi
}

build() { # module  outfile  goos  pkg
  local mod="$1" out="$2" goos="$3" pkg="$4"
  if ! ls "$mod/$pkg"/*.go >/dev/null 2>&1; then
    printf '  SKIP  %s (%s not written yet)\n' "$out" "$mod/$pkg"
    return 0
  fi
  if (cd "$mod" && CGO_ENABLED=0 GOOS="$goos" GOARCH=amd64 \
        go build -trimpath -ldflags "-s -w $(stamp)" -o "../bin/$out" "./$pkg"); then
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
  all|lobbyapp)    build lobbyapp    lobbyapp.exe   windows . ;;&
  all|lobbycli)    build lobbycli    lobbycli.exe   windows . ;;&
  all|loadtest)    build loadtest    loadtest       linux   . ;;&
  all|desktop)     build lobbyapp    lobbyapp.exe   windows . ; build_desktop ;;&
  all|installer)
    # The installer carries the other four inside it, so they are built and
    # packed first regardless of which target was asked for.
    build netservice netservice.exe windows cmd/netservice
    build lobbyapp   lobbyapp.exe   windows .
    build lobbycli   lobbycli.exe   windows .
    build_desktop
    pack netservice.exe
    pack lobbyapp.exe
    pack lobbycli.exe
    pack lobbybaz.exe
    if [ "$FAIL" -ne 0 ]; then
      printf '  SKIP  bin/LobbyBaz-Setup.exe (its payload is incomplete)'; echo
    else
      build installer LobbyBaz-Setup.exe windows .
    fi
    ;;&
  all|relay|coordinator|netservice|lobbyapp|lobbycli|loadtest|desktop|installer) ;;
  *) echo "unknown target: $target" >&2; exit 2 ;;
esac

exit "$FAIL"
