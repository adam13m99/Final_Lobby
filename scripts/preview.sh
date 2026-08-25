#!/usr/bin/env bash
# Photograph the interface, on a lobby that looks like a lobby.
#
#   bash scripts/preview.sh <name>          # 1440x820
#   WIDE=1366 TALL=768 bash scripts/preview.sh small
#
# Writes one PNG per screen into scripts/shots/<name>/.
#
# Design work needs to be looked at, and nothing else here can look at
# anything: check.sh proves the CSS parses, smoke.sh proves the page renders
# and the console is quiet, and neither can tell you the room list is ugly or
# that half the window is empty. This boots a real coordinator and a real app
# on a throwaway database, seeds four players and three rooms with different
# doors, and drives headless Chrome through every screen.
#
# Loopback only, throwaway database, redirected APPDATA, its own Chrome
# profile. Never touches the live server or the developer's own session.
set -uo pipefail
cd /c/Users/Mcc/Desktop/Final_Lobby
. scripts/env.sh

TAG="${1:-shot}"
SP="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$SP/shots/$TAG"; mkdir -p "$OUT"
CHROME="/c/Program Files/Google/Chrome/Application/chrome.exe"
export WIDE="${WIDE:-1440}" TALL="${TALL:-820}"

. scripts/sandbox.sh
cleanup() { [ -n "${CH_PID:-}" ] && kill "$CH_PID" 2>/dev/null; sandbox_cleanup; }
trap cleanup EXIT

sandbox_build || exit 1
sandbox_coordinator || exit 1
sandbox_app || exit 1
sandbox_seed
sandbox_you
CDP=$(sandbox_port)

echo "app: $APP_URL"

# --- photograph -----------------------------------------------------------
"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check   --remote-debugging-port="$CDP" --user-data-dir="$(cygpath -w "$WORK")\chrome"   --window-size=$WIDE,$TALL about:blank > "$WORK/chrome.log" 2>&1 &
CH_PID=$!
sleep 3

export SHOTS='[["lobby",""],["room","show(\"room\")"],["mod","show(\"mod\")"],["checks","show(\"checks\")"],["profile","$(\"mebtn\").click()"]]'
node "$SP/preview-shots.js" "$APP_URL" "$(cygpath -w "$OUT")" "$CDP"
echo "wrote $OUT"
