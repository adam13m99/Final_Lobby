#!/usr/bin/env bash
# Prove the terms cannot be accepted without being read (D61).
#
#   bash scripts/termscheck.sh
#
# The rest of the ladder cannot see this. check.sh proves the script parses,
# smoke.sh renders the page once, preview.sh photographs it - but the gate is
# a relationship between a scroll position and a disabled attribute, and it
# has two opposite ways to fail. Too strict and nobody can ever create an
# account, which is the entire product; too loose and the button recording
# somebody's consent records nothing.
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

sandbox_build       || exit 1
sandbox_coordinator || exit 1
sandbox_app         || exit 1
sandbox_seed
sandbox_you
CDP=$(sandbox_port)

echo "=== the terms, while somebody is reading them ==="

"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --remote-debugging-port="$CDP" --user-data-dir="$(cygpath -w "$WORK")\chrome" \
  --window-size=1440,820 about:blank > "$WORK/chrome.log" 2>&1 &
CH_PID=$!
sleep 3

node scripts/live-terms.js "$APP_URL" "$CDP"
RC=$?

echo ""
if [ "$RC" -eq 0 ]; then
  echo "RESULT: the terms are read before they are accepted, and can be accepted"
else
  echo "RESULT: PROBLEMS FOUND"
fi
exit "$RC"
