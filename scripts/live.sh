#!/usr/bin/env bash
# LobbyBaz, running, at one address, updating itself while it runs.
#
#   bash scripts/live.sh
#
# Open the address it prints once and leave the window open. From then on:
#
#   - a change to anything under lobbyapp/ui (the stylesheet, the page, the
#     scripts, the strings) appears within two seconds. No rebuild, no
#     restart, no clicking anything;
#   - a change to Go code is rebuilt and the app restarted, at the same
#     address, and the window picks itself back up on its next poll.
#
# The address does not change between runs either: the token is kept in
# scripts/.live-token, which is gitignored. So it can be pinned as a tab, or
# opened as its own window, and left there for days.
#
# SAFETY. Everything here is local and disposable: both processes bind
# 127.0.0.1, the database is a temporary file deleted on exit, APPDATA is
# redirected so the developer's own signed-in session is untouched, and the
# relay key is a throwaway. It never contacts 87.107.110.199, and no build it
# makes goes anywhere near a player.
set -uo pipefail
cd /c/Users/Mcc/Desktop/Final_Lobby
. scripts/env.sh
. scripts/sandbox.sh

APP_PORT=${LIVE_PORT:-7788}
PORT=${LIVE_COORD_PORT:-$((APP_PORT + 1))}

# One token, kept, so the address is the same tomorrow as it is today. It is
# a loopback development sandbox on a throwaway database; the token is here to
# keep other programs on this PC from driving the window, which it still does.
TOKENFILE="scripts/.live-token"
[ -s "$TOKENFILE" ] || python -c "import secrets;print(secrets.token_hex(32))" > "$TOKENFILE"
export LOBBYBAZ_DEV_TOKEN="$(tr -d '\r\n' < "$TOKENFILE")"

SANDBOX_APP_ARGS=(-dev-ui lobbyapp/ui)

cleanup() {
  echo ""
  echo "stopping, and deleting everything this made."
  sandbox_cleanup
}
trap cleanup EXIT INT TERM

sandbox_build       || exit 1
sandbox_coordinator || exit 1
sandbox_app         || exit 1
sandbox_seed
sandbox_you

LIVE_URL="http://127.0.0.1:$APP_PORT/?t=$LOBBYBAZ_DEV_TOKEN"
printf '%s\n' "$LIVE_URL" > scripts/.live-url

echo ""
echo "  LobbyBaz is live at"
echo ""
echo "    $LIVE_URL"
echo ""
echo "  Leave that window open. Front-end changes appear in it by themselves;"
echo "  Go changes rebuild and the window recovers on its own."
echo "  Ctrl-C here stops everything and deletes the throwaway database."
echo ""

# Opened as its own window rather than a tab: it is meant to be looked at as
# the product, and a browser's furniture around it is not the product.
if [ -z "${NO_OPEN:-}" ]; then
  for c in "/c/Program Files/Google/Chrome/Application/chrome.exe" \
           "/c/Program Files (x86)/Google/Chrome/Application/chrome.exe" \
           "$LOCALAPPDATA/Google/Chrome/Application/chrome.exe" \
           "/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"; do
    if [ -x "$c" ]; then
      "$c" --app="$LIVE_URL" --window-size=1500,940 >/dev/null 2>&1 &
      break
    fi
  done
fi

# --- the watch ------------------------------------------------------------
#
# The interface reloads itself: the app serves it from disk and tells the page
# when it changed, so nothing here has to watch it. What this loop is for is
# Go, which has to be compiled and cannot be.
gostamp() {
  find lobbyapp client coordinator protocol -name '*.go' -newermt '1970-01-01' \
    -printf '%T@ %s %p\n' 2>/dev/null | sort | md5sum
}

LAST=$(gostamp)
while kill -0 "$APP_PID" 2>/dev/null; do
  sleep 2
  NOW=$(gostamp)
  [ "$NOW" = "$LAST" ] && continue
  LAST="$NOW"

  echo "  Go changed - rebuilding..."
  if ! (cd coordinator && go build -o "$WORK/coordinator.new" ./cmd/coordinator) 2>"$WORK/build.err"; then
    echo "  build failed, keeping what is running:"
    sed 's/^/    /' "$WORK/build.err" | head -20
    continue
  fi
  if ! (cd lobbyapp && go build -ldflags "-X lobbybaz/client/build.Coordinator=$COORD" \
        -o "$WORK/lobbyapp.new" .) 2>"$WORK/build.err"; then
    echo "  build failed, keeping what is running:"
    sed 's/^/    /' "$WORK/build.err" | head -20
    continue
  fi

  # The database is kept, so the seeded lobby survives a restart and nobody
  # has to sign in again.
  kill "$APP_PID" 2>/dev/null; kill "$COORD_PID" 2>/dev/null
  sleep 1
  mv -f "$WORK/coordinator.new" "$WORK/coordinator.exe"
  mv -f "$WORK/lobbyapp.new" "$WORK/lobbyapp.exe"
  sandbox_coordinator || { echo "  the coordinator would not come back up"; break; }
  sandbox_app         || { echo "  the app would not come back up"; break; }
  echo "  back up at the same address."
done
