#!/usr/bin/env bash
# Prove the chat dock reacts to a message arriving (D56).
#
#   bash scripts/chatcheck.sh
#
# This is the one thing the rest of the ladder cannot see. check.sh proves the
# JS parses; smoke.sh renders the page once and reads the DOM - but the dock
# opens on a *change* between two polls, so a single snapshot always shows it
# minimised however the code behaves. This boots a real coordinator and a real
# app, drives the live page over the DevTools Protocol, has somebody else send
# this player a private message, and looks again.
#
# Loopback only, throwaway database, redirected APPDATA, its own Chrome
# profile. Never touches the live server or the developer's own session.
set -uo pipefail
cd /c/Users/Mcc/Desktop/Final_Lobby
. scripts/env.sh
. scripts/sandbox.sh

CHROME=""
for c in "/c/Program Files/Google/Chrome/Application/chrome.exe" \
         "/c/Program Files (x86)/Google/Chrome/Application/chrome.exe" \
         "$LOCALAPPDATA/Google/Chrome/Application/chrome.exe"; do
  [ -x "$c" ] && { CHROME="$c"; break; }
done
if [ -z "$CHROME" ]; then
  echo "  WARN  no Chrome found to drive - skipping"
  exit 0
fi

cleanup() { [ -n "${CH_PID:-}" ] && kill "$CH_PID" 2>/dev/null; sandbox_cleanup; }
trap cleanup EXIT

sandbox_build   || exit 1
sandbox_coordinator || exit 1
sandbox_app     || exit 1
sandbox_seed
sandbox_you
CDP=$(sandbox_port)

echo "=== the chat dock, while it is running ==="

"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --remote-debugging-port="$CDP" --user-data-dir="$(cygpath -w "$WORK")\chrome" \
  --window-size=1440,820 about:blank > "$WORK/chrome.log" 2>&1 &
CH_PID=$!
sleep 3

COORD="$COORD" SENDER_SESSION="$SA" TARGET_ID="$YOU" \
  node scripts/live-chat.js "$APP_URL" "$CDP"
RC=$?

echo ""
if [ "$RC" -eq 0 ]; then
  echo "RESULT: a message that arrives opens the chat and names who sent it"
else
  echo "RESULT: PROBLEMS FOUND"
fi
exit "$RC"
