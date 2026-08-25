#!/usr/bin/env bash
# Run a complete LobbyBaz on this PC and open it.
#
#   ./scripts/try.sh
#
# For looking at the product: clicking through the lobby, the room screen,
# signing up, changing the door on a room. It builds a coordinator and an app,
# starts both on loopback, fills the lobby with four players and three rooms
# so there is something to look at, and opens it in the browser. Ctrl-C stops
# it and deletes everything it made.
#
# This is not how a player gets the app - that is ./scripts/publish.sh, which
# builds an installer and puts it on the server. Nothing here touches the
# server, and no tunnel is opened, so the network parts of the room screen
# will say the service is not running. Everything else is real.
set -uo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. scripts/env.sh
. scripts/sandbox.sh

trap sandbox_cleanup EXIT INT TERM

sandbox_build   || exit 1
sandbox_coordinator || exit 1
sandbox_app     || exit 1

echo "filling the lobby..."
sandbox_seed
sandbox_you

echo
echo "  LobbyBaz is running on this PC."
echo
echo "      $APP_URL"
echo
echo "  Signed in as \"You\". The other players are seeded accounts;"
echo "  the password for every one of them is: a long enough one"
echo "  The room called Turbo has the password: hunter2"
echo
echo "  Ctrl-C here stops it and deletes the throwaway database."
echo

# start is Windows' own "open this the way the user would open it", so the
# page lands in whatever browser they actually use.
# NO_OPEN=1 skips this, for running from somewhere with no desktop.
if [ -z "${NO_OPEN:-}" ]; then
  start "$APP_URL" 2>/dev/null || \
    powershell -NoProfile -Command "Start-Process '$APP_URL'" 2>/dev/null || \
    echo "  (open that address yourself - could not launch a browser)"
fi

# Wait on the app rather than sleeping, so closing it here ends the script.
wait "$APP_PID"
